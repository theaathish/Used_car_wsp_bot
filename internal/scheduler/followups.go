package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"sellingbot/internal/botengine"
	"sellingbot/internal/whatsapp"
)

// Followups polls every minute: due followups, test-drive reminders
// (24h + 2h), post-test-drive classification, inspection reminders,
// and outbox PENDING->SENT flush. Survives worker restarts
// because all state lives in Postgres.
func Followups(ctx context.Context, pool *pgxpool.Pool, w *whatsapp.Worker) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			runOnce(ctx, pool, w)
			remindTestDrives(ctx, pool, w)
			postTestDriveFollowups(ctx, pool, w)
			remindInspections(ctx, pool, w)
			autoCompleteInspections(ctx, pool)
			autoReviewSellRequests(ctx, pool, w)
			autoReviewFinanceRequests(ctx, pool, w)
			autoPromptReviews(ctx, pool, w)
			w.FlushOutbox(ctx)
		}
	}
}

func runOnce(ctx context.Context, pool *pgxpool.Pool, w *whatsapp.Worker) {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Crash recovery: claims stuck in 'sending' >10min are released.
	_, _ = pool.Exec(c, `UPDATE followups SET status='pending' WHERE status='sending' AND scheduled_at < now() - interval '10 minutes'`)
	// Claim-first (§17): rows flip pending->sending atomically with
	// SKIP LOCKED, so two workers/ticks can never send the same follow-up.
	rows, err := pool.Query(c, `UPDATE followups SET status='sending' WHERE id IN (
		SELECT id FROM followups WHERE status='pending' AND scheduled_at <= now()
		ORDER BY scheduled_at LIMIT 20 FOR UPDATE SKIP LOCKED)
		RETURNING id::text`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	type item struct{ id, phone, msg string }
	var items []item
	for _, id := range ids {
		var it item
		err := pool.QueryRow(c, `SELECT f.id::text, COALESCE(cu.phone, lc.phone, ''), f.message
			FROM followups f LEFT JOIN customers cu ON cu.id=f.customer_id
			LEFT JOIN leads l ON l.id=f.lead_id LEFT JOIN customers lc ON lc.id=l.customer_id
			WHERE f.id=$1`, id).Scan(&it.id, &it.phone, &it.msg)
		if err != nil {
			continue
		}
		items = append(items, it)
	}
	for _, it := range items {
		if it.phone == "" {
			_, _ = pool.Exec(c, `UPDATE followups SET status='pending' WHERE id=$1`, it.id)
			continue // no recipient yet; retry next tick
		}
		msg := it.msg
		if msg == "" {
			msg = "Hi! This is a friendly reminder from AutoKart. Reply here for help."
		}
		if err := w.Send(c, it.phone, msg); err != nil {
			log.Printf("[scheduler] send %s: %v", it.id, err)
			_, _ = pool.Exec(c, `UPDATE followups SET status='pending' WHERE id=$1`, it.id)
			continue
		}
		_, _ = pool.Exec(c, `UPDATE followups SET status='sent' WHERE id=$1`, it.id)
	}
}

// remindTestDrives sends 24h and 2h reminders (REM-001/002), recording each
// in followups so restarts never double-send (REM-003).
func remindTestDrives(ctx context.Context, pool *pgxpool.Pool, w *whatsapp.Worker) {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, _ = pool.Exec(c, `DELETE FROM followups WHERE status='sending' AND scheduled_at < now() - interval '10 minutes'`)
	rows, err := pool.Query(c, `SELECT t.id::text, cu.phone, v.make, v.model, t.scheduled_at
		FROM test_drives t JOIN leads l ON l.id=t.lead_id
		JOIN customers cu ON cu.id=l.customer_id JOIN vehicles v ON v.id=t.vehicle_id
		WHERE t.status='SCHEDULED' AND t.scheduled_at > now()
		AND t.scheduled_at <= now() + interval '25 hours' LIMIT 20`)
	if err != nil {
		return
	}
	type td struct {
		id, phone, make, model string
		at                    time.Time
	}
	var items []td
	for rows.Next() {
		var it td
		if err := rows.Scan(&it.id, &it.phone, &it.make, &it.model, &it.at); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()
	for _, it := range items {
		until := time.Until(it.at)
		kind := ""
		switch {
		case until <= 2*time.Hour+5*time.Minute:
			kind = "testdrive_reminder_2h"
		case until <= 24*time.Hour+5*time.Minute:
			kind = "testdrive_reminder_24h"
		default:
			continue
		}
		var exists bool
		_ = pool.QueryRow(c, `SELECT EXISTS(SELECT 1 FROM followups WHERE type=$1 AND message LIKE '%'||$2||'%')`, kind, it.id).Scan(&exists)
		if exists {
			continue
		}
		msg := "Reminder: your test drive for " + it.make + " " + it.model + " is at " + whatsapp.FormatTime(it.at) + ". Reply here to reschedule."
		marker := "[" + it.id + "] " + msg
		// Claim first: unique index makes the second claimer fail, so a
		// restart (REM-003) or overlapping tick can never double-send.
		tag, err := pool.Exec(c, `INSERT INTO followups(lead_id,type,scheduled_at,status,message)
			SELECT t.lead_id,$1,now(),'sending',$2 FROM test_drives t WHERE t.id=$3`, kind, marker, it.id)
		if err != nil || tag.RowsAffected() == 0 {
			continue
		}
		if err := w.Send(c, it.phone, msg); err != nil {
			_, _ = pool.Exec(c, `DELETE FROM followups WHERE type=$1 AND message=$2 AND status='sending'`, kind, marker)
			continue
		}
		_, _ = pool.Exec(c, `UPDATE followups SET status='sent' WHERE type=$1 AND message=$2`, kind, marker)
	}
}

// postTestDriveFollowups moves stale SCHEDULED test drives to COMPLETED
// (3h buffer past scheduled_at) and fires the post-drive classification
// message once per test drive: "How did the test drive go? Reply
// 1 Interested / 2 Still thinking / 3 Not interested".
// Staff PATCH to COMPLETED is picked up on the next tick — no direct
// WhatsApp dependency in the admin handler.
func postTestDriveFollowups(ctx context.Context, pool *pgxpool.Pool, w *whatsapp.Worker) {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Auto-complete: slot passed + buffer, staff never marked it.
	_, _ = pool.Exec(c, `UPDATE test_drives SET status='COMPLETED' WHERE status='SCHEDULED' AND scheduled_at < now() - interval '3 hours'`)
	rows, err := pool.Query(c, `SELECT t.id::text, t.lead_id::text, cu.phone
		FROM test_drives t JOIN leads l ON l.id=t.lead_id
		JOIN customers cu ON cu.id=l.customer_id
		WHERE t.status='COMPLETED'
		AND NOT EXISTS (SELECT 1 FROM followups f WHERE f.type='post_testdrive' AND f.message LIKE '%'||t.id::text||'%')
		LIMIT 20`)
	if err != nil {
		return
	}
	type td struct{ id, leadID, phone string }
	var items []td
	for rows.Next() {
		var it td
		if err := rows.Scan(&it.id, &it.leadID, &it.phone); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()
	for _, it := range items {
		if it.phone == "" {
			continue
		}
		msg := "How did the test drive go? Reply 1 Interested / 2 Still thinking / 3 Not interested"
		marker := "[" + it.id + "] " + msg
		// Move lead into classification state (preserve match data).
		_, _ = pool.Exec(c, `UPDATE leads SET state='POST_TESTDRIVE_FOLLOWUP', state_data = COALESCE(state_data,'{}'::jsonb) || '{"prev_state":"BUY_RESULTS"}', updated_at=now() WHERE id=$1`, it.leadID)
		// Claim first so restarts/overlapping ticks never double-send.
		tag, err := pool.Exec(c, `INSERT INTO followups(lead_id,type,scheduled_at,status,message)
			SELECT $1,'post_testdrive',now(),'sending',$2 WHERE NOT EXISTS
			(SELECT 1 FROM followups WHERE type='post_testdrive' AND message LIKE '%'||$3||'%')`, it.leadID, marker, it.id)
		if err != nil || tag.RowsAffected() == 0 {
			continue
		}
		if err := w.Send(c, it.phone, msg); err != nil {
			_, _ = pool.Exec(c, `DELETE FROM followups WHERE type='post_testdrive' AND message=$1 AND status='sending'`, marker)
			continue
		}
		_, _ = pool.Exec(c, `UPDATE followups SET status='sent' WHERE type='post_testdrive' AND message=$1`, marker)
	}
}

// remindInspections sends 24h and 2h reminders for sell inspections,
// mirroring test-drive reminders (deduped via followups claim).
func remindInspections(ctx context.Context, pool *pgxpool.Pool, w *whatsapp.Worker) {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := pool.Query(c, `SELECT i.id::text, cu.phone, i.scheduled_at
		FROM inspections i JOIN leads l ON l.id=i.lead_id
		JOIN customers cu ON cu.id=l.customer_id
		WHERE i.status='SCHEDULED' AND i.scheduled_at > now()
		AND i.scheduled_at <= now() + interval '25 hours' LIMIT 20`)
	if err != nil {
		return
	}
	type insp struct {
		id, phone string
		at        time.Time
	}
	var items []insp
	for rows.Next() {
		var it insp
		if err := rows.Scan(&it.id, &it.phone, &it.at); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()
	for _, it := range items {
		until := time.Until(it.at)
		kind := ""
		switch {
		case until <= 2*time.Hour+5*time.Minute:
			kind = "inspection_reminder_2h"
		case until <= 24*time.Hour+5*time.Minute:
			kind = "inspection_reminder_24h"
		default:
			continue
		}
		var exists bool
		_ = pool.QueryRow(c, `SELECT EXISTS(SELECT 1 FROM followups WHERE type=$1 AND message LIKE '%'||$2||'%')`, kind, it.id).Scan(&exists)
		if exists {
			continue
		}
		msg := "Reminder: your car inspection is at " + whatsapp.FormatTime(it.at) + ". Reply here to reschedule."
		marker := "[" + it.id + "] " + msg
		_, _ = pool.Exec(c, `INSERT INTO followups(lead_id,type,scheduled_at,status,message)
			SELECT i.lead_id,$1,now(),'sending',$2 FROM inspections i WHERE i.id=$3`, kind, marker, it.id)
		if err := w.Send(c, it.phone, msg); err != nil {
			_, _ = pool.Exec(c, `DELETE FROM followups WHERE type=$1 AND message=$2 AND status='sending'`, kind, marker)
			continue
		}
		_, _ = pool.Exec(c, `UPDATE followups SET status='sent' WHERE type=$1 AND message=$2`, kind, marker)
	}
}

// autoCompleteInspections marks past scheduled inspections as COMPLETED (2h buffer past scheduled_at).
func autoCompleteInspections(ctx context.Context, pool *pgxpool.Pool) {
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, _ = pool.Exec(c, `UPDATE inspections SET status='COMPLETED' WHERE status='SCHEDULED' AND scheduled_at < now() - interval '2 hours'`)
}

// autoReviewSellRequests automatically evaluates pending sell requests, calculates valuation,
// adds them into inventory as AVAILABLE with linked images, marks status as ACCEPTED, and alerts the customer.
func autoReviewSellRequests(ctx context.Context, pool *pgxpool.Pool, w *whatsapp.Worker) {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	rows, err := pool.Query(c, `SELECT s.id::text, s.lead_id::text, cu.phone, s.brand, s.model, s.year, s.registration, s.km, s.fuel, s.transmission, s.condition, s.location
		FROM sell_requests s JOIN leads l ON l.id=s.lead_id
		JOIN customers cu ON cu.id=l.customer_id
		WHERE s.status='VALUATION_PENDING' LIMIT 10`)
	if err != nil {
		return
	}
	defer rows.Close()

	type sr struct {
		id, leadID, phone, brand, model, reg, fuel, trans, cond, loc string
		year, km                                                     int
	}
	var items []sr
	for rows.Next() {
		var it sr
		if err := rows.Scan(&it.id, &it.leadID, &it.phone, &it.brand, &it.model, &it.year, &it.reg, &it.km, &it.fuel, &it.trans, &it.cond, &it.loc); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	for _, it := range items {
		price := botengine.EstimateVehicleValuation(it.brand, it.model, it.year, it.km, it.cond, 0)
		vid := uuid.NewString()
		desc := strings.TrimSpace(it.cond + " " + it.loc + " " + it.reg)
		if desc == "" {
			desc = "Verified pre-owned vehicle (auto-reviewed)"
		}

		tx, err := pool.Begin(c)
		if err != nil {
			continue
		}

		if _, err := tx.Exec(c, `INSERT INTO vehicles(id,make,model,year,price,fuel,transmission,km,status,description,acquired_via)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,'AVAILABLE',$9,'customer_sell')`,
			vid, it.brand, it.model, it.year, price, it.fuel, it.trans, it.km, desc); err != nil {
			_ = tx.Rollback(c)
			continue
		}

		// Link photos
		pRows, err := tx.Query(c, `SELECT DISTINCT m.media_path FROM messages m
			JOIN conversations conv ON conv.id=m.conversation_id
			WHERE conv.lead_id=$1 AND m.media_path<>'' ORDER BY m.media_path`, it.leadID)
		if err == nil {
			var photoPaths []string
			for pRows.Next() {
				var mp string
				if err := pRows.Scan(&mp); err == nil {
					photoPaths = append(photoPaths, mp)
				}
			}
			pRows.Close()
			for n, mp := range photoPaths {
				_, _ = tx.Exec(c, `INSERT INTO vehicle_images(vehicle_id,path,sort_order) VALUES($1,$2,$3)`, vid, mp, n)
			}
		}

		if _, err := tx.Exec(c, `UPDATE sell_requests SET status='ACCEPTED' WHERE id=$1`, it.id); err != nil {
			_ = tx.Rollback(c)
			continue
		}

		if err := tx.Commit(c); err == nil {
			if w != nil && it.phone != "" {
				msg := fmt.Sprintf("🎉 Vehicle Review Complete: Your %d %s %s has been automatically evaluated and accepted! Estimated valuation: ₹%s. It is now listed in our inventory.", it.year, it.brand, it.model, botengine.FormatPrice(price))
				_ = w.Send(c, it.phone, msg)
			}
		}
	}
}

// autoReviewFinanceRequests evaluates pending finance requests and automatically pre-approves qualified applicants.
func autoReviewFinanceRequests(ctx context.Context, pool *pgxpool.Pool, w *whatsapp.Worker) {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	rows, err := pool.Query(c, `SELECT f.id::text, cu.phone, f.loan_amount, f.income
		FROM finance_requests f JOIN leads l ON l.id=f.lead_id
		JOIN customers cu ON cu.id=l.customer_id
		WHERE f.status='NEW' LIMIT 10`)
	if err != nil {
		return
	}
	defer rows.Close()

	type fin struct {
		id, phone          string
		loanAmount, income int
	}
	var items []fin
	for rows.Next() {
		var it fin
		if err := rows.Scan(&it.id, &it.phone, &it.loanAmount, &it.income); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	for _, it := range items {
		// Auto pre-approval criteria: income * 36 >= loan or reasonable loan size
		if it.loanAmount > 0 && it.income > 0 && (it.income*36 >= it.loanAmount || it.loanAmount <= 1500000) {
			tag, err := pool.Exec(c, `UPDATE finance_requests SET status='PRE_APPROVED' WHERE id=$1 AND status='NEW'`, it.id)
			if err == nil && tag.RowsAffected() > 0 {
				if w != nil && it.phone != "" {
					msg := fmt.Sprintf("🎉 Finance Pre-Approval: Your application for ₹%s has been automatically pre-approved. Our financing specialist will contact you shortly to collect paperwork.", botengine.FormatPrice(it.loanAmount))
					_ = w.Send(c, it.phone, msg)
				}
			}
		}
	}
}

// autoPromptReviews requests feedback for customers whose test drive or delivery completed > 1h ago,
// unless already reviewed or prompted.
func autoPromptReviews(ctx context.Context, pool *pgxpool.Pool, w *whatsapp.Worker) {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	rows, err := pool.Query(c, `SELECT t.id::text, t.lead_id::text, cu.phone, v.make, v.model
		FROM test_drives t JOIN leads l ON l.id=t.lead_id
		JOIN customers cu ON cu.id=l.customer_id JOIN vehicles v ON v.id=t.vehicle_id
		WHERE t.status='COMPLETED' AND t.scheduled_at < now() - interval '1 hour'
		AND NOT EXISTS (SELECT 1 FROM followups f WHERE f.type='review_prompt' AND f.lead_id=t.lead_id)
		LIMIT 10`)
	if err != nil {
		return
	}
	defer rows.Close()

	type tdRev struct {
		id, leadID, phone, make, model string
	}
	var items []tdRev
	for rows.Next() {
		var it tdRev
		if err := rows.Scan(&it.id, &it.leadID, &it.phone, &it.make, &it.model); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	for _, it := range items {
		if it.phone == "" {
			continue
		}
		msg := fmt.Sprintf("🌟 Thank you for test driving the %s %s! How was your experience? Reply with a rating (1 to 5 stars) and a short review.", it.make, it.model)
		marker := "[" + it.id + "] " + msg
		tag, err := pool.Exec(c, `INSERT INTO followups(lead_id, type, scheduled_at, status, message)
			SELECT $1, 'review_prompt', now(), 'sending', $2
			WHERE NOT EXISTS (SELECT 1 FROM followups WHERE type='review_prompt' AND lead_id=$1)`, it.leadID, marker)
		if err != nil || tag.RowsAffected() == 0 {
			continue
		}
		if w != nil {
			_ = w.Send(c, it.phone, msg)
		}
		_, _ = pool.Exec(c, `UPDATE followups SET status='sent' WHERE type='review_prompt' AND message=$1`, marker)
	}
}
