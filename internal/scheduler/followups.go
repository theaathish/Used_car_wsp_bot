package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sellingbot/internal/whatsapp"
)

// Followups polls every minute: due followups, test-drive reminders
// (24h + 2h), and outbox PENDING->SENT flush. Survives worker restarts
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
