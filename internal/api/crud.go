package api

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"sellingbot/internal/auth"
)

func mustRead(f multipart.File) []byte {
	b, _ := io.ReadAll(io.LimitReader(f, 6<<20))
	return b
}

func qStr(r *http.Request, k, d string) string {
	if v := r.URL.Query().Get(k); v != "" {
		return v
	}
	return d
}

func qInt(r *http.Request, k string, d int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(k))
	if err != nil || v <= 0 {
		return d
	}
	return v
}

func (s *Server) listCustomers(w http.ResponseWriter, r *http.Request) {
	limit := qInt(r, "limit", 50)
	rows, err := s.Pool.Query(r.Context(), `SELECT id::text,name,phone,source,created_at FROM customers ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, name, phone, source string
		var ts any
		_ = rows.Scan(&id, &name, &phone, &source, &ts)
		out = append(out, map[string]any{"id": id, "name": name, "phone": phone, "source": source, "created_at": ts})
	}
	writeJSON(w, out)
}

func (s *Server) createCustomer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  string `json:"name"`
		Phone string `json:"phone"`
	}
	if err := readJSON(r, &in); err != nil || in.Phone == "" {
		http.Error(w, `{"error":"phone required"}`, 400)
		return
	}
	id := uuid.NewString()
	if _, err := s.Pool.Exec(r.Context(), `INSERT INTO customers(id,name,phone) VALUES($1,$2,$3) ON CONFLICT(phone) DO UPDATE SET name=EXCLUDED.name`, id, in.Name, in.Phone); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) listLeads(w http.ResponseWriter, r *http.Request) {
	limit := qInt(r, "limit", 50)
	status := r.URL.Query().Get("status")
	// Salespeople see only assigned leads; admins see everything.
	cl := auth.Current(r)
	q := `SELECT l.id::text,c.name,c.phone,l.intent,l.status,l.state,COALESCE(l.interest,''),l.source,l.created_at FROM leads l JOIN customers c ON c.id=l.customer_id`
	conds := []string{}
	args := []any{}
	if status != "" {
		args = append(args, status)
		conds = append(conds, `l.status=$`+strconv.Itoa(len(args)))
	}
	if cl != nil && cl.Role == "sales" {
		args = append(args, cl.UserID)
		conds = append(conds, `l.sales_user_id=$`+strconv.Itoa(len(args)))
	}
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, " AND ")
	}
	if limit > 100 {
		limit = 100
	}
	q += ` ORDER BY l.created_at DESC LIMIT ` + strconv.Itoa(limit) + ` OFFSET ` + strconv.Itoa(qInt(r, "offset", 0))
	rows, err := s.Pool.Query(r.Context(), q, args...)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, name, phone, intent, st, state, interest, source string
		var ts any
		_ = rows.Scan(&id, &name, &phone, &intent, &st, &state, &interest, &source, &ts)
		out = append(out, map[string]any{"id": id, "customer": name, "phone": phone, "intent": intent, "status": st, "state": state, "interest": interest, "source": source, "created_at": ts})
	}
	writeJSON(w, out)
}

func (s *Server) listVehicles(w http.ResponseWriter, r *http.Request) {
	limit := qInt(r, "limit", 50)
	if limit > 100 {
		limit = 100
	}
	offset := qInt(r, "offset", 0)
	rows, err := s.Pool.Query(r.Context(), `SELECT id::text,make,model,year,price,fuel,transmission,km,status,description FROM vehicles ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, make, model, fuel, trans, status, desc string
		var year, price, km int
		_ = rows.Scan(&id, &make, &model, &year, &price, &fuel, &trans, &km, &status, &desc)
		imgs := s.vehicleImages(r, id)
		out = append(out, map[string]any{"id": id, "make": make, "model": model, "year": year, "price": price, "fuel": fuel, "transmission": trans, "km": km, "status": status, "description": desc, "images": imgs})
	}
	writeJSON(w, out)
}

func (s *Server) vehicleImages(r *http.Request, vehicleID string) []any {
	rows, err := s.Pool.Query(r.Context(), `SELECT path FROM vehicle_images WHERE vehicle_id=$1 ORDER BY sort_order`, vehicleID)
	if err != nil {
		return []any{}
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var p string
		_ = rows.Scan(&p)
		out = append(out, "/"+p)
	}
	return out
}

func (s *Server) createVehicle(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Make, Model, Fuel, Transmission, Status, Description string
		Year, Price, Km                                      int
	}
	if err := readJSON(r, &in); err != nil || in.Make == "" || in.Model == "" {
		http.Error(w, `{"error":"make+model required"}`, 400)
		return
	}
	if in.Status == "" {
		in.Status = "AVAILABLE"
	}
	if in.Status != "DRAFT" && in.Status != "AVAILABLE" {
		http.Error(w, `{"error":"new vehicle must be DRAFT or AVAILABLE"}`, 400)
		return
	}
	if in.Year < 0 || in.Price < 0 || in.Km < 0 {
		http.Error(w, `{"error":"year/price/km must be >= 0"}`, 400)
		return
	}
	if in.Year > time.Now().Year()+1 {
		http.Error(w, `{"error":"year is in the future"}`, 400)
		return
	}
	id := uuid.NewString()
	_, err := s.Pool.Exec(r.Context(), `INSERT INTO vehicles(id,make,model,year,price,fuel,transmission,km,status,description) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		id, in.Make, in.Model, in.Year, in.Price, in.Fuel, in.Transmission, in.Km, in.Status, in.Description)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

// vehicleTransitions guards lifecycle (§12). Terminal states need ?force=1.
var vehicleTransitions = map[string][]string{
	"DRAFT":     {"AVAILABLE"},
	"AVAILABLE": {"RESERVED", "BOOKED", "SOLD"},
	"RESERVED":  {"AVAILABLE", "BOOKED", "SOLD"},
	"BOOKED":    {"AVAILABLE", "DELIVERED", "SOLD"},
	"SOLD":      {},
	"DELIVERED": {},
}

func (s *Server) patchVehicle(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "/api/vehicles/")
	if badUUID(w, id) {
		return
	}
	var in map[string]any
	if err := readJSON(r, &in); err != nil || id == "" {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	for _, k := range []string{"year", "price", "km"} {
		if v, ok := in[k].(float64); ok && v < 0 {
			http.Error(w, `{"error":"`+k+` must be >= 0"}`, 400)
			return
		}
	}
	actor := ""
	if cl := auth.Current(r); cl != nil {
		actor = cl.Email
	}
	if st, ok := in["status"].(string); ok {
		var cur string
		if err := s.Pool.QueryRow(r.Context(), `SELECT status FROM vehicles WHERE id=$1`, id).Scan(&cur); err != nil {
			http.Error(w, `{"error":"vehicle not found"}`, 404)
			return
		}
		if cur != st {
			allowed := false
			for _, t := range vehicleTransitions[cur] {
				if t == st {
					allowed = true
				}
			}
			if !allowed && r.URL.Query().Get("force") != "1" {
				http.Error(w, `{"error":"invalid transition `+cur+` -> `+st+`"}`, 400)
				return
			}
			audit(r.Context(), s.Pool, actor, "vehicle.status", "vehicle", id, cur, st)
		}
	}
	allowed := map[string]bool{"make": true, "model": true, "year": true, "price": true, "fuel": true, "transmission": true, "km": true, "status": true, "description": true}
	for k, v := range in {
		if !allowed[k] {
			continue
		}
		if k == "price" {
			var old int
			_ = s.Pool.QueryRow(r.Context(), `SELECT price FROM vehicles WHERE id=$1`, id).Scan(&old)
			audit(r.Context(), s.Pool, actor, "vehicle.price", "vehicle", id, strconv.Itoa(old), fmt.Sprintf("%v", v))
		}
		if _, err := s.Pool.Exec(r.Context(), `UPDATE vehicles SET `+k+`=$1, updated_at=now() WHERE id=$2`, v, id); err != nil {
			http.Error(w, `{"error":"db"}`, 500)
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) uploadVehicleImage(w http.ResponseWriter, r *http.Request) {
	// /api/vehicles/{id}/images
	trim := len("/api/vehicles/")
	rest := r.URL.Path[trim:]
	id := rest[:len(rest)-len("/images")]
	if id == "" {
		http.Error(w, `{"error":"vehicle id required"}`, 400)
		return
	}
	if badUUID(w, id) {
		return
	}
	if err := r.ParseMultipartForm(6 << 20); err != nil {
		http.Error(w, `{"error":"multipart parse"}`, 400)
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"file field required"}`, 400)
		return
	}
	defer f.Close()
	rel, hash, err := s.Images.SaveData(hdr.Filename, mustRead(f))
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 400)
		return
	}
	var n int
	_ = s.Pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM vehicle_images WHERE vehicle_id=$1`, id).Scan(&n)
	if n >= 10 {
		http.Error(w, `{"error":"max 10 images per vehicle"}`, 400)
		return
	}
	var dup string
	_ = s.Pool.QueryRow(r.Context(), `SELECT path FROM vehicle_images WHERE vehicle_id=$1 AND hash=$2`, id, hash).Scan(&dup)
	if dup != "" {
		writeJSON(w, map[string]any{"path": "/" + dup, "duplicate": true})
		return
	}
	if _, err := s.Pool.Exec(r.Context(), `INSERT INTO vehicle_images(vehicle_id,path,hash,sort_order) VALUES($1,$2,$3,$4)`, id, rel, hash, n); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	writeJSON(w, map[string]any{"path": "/" + rel})
}

func (s *Server) listRequirements(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Pool.Query(r.Context(), `SELECT r.id::text,l.id::text,c.phone,r.budget_min,r.budget_max,r.brand,r.fuel,r.transmission,r.year_min FROM requirements r JOIN leads l ON l.id=r.lead_id JOIN customers c ON c.id=l.customer_id ORDER BY r.created_at DESC LIMIT 50`)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, lead, phone, brand, fuel, trans string
		var mn, mx, ym int
		_ = rows.Scan(&id, &lead, &phone, &mn, &mx, &brand, &fuel, &trans, &ym)
		out = append(out, map[string]any{"id": id, "lead_id": lead, "phone": phone, "budget_min": mn, "budget_max": mx, "brand": brand, "fuel": fuel, "transmission": trans, "year_min": ym})
	}
	writeJSON(w, out)
}

func (s *Server) matchLead(w http.ResponseWriter, r *http.Request) {
	// POST /api/leads/{id}/match — recompute matches from stored requirement/state.
	p := r.URL.Path[len("/api/leads/"):]
	id := p[:len(p)-len("/match")]
	if badUUID(w, id) {
		return
	}
	var data map[string]string
	var req struct {
		Mn, Mx, Ym                int
		Brand, Fuel, Trans, Model string
	}
	_ = s.Pool.QueryRow(r.Context(), `SELECT state_data FROM leads WHERE id=$1`, id).Scan(&data)
	_ = s.Pool.QueryRow(r.Context(), `SELECT budget_min,budget_max,brand,fuel,transmission,year_min FROM requirements WHERE lead_id=$1 ORDER BY created_at DESC LIMIT 1`,
		id).Scan(&req.Mn, &req.Mx, &req.Brand, &req.Fuel, &req.Trans, &req.Ym)
	if data == nil {
		data = map[string]string{}
	}
	merge := func(k string, v int) {
		if data[k] == "" && v > 0 {
			data[k] = strconv.Itoa(v)
		}
	}
	merge("budget_min", req.Mn)
	merge("budget_max", req.Mx)
	merge("year_min", req.Ym)
	if data["brand"] == "" {
		data["brand"] = req.Brand
	}
	if data["fuel"] == "" {
		data["fuel"] = req.Fuel
	}
	if data["transmission"] == "" {
		data["transmission"] = req.Trans
	}
	_, _ = s.Pool.Exec(r.Context(), `DELETE FROM vehicle_matches WHERE lead_id=$1`, id)
	rows, _ := s.Pool.Query(r.Context(), `SELECT id::text,make,model,year,price,fuel,transmission FROM vehicles WHERE status='AVAILABLE' LIMIT 50`)
	out := []any{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var vid, make, model, fuel, trans string
			var year, price int
			_ = rows.Scan(&vid, &make, &model, &year, &price, &fuel, &trans)
			score := 0
			if mx, _ := strconv.Atoi(data["budget_max"]); mx > 0 && price <= mx {
				score += 3
			} else if data["budget_max"] != "" && data["budget_max"] != "0" {
				continue
			}
			out = append(out, map[string]any{"vehicle_id": vid, "make": make, "model": model, "year": year, "price": price, "fuel": fuel, "transmission": trans, "score": score})
			_, _ = s.Pool.Exec(r.Context(), `INSERT INTO vehicle_matches(lead_id,vehicle_id,score) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, vid, score)
			if len(out) >= 10 {
				break
			}
		}
	}
	writeJSON(w, out)
}

func (s *Server) listTestDrives(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Pool.Query(r.Context(), `SELECT t.id::text,c.phone,v.make,v.model,t.scheduled_at,t.status,t.notes FROM test_drives t JOIN leads l ON l.id=t.lead_id JOIN customers c ON c.id=l.customer_id JOIN vehicles v ON v.id=t.vehicle_id ORDER BY t.scheduled_at DESC LIMIT 50`)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, phone, make, model, status, notes string
		var ts any
		_ = rows.Scan(&id, &phone, &make, &model, &ts, &status, &notes)
		out = append(out, map[string]any{"id": id, "phone": phone, "vehicle": make + " " + model, "scheduled_at": ts, "status": status, "notes": notes})
	}
	writeJSON(w, out)
}

func (s *Server) createTestDrive(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LeadID    string `json:"lead_id"`
		VehicleID string `json:"vehicle_id"`
		Scheduled string `json:"scheduled_at"`
		Notes     string `json:"notes"`
	}
	if err := readJSON(r, &in); err != nil || in.LeadID == "" || in.VehicleID == "" || in.Scheduled == "" {
		http.Error(w, `{"error":"lead_id+vehicle_id+scheduled_at required"}`, 400)
		return
	}
	if badUUID(w, in.LeadID, in.VehicleID) || badTime(w, in.Scheduled) {
		return
	}
	id := uuid.NewString()
	if _, err := s.Pool.Exec(r.Context(), `INSERT INTO test_drives(id,lead_id,vehicle_id,scheduled_at,notes) VALUES($1,$2,$3,$4,$5)`, id, in.LeadID, in.VehicleID, in.Scheduled, in.Notes); err != nil {
		if isDup(err) {
			http.Error(w, `{"error":"slot unavailable, please choose another time"}`, http.StatusConflict)
			return
		}
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	_, _ = s.Pool.Exec(r.Context(), `UPDATE leads SET status='TEST_DRIVE' WHERE id=$1`, in.LeadID)
	writeJSON(w, map[string]any{"id": id})
}

func (s *Server) listFollowups(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Pool.Query(r.Context(), `SELECT f.id::text,c.phone,f.type,f.scheduled_at,f.status,f.message FROM followups f LEFT JOIN customers c ON c.id=f.customer_id ORDER BY f.scheduled_at DESC LIMIT 50`)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, phone, typ, status, msg string
		var ts any
		_ = rows.Scan(&id, &phone, &typ, &ts, &status, &msg)
		out = append(out, map[string]any{"id": id, "phone": phone, "type": typ, "scheduled_at": ts, "status": status, "message": msg})
	}
	writeJSON(w, out)
}

func (s *Server) createFollowup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CustomerID string `json:"customer_id"`
		LeadID     string `json:"lead_id"`
		Type       string `json:"type"`
		Scheduled  string `json:"scheduled_at"`
		Message    string `json:"message"`
	}
	if err := readJSON(r, &in); err != nil || in.Scheduled == "" {
		http.Error(w, `{"error":"scheduled_at required"}`, 400)
		return
	}
	if badUUID(w, in.CustomerID, in.LeadID) || badTime(w, in.Scheduled) {
		return
	}
	if in.Type == "" {
		in.Type = "general"
	}
	if in.CustomerID == "" && in.LeadID == "" {
		http.Error(w, `{"error":"customer_id or lead_id required"}`, 400)
		return
	}
	id := uuid.NewString()
	var cust, lead any
	if in.CustomerID != "" {
		cust = in.CustomerID
	}
	if in.LeadID != "" {
		lead = in.LeadID
	}
	if _, err := s.Pool.Exec(r.Context(), `INSERT INTO followups(id,customer_id,lead_id,type,scheduled_at,message) VALUES($1,$2,$3,$4,$5,$6)`, id, cust, lead, in.Type, in.Scheduled, in.Message); err != nil {
		if isDup(err) {
			http.Error(w, `{"error":"duplicate followup"}`, http.StatusConflict)
			return
		}
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

func (s *Server) patchFollowup(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "/api/followups/")
	if badUUID(w, id) {
		return
	}
	var in map[string]any
	if err := readJSON(r, &in); err != nil {
		http.Error(w, `{"error":"bad json"}`, 400)
		return
	}
	if st, ok := in["status"].(string); ok {
		_, _ = s.Pool.Exec(r.Context(), `UPDATE followups SET status=$1 WHERE id=$2`, st, id)
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) listBookings(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Pool.Query(r.Context(), `SELECT b.id::text,c.phone,v.make,v.model,b.amount,b.status,b.delivery_at FROM bookings b JOIN leads l ON l.id=b.lead_id JOIN customers c ON c.id=l.customer_id JOIN vehicles v ON v.id=b.vehicle_id ORDER BY b.created_at DESC LIMIT 50`)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, phone, make, model, status string
		var amount int
		var del any
		_ = rows.Scan(&id, &phone, &make, &model, &amount, &status, &del)
		out = append(out, map[string]any{"id": id, "phone": phone, "vehicle": make + " " + model, "amount": amount, "status": status, "delivery_at": del})
	}
	writeJSON(w, out)
}

func (s *Server) createBooking(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LeadID    string `json:"lead_id"`
		VehicleID string `json:"vehicle_id"`
		Amount    int    `json:"amount"`
	}
	if err := readJSON(r, &in); err != nil || in.LeadID == "" || in.VehicleID == "" {
		http.Error(w, `{"error":"lead_id+vehicle_id required"}`, 400)
		return
	}
	if badUUID(w, in.LeadID, in.VehicleID) {
		return
	}
	id := uuid.NewString()
	// BOOK-002: lock the vehicle row so two concurrent bookings can't both win.
	tx, err := s.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer tx.Rollback(r.Context())
	var vst string
	if err := tx.QueryRow(r.Context(), `SELECT status FROM vehicles WHERE id=$1 FOR UPDATE`, in.VehicleID).Scan(&vst); err != nil {
		http.Error(w, `{"error":"vehicle not found"}`, 404)
		return
	}
	if vst != "AVAILABLE" {
		http.Error(w, `{"error":"vehicle unavailable"}`, http.StatusConflict)
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO bookings(id,lead_id,vehicle_id,amount) VALUES($1,$2,$3,$4)`, id, in.LeadID, in.VehicleID, in.Amount); err != nil {
		if isDup(err) {
			http.Error(w, `{"error":"vehicle unavailable"}`, http.StatusConflict)
			return
		}
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE vehicles SET status='RESERVED', updated_at=now() WHERE id=$1`, in.VehicleID); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE leads SET status='BOOKED', updated_at=now() WHERE id=$1`, in.LeadID); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

func (s *Server) patchBooking(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "/api/bookings/")
	if badUUID(w, id) {
		return
	}
	var in map[string]any
	if err := readJSON(r, &in); err != nil {
		http.Error(w, `{"error":"bad json"}`, 400)
		return
	}
	if st, ok := in["status"].(string); ok {
		var prev string
		_ = s.Pool.QueryRow(r.Context(), `SELECT status FROM bookings WHERE id=$1`, id).Scan(&prev)
		_, _ = s.Pool.Exec(r.Context(), `UPDATE bookings SET status=$1, updated_at=now() WHERE id=$2`, st, id)
		if cl := auth.Current(r); cl != nil {
			audit(r.Context(), s.Pool, cl.Email, "booking.status", "booking", id, prev, st)
		}
		if st == "CANCELLED" && prev != "CANCELLED" {
			// Release the vehicle back to the lot.
			_, _ = s.Pool.Exec(r.Context(), `UPDATE vehicles SET status='AVAILABLE', updated_at=now() WHERE id=(SELECT vehicle_id FROM bookings WHERE id=$1) AND status IN ('RESERVED','BOOKED')`, id)
		}
		if st == "DELIVERED" || st == "COMPLETED" {
			_, _ = s.Pool.Exec(r.Context(), `UPDATE bookings SET status='COMPLETED', updated_at=now() WHERE id=$1`, id)
			_, _ = s.Pool.Exec(r.Context(), `UPDATE vehicles SET status='DELIVERED', updated_at=now() WHERE id=(SELECT vehicle_id FROM bookings WHERE id=$1)`, id)
			_, _ = s.Pool.Exec(r.Context(), `UPDATE leads SET status='CONVERTED', updated_at=now() WHERE id=(SELECT lead_id FROM bookings WHERE id=$1)`, id)
		}
		if d, ok := in["delivery_at"].(string); ok && d != "" {
			_, _ = s.Pool.Exec(r.Context(), `UPDATE bookings SET delivery_at=$1 WHERE id=$2`, d, id)
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) listConversations(w http.ResponseWriter, r *http.Request) {
	limit := qInt(r, "limit", 30)
	if limit > 100 {
		limit = 100
	}
	offset := qInt(r, "offset", 0)
	rows, err := s.Pool.Query(r.Context(), `SELECT c.id::text,cu.name,cu.phone,c.status,c.updated_at,(SELECT COUNT(*) FROM messages m WHERE m.conversation_id=c.id),c.lead_id::text FROM conversations c JOIN customers cu ON cu.id=c.customer_id ORDER BY c.updated_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, name, phone, status string
		var ts any
		var n int
		var leadID *string
		_ = rows.Scan(&id, &name, &phone, &status, &ts, &n, &leadID)
		lid := ""
		if leadID != nil {
			lid = *leadID
		}
		out = append(out, map[string]any{"id": id, "name": name, "phone": phone, "status": status, "updated_at": ts, "messages": n, "lead_id": lid})
	}
	writeJSON(w, out)
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	conv := r.URL.Query().Get("conversation_id")
	if conv == "" {
		http.Error(w, `{"error":"conversation_id required"}`, 400)
		return
	}
	if badUUID(w, conv) {
		return
	}
	rows, err := s.Pool.Query(r.Context(), `SELECT direction,body,media_path,created_at FROM messages WHERE conversation_id=$1 ORDER BY created_at ASC LIMIT 200`, conv)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var d, b, mp string
		var ts any
		_ = rows.Scan(&d, &b, &mp, &ts)
		out = append(out, map[string]any{"direction": d, "body": b, "media": mp, "created_at": ts})
	}
	writeJSON(w, out)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var leads, vehicles, td, fu, bk int
	_ = s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM leads`).Scan(&leads)
	_ = s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM vehicles WHERE status='AVAILABLE'`).Scan(&vehicles)
	_ = s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM test_drives WHERE status='SCHEDULED'`).Scan(&td)
	_ = s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM followups WHERE status='pending'`).Scan(&fu)
	_ = s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM bookings WHERE status IN ('PENDING','CONFIRMED')`).Scan(&bk)
	writeJSON(w, map[string]any{"leads": leads, "available_vehicles": vehicles, "scheduled_test_drives": td, "pending_followups": fu, "open_bookings": bk})
}
