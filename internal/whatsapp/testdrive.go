package whatsapp

// Conversational test-drive booking: turn "Swift, tomorrow 10am" into a real
// slot. All interpretation happens in IST (fixed offset, no tzdata needed).

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ist = time.FixedZone("IST", 5*3600+1800)

type tdVehicle struct {
	id, make, model string
}

type tdResult struct {
	vehicleID            string
	at                   time.Time // UTC
	needVehicle          bool
	needDate             bool
	needTime             bool
	past                 bool
}

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
	"wednesday": time.Wednesday, "thursday": time.Thursday, "friday": time.Friday,
	"saturday": time.Saturday,
}

func parseTestDrive(body string, candidates []tdVehicle, now time.Time) tdResult {
	b := norm(body)
	var r tdResult

	// Vehicle: explicit number ("1", "car 2", leading "2, ..."), else
	// model/make mention.
	if n := parseSelection(body); n > 0 && n <= len(candidates) {
		r.vehicleID = candidates[n-1].id
	} else if m := regexp.MustCompile(`^\s*([1-9])\b`).FindStringSubmatch(b); m != nil {
		if n, _ := strconv.Atoi(m[1]); n <= len(candidates) {
			r.vehicleID = candidates[n-1].id
		}
	} else {
		for _, c := range candidates {
			if c.model != "" && c.model != "ANY" && strings.Contains(b, strings.ToLower(c.model)) {
				r.vehicleID = c.id
				break
			}
		}
		if r.vehicleID == "" {
			for _, c := range candidates {
				if c.make != "" && c.make != "ANY" && strings.Contains(b, strings.ToLower(c.make)) {
					r.vehicleID = c.id
					break
				}
			}
		}
	}
	if r.vehicleID == "" {
		if len(candidates) == 1 {
			r.vehicleID = candidates[0].id
		} else {
			r.needVehicle = true
		}
	}

	// Date.
	todayIST := now.In(ist)
	day := todayIST.Truncate(24 * time.Hour)
	dateFound := false
	switch {
	case strings.Contains(b, "day after"):
		day = day.AddDate(0, 0, 2)
		dateFound = true
	case strings.Contains(b, "tomorow") || strings.Contains(b, "tomorrow") || strings.Contains(b, "tmrw") || strings.Contains(b, "2morrow"):
		day = day.AddDate(0, 0, 1)
		dateFound = true
	case strings.Contains(b, "today") || strings.Contains(b, "tonight") || strings.Contains(b, "this evening"):
		dateFound = true
	default:
		if m := regexp.MustCompile(`(\d{1,2})\s*[/-]\s*(\d{1,2})(?:\s*[/-]\s*(\d{2,4}))?`).FindStringSubmatch(b); m != nil {
			dd, _ := strconv.Atoi(m[1])
			mm, _ := strconv.Atoi(m[2])
			yy := todayIST.Year()
			if len(m) == 4 && m[3] != "" {
				y, _ := strconv.Atoi(m[3])
				if y < 100 {
					y += 2000
				}
				yy = y
			}
			if dd >= 1 && dd <= 31 && mm >= 1 && mm <= 12 {
				day = time.Date(yy, time.Month(mm), dd, 0, 0, 0, 0, ist)
				if day.Before(todayIST.Truncate(24 * time.Hour)) {
					day = day.AddDate(1, 0, 0)
				}
				dateFound = true
			}
		}
		if !dateFound {
			for name, wd := range weekdays {
				if strings.Contains(b, name) {
					ahead := (int(wd) - int(todayIST.Weekday()) + 7) % 7
					if ahead == 0 {
						ahead = 7
					}
					day = todayIST.Truncate(24 * time.Hour).AddDate(0, 0, ahead)
					dateFound = true
					break
				}
			}
		}
	}
	if !dateFound {
		r.needDate = true
		return r
	}

	// Time. Strip years, prices, dates and kms first so "2016" or "55000"
	// can never be read as a time. Explicit am/pm or colon wins; bare
	// showroom hours otherwise (8-11am, 12 noon, 1-7pm).
	t := regexp.MustCompile(`\d{4,}`).ReplaceAllString(b, " ")
	t = regexp.MustCompile(`\d{1,2}\s*[/-]\s*\d{1,2}`).ReplaceAllString(t, " ")
	t = regexp.MustCompile(`\d+\s?km`).ReplaceAllString(t, " ")
	t = regexp.MustCompile(`[\d,]+\s*(lakh|lac|l|k)\b`).ReplaceAllString(t, " ")
	hour, min := -1, 0
	timeFound := false
	if m := regexp.MustCompile(`(\d{1,2})(?::(\d{2}))?\s*(am|pm)`).FindStringSubmatch(t); m != nil {
		h, _ := strconv.Atoi(m[1])
		mi := 0
		if m[2] != "" {
			mi, _ = strconv.Atoi(m[2])
		}
		if m[3] == "am" && h == 12 {
			h = 0
		} else if m[3] == "pm" && h < 12 {
			h += 12
		}
		if h >= 0 && h < 24 && mi < 60 {
			hour, min = h, mi
			timeFound = true
		}
	} else if m := regexp.MustCompile(`\b(\d{1,2}):(\d{2})\b`).FindStringSubmatch(t); m != nil {
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		if h < 24 && mi < 60 {
			hour, min = h, mi
			timeFound = true
		}
	} else if m := regexp.MustCompile(`\b(8|9|10|11|12)\b`).FindStringSubmatch(t); m != nil {
		hour, _ = strconv.Atoi(m[1])
		timeFound = true
	} else if m := regexp.MustCompile(`\b([1-7])\b`).FindStringSubmatch(t); m != nil {
		hour, _ = strconv.Atoi(m[1])
		hour += 12 // showroom hours: bare 4 means 4pm, never 4am
		timeFound = true
	}
	if !timeFound {
		r.needTime = true
		return r
	}
	at := time.Date(day.Year(), day.Month(), day.Day(), hour, min, 0, 0, ist)
	if !at.After(now.Add(30 * time.Minute)) {
		r.past = true
		return r
	}
	r.at = at.UTC()
	return r
}

// candidatesTx loads match-list vehicles for test-drive resolution.
func candidatesTx(ctx context.Context, tx pgx.Tx, matchIDs string) []tdVehicle {
	var out []tdVehicle
	for _, id := range strings.Split(matchIDs, ",") {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		var c tdVehicle
		if err := tx.QueryRow(ctx, `SELECT id::text, make, model FROM vehicles WHERE id=$1 AND status='AVAILABLE'`, id).Scan(&c.id, &c.make, &c.model); err == nil {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		rows, err := tx.Query(ctx, `SELECT id::text, make, model FROM vehicles WHERE status='AVAILABLE' ORDER BY created_at DESC LIMIT 20`)
		if err != nil {
			return out
		}
		for rows.Next() {
			var c tdVehicle
			if err := rows.Scan(&c.id, &c.make, &c.model); err == nil {
				out = append(out, c)
			}
		}
		rows.Close()
	}
	return out
}

// bookTestDriveTx tries a real booking. Returns (reply, reopened): reopened
// means the bot must ask again (missing info or taken slot).
func (w *Worker) bookTestDriveTx(ctx context.Context, tx pgx.Tx, leadID, custID, body string, data map[string]string) (string, bool) {
	cands := candidatesTx(ctx, tx, data["match_ids"])
	res := parseTestDrive(body, cands, time.Now())
	switch {
	case res.needVehicle:
		return "Which car? Reply the number from the list (e.g. *1*), or the model name with day and time.", true
	case res.needDate:
		return "Which day? Reply *today*, *tomorrow* or a weekday — e.g. *tomorrow 10am*.", true
	case res.needTime:
		return "What time? Reply e.g. *10am* or *4:30pm* with the day.", true
	case res.past:
		return "That time has already passed — please pick a future slot.", true
	}
	var mk, md string
	_ = tx.QueryRow(ctx, `SELECT make, model FROM vehicles WHERE id=$1`, res.vehicleID).Scan(&mk, &md)
	// Speculative insert: a taken slot must NOT poison the tx (Postgres
	// aborts everything after any failed statement), so guard it.
	_, _ = tx.Exec(ctx, "SAVEPOINT td_book")
	if _, err := tx.Exec(ctx, `INSERT INTO test_drives(lead_id,vehicle_id,scheduled_at,notes) VALUES($1,$2,$3,'via WhatsApp')`,
		leadID, res.vehicleID, res.at); err != nil {
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT td_book")
		log.Printf("[td] insert: %v", err)
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return "That slot is already booked. Please pick another day or time.", true
		}
		return "I couldn't lock that slot — please try again or ask our team here.", true
	}
	_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT td_book")
	_, _ = tx.Exec(ctx, `UPDATE leads SET status='TEST_DRIVE' WHERE id=$1`, leadID)
	_, _ = tx.Exec(ctx, `INSERT INTO followups(customer_id,lead_id,type,scheduled_at,message) VALUES($1,$2,'post_match',now()+interval '24 hours','Follow up after test drive') ON CONFLICT DO NOTHING`, custID, leadID)
	return fmt.Sprintf("Test drive confirmed: *%s %s* on %s. We'll remind you before. Reply here to change it.",
		mk, md, res.at.In(ist).Format("Mon 2 Jan, 3:04 PM")), false
}
