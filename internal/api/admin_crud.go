package api

import (
	"net/http"

	"sellingbot/internal/auth"
)

func actorOf(r *http.Request) string {
	if cl := auth.Current(r); cl != nil {
		return cl.Email
	}
	return ""
}

func needAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !isAdmin(r) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return false
	}
	return true
}

// PATCH /api/customers/{id} {name} — rename a customer.
func (s *Server) patchCustomer(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "/api/customers/")
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil || badUUID(w, id) || in.Name == "" {
		http.Error(w, `{"error":"name required"}`, 400)
		return
	}
	var prev string
	_ = s.Pool.QueryRow(r.Context(), `SELECT name FROM customers WHERE id=$1`, id).Scan(&prev)
	if _, err := s.Pool.Exec(r.Context(), `UPDATE customers SET name=$1, updated_at=now() WHERE id=$2`, in.Name, id); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	audit(r.Context(), s.Pool, actorOf(r), "customer.rename", "customer", id, prev, in.Name)
	writeJSON(w, map[string]any{"ok": true})
}

// DELETE /api/customers/{id} (admin) — cascades leads, conversations, follow-ups.
func (s *Server) deleteCustomer(w http.ResponseWriter, r *http.Request) {
	if !needAdmin(w, r) {
		return
	}
	id := idParam(r, "/api/customers/")
	if badUUID(w, id) {
		return
	}
	var name, phone string
	_ = s.Pool.QueryRow(r.Context(), `SELECT name,phone FROM customers WHERE id=$1`, id).Scan(&name, &phone)
	tag, err := s.Pool.Exec(r.Context(), `DELETE FROM customers WHERE id=$1`, id)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"not found"}`, 404)
		return
	}
	audit(r.Context(), s.Pool, actorOf(r), "customer.delete", "customer", id, name+" "+phone, "")
	writeJSON(w, map[string]any{"ok": true})
}

// DELETE /api/vehicles/{id} (admin) — blocked while booked or TD-scheduled.
func (s *Server) deleteVehicle(w http.ResponseWriter, r *http.Request) {
	if !needAdmin(w, r) {
		return
	}
	id := idParam(r, "/api/vehicles/")
	if badUUID(w, id) {
		return
	}
	var active int
	_ = s.Pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM bookings WHERE vehicle_id=$1 AND status IN ('PENDING','CONFIRMED')`, id).Scan(&active)
	var td int
	_ = s.Pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM test_drives WHERE vehicle_id=$1 AND status='SCHEDULED'`, id).Scan(&td)
	if active+td > 0 {
		http.Error(w, `{"error":"vehicle has active booking or test drive"}`, 409)
		return
	}
	var label string
	_ = s.Pool.QueryRow(r.Context(), `SELECT make||' '||model||' '||year FROM vehicles WHERE id=$1`, id).Scan(&label)
	_, _ = s.Pool.Exec(r.Context(), `DELETE FROM vehicle_images WHERE vehicle_id=$1`, id)
	tag, err := s.Pool.Exec(r.Context(), `DELETE FROM vehicles WHERE id=$1`, id)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"not found"}`, 404)
		return
	}
	audit(r.Context(), s.Pool, actorOf(r), "vehicle.delete", "vehicle", id, label, "")
	writeJSON(w, map[string]any{"ok": true})
}

// DELETE /api/leads/{id} (admin) — removes lead + orphan-prone children.
func (s *Server) deleteLead(w http.ResponseWriter, r *http.Request) {
	if !needAdmin(w, r) {
		return
	}
	id := idParam(r, "/api/leads/")
	if badUUID(w, id) {
		return
	}
	tx, err := s.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer tx.Rollback(r.Context())
	// Tables without FK to leads must be cleaned explicitly.
	_, _ = tx.Exec(r.Context(), `DELETE FROM requirements WHERE lead_id=$1`, id)
	_, _ = tx.Exec(r.Context(), `DELETE FROM vehicle_matches WHERE lead_id=$1`, id)
	tag, err := tx.Exec(r.Context(), `DELETE FROM leads WHERE id=$1`, id)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"not found"}`, 404)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	audit(r.Context(), s.Pool, actorOf(r), "lead.delete", "lead", id, "", "")
	writeJSON(w, map[string]any{"ok": true})
}

// PATCH /api/test-drives/{id} {status}
func (s *Server) patchTestDrive(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "/api/test-drives/")
	var in struct {
		Status string `json:"status"`
	}
	if err := readJSON(r, &in); err != nil || badUUID(w, id) {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	switch in.Status {
	case "SCHEDULED", "COMPLETED", "CANCELLED", "NO_SHOW":
	default:
		http.Error(w, `{"error":"invalid status"}`, 400)
		return
	}
	var prev string
	_ = s.Pool.QueryRow(r.Context(), `SELECT status FROM test_drives WHERE id=$1`, id).Scan(&prev)
	if _, err := s.Pool.Exec(r.Context(), `UPDATE test_drives SET status=$1 WHERE id=$2`, in.Status, id); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	audit(r.Context(), s.Pool, actorOf(r), "testdrive.status", "test_drive", id, prev, in.Status)
	writeJSON(w, map[string]any{"ok": true})
}

// GET /api/inspections — sell inspection appointments (same section as Test Drives).
func (s *Server) listInspections(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Pool.Query(r.Context(), `SELECT i.id::text,c.phone,i.scheduled_at,i.status,i.notes FROM inspections i JOIN leads l ON l.id=i.lead_id JOIN customers c ON c.id=l.customer_id ORDER BY i.scheduled_at DESC LIMIT 50`)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, phone, status, notes string
		var ts any
		_ = rows.Scan(&id, &phone, &ts, &status, &notes)
		out = append(out, map[string]any{"id": id, "phone": phone, "scheduled_at": ts, "status": status, "notes": notes})
	}
	writeJSON(w, out)
}

// PATCH /api/inspections/{id} {status, scheduled_at}
func (s *Server) patchInspection(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "/api/inspections/")
	var in struct {
		Status      string `json:"status"`
		ScheduledAt string `json:"scheduled_at"`
	}
	if err := readJSON(r, &in); err != nil || badUUID(w, id) {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	if in.Status != "" {
		switch in.Status {
		case "SCHEDULED", "COMPLETED", "CANCELLED", "NO_SHOW":
		default:
			http.Error(w, `{"error":"invalid status"}`, 400)
			return
		}
		var prev string
		_ = s.Pool.QueryRow(r.Context(), `SELECT status FROM inspections WHERE id=$1`, id).Scan(&prev)
		if _, err := s.Pool.Exec(r.Context(), `UPDATE inspections SET status=$1 WHERE id=$2`, in.Status, id); err != nil {
			http.Error(w, `{"error":"db"}`, 500)
			return
		}
		audit(r.Context(), s.Pool, actorOf(r), "inspection.status", "inspection", id, prev, in.Status)
	}
	if in.ScheduledAt != "" {
		if badTime(w, in.ScheduledAt) {
			return
		}
		if _, err := s.Pool.Exec(r.Context(), `UPDATE inspections SET scheduled_at=$1 WHERE id=$2`, in.ScheduledAt, id); err != nil {
			http.Error(w, `{"error":"db"}`, 500)
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

// PATCH /api/finance/{id} {status}
func (s *Server) patchFinance(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "/api/finance/")
	var in struct {
		Status string `json:"status"`
	}
	if err := readJSON(r, &in); err != nil || badUUID(w, id) {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	switch in.Status {
	case "NEW", "CONTACTED", "APPROVED", "REJECTED", "CLOSED":
	default:
		http.Error(w, `{"error":"invalid status"}`, 400)
		return
	}
	var prev string
	_ = s.Pool.QueryRow(r.Context(), `SELECT status FROM finance_requests WHERE id=$1`, id).Scan(&prev)
	if _, err := s.Pool.Exec(r.Context(), `UPDATE finance_requests SET status=$1 WHERE id=$2`, in.Status, id); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	audit(r.Context(), s.Pool, actorOf(r), "finance.status", "finance", id, prev, in.Status)
	writeJSON(w, map[string]any{"ok": true})
}

// DELETE /api/followups/{id} (admin)
func (s *Server) deleteFollowup(w http.ResponseWriter, r *http.Request) {
	if !needAdmin(w, r) {
		return
	}
	id := idParam(r, "/api/followups/")
	if badUUID(w, id) {
		return
	}
	tag, err := s.Pool.Exec(r.Context(), `DELETE FROM followups WHERE id=$1`, id)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"not found"}`, 404)
		return
	}
	audit(r.Context(), s.Pool, actorOf(r), "followup.delete", "followup", id, "", "")
	writeJSON(w, map[string]any{"ok": true})
}

// DELETE /api/reviews/{id} (admin)
func (s *Server) deleteReview(w http.ResponseWriter, r *http.Request) {
	if !needAdmin(w, r) {
		return
	}
	id := idParam(r, "/api/reviews/")
	if badUUID(w, id) {
		return
	}
	tag, err := s.Pool.Exec(r.Context(), `DELETE FROM reviews WHERE id=$1`, id)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"not found"}`, 404)
		return
	}
	audit(r.Context(), s.Pool, actorOf(r), "review.delete", "review", id, "", "")
	writeJSON(w, map[string]any{"ok": true})
}

// PATCH /api/users/{id} {role} (admin) — never touch own role or the last admin.
func (s *Server) patchUser(w http.ResponseWriter, r *http.Request) {
	if !needAdmin(w, r) {
		return
	}
	id := idParam(r, "/api/users/")
	var in struct {
		Role string `json:"role"`
	}
	if err := readJSON(r, &in); err != nil || badUUID(w, id) {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	if in.Role != "admin" && in.Role != "sales" {
		http.Error(w, `{"error":"role must be admin|sales"}`, 400)
		return
	}
	me := auth.Current(r)
	var prev, email string
	if err := s.Pool.QueryRow(r.Context(), `SELECT role,email FROM users WHERE id=$1`, id).Scan(&prev, &email); err != nil {
		http.Error(w, `{"error":"not found"}`, 404)
		return
	}
	if me != nil && me.UserID == id {
		http.Error(w, `{"error":"cannot change your own role"}`, 409)
		return
	}
	if prev == "admin" && in.Role != "admin" {
		var n int
		_ = s.Pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM users WHERE role='admin'`).Scan(&n)
		if n <= 1 {
			http.Error(w, `{"error":"cannot demote the last admin"}`, 409)
			return
		}
	}
	if _, err := s.Pool.Exec(r.Context(), `UPDATE users SET role=$1 WHERE id=$2`, in.Role, id); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	audit(r.Context(), s.Pool, actorOf(r), "user.role", "user", id, email+" "+prev, email+" "+in.Role)
	writeJSON(w, map[string]any{"ok": true})
}

// DELETE /api/users/{id} (admin) — never self or the last admin.
func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	if !needAdmin(w, r) {
		return
	}
	id := idParam(r, "/api/users/")
	if badUUID(w, id) {
		return
	}
	me := auth.Current(r)
	if me != nil && me.UserID == id {
		http.Error(w, `{"error":"cannot delete yourself"}`, 409)
		return
	}
	var role, email string
	if err := s.Pool.QueryRow(r.Context(), `SELECT role,email FROM users WHERE id=$1`, id).Scan(&role, &email); err != nil {
		http.Error(w, `{"error":"not found"}`, 404)
		return
	}
	if role == "admin" {
		var n int
		_ = s.Pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM users WHERE role='admin'`).Scan(&n)
		if n <= 1 {
			http.Error(w, `{"error":"cannot delete the last admin"}`, 409)
			return
		}
	}
	if _, err := s.Pool.Exec(r.Context(), `DELETE FROM users WHERE id=$1`, id); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	audit(r.Context(), s.Pool, actorOf(r), "user.delete", "user", id, email+" "+role, "")
	writeJSON(w, map[string]any{"ok": true})
}
