package whatsapp

// Buyer-facing media + detail helpers: vehicle photos over WhatsApp,
// numbered-selection details, test-drive booking inside the chat.

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

type photoJob struct {
	path    string // absolute file path
	caption string
}

// SendImage delivers a photo with caption. Stub-logs when disconnected so
// simulators and tests flow through without a live session.
func (w *Worker) SendImage(ctx context.Context, phone, absPath, caption string) error {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > 15<<20 {
		return fmt.Errorf("bad image size %d", len(data))
	}
	w.mu.RLock()
	cli := w.client
	st := w.status
	w.mu.RUnlock()
	if !w.enabled || cli == nil || st != "connected" {
		log.Printf("[whatsapp:stub-image] -> %s: %s (%d bytes)", phone, caption, len(data))
		return nil
	}
	up, err := cli.Upload(ctx, data, whatsmeow.MediaImage)
	if err != nil {
		return err
	}
	msg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption:       proto.String(caption),
		Mimetype:      proto.String(http.DetectContentType(data)),
		URL:           &up.URL,
		DirectPath:    &up.DirectPath,
		MediaKey:      up.MediaKey,
		FileEncSHA256: up.FileEncSHA256,
		FileSHA256:    up.FileSHA256,
		FileLength:    &up.FileLength,
	}}
	_, err = cli.SendMessage(ctx, phoneToJID(phone), msg)
	return err
}

func (w *Worker) sendPhotos(phone string, jobs []photoJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for i, j := range jobs {
		if i > 0 {
			time.Sleep(500 * time.Millisecond)
		}
		if err := w.SendImage(ctx, phone, j.path, j.caption); err != nil {
			log.Printf("[whatsapp] photo to %s failed: %v", phone, err)
		}
	}
}

// vehiclePhotoJobs resolves stored relative paths to existing files.
func (w *Worker) vehiclePhotoJobs(relPaths []string, caption string, max int) []photoJob {
	var out []photoJob
	for _, rel := range relPaths {
		if len(out) >= max {
			break
		}
		abs := filepath.Join(w.dataDir, rel)
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() {
			continue
		}
		out = append(out, photoJob{path: abs, caption: caption})
	}
	return out
}

func (w *Worker) vehiclePhotosTx(ctx context.Context, tx pgx.Tx, vehicleID string, limit int) []string {
	rows, err := tx.Query(ctx, `SELECT path FROM vehicle_images WHERE vehicle_id=$1 ORDER BY sort_order LIMIT $2`, vehicleID, limit)
	if err != nil {
		return nil
	}
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err == nil && p != "" {
			out = append(out, p)
		}
	}
	rows.Close()
	return out
}

type vehicleDetail struct {
	id, text, caption string
	photos            []string
}

// vehicleDetails resolves selection N (1-based) against the stored match list.
func (w *Worker) vehicleDetails(ctx context.Context, tx pgx.Tx, matchIDs string, sel int) (vehicleDetail, bool) {
	ids := strings.Split(matchIDs, ",")
	if sel < 1 || sel > len(ids) || strings.TrimSpace(ids[0]) == "" {
		return vehicleDetail{}, false
	}
	return w.vehicleByID(ctx, tx, strings.TrimSpace(ids[sel-1]))
}

// vehicleByID loads one vehicle card (SDAS fields appended when present).
func (w *Worker) vehicleByID(ctx context.Context, tx pgx.Tx, id string) (vehicleDetail, bool) {
	var vd vehicleDetail
	var mk, md, fuel, trans, desc, status string
	var stockNo, stockLoc, regNum, colour, stockStatus, warranty, claims string
	var year, price, km int
	if err := tx.QueryRow(ctx, `SELECT make,model,year,price,fuel,transmission,km,COALESCE(description,''),status,
		COALESCE(stock_no,''),COALESCE(stock_location,''),COALESCE(reg_num,''),COALESCE(colour,''),
		COALESCE(stock_status,''),COALESCE(warranty,''),COALESCE(claims,'') FROM vehicles WHERE id=$1`,
		id).Scan(&mk, &md, &year, &price, &fuel, &trans, &km, &desc, &status,
		&stockNo, &stockLoc, &regNum, &colour, &stockStatus, &warranty, &claims); err != nil {
		return vd, false
	}
	vd.id = id
	extra := ""
	if regNum != "" || colour != "" || stockLoc != "" {
		extra += "\n" + strings.TrimSpace(regNum+" · "+colour+" · "+stockLoc)
	}
	if stockStatus != "" || warranty != "" {
		extra += "\n" + strings.TrimSpace(stockStatus+" · "+warranty)
	}
	if claims != "" {
		extra += "\nNote: " + claims
	}
	vd.text = fmt.Sprintf("*%s %s %d* — RM%d\n%s · %s · %s km%s%s%s",
		mk, md, year, price, fuel, trans, itoaComma(km),
		commaDesc(desc), extra,
		notAvailNote(status))
	vd.caption = fmt.Sprintf("%s %s %d — RM%d", mk, md, year, price)
	vd.photos = w.vehiclePhotosTx(ctx, tx, id, 6)
	return vd, true
}

func itoaComma(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

func commaDesc(d string) string {
	if strings.TrimSpace(d) == "" {
		return ""
	}
	return "\n" + d
}

func notAvailNote(status string) string {
	if status != "" && status != "AVAILABLE" {
		return "\nNote: this car was just " + strings.ToLower(status) + " — reply *more cars* for alternatives."
	}
	return ""
}

// matchItemsTx loads display items for stored match ids at [offset, offset+limit).
func (w *Worker) matchItemsTx(ctx context.Context, tx pgx.Tx, matchIDs string, offset, limit int) []matchItem {
	ids := strings.Split(matchIDs, ",")
	var clean []string
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			clean = append(clean, id)
		}
	}
	if offset >= len(clean) {
		return nil
	}
	end := offset + limit
	if end > len(clean) {
		end = len(clean)
	}
	want := clean[offset:end]
	rows, err := tx.Query(ctx, `SELECT id::text, make, model, year, price, fuel, transmission FROM vehicles WHERE id = ANY($1)`, want)
	if err != nil {
		return nil
	}
	byID := map[string]matchItem{}
	for rows.Next() {
		var id, make, model, fuel, trans string
		var year, price int
		if err := rows.Scan(&id, &make, &model, &year, &price, &fuel, &trans); err == nil {
			byID[id] = matchItem{id: id, text: fmt.Sprintf("%s %s %d — RM%d (%s/%s)", make, model, year, price, fuel, trans)}
		}
	}
	rows.Close()
	var out []matchItem
	for _, id := range want {
		if it, ok := byID[id]; ok {
			out = append(out, it)
		}
	}
	return out
}
