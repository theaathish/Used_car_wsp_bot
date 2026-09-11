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
}

func New(pool *pgxpool.Pool, dataDir string, enabled bool, dbURL string) *Worker {
	return &Worker{pool: pool, dataDir: dataDir, enabled: enabled, dbURL: dbURL, status: "connecting"}
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
	return map[string]any{"status": w.status, "jid": w.lastJID, "enabled": w.enabled, "has_qr": w.lastQR != ""}
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
	w.status = s
	if jid != "" {
		w.lastJID = jid
	}
	w.mu.Unlock()
	_, _ = w.pool.Exec(context.Background(),
		`INSERT INTO whatsapp_sessions(id,connected,jid) VALUES('default',$1,$2)
		 ON CONFLICT (id) DO UPDATE SET connected=$1, jid=$2, updated_at=now()`,
		s == "connected", w.lastJID)
}

// Start connects; blocks only for initial setup, runs event loop in background.
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
	var container *sqlstore.Container
	if w.dbURL != "" {
		if c, err := sqlstore.New(ctx, "pgx", w.dbURL, waLog.Noop); err != nil {
			log.Printf("[whatsapp] postgres session store unavailable, falling back to file: %v", err)
		} else {
			container = c
			log.Println("[whatsapp] session store: postgres")
		}
	}
	if container == nil {
		dbPath := filepath.Join(w.dataDir, "whatsapp.db")
		c, err := sqlstore.New(ctx, "sqlite3", "file:"+dbPath+"?_foreign_keys=on", waLog.Noop)
		if err != nil {
			log.Printf("[whatsapp] store: %v (stub mode)", err)
			w.setStatus("disabled", "")
			return
		}
		container = c
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		log.Printf("[whatsapp] device: %v", err)
		w.setStatus("disabled", "")
		return
	}
	w.client = whatsmeow.NewClient(device, waLog.Noop)
	w.client.AddEventHandler(w.onEvent)
	if w.client.Store.ID == nil {
		w.setStatus("qr", "")
		qrCh, _ := w.client.GetQRChannel(ctx)
		if err := w.client.Connect(); err != nil {
			log.Printf("[whatsapp] connect: %v", err)
			w.setStatus("qr", "")
			return
		}
		for evt := range qrCh {
			if evt.Event == "code" {
				w.mu.Lock()
				w.lastQR = evt.Code
				w.mu.Unlock()
				log.Println("[whatsapp] QR ready — open /admin to scan")
			} else if evt.Event == "success" {
				w.mu.Lock()
				w.lastQR = ""
				w.mu.Unlock()
				w.setStatus("connected", "")
				log.Println("[whatsapp] paired OK")
				break
			}
		}
	} else {
		// Reconnect loop with backoff (WA-104/105/107): never spin hot,
		// surface terminal logout so an admin can re-authenticate (WA-107).
		backoff := 2 * time.Second
		for {
			w.setStatus("connecting", "")
			if err := w.client.Connect(); err != nil {
				log.Printf("[whatsapp] reconnect failed: %v (retry in %s)", err, backoff)
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				backoff *= 2
				if backoff > 5*time.Minute {
					backoff = 5 * time.Minute
				}
				continue
			}
			backoff = 2 * time.Second
			w.setStatus("connected", w.client.Store.ID.String())
			select {
			case <-ctx.Done():
				w.client.Disconnect()
				return
			case <-time.After(30 * time.Second):
				if !w.client.IsConnected() && w.client.Store.ID != nil {
					log.Println("[whatsapp] connection lost, retrying")
					w.client.Disconnect()
					continue
				}
			}
		}
	}
}

func (w *Worker) onEvent(evt any) {
	switch e := evt.(type) {
	case *events.LoggedOut:
		log.Printf("[whatsapp] logged out (reason %v) — admin re-auth required", e.Reason)
		w.setStatus("logged_out", "")
		return
	case *events.Disconnected:
		log.Println("[whatsapp] disconnected, reconnect loop continues")
		w.setStatus("connecting", w.lastJID)
		return
	}
	m, ok := evt.(*events.Message)
	if !ok || m.Info.IsFromMe {
		return
	}
	// WhatsApp increasingly addresses senders by LID (...@lid) instead of
	// phone number. Always resolve to the phone-number (PN) address so the
	// customer identity is stable and replies are deliverable.
	sender := m.Info.Sender
	if alt := m.Info.SenderAlt; alt.User != "" && alt.Server == types.DefaultUserServer && sender.Server != types.DefaultUserServer {
		sender = alt
	}
	phone := normalizePhone(sender.User)
	name := m.Info.PushName
	if phone == "" {
		return
	}
	waID := string(m.Info.ID)
	if img := m.Message.GetImageMessage(); img != nil {
		w.onImage(phone, name, waID, img)
		return
	}
	body := messageText(m.Message)
	if body == "" {
		return
	}
	reply, err := w.HandleInbound(context.Background(), phone, name, body, waID)
	if err != nil {
		log.Printf("[whatsapp] inbound %s: %v", phone, err)
		return
	}
	if reply != "" {
		if err := w.Send(context.Background(), phone, reply); err != nil {
			log.Printf("[whatsapp] send to %s failed: %v (queued in outbox)", phone, err)
			w.markLastOutFailed(context.Background(), phone)
		}
	}
}

// onImage stores inbound media without breaking the conversation (INT-002).
func (w *Worker) onImage(phone, name, waID string, img *waE2E.ImageMessage) {
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
		_ = w.Send(ctx, phone, ack)
	}
}

func messageText(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	if t := m.GetConversation(); t != "" {
		return t
	}
	if m.ExtendedTextMessage != nil {
		return m.ExtendedTextMessage.GetText()
	}
	return ""
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

// Send delivers text via whatsmeow (stub-logs when disabled), enqueueing to
// outbox on transport failure so WA-F003 PENDING->SENT recovery holds.
// A 2s per-phone cooldown queues (not drops) bursts (§21: no spam behavior).
func (w *Worker) Send(ctx context.Context, phone, text string) error {
	w.sendMu.Lock()
	if w.lastTx == nil {
		w.lastTx = map[string]time.Time{}
	}
	if dt := time.Since(w.lastTx[phone]); dt < 2*time.Second {
		w.sendMu.Unlock()
		_, _ = w.pool.Exec(ctx, `INSERT INTO outbox(phone,body,status) VALUES($1,$2,'PENDING') ON CONFLICT DO NOTHING`, phone, text)
		return nil
	}
	w.lastTx[phone] = time.Now()
	w.sendMu.Unlock()

	w.mu.RLock()
	cli := w.client
	st := w.status
	w.mu.RUnlock()
	if !w.enabled || cli == nil || st != "connected" {
		log.Printf("[whatsapp:stub] -> %s: %s", phone, text)
		return nil
	}
	jid := types.NewJID(phone, types.DefaultUserServer)
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
		jid := types.NewJID(it.phone, types.DefaultUserServer)
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

	// open conversation (STATE-007: human takeover flag)
	var convID string
	var botEnabled = true
	err = tx.QueryRow(ctx, `SELECT id::text, bot_enabled FROM conversations WHERE customer_id=$1 AND status='open' ORDER BY created_at DESC LIMIT 1`, custID).Scan(&convID, &botEnabled)
	if err != nil {
		convID = uuid.NewString()
		if _, err := tx.Exec(ctx, `INSERT INTO conversations(id,customer_id,channel,status) VALUES($1,$2,'whatsapp','open')`, convID, custID); err != nil {
			return "", err
		}
	}
	// lead (latest non-terminal)
	var leadID, state, intent, status, interest string
	var stateRaw []byte
	err = tx.QueryRow(ctx, `SELECT id::text, state, intent, status, COALESCE(interest,''), state_data FROM leads WHERE customer_id=$1 ORDER BY created_at DESC LIMIT 1`, custID).Scan(&leadID, &state, &intent, &status, &interest, &stateRaw)
	if err != nil || status == "DONE" || status == "LOST" {
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
		reply := "No problem — starting fresh. Are you looking to *BUY*, *SELL* or *EXCHANGE* a car?"
		merged, _ := json.Marshal(data)
		_, _ = tx.Exec(ctx, `UPDATE leads SET state='ASK_INTENT',intent='UNKNOWN',status='CONTACTED',state_data=$1,updated_at=now() WHERE id=$2`, string(merged), leadID)
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
	if _, err := tx.Exec(ctx, `INSERT INTO messages(conversation_id,direction,body,status) VALUES($1,'out',$2,'SENT')`, convID, reply); err != nil {
		return "", err
	}

	// BUY_RESULTS: first arrival runs matching; "more cars" pages forward.
	if next == "BUY_RESULTS" {
		page := atoi(data["page_num"])
		if moreCars {
			page++
			data["page_num"] = strconv.Itoa(page)
			m, _ := json.Marshal(data)
			_, _ = tx.Exec(ctx, `UPDATE leads SET state_data=$1 WHERE id=$2`, string(m), leadID)
			exact, similar := runMatchingTx(ctx, tx, leadID, data)
			_ = exact
			extra := pageSlice(similar, page)
			if len(extra) == 0 {
				reply = "That's all matching cars for now. Our team will call you with fresh arrivals."
			} else {
				reply = "More options:\n" + strings.Join(extra, "\n")
			}
		} else if state != "BUY_RESULTS" {
			exact, similar := runMatchingTx(ctx, tx, leadID, data)
			if len(exact) == 0 && len(similar) == 0 {
				reply += "\nWe couldn't find an exact match. Would you like to see similar vehicles? Reply *more cars*."
			} else if len(exact) == 0 {
				reply += "\nNo exact match, but similar options:\n" + strings.Join(pageSlice(similar, 0), "\n")
			} else {
				reply += "\nTop picks:\n" + strings.Join(pageSlice(exact, 0), "\n")
			}
			_, _ = tx.Exec(ctx, `INSERT INTO requirements(lead_id,budget_min,budget_max,brand,model,fuel,transmission,year_min)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
				leadID, atoi(data["budget_min"]), atoi(data["budget_max"]), data["brand"], data["model"], data["fuel"], data["transmission"], atoi(data["year_min"]))
			_, _ = tx.Exec(ctx, `INSERT INTO messages(conversation_id,direction,body,status) VALUES($1,'out',$2,'SENT')`, convID, reply)
			_, _ = tx.Exec(ctx, `INSERT INTO followups(customer_id,lead_id,type,scheduled_at,message) VALUES($1,$2,'post_match',now()+interval '24 hours','Follow up on matched cars') ON CONFLICT DO NOTHING`, custID, leadID)
		}
	}

	// SELL done -> valuation handoff row (idempotent per lead)
	if next == "DONE" && intent == "SELL" {
		_, _ = tx.Exec(ctx, `INSERT INTO sell_requests(lead_id,brand,model,year,registration,km,fuel,transmission,condition,location,photo_count,status)
			SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'VALUATION_PENDING'
			WHERE NOT EXISTS (SELECT 1 FROM sell_requests WHERE lead_id=$1)`,
			leadID, data["sell_brand"], data["sell_model"], atoi(data["sell_year"]), data["sell_reg"],
			atoi(data["sell_km"]), data["sell_fuel"], data["sell_trans"], data["sell_specs"], data["sell_location"], atoi(data["sell_photos"]))
	}

	// Finance enquiry captured
	if fr, ok := data["finance_raw"]; ok && fr != "" && next == "DONE" {
		_, _ = tx.Exec(ctx, `INSERT INTO finance_requests(lead_id,loan_amount,tenure_months,employment,income,status)
			SELECT $1,0,0,'','', 'NEW' WHERE NOT EXISTS (SELECT 1 FROM finance_requests WHERE lead_id=$1)`, leadID)
	}

	// Test-drive request without a firm slot -> sales followup
	if tr, ok := data["testdrive_raw"]; ok && tr != "" && next == "DONE" {
		_, _ = tx.Exec(ctx, `INSERT INTO followups(customer_id,lead_id,type,scheduled_at,message)
			VALUES($1,$2,'testdrive_request',now()+interval '1 hour',$3) ON CONFLICT DO NOTHING`, custID, leadID, "Test drive request: "+tr)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
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

// runMatchingTx returns (exact, similar). Exact = in budget + all stated
// filters; similar = same brand OR budget-adjacent, excluding exact picks.
func runMatchingTx(ctx context.Context, tx pgx.Tx, leadID string, data map[string]string) (exact, similar []string) {
	rows, err := tx.Query(ctx, `SELECT id::text, make, model, year, price, fuel, transmission, km FROM vehicles WHERE status='AVAILABLE' LIMIT 100`)
	if err != nil {
		return nil, nil
	}
	type v struct {
		id, make, model, fuel, trans string
		year, price, km              int
	}
	var all []v
	for rows.Next() {
		var c v
		if err := rows.Scan(&c.id, &c.make, &c.model, &c.year, &c.price, &c.fuel, &c.trans, &c.km); err == nil {
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
		if model != "" && model != "any" && !strings.Contains(strings.ToLower(c.model), model) {
			return false, 0
		}
		if model != "" && model != "any" {
			score += 2
		}
		if fuel != "" && fuel != "any" && !strings.EqualFold(c.fuel, data["fuel"]) {
			return false, 0
		}
		if fuel != "" && fuel != "any" {
			score++
		}
		if trans != "" && trans != "any" && !strings.Contains(strings.ToLower(c.trans), trans) {
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
	n := 0
	for _, c := range all {
		ok, score := isExact(c)
		if !ok {
			continue
		}
		_, _ = tx.Exec(ctx, `INSERT INTO vehicle_matches(lead_id,vehicle_id,score) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, leadID, c.id, score)
		exactIDs[c.id] = true
		n++
		exact = append(exact, fmt.Sprintf("%d. %s %s %d — Rs.%d (%s/%s)", n, c.make, c.model, c.year, c.price, c.fuel, c.trans))
	}
	// Similar: same brand, or within ±25% of budget, or same fuel — excluding exact.
	m := 0
	for _, c := range all {
		if exactIDs[c.id] {
			continue
		}
		sim := false
		if brand != "" && brand != "any" && strings.Contains(strings.ToLower(c.make), brand) {
			sim = true
		}
		if mx > 0 && c.price >= mx*75/100 && c.price <= mx*125/100 {
			sim = true
		}
		if fuel != "" && fuel != "any" && strings.EqualFold(c.fuel, data["fuel"]) && mx == 0 {
			sim = true
		}
		if !sim {
			continue
		}
		m++
		similar = append(similar, fmt.Sprintf("%d. %s %s %d — Rs.%d (%s/%s)", m, c.make, c.model, c.year, c.price, c.fuel, c.trans))
		if m >= 9 {
			break
		}
	}
	return exact, similar
}
