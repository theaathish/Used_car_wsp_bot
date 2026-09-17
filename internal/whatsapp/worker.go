package whatsapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	_ "github.com/mattn/go-sqlite3"
)

// Worker wraps whatsmeow with Postgres-backed CRM state.
// Session (device keys) lives in SQLite file on Railway volume.
type Worker struct {
	pool    *pgxpool.Pool
	enabled bool
	dataDir string
	dbURL   string

	mu      sync.RWMutex
	client  *whatsmeow.Client
	status  string // disabled|qr|connecting|connected|logged_out
	lastQR  string
	lastJID string

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex // per-phone serialization (INT-003)

	sendMu sync.Mutex
	lastTx map[string]time.Time // per-phone send cooldown (rate safety)

	container *sqlstore.Container
	repairCh  chan struct{} // wakes the supervisor to re-pair immediately

	// Failure accounting: after sustained connect failures the session is
	// almost certainly dead server-side. Surface it instead of retrying
	// silently forever.
	nFails  int
	lastErr string

	// Flap accounting: Connect() succeeds but the socket drops within a
	// minute, over and over (duplicate worker sharing one Postgres device,
	// phone killing companions, bad network). Without this the UI sits at
	// "connecting" for a day with fail_count 0 and no last_error.
	cycles    []time.Time
	lastCycle string
}

// failCount returns consecutive connect failures (for logging).
func (w *Worker) failCount() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.nFails
}
// the status becomes "expired" so the admin knows to press Reconnect.
// Returns the backoff to wait.
// noteFailure records a connect failure. After maxFails consecutive failures
// the status becomes "expired" so the admin knows to press Reconnect.
// Returns the backoff to wait.
func (w *Worker) noteFailure(err error) time.Duration {
	const maxFails = 15
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	w.mu.Lock()
	w.nFails++
	n := w.nFails
	w.lastErr = msg
	st := "connecting"
	if n >= maxFails {
		st = "expired"
	}
	w.setStatusLocked(st, "")
	jid := w.lastJID
	w.mu.Unlock()
	_, _ = w.pool.Exec(context.Background(),
		`INSERT INTO whatsapp_sessions(id,connected,jid) VALUES('default',$1,$2)
		 ON CONFLICT (id) DO UPDATE SET connected=$1, jid=$2, updated_at=now()`,
		false, jid)
	backoff := time.Duration(n*2) * time.Second
	if backoff > 5*time.Minute {
		backoff = 5 * time.Minute
	}
	return backoff
}

func (w *Worker) noteSuccess(jid string) {
	w.mu.Lock()
	w.nFails = 0
	w.lastErr = ""
	w.setStatusLocked("connected", jid)
	j := w.lastJID
	w.mu.Unlock()
	_, _ = w.pool.Exec(context.Background(),
		`INSERT INTO whatsapp_sessions(id,connected,jid) VALUES('default',$1,$2)
		 ON CONFLICT (id) DO UPDATE SET connected=$1, jid=$2, updated_at=now()`,
		true, j)
}

// noteFlap records a connect-then-drop cycle. 6+ cycles in 10 min flips to
// "expired" so the admin gets the Reconnect prompt instead of a day of
// "connecting". Returns true when expired.
func (w *Worker) noteFlap(reason string) bool {
	if len(reason) > 200 {
		reason = reason[:200]
	}
	w.mu.Lock()
	now := time.Now()
	keep := w.cycles[:0]
	for _, t := range w.cycles {
		if now.Sub(t) < 10*time.Minute {
			keep = append(keep, t)
		}
	}
	keep = append(keep, now)
	w.cycles = keep
	w.lastCycle = reason
	w.lastErr = reason
	expired := len(keep) >= 6
	st := "connecting"
	if expired {
		st = "expired"
	}
	w.setStatusLocked(st, "")
	jid := w.lastJID
	n := len(keep)
	w.mu.Unlock()
	_, _ = w.pool.Exec(context.Background(),
		`INSERT INTO whatsapp_sessions(id,connected,jid) VALUES('default',$1,$2)
		 ON CONFLICT (id) DO UPDATE SET connected=$1, jid=$2, updated_at=now()`,
		false, jid)
	log.Printf("[whatsapp] cycle #%d in 10min (%s) -> %s", n, reason, st)
	return expired
}

func New(pool *pgxpool.Pool, dataDir string, enabled bool, dbURL string) *Worker {
	return &Worker{pool: pool, dataDir: dataDir, enabled: enabled, dbURL: dbURL, status: "connecting", repairCh: make(chan struct{}, 1)}
}

func (w *Worker) phoneLock(phone string) *sync.Mutex {
	w.locksMu.Lock()
	defer w.locksMu.Unlock()
	if w.locks == nil {
		w.locks = map[string]*sync.Mutex{}
	}
	if _, ok := w.locks[phone]; !ok {
		w.locks[phone] = &sync.Mutex{}
	}
	return w.locks[phone]
}

// sessionTimeout bounds one chat session: 30 min without activity closes
// the open conversation, so a later "hi" starts fresh instead of resuming
// stale state (e.g. yesterday's BUY_RESULTS). Tune here, no redeploy of
// prompts needed.
const sessionTimeout = 30 * time.Minute

// isSessionExpired reports whether the last activity is older than the timeout.
func isSessionExpired(updatedAt, now time.Time) bool {
	if updatedAt.IsZero() {
		return false
	}
	return now.Sub(updatedAt) > sessionTimeout
}

// validStates guards against corrupt state (STATE-004: recover, never crash).
var validStates = map[string]bool{
	"NEW": true, "ASK_INTENT": true,
	"BUY_BUDGET": true, "BUY_BRAND": true, "BUY_MODEL": true, "BUY_FUEL": true,
	"BUY_TRANS": true, "BUY_YEAR": true, "BUY_RESULTS": true,
	"FINANCE_INFO": true, "TESTDRIVE_ASK": true,
	"SELL_CAR": true, "SELL_YEAR": true, "SELL_DETAILS": true, "SELL_SPECS": true, "SELL_PHOTOS": true,
	"EXCHANGE_CURRENT": true, "EXCHANGE_WANT": true, "DONE": true,
}

func (w *Worker) Status() map[string]any {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return map[string]any{"status": w.status, "jid": w.lastJID, "enabled": w.enabled, "has_qr": w.lastQR != "",
		"fail_count": w.nFails, "last_error": w.lastErr, "last_cycle": w.lastCycle, "cycles_10m": len(w.cycles)}
}

func (w *Worker) QRPNG() ([]byte, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.lastQR == "" {
		return nil, fmt.Errorf("no qr available")
	}
	return qrcode.Encode(w.lastQR, qrcode.Medium, 256)
}

func (w *Worker) setStatus(s, jid string) {
	w.mu.Lock()
	w.setStatusLocked(s, jid)
	w.mu.Unlock()
	_, _ = w.pool.Exec(context.Background(),
		`INSERT INTO whatsapp_sessions(id,connected,jid) VALUES('default',$1,$2)
		 ON CONFLICT (id) DO UPDATE SET connected=$1, jid=$2, updated_at=now()`,
		s == "connected", w.lastJID)
}

// setStatusLocked changes status + failure counters; caller holds w.mu.
func (w *Worker) setStatusLocked(s, jid string) {
	w.status = s
	if jid != "" {
		w.lastJID = jid
	}
}

// Start supervises the WhatsApp connection for the process lifetime:
// pair over QR when there is no device, connect otherwise, monitor, and
// cycle back on disconnect, logout, or an explicit Reconnect() call.
func (w *Worker) Start(ctx context.Context) {
	if !w.enabled {
		w.setStatus("disabled", "")
		log.Println("[whatsapp] disabled via WHATSAPP_ENABLED=false (stub mode, messages logged only)")
		return
	}
	if err := os.MkdirAll(w.dataDir, 0o755); err != nil {
		log.Printf("[whatsapp] datadir: %v", err)
	}
	// Session store: Postgres first (survives redeploys and volume loss),
	// SQLite file on the volume as fallback.
	if w.dbURL != "" {
		if c, err := sqlstore.New(ctx, "pgx", w.dbURL, waLog.Noop); err != nil {
			log.Printf("[whatsapp] postgres session store unavailable, falling back to file: %v", err)
		} else {
			w.container = c
			log.Println("[whatsapp] session store: postgres")
		}
	}
	if w.container == nil {
		dbPath := filepath.Join(w.dataDir, "whatsapp.db")
		c, err := sqlstore.New(ctx, "sqlite3", "file:"+dbPath+"?_foreign_keys=on", waLog.Noop)
		if err != nil {
			log.Printf("[whatsapp] store: %v (stub mode)", err)
			w.setStatus("disabled", "")
			return
		}
		w.container = c
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if !w.runOnce(ctx) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

// runOnce pairs (QR) or connects, then monitors. False = context done.
func (w *Worker) runOnce(ctx context.Context) bool {
	device, err := w.container.GetFirstDevice(ctx)
	if err != nil {
		log.Printf("[whatsapp] device: %v", err)
		w.setStatus("connecting", "")
		return true
	}
	cli := whatsmeow.NewClient(device, waLog.Noop)
	cli.AddEventHandler(w.onEvent)
	w.mu.Lock()
	if w.client != nil {
		w.client.Disconnect()
	}
	w.client = cli
	w.mu.Unlock()
	if cli.Store.ID == nil {
		paired, alive := w.pair(ctx, cli)
		if !alive {
			return false
		}
		if !paired {
			return true // aborted by repair signal; outer loop re-cycles
		}
	} else {
		// Connect with timeout + backoff; a hanging dial can't stick at
		// "connecting" for a day, and sustained failure marks the session
		// expired instead of retrying silently forever.
		for {
			w.setStatus("connecting", "")
			cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
			err := cli.ConnectContext(cctx)
			cancel()
			if err != nil {
				backoff := w.noteFailure(err)
				log.Printf("[whatsapp] reconnect failed (#%d): %v (retry in %s)", w.failCount(), err, backoff)
				select {
				case <-ctx.Done():
					return false
				case <-w.repairCh:
					return true
				case <-time.After(backoff):
				}
				continue
			}
			break
		}
	}
	jid := ""
	if cli.Store.ID != nil {
		jid = cli.Store.ID.String()
	}
	w.noteSuccess(jid)
	connectedAt := time.Now()
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			cli.Disconnect()
			return false
		case <-w.repairCh:
			cli.Disconnect()
			return true
		case <-t.C:
			if !cli.IsConnected() {
				uptime := time.Since(connectedAt).Round(time.Second)
				reason := "socket dropped after " + uptime.String() + " (duplicate worker sharing one device, phone offline, or network flap)"
				w.noteFlap(reason)
				cli.Disconnect()
				return true
			}
		}
	}
}

// pair runs QR pairing. Returns (paired, alive): alive=false only when the
// context is done; paired=false means aborted by repair signal.
func (w *Worker) pair(ctx context.Context, cli *whatsmeow.Client) (bool, bool) {
	w.setStatus("qr", "")
	qrCh, _ := cli.GetQRChannel(ctx)
	if err := cli.Connect(); err != nil {
		log.Printf("[whatsapp] pair connect: %v", err)
		w.noteFailure(err)
		return false, true
	}
	for {
		select {
		case <-ctx.Done():
			return false, false
		case <-w.repairCh:
			cli.Disconnect()
			return false, true
		case evt, ok := <-qrCh:
			if !ok {
				return false, true
			}
			if evt.Event == "code" {
				w.mu.Lock()
				w.lastQR = evt.Code
				w.mu.Unlock()
				log.Println("[whatsapp] QR ready — open admin to scan")
			} else if evt.Event == "success" {
				w.mu.Lock()
				w.lastQR = ""
				w.mu.Unlock()
				w.setStatus("connected", "")
				log.Println("[whatsapp] paired OK")
				return true, true
			}
		}
	}
}

// Reconnect clears every stored device (wipes dead sessions) and forces an
// immediate fresh QR pairing cycle. Safe to call in any state.
func (w *Worker) Reconnect(ctx context.Context) error {
	w.mu.RLock()
	cli := w.client
	w.mu.RUnlock()
	if cli != nil {
		cli.Disconnect()
	}
	if w.container != nil {
		if devs, err := w.container.GetAllDevices(ctx); err == nil {
			for _, d := range devs {
				_ = w.container.DeleteDevice(ctx, d)
			}
		}
	}
	w.mu.Lock()
	w.lastQR = ""
	w.nFails = 0
	w.lastErr = ""
	w.cycles = nil
	w.lastCycle = ""
	w.mu.Unlock()
	select {
	case w.repairCh <- struct{}{}:
	default:
	}
	w.setStatus("qr", "")
	log.Println("[whatsapp] session cleared by admin — fresh QR pairing starting")
	return nil
}

func (w *Worker) onEvent(evt any) {
	switch e := evt.(type) {
	case *events.LoggedOut:
		log.Printf("[whatsapp] logged out (reason %v) — admin re-auth required", e.Reason)
		w.setStatus("logged_out", "")
		return
	case *events.Connected:
		log.Println("[whatsapp] socket connected")
		return
	case *events.ConnectFailure:
		log.Printf("[whatsapp] connect failure: %s", e.Reason.String())
		return
	case *events.TemporaryBan:
		log.Printf("[whatsapp] temp ban code=%v expire=%v", e.Code, e.Expire)
		return
	case *events.Disconnected:
		log.Printf("[whatsapp] disconnected event, reconnect loop continues")
		w.setStatus("connecting", w.lastJID)
		return
	}
	m, ok := evt.(*events.Message)
	if !ok || m.Info.IsFromMe {
		return
	}
	// Direct chats only: ignore groups, broadcast lists, status updates
	// and channels/newsletters. Otherwise group chatter and status views
	// create bogus customers and get bot replies.
	if !isDirectChat(m.Info) {
		log.Printf("[whatsapp] ignored non-direct chat=%s group=%v broadcast=%v newsletter=%v from=%s",
			m.Info.Chat.String(), m.Info.IsGroup, m.Info.IsIncomingBroadcast(), m.Info.IsNewsletterStatus, m.Info.Sender.String())
		return
	}
	// Identity: prefer the phone-number (PN) address so the customer row is
	// stable across LID/PN rotations. LID-only contacts (SenderAlt empty,
	// privacy mode) fall back to "lid:<id>" so replies still route via @lid
	// instead of a bogus @s.whatsapp.net JID (which silently never delivers).
	phone, replyJID := resolveIdentity(m.Info)
	name := m.Info.PushName
	if phone == "" {
		log.Printf("[whatsapp] ignored: empty identity sender=%s chat=%s", m.Info.Sender.String(), m.Info.Chat.String())
		return
	}
	waID := string(m.Info.ID)
	if img := unwrap(m.Message).GetImageMessage(); img != nil {
		caption := img.GetCaption()
		w.onImageJID(replyJID, phone, name, waID, img)
		if caption != "" {
			// Photo caption can carry a command ("DONE", "1", ...): process
			// it as text too (media dedup key differs, so no double-drop).
			body := caption
			reply, err := w.HandleInbound(context.Background(), phone, name, body, waID)
			if err != nil {
				log.Printf("[whatsapp] inbound %s (caption): %v", phone, err)
				return
			}
			if reply != "" {
				if err := w.SendToJID(context.Background(), replyJID, phone, reply); err != nil {
					log.Printf("[whatsapp] send to %s (%s) failed: %v (queued in outbox)", phone, replyJID.String(), err)
					w.markLastOutFailed(context.Background(), phone)
				}
			}
		}
		return
	}
	body := messageText(m.Message)
	if body == "" {
		// Never go silent on a real human message: voice notes, videos
		// and documents without captions carry no text, so they flow
		// through the state machine as a labelled placeholder and get
		// the current step re-asked instead of nothing at all.
		switch messageKind(m.Message) {
		case "audio/ptt":
			body = "[voice message]"
		case "video":
			body = "[video]"
		case "document":
			body = "[document]"
		case "image":
			body = "[photo]"
		case "location":
			body = "[location]"
		case "contact":
			body = "[contact]"
		case "sticker", "reaction":
			return // reactions/stickers stay silent by design
		default:
			log.Printf("[whatsapp] ignored empty-text from %s chat=%s type=%s", phone, m.Info.Chat.String(), messageKind(m.Message))
			return
		}
		log.Printf("[whatsapp] non-text %s from %s treated as %s", messageKind(m.Message), phone, body)
	}
	reply, err := w.HandleInbound(context.Background(), phone, name, body, waID)
	if err != nil {
		log.Printf("[whatsapp] inbound %s: %v", phone, err)
		return
	}
	if reply != "" {
		if err := w.SendToJID(context.Background(), replyJID, phone, reply); err != nil {
			log.Printf("[whatsapp] send to %s (%s) failed: %v (queued in outbox)", phone, replyJID.String(), err)
			w.markLastOutFailed(context.Background(), phone)
		}
	}
}

// onImage stores inbound media without breaking the conversation (INT-002).
func (w *Worker) onImage(phone, name, waID string, img *waE2E.ImageMessage) {
	w.onImageJID(phoneToJID(phone), phone, name, waID, img)
}

func (w *Worker) onImageJID(replyJID types.JID, phone, name, waID string, img *waE2E.ImageMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var data []byte
	w.mu.RLock()
	cli := w.client
	w.mu.RUnlock()
	if cli != nil && img.GetDirectPath() != "" {
		if d, err := cli.Download(ctx, img); err == nil {
			data = d
		} else {
			log.Printf("[whatsapp] image download: %v", err)
		}
	}
	ack, err := w.HandleMedia(ctx, phone, name, waID, data)
	if err != nil {
		log.Printf("[whatsapp] media %s: %v", phone, err)
		return
	}
	if ack != "" {
		_ = w.SendToJID(ctx, replyJID, phone, ack)
	}
}

// unwrap strips disappearing/view-once/edited/device-sent wrappers so text
// and captions are found even when the user enabled disappearing messages
// (the top #1 "no reply at all" cause on live phones; simulators never use them).
func unwrap(m *waE2E.Message) *waE2E.Message {
	for i := 0; i < 5 && m != nil; i++ {
		if dm := m.GetDeviceSentMessage(); dm != nil && dm.GetMessage() != nil {
			m = dm.GetMessage()
			continue
		}
		if em := m.GetEphemeralMessage(); em != nil && em.GetMessage() != nil {
			m = em.GetMessage()
			continue
		}
		if vm := m.GetViewOnceMessage(); vm != nil && vm.GetMessage() != nil {
			m = vm.GetMessage()
			continue
		}
		if v2 := m.GetViewOnceMessageV2(); v2 != nil && v2.GetMessage() != nil {
			m = v2.GetMessage()
			continue
		}
		if v2e := m.GetViewOnceMessageV2Extension(); v2e != nil && v2e.GetMessage() != nil {
			m = v2e.GetMessage()
			continue
		}
		if ed := m.GetEditedMessage(); ed != nil && ed.GetMessage() != nil {
			m = ed.GetMessage()
			continue
		}
		if dc := m.GetDocumentWithCaptionMessage(); dc != nil && dc.GetMessage() != nil {
			m = dc.GetMessage()
			continue
		}
		break
	}
	if m == nil {
		return &waE2E.Message{}
	}
	return m
}

func messageText(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	m = unwrap(m)
	if t := m.GetConversation(); t != "" {
		return t
	}
	if m.ExtendedTextMessage != nil {
		return m.ExtendedTextMessage.GetText()
	}
	// Interactive / button / list taps arrive as response objects, not text.
	// Without these, tapping a reply button looks like "no reply at all".
	if br := m.GetButtonsResponseMessage(); br != nil {
		if t := br.GetSelectedDisplayText(); t != "" {
			return t
		}
		return br.GetSelectedButtonID()
	}
	if lr := m.GetListResponseMessage(); lr != nil {
		if s := lr.GetSingleSelectReply(); s != nil && s.GetSelectedRowID() != "" {
			if t := lr.GetTitle(); t != "" {
				return t
			}
			return s.GetSelectedRowID()
		}
		return lr.GetTitle()
	}
	if tr := m.GetTemplateButtonReplyMessage(); tr != nil {
		if t := tr.GetSelectedDisplayText(); t != "" {
			return t
		}
		return tr.GetSelectedID()
	}
	if ir := m.GetInteractiveResponseMessage(); ir != nil {
		if b := ir.GetBody(); b != nil && b.GetText() != "" {
			return b.GetText()
		}
		if n := ir.GetNativeFlowResponseMessage(); n != nil && n.GetParamsJSON() != "" {
			return n.GetParamsJSON()
		}
	}
	// Media captions (user sends photo/video with "DONE" or "1").
	if im := m.GetImageMessage(); im != nil && im.GetCaption() != "" {
		return im.GetCaption()
	}
	if vm := m.GetVideoMessage(); vm != nil && vm.GetCaption() != "" {
		return vm.GetCaption()
	}
	if dm := m.GetDocumentMessage(); dm != nil && dm.GetCaption() != "" {
		return dm.GetCaption()
	}
	return ""
}

// messageKind names the payload for "ignored empty-text" diagnostics.
func messageKind(m *waE2E.Message) string {
	if m == nil {
		return "nil"
	}
	m = unwrap(m)
	switch {
	case m.GetConversation() != "":
		return "conversation"
	case m.ExtendedTextMessage != nil:
		return "extended"
	case m.GetImageMessage() != nil:
		return "image"
	case m.GetVideoMessage() != nil:
		return "video"
	case m.GetDocumentMessage() != nil:
		return "document"
	case m.GetAudioMessage() != nil:
		return "audio/ptt"
	case m.GetButtonsResponseMessage() != nil:
		return "buttons-response"
	case m.GetListResponseMessage() != nil:
		return "list-response"
	case m.GetTemplateButtonReplyMessage() != nil:
		return "template-reply"
	case m.GetInteractiveResponseMessage() != nil:
		return "interactive-response"
	case m.GetReactionMessage() != nil:
		return "reaction"
	case m.GetStickerMessage() != nil:
		return "sticker"
	case m.GetLocationMessage() != nil || m.GetLiveLocationMessage() != nil:
		return "location"
	case m.GetContactMessage() != nil || m.ContactsArrayMessage != nil:
		return "contact"
	case m.GetPollCreationMessage() != nil || m.GetPollUpdateMessage() != nil:
		return "poll"
	default:
		return "other/unsupported"
	}
}

func normalizePhone(s string) string {
	d := ""
	for _, r := range s {
		if r >= '0' && r <= '9' {
			d += string(r)
		}
	}
	return d
}

// isDirectChat reports whether an incoming message belongs to a 1:1 chat.
// Groups, broadcast lists, status updates, channels and newsletter statuses
// are ignored. Note 1:1 chats may be addressed by phone number, LID, or
// hosted LID.
func isDirectChat(info types.MessageInfo) bool {
	if info.IsGroup || info.IsIncomingBroadcast() || info.IsNewsletterStatus {
		return false
	}
	if info.Chat == types.StatusBroadcastJID {
		return false
	}
	switch info.Chat.Server {
	case types.DefaultUserServer, types.HiddenUserServer, types.HostedLIDServer:
		return info.Chat.User != ""
	default:
		return false
	}
}

// phoneToJID routes stored identities: "lid:<id>" goes to @lid, everything
// else (digits phone) goes to @s.whatsapp.net.
func phoneToJID(phone string) types.JID {
	if strings.HasPrefix(phone, "lid:") {
		return types.NewJID(strings.TrimPrefix(phone, "lid:"), types.HiddenUserServer)
	}
	return types.NewJID(phone, types.DefaultUserServer)
}

// resolveIdentity prefers the PN address for a stable customer row, and falls
// back to "lid:<id>" for LID-only privacy contacts. It also returns the
// Chat JID to reply to (never reconstruct from phone alone).
func resolveIdentity(info types.MessageInfo) (string, types.JID) {
	replyJID := info.Chat
	if replyJID.User == "" {
		replyJID = info.Sender
	}
	pn := ""
	for _, j := range []types.JID{info.SenderAlt, info.Sender, info.Chat} {
		if j.Server == types.DefaultUserServer {
			if d := normalizePhone(j.User); d != "" {
				pn = d
				break
			}
		}
	}
	if pn != "" {
		return pn, replyJID
	}
	for _, j := range []types.JID{info.Sender, info.SenderAlt, info.Chat} {
		if j.Server == types.HiddenUserServer || j.Server == types.HostedLIDServer {
			if d := normalizePhone(j.User); d != "" {
				return "lid:" + d, replyJID
			}
		}
	}
	// Last resort: raw digits (keeps old behaviour, logged upstream).
	if d := normalizePhone(info.Sender.User); d != "" {
		return d, replyJID
	}
	return "", replyJID
}

// sendCooldown spaces same-phone sends by 2s (rate safety). Returns how
// long the caller must wait; zero means send now. Pure for tests.
func sendCooldown(last, now time.Time) time.Duration {
	if last.IsZero() {
		return 0
	}
	if d := now.Sub(last); d < 2*time.Second {
		return 2*time.Second - d
	}
	return 0
}

// Send delivers text via whatsmeow (stub-logs when disabled), enqueueing to
// outbox on transport failure so WA-F003 PENDING->SENT recovery holds.
func (w *Worker) Send(ctx context.Context, phone, text string) error {
	return w.SendToJID(ctx, phoneToJID(phone), phone, text)
}

// SendToJID delivers to the exact Chat JID (LID-safe) while keeping the
// stable phone identity for outbox retries and the CRM row.
// The 2s per-phone cooldown (§21: no spam behavior) waits out the remainder
// instead of parking in the outbox — parking delayed rapid replies by up to
// a full 60s scheduler tick. The slot is reserved under lock so concurrent
// senders line up instead of bursting together.
func (w *Worker) SendToJID(ctx context.Context, jid types.JID, phone, text string) error {
	key := phone
	if key == "" {
		key = jid.String()
	}
	w.sendMu.Lock()
	if w.lastTx == nil {
		w.lastTx = map[string]time.Time{}
	}
	wait := sendCooldown(w.lastTx[key], time.Now())
	if wait > 0 {
		w.lastTx[key] = time.Now().Add(wait)
	}
	w.sendMu.Unlock()
	if wait > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	w.sendMu.Lock()
	w.lastTx[key] = time.Now()
	w.sendMu.Unlock()

	w.mu.RLock()
	cli := w.client
	st := w.status
	w.mu.RUnlock()
	if !w.enabled || cli == nil || st != "connected" {
		// Queue instead of dropping: the reply is delivered by FlushOutbox
		// on reconnect. (Flow-bug: stubbed replies were logged only, so the
		// admin saw SENT while the human got nothing.)
		_, _ = w.pool.Exec(ctx, `INSERT INTO outbox(phone,body,status) VALUES($1,$2,'PENDING') ON CONFLICT DO NOTHING`, phone, text)
		log.Printf("[whatsapp:stub-queued] -> %s (%s): %s", phone, jid.String(), text)
		return nil
	}
	_, err := cli.SendMessage(ctx, jid, &waE2E.Message{Conversation: proto.String(text)})
	if err != nil {
		_, _ = w.pool.Exec(ctx, `INSERT INTO outbox(phone,body,status) VALUES($1,$2,'PENDING') ON CONFLICT DO NOTHING`, phone, text)
	}
	return err
}

// FlushOutbox retries PENDING rows (scheduler calls this every tick).
// Claims rows first so concurrent flushers can't double-send; after 10
// failed attempts a row is FAILED_PERMANENTLY and visible in /api/outbox.
func (w *Worker) FlushOutbox(ctx context.Context) {
	w.mu.RLock()
	cli := w.client
	st := w.status
	w.mu.RUnlock()
	if !w.enabled || cli == nil || st != "connected" {
		return
	}
	rows, err := w.pool.Query(ctx, `UPDATE outbox SET attempts=attempts WHERE id IN (
		SELECT id FROM outbox WHERE status='PENDING' ORDER BY created_at ASC LIMIT 20 FOR UPDATE SKIP LOCKED)
		RETURNING id::text, phone, body, attempts`)
	if err != nil {
		return
	}
	type item struct {
		id, phone, body string
		att             int
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.phone, &it.body, &it.att); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()
	for _, it := range items {
		jid := phoneToJID(it.phone)
		_, err := cli.SendMessage(ctx, jid, &waE2E.Message{Conversation: proto.String(it.body)})
		if err != nil {
			if it.att+1 >= 10 {
				_, _ = w.pool.Exec(ctx, `UPDATE outbox SET status='FAILED_PERMANENTLY', attempts=attempts+1, updated_at=now() WHERE id=$1`, it.id)
			} else {
				_, _ = w.pool.Exec(ctx, `UPDATE outbox SET attempts=attempts+1, updated_at=now() WHERE id=$1`, it.id)
			}
			continue
		}
		_, _ = w.pool.Exec(ctx, `UPDATE outbox SET status='SENT', updated_at=now() WHERE id=$1`, it.id)
	}
}

func (w *Worker) Logout(ctx context.Context) error {
	w.mu.RLock()
	cli := w.client
	w.mu.RUnlock()
	if cli != nil {
		_ = cli.Logout(ctx)
	}
	w.mu.Lock()
	w.lastQR, w.lastJID = "", ""
	w.mu.Unlock()
	w.setStatus("logged_out", "")
	return nil
}

// HandleInbound implements: dedup -> customer -> conversation+lead -> state
// machine -> side effects -> reply. waMsgID is optional (dedup skipped when "").
// Serialized per phone so rapid bursts (INT-003) can't interleave writes.
func (w *Worker) HandleInbound(ctx context.Context, phone, name, body string, waMsgID ...string) (string, error) {
	lk := w.phoneLock(phone)
	lk.Lock()
	defer lk.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if len(waMsgID) > 0 && waMsgID[0] != "" {
		tag, err := w.pool.Exec(ctx, `INSERT INTO processed_messages(wa_msg_id) VALUES($1) ON CONFLICT DO NOTHING`, waMsgID[0])
		if err == nil && tag.RowsAffected() == 0 {
			return "", nil // duplicate delivery: exactly one business action
		}
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	// customer (WA-003: never duplicate on phone)
	var custID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM customers WHERE phone=$1`, phone).Scan(&custID)
	if err != nil {
		custID = uuid.NewString()
		nm := name
		if nm == "" {
			nm = phone
		}
		if _, err := tx.Exec(ctx, `INSERT INTO customers(id,name,phone,source) VALUES($1,$2,$3,'whatsapp')`, custID, nm, phone); err != nil {
			return "", err
		}
	} else if name != "" {
		_, _ = tx.Exec(ctx, `UPDATE customers SET name=$1, updated_at=now() WHERE id=$2 AND (name='' OR name=$3)`, name, custID, phone)
	}

	// open conversation (STATE-007: human takeover flag). Idle past the
	// session timeout closes the stale chat so the next message starts new.
	var convID string
	var botEnabled = true
	var convUpdated time.Time
	err = tx.QueryRow(ctx, `SELECT id::text, bot_enabled, updated_at FROM conversations WHERE customer_id=$1 AND status='open' ORDER BY created_at DESC LIMIT 1`, custID).Scan(&convID, &botEnabled, &convUpdated)
	freshSession := false
	if err != nil {
		convID = uuid.NewString()
		if _, err := tx.Exec(ctx, `INSERT INTO conversations(id,customer_id,channel,status) VALUES($1,$2,'whatsapp','open')`, convID, custID); err != nil {
			return "", err
		}
		freshSession = true
	} else if isSessionExpired(convUpdated, time.Now()) {
		log.Printf("[whatsapp] session timeout for %s (idle since %s), closing", phone, convUpdated.Format(time.RFC3339))
		_, _ = tx.Exec(ctx, `UPDATE conversations SET status='closed', updated_at=now() WHERE id=$1`, convID)
		convID = uuid.NewString()
		if _, err := tx.Exec(ctx, `INSERT INTO conversations(id,customer_id,channel,status) VALUES($1,$2,'whatsapp','open')`, convID, custID); err != nil {
			return "", err
		}
		freshSession = true
	}
	// lead (latest non-terminal; a fresh session always starts a new lead)
	var leadID, state, intent, status, interest string
	var stateRaw []byte
	err = tx.QueryRow(ctx, `SELECT id::text, state, intent, status, COALESCE(interest,''), state_data FROM leads WHERE customer_id=$1 ORDER BY created_at DESC LIMIT 1`, custID).Scan(&leadID, &state, &intent, &status, &interest, &stateRaw)
	if err != nil || freshSession || status == "DONE" || status == "LOST" {
		leadID = uuid.NewString()
		state, intent, status, interest = "NEW", "UNKNOWN", "NEW", ""
		stateRaw = []byte("{}")
		if _, err := tx.Exec(ctx, `INSERT INTO leads(id,customer_id,intent,status,state,source) VALUES($1,$2,'UNKNOWN','NEW','NEW','whatsapp')`, leadID, custID); err != nil {
			return "", err
		}
	}
	data := map[string]string{}
	_ = json.Unmarshal(stateRaw, &data)

	if _, err := tx.Exec(ctx, `INSERT INTO messages(conversation_id,direction,body,status) VALUES($1,'in',$2,'PROCESSED')`, convID, body); err != nil {
		return "", err
	}

	// Human takeover: store, stay silent, let the salesperson talk.
	if !botEnabled {
		if _, err := tx.Exec(ctx, `UPDATE conversations SET lead_id=$1, updated_at=now() WHERE id=$2`, leadID, convID); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return "", nil
	}

	// STATE-004: corrupt state recovers to menu instead of crashing.
	if !validStates[state] {
		log.Printf("[whatsapp] invalid state %q for %s, resetting", state, phone)
		state, intent, status = "ASK_INTENT", "UNKNOWN", "CONTACTED"
		data = map[string]string{}
	}

	// STATE-006 / menu: cancel current flow, back to menu.
	if tb := norm(body); tb == "start again" || tb == "restart" || tb == "menu" || tb == "main menu" || tb == "hi menu" {
		state, intent, status = "ASK_INTENT", "UNKNOWN", "CONTACTED"
		data = map[string]string{}
		reply := "No problem — starting fresh. Reply 1️⃣ BUY, 2️⃣ SELL or 3️⃣ EXCHANGE."
		merged, _ := json.Marshal(data)
		_, _ = tx.Exec(ctx, `UPDATE leads SET state='ASK_INTENT',intent='UNKNOWN',status='CONTACTED',state_data=$1,updated_at=now() WHERE id=$2`, string(merged), leadID)
		_, _ = tx.Exec(ctx, `INSERT INTO messages(conversation_id,direction,body,status) VALUES($1,'out',$2,'SENT')`, convID, reply)
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return reply, nil
	}

	// "hi" is init-only: a bare greeting from any live state restarts the
	// menu instead of landing in the current step as an answer (e.g. "hi"
	// at BUY_RESULTS used to get the generic more-cars reply). NEW falls
	// through to the normal welcome; takeover stays silent above.
	if isGreetingOnly(body) && state != "NEW" {
		data = map[string]string{}
		reply := "Welcome back! Reply 1️⃣ BUY, 2️⃣ SELL or 3️⃣ EXCHANGE."
		merged, _ := json.Marshal(data)
		_, _ = tx.Exec(ctx, `UPDATE leads SET state='ASK_INTENT',intent='UNKNOWN',status='CONTACTED',state_data=$1,updated_at=now() WHERE id=$2`, string(merged), leadID)
		_, _ = tx.Exec(ctx, `UPDATE conversations SET lead_id=$1, updated_at=now() WHERE id=$2`, leadID, convID)
		_, _ = tx.Exec(ctx, `INSERT INTO messages(conversation_id,direction,body,status) VALUES($1,'out',$2,'SENT')`, convID, reply)
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return reply, nil
	}

	// STATE-005: back to previous step.
	if norm(body) == "back" || norm(body) == "go back" {
		if prev := data["prev_state"]; prev != "" && validStates[prev] {
			prevData := map[string]string{}
			for k, v := range data {
				prevData[k] = v
			}
			delete(prevData, "prev_state")
			merged, _ := json.Marshal(prevData)
			_, _ = tx.Exec(ctx, `UPDATE leads SET state=$1,state_data=$2,updated_at=now() WHERE id=$3`, prev, string(merged), leadID)
			reply := "Back one step. " + promptFor(prev)
			_, _ = tx.Exec(ctx, `INSERT INTO messages(conversation_id,direction,body,status) VALUES($1,'out',$2,'SENT')`, convID, reply)
			if err := tx.Commit(ctx); err != nil {
				return "", err
			}
			return reply, nil
		}
		// No step to go back to (e.g. double "back"): stay put instead of
		// storing "back" as an answer like brand or model.
		reply := "You're already at the start of this step. " + promptFor(state)
		_, _ = tx.Exec(ctx, `INSERT INTO messages(conversation_id,direction,body,status) VALUES($1,'out',$2,'SENT')`, convID, reply)
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return reply, nil
	}

	// P0-13: explicit intent switch mid-flow restarts cleanly into the new flow.
	if target := detectIntentSwitch(body, intent); target != "" {
		log.Printf("[whatsapp] intent switch %s -> %s for %s", intent, target, phone)
		state, intent, data = "ASK_INTENT", "UNKNOWN", map[string]string{}
	}

	prevState := state
	next, reply, newIntent, newStatus, patch := Next(state, body, data)
	moreCars := patch["page"] == "next"
	delete(patch, "page")
	for k, v := range patch {
		data[k] = v
	}
	if newIntent != "" {
		intent = newIntent
	}
	if newStatus != "" {
		status = newStatus
	}
	if iv, ok := patch["interest"]; ok && iv != "" {
		interest = iv
	}
	merged, _ := json.Marshal(dataWithPrev(data, prevState))
	if _, err := tx.Exec(ctx, `UPDATE leads SET state=$1,intent=$2,status=$3,interest=$4,state_data=$5,updated_at=now() WHERE id=$6`, next, intent, status, interest, string(merged), leadID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE conversations SET lead_id=$1, updated_at=now() WHERE id=$2`, leadID, convID); err != nil {
		return "", err
	}

	var photoJobs []photoJob

	// BUY_RESULTS: first arrival runs matching; "more cars" pages forward
	// through the WHOLE list; a bare number ("1") opens that car's details
	// with ALL its photos; "more photos" resends the full set; any other
	// model text starts a fresh search (sell loop, never a dead end).
	if next == "BUY_RESULTS" {
		research := false
		if state == "BUY_RESULTS" && atoi(patch["select_idx"]) == 0 && patch["more_photos"] == "" && !moreCars && patch["interest"] == "" {
			if nb := dropNoise(body); nb != "" {
				if br, mo := extractBrandModel(nb); br != "" {
					if ex := ExtractAll(nb); len(ex) > 0 {
						for k, v := range ex {
							data[k] = v
						}
						if ex["budget_max"] != "" {
							delete(data, "budget_unknown") // explicit budget beats an old skip
						}
					}
					if mo != "" {
						data["brand"] = br
						data["model"] = mo
					} else {
						data["model"] = br // single token: search by model, any make
						data["brand"] = "ANY"
					}
					data["page_num"] = "0"
					delete(data, "match_ids")
					delete(data, "selected_vehicle")
					m, _ := json.Marshal(data)
					_, _ = tx.Exec(ctx, `UPDATE leads SET state_data=$1 WHERE id=$2`, string(m), leadID)
					research = true
				}
			}
		}
		page := atoi(data["page_num"])
		if sel := atoi(patch["select_idx"]); sel > 0 {
			if vd, ok := w.vehicleDetails(ctx, tx, data["match_ids"], sel); ok {
				reply = vd.text + "\nLike it? Reply *YES* to confirm, *test drive* to book a visit, or *more cars* for others."
				photoJobs = w.vehiclePhotoJobs(vd.photos, vd.caption, 5)
				data["selected_vehicle"] = vd.id
				m, _ := json.Marshal(data)
				_, _ = tx.Exec(ctx, `UPDATE leads SET state_data=$1 WHERE id=$2`, string(m), leadID)
			} else {
				total := len(strings.Split(data["match_ids"], ","))
				if data["match_ids"] == "" {
					total = 0
				}
				if total == 0 {
					reply = "No cars in your list yet. Tell me your budget and brand, or reply *more cars* and our team will call with fresh arrivals."
				} else {
					reply = "That number isn't on the list. Reply a number 1–" + strconv.Itoa(total) + ", *more cars*, or *test drive*."
				}
			}
		} else if patch["more_photos"] != "" {
			vid := data["selected_vehicle"]
			if vid == "" {
				ids := strings.Split(data["match_ids"], ",")
				if len(ids) > 0 {
					vid = strings.TrimSpace(ids[0])
				}
			}
			if _, err := uuid.Parse(vid); err != nil {
				reply = "No photos uploaded for this car yet — our team will share them on call. Reply *YES* to confirm interest or *test drive* to visit."
				vid = ""
			} else if photos := w.vehiclePhotosTx(ctx, tx, vid, 6); len(photos) == 0 {
				reply = "No photos uploaded for this car yet — our team will share them on call. Reply *YES* to confirm interest or *test drive* to visit."
			} else {
				cap_ := ""
				if vd, ok := w.vehicleByID(ctx, tx, vid); ok {
					cap_ = " of " + vd.caption
				}
				reply = "Here are all photos" + cap_ + ": Reply *YES* to confirm or *test drive* to book."
				photoJobs = w.vehiclePhotoJobs(photos, strings.TrimPrefix(cap_, " of "), 6)
			}
		} else if moreCars {
			page++
			data["page_num"] = strconv.Itoa(page)
			m, _ := json.Marshal(data)
			_, _ = tx.Exec(ctx, `UPDATE leads SET state_data=$1 WHERE id=$2`, string(m), leadID)
			items := w.matchItemsTx(ctx, tx, data["match_ids"], page*3, 3)
			if len(items) == 0 {
				reply = "That's everything matching your search. Reply a model name to search again, or *interested* and our team will call you with fresh arrivals."
			} else {
				reply = "More options (reply the number to see photos):\n" + strings.Join(numbered(items, page*3+1), "\n")
			}
		} else if state != "BUY_RESULTS" || research {
			exact, similar := runMatchingTx(ctx, tx, leadID, data)
			combined := append(append([]matchItem{}, exact...), similar...)
			// Page through the whole lot, not just the first screen.
			if len(combined) > 1000 {
				combined = combined[:1000]
			}
			// Name the sought vehicle so a miss is legible instead of a
			// bare "no match" (e.g. "for BMW C400GT 2025").
			want := ""
			if d := describeFind(data); d != "" {
				want = " for " + strings.TrimSuffix(d, ". ")
			}
			if research {
				reply = "Searching" + want + ":"
			}
			if len(combined) == 0 {
				reply += "\nNothing in stock" + want + " right now. Reply another model to keep searching, or *interested* and our team will call you."
			} else {
				if len(exact) == 0 {
					reply += "\nNo exact match" + want + ", but similar options (reply the number to see all photos):\n"
				} else {
					reply += "\nTop picks (reply the number to see all photos):\n"
				}
				first := combined
				if len(first) > 3 {
					first = first[:3]
				}
				reply += strings.Join(numbered(first, 1), "\n")
				ids := make([]string, 0, len(combined))
				for _, it := range combined {
					ids = append(ids, it.id)
				}
				data["match_ids"] = strings.Join(ids, ",")
				m, _ := json.Marshal(data)
				_, _ = tx.Exec(ctx, `UPDATE leads SET state_data=$1 WHERE id=$2`, string(m), leadID)
				// One teaser photo of the top car; the full set comes when
				// the buyer picks the number or asks for more photos.
				if len(first) > 0 {
					photoJobs = append(photoJobs, w.vehiclePhotoJobs(w.vehiclePhotosTx(ctx, tx, first[0].id, 1), first[0].text, 1)...)
				}
			}
			_, _ = tx.Exec(ctx, `INSERT INTO requirements(lead_id,budget_min,budget_max,brand,model,fuel,transmission,year_min)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
				leadID, atoi(data["budget_min"]), atoi(data["budget_max"]), data["brand"], data["model"], data["fuel"], data["transmission"], atoi(data["year_min"]))
			_, _ = tx.Exec(ctx, `INSERT INTO followups(customer_id,lead_id,type,scheduled_at,message) VALUES($1,$2,'post_match',now()+interval '24 hours','Follow up on matched cars') ON CONFLICT DO NOTHING`, custID, leadID)
		}
	}

	// SELL done -> valuation handoff row (idempotent per lead). Guarded by
	// prevState so a hijacked DONE (e.g. finance keyword mid-sell, now
	// scoped out above, or any future global) can never file a junk
	// VALUATION_PENDING row from a half-filled form.
	if prevState == "SELL_PHOTOS" && next == "DONE" && intent == "SELL" {
		_, _ = tx.Exec(ctx, `INSERT INTO sell_requests(lead_id,brand,model,year,registration,km,fuel,transmission,condition,location,photo_count,status)
			SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'VALUATION_PENDING'
			WHERE NOT EXISTS (SELECT 1 FROM sell_requests WHERE lead_id=$1)`,
			leadID, data["sell_brand"], data["sell_model"], atoi(data["sell_year"]), data["sell_reg"],
			atoi(data["sell_km"]), data["sell_fuel"], data["sell_trans"], data["sell_specs"], data["sell_location"], atoi(data["sell_photos"]))
	}

	// Finance enquiry captured (income is INT: use 0, never '').
	if fr, ok := data["finance_raw"]; ok && fr != "" && next == "DONE" {
		_, _ = tx.Exec(ctx, `INSERT INTO finance_requests(lead_id,loan_amount,tenure_months,employment,income,status)
			SELECT $1,0,0,'',0, 'NEW' WHERE NOT EXISTS (SELECT 1 FROM finance_requests WHERE lead_id=$1)`, leadID)
	}

	// Test-drive details arrived: book a real slot when possible, otherwise
	// reopen the question. Either way the generic sales-followup is skipped.
	tdHandled := false
	if state == "TESTDRIVE_ASK" && next == "DONE" {
		tdHandled = true
		if tdReply, reopened := w.bookTestDriveTx(ctx, tx, leadID, custID, body, data); reopened {
			next = "TESTDRIVE_ASK"
			merged2, _ := json.Marshal(dataWithPrev(data, "TESTDRIVE_ASK"))
			_, _ = tx.Exec(ctx, `UPDATE leads SET state='TESTDRIVE_ASK',state_data=$1,updated_at=now() WHERE id=$2`, string(merged2), leadID)
			reply = tdReply
		} else {
			reply = tdReply
		}
	}

	// Test-drive request without a firm slot -> sales followup
	if tr, ok := data["testdrive_raw"]; ok && tr != "" && next == "DONE" && !tdHandled {
		_, _ = tx.Exec(ctx, `INSERT INTO followups(customer_id,lead_id,type,scheduled_at,message)
			VALUES($1,$2,'testdrive_request',now()+interval '1 hour',$3) ON CONFLICT DO NOTHING`, custID, leadID, "Test drive request: "+tr)
	}

	if reply != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO messages(conversation_id,direction,body,status) VALUES($1,'out',$2,'SENT')`, convID, reply); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	if len(photoJobs) > 0 {
		go w.sendPhotos(phone, photoJobs)
	}
	return reply, nil
}

// markLastOutFailed flags the newest outbound message as FAILED so the
// admin UI no longer shows undelivered replies as SENT.
func (w *Worker) markLastOutFailed(ctx context.Context, phone string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, _ = w.pool.Exec(ctx, `UPDATE messages SET status='FAILED' WHERE id = (
		SELECT m.id FROM messages m JOIN conversations c ON c.id=m.conversation_id
		JOIN customers cu ON cu.id=c.customer_id
		WHERE cu.phone=$1 AND m.direction='out' ORDER BY m.created_at DESC LIMIT 1)`, phone)
}

// HandleMedia stores an inbound photo (INT-002 / SELL-003). The conversation
// stays valid: SELL_PHOTOS progress advances, anything else gets a receipt.
// Returns the ack to send ("" when taken over or duplicate).
func (w *Worker) HandleMedia(ctx context.Context, phone, name, waMsgID string, data []byte) (string, error) {
	lk := w.phoneLock(phone)
	lk.Lock()
	defer lk.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if waMsgID != "" {
		tag, err := w.pool.Exec(ctx, `INSERT INTO processed_messages(wa_msg_id) VALUES($1) ON CONFLICT DO NOTHING`, "media:"+waMsgID)
		if err == nil && tag.RowsAffected() == 0 {
			return "", nil
		}
	}
	rel := ""
	if len(data) > 0 {
		sum := sha256.Sum256(data)
		name := hex.EncodeToString(sum[:]) + ".jpg"
		dir := filepath.Join(w.dataDir, "media")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		rel = "media/" + name
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				return "", err
			}
		}
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var custID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM customers WHERE phone=$1`, phone).Scan(&custID); err != nil {
		custID = uuid.NewString()
		nm := name
		if nm == "" {
			nm = phone
		}
		if _, err := tx.Exec(ctx, `INSERT INTO customers(id,name,phone,source) VALUES($1,$2,$3,'whatsapp')`, custID, nm, phone); err != nil {
			return "", err
		}
	}
	var convID string
	var botEnabled = true
	if err := tx.QueryRow(ctx, `SELECT id::text, bot_enabled FROM conversations WHERE customer_id=$1 AND status='open' ORDER BY created_at DESC LIMIT 1`, custID).Scan(&convID, &botEnabled); err != nil {
		convID = uuid.NewString()
		if _, err := tx.Exec(ctx, `INSERT INTO conversations(id,customer_id,channel,status) VALUES($1,$2,'whatsapp','open')`, convID, custID); err != nil {
			return "", err
		}
	}
	var leadID, state string
	var stateRaw []byte
	if err := tx.QueryRow(ctx, `SELECT id::text, state, state_data FROM leads WHERE customer_id=$1 ORDER BY created_at DESC LIMIT 1`, custID).Scan(&leadID, &state, &stateRaw); err != nil {
		leadID = uuid.NewString()
		state = "NEW"
		stateRaw = []byte("{}")
		_, _ = tx.Exec(ctx, `INSERT INTO leads(id,customer_id,intent,status,state,source) VALUES($1,$2,'UNKNOWN','NEW','NEW','whatsapp')`, leadID, custID)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO messages(conversation_id,direction,body,media_path,status) VALUES($1,'in','[photo]',$2,'PROCESSED')`, convID, rel); err != nil {
		return "", err
	}
	if !botEnabled {
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return "", nil
	}
	ack := "Photo received and saved."
	if state == "DONE" {
		// Request already closed (e.g. valuation recorded): don't reopen the
		// flow with photo noise, just close the loop politely.
		ack = "Thanks! Your request is already recorded — our team will call you. Reply BUY, SELL or EXCHANGE for anything new."
	} else if state == "SELL_PHOTOS" {
		data := map[string]string{}
		_ = json.Unmarshal(stateRaw, &data)
		n := atoi(data["sell_photos"]) + 1
		if n >= 10 {
			ack = "10 photos are enough, thank you! Reply *DONE* and we'll proceed to valuation."
			n = 10
		} else {
			ack = "Photo saved. Keep sending, then reply *DONE*."
		}
		data["sell_photos"] = strconv.Itoa(n)
		m, _ := json.Marshal(dataWithPrev(data, state))
		_, _ = tx.Exec(ctx, `UPDATE leads SET state_data=$1, updated_at=now() WHERE id=$2`, string(m), leadID)
	} else {
		ack += " Our team can view it. How can I help — *BUY*, *SELL* or *EXCHANGE*?"
	}
	_, _ = tx.Exec(ctx, `INSERT INTO messages(conversation_id,direction,body,status) VALUES($1,'out',$2,'SENT')`, convID, ack)
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return ack, nil
}

func dataWithPrev(data map[string]string, prev string) map[string]string {
	m := map[string]string{}
	for k, v := range data {
		m[k] = v
	}
	if validStates[prev] {
		m["prev_state"] = prev
	}
	return m
}

func atoi(s string) int {
	var v int
	_, _ = fmt.Sscan(s, &v)
	return v
}

func pageSlice(in []string, page int) []string {
	start := page * 3
	if start >= len(in) {
		return nil
	}
	end := start + 3
	if end > len(in) {
		end = len(in)
	}
	return in[start:end]
}

// matchItem is a scored vehicle with display text (numbering added at send
// time so paging and selection stay consistent).
type matchItem struct {
	id   string
	text string
}

func numbered(items []matchItem, start int) []string {
	out := make([]string, 0, len(items))
	for i, it := range items {
		out = append(out, fmt.Sprintf("%d. %s", start+i, it.text))
	}
	return out
}

func pageItems(in []matchItem, page int) []matchItem {
	start := page * 3
	if start >= len(in) {
		return nil
	}
	end := start + 3
	if end > len(in) {
		end = len(in)
	}
	return in[start:end]
}

// modelMatches reports whether a stored vehicle fits the wanted model.
// Spaceless on both sides ("C400GT" = "C 400 GT") and falls back to the
// SDAS model description and make, so odd column splits (model living in
// the description, make holding "BMW C400GT", ...) never hide stock.
// A stored fragment ("c400") also matches a longer query ("c400gt");
// tiny fragments ("gt", "x1") are ignored so they can't match anything.
func modelMatches(make_, model, desc, want string) bool {
	w := nospace(want)
	for _, s := range []string{model, desc, make_} {
		ns := nospace(s)
		if ns == "" {
			continue
		}
		if strings.Contains(ns, w) {
			return true
		}
		if len(ns) >= 4 && strings.Contains(w, ns) {
			return true
		}
	}
	return false
}

// runMatchingTx returns (exact, similar). Exact = in budget + all stated
// filters; similar = same model/brand/budget-adjacent, excluding exact picks.
func runMatchingTx(ctx context.Context, tx pgx.Tx, leadID string, data map[string]string) (exact, similar []matchItem) {
	// Scan the whole lot (SDAS bulk imports can exceed 100 rows; a low
	// LIMIT silently cut matching off before the wanted bikes). 2000 rows
	// of small columns is trivial for pgx and the in-Go scorer.
	rows, err := tx.Query(ctx, `SELECT id::text, make, model, year, price, fuel, transmission, km, COALESCE(model_description,'') FROM vehicles WHERE status='AVAILABLE' LIMIT 2000`)
	if err != nil {
		return nil, nil
	}
	type v struct {
		id, make, model, fuel, trans, desc string
		year, price, km                     int
	}
	var all []v
	for rows.Next() {
		var c v
		if err := rows.Scan(&c.id, &c.make, &c.model, &c.year, &c.price, &c.fuel, &c.trans, &c.km, &c.desc); err == nil {
			all = append(all, c)
		}
	}
	rows.Close() // must close before Execs below: pgx forbids queries on a tx with open rows
	mx := atoi(data["budget_max"])
	mn := atoi(data["budget_min"])
	brand := strings.ToLower(data["brand"])
	model := strings.ToLower(data["model"])
	fuel := strings.ToLower(data["fuel"])
	trans := strings.ToLower(data["transmission"])
	ym := atoi(data["year_min"])
	log.Printf("[match] lead=%s filters brand=%q model=%q year>=%d budget<=%d avail=%d", leadID, brand, model, ym, mx, len(all))

	isExact := func(c v) (bool, int) {
		score := 0
		if mx > 0 {
			if c.price > mx {
				return false, 0
			}
			score += 3
			if mn == 0 || c.price >= mn {
				score++
			}
		}
		if brand != "" && brand != "any" && !strings.Contains(strings.ToLower(c.make), brand) {
			return false, 0
		}
		score += 2
		if model != "" && model != "any" && !modelMatches(c.make, c.model, c.desc, model) {
			return false, 0
		}
		if model != "" && model != "any" {
			score += 2
		}
		if fuel != "" && fuel != "any" && c.fuel != "" && !strings.EqualFold(c.fuel, data["fuel"]) {
			return false, 0
		}
		if fuel != "" && fuel != "any" {
			score++
		}
		if trans != "" && trans != "any" && c.trans != "" && !strings.Contains(strings.ToLower(c.trans), trans) {
			return false, 0
		}
		if trans != "" && trans != "any" {
			score++
		}
		if ym > 0 && c.year < ym {
			return false, 0
		}
		if ym > 0 {
			score++
		}
		if score == 0 && (mx > 0 || brand != "" || model != "" || fuel != "" || trans != "" || ym > 0) {
			return false, 0
		}
		return true, score
	}
	exactIDs := map[string]bool{}
	for _, c := range all {
		ok, score := isExact(c)
		if !ok {
			continue
		}
		_, _ = tx.Exec(ctx, `INSERT INTO vehicle_matches(lead_id,vehicle_id,score) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, leadID, c.id, score)
		exactIDs[c.id] = true
		exact = append(exact, matchItem{id: c.id, text: fmt.Sprintf("%s %s %d — RM%d%s", c.make, c.model, c.year, c.price, specSuffix(c.fuel, c.trans))})
	}
	// Similar: same model (different year/price), same brand, budget-adjacent,
	// or same fuel — excluding exact. Ranked by relevance so a same-model
	// bike is never buried under unrelated cars (was: DB order, cap 9).
	type scored struct {
		it    matchItem
		score int
	}
	var ranked []scored
	for _, c := range all {
		if exactIDs[c.id] {
			continue
		}
		score := 0
		if model != "" && model != "any" && modelMatches(c.make, c.model, c.desc, model) {
			score += 4 // same model family first (e.g. other C400GT years)
		}
		if brand != "" && brand != "any" && strings.Contains(strings.ToLower(c.make), brand) {
			score += 2
		}
		if mx > 0 && c.price >= mx*75/100 && c.price <= mx*125/100 {
			score += 2
		}
		if fuel != "" && fuel != "any" && strings.EqualFold(c.fuel, data["fuel"]) && mx == 0 {
			score += 1
		}
		if score == 0 {
			continue
		}
		ranked = append(ranked, scored{
			it:    matchItem{id: c.id, text: fmt.Sprintf("%s %s %d — RM%d%s", c.make, c.model, c.year, c.price, specSuffix(c.fuel, c.trans))},
			score: score,
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	for i, s := range ranked {
		if i >= 1000 {
			break
		}
		similar = append(similar, s.it)
	}
	log.Printf("[match] lead=%s result exact=%d similar=%d", leadID, len(exact), len(similar))
	return exact, similar
}

// specSuffix renders " (PETROL/AUTOMATIC)", " (PETROL)" or "" — never "(/)"
// for SDAS rows with blank fuel/transmission.
func specSuffix(fuel, trans string) string {
	fuel, trans = strings.TrimSpace(fuel), strings.TrimSpace(trans)
	switch {
	case fuel != "" && trans != "":
		return " (" + fuel + "/" + trans + ")"
	case fuel != "":
		return " (" + fuel + ")"
	case trans != "":
		return " (" + trans + ")"
	default:
		return ""
	}
}
