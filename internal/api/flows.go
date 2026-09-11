package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"sellingbot/internal/auth"
)

func isAdmin(r *http.Request) bool {
	cl := auth.Current(r)
	return cl != nil && cl.Role == "admin"
}

func isDup(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// POST /api/users (admin only) — create sales/admin logins.
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := readJSON(r, &in); err != nil || in.Email == "" || in.Password == "" {
		http.Error(w, `{"error":"email+password required"}`, 400)
		return
	}
	if in.Role != "sales" && in.Role != "admin" {
		in.Role = "sales"
	}
	hash, err := auth.Hash(in.Password)
	if err != nil {
		http.Error(w, `{"error":"hash failed"}`, 500)
		return
	}
	id := uuid.NewString()
	if _, err := s.Pool.Exec(r.Context(), `INSERT INTO users(id,email,password_hash,role) VALUES($1,$2,$3,$4)`, id, in.Email, hash, in.Role); err != nil {
		if isDup(err) {
			http.Error(w, `{"error":"email exists"}`, 409)
			return
		}
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	writeJSON(w, map[string]any{"id": id, "email": in.Email, "role": in.Role})
}

// GET /api/users (admin only)
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	rows, err := s.Pool.Query(r.Context(), `SELECT id::text,email,role,created_at FROM users ORDER BY created_at`)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, email, role string
		var ts any
		_ = rows.Scan(&id, &email, &role, &ts)
		out = append(out, map[string]any{"id": id, "email": email, "role": role, "created_at": ts})
	}
	writeJSON(w, out)
}
func (s *Server) assignLead(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	p := r.URL.Path[len("/api/leads/"):]
	id := p[:len(p)-len("/assign")]
	var in struct {
		SalesUserID string `json:"sales_user_id"`
	}
	if err := readJSON(r, &in); err != nil || id == "" {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	if badUUID(w, id, in.SalesUserID) {
		return
	}
	var arg any
	if in.SalesUserID != "" {
		arg = in.SalesUserID
	}
	var prev any
	_ = s.Pool.QueryRow(r.Context(), `SELECT sales_user_id::text FROM leads WHERE id=$1`, id).Scan(&prev)
	if _, err := s.Pool.Exec(r.Context(), `UPDATE leads SET sales_user_id=$1, updated_at=now() WHERE id=$2`, arg, id); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	actor := ""
	if cl := auth.Current(r); cl != nil {
		actor = cl.Email
	}
	audit(r.Context(), s.Pool, actor, "lead.assign", "lead", id, fmt.Sprintf("%v", prev), in.SalesUserID)
	writeJSON(w, map[string]any{"ok": true})
}

// PATCH /api/leads/{id}/interest {interest: INTERESTED|THINKING|NOT_INTERESTED}
func (s *Server) leadInterest(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path[len("/api/leads/"):]
	id := p[:len(p)-len("/interest")]
	var in struct {
		Interest string `json:"interest"`
	}
	if err := readJSON(r, &in); err != nil || id == "" {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	if badUUID(w, id) {
		return
	}
	in.Interest = strings.ToUpper(strings.TrimSpace(in.Interest))
	status := ""
	switch in.Interest {
	case "INTERESTED":
		status = "QUALIFIED"
	case "THINKING":
		status = "FOLLOWUP"
	case "NOT_INTERESTED":
		status = "LOST"
	default:
		http.Error(w, `{"error":"interest must be INTERESTED|THINKING|NOT_INTERESTED"}`, 400)
		return
	}
	if _, err := s.Pool.Exec(r.Context(), `UPDATE leads SET interest=$1, status=$2, updated_at=now() WHERE id=$3`, in.Interest, status, id); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// GET /api/leads/{id}/more?offset=&limit= — next inventory page without restarting convo.
func (s *Server) moreCars(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path[len("/api/leads/"):]
	id := p[:len(p)-len("/more")]
	if badUUID(w, id) {
		return
	}
	offset := qInt(r, "offset", 0)
	limit := qInt(r, "limit", 3)
	if limit > 10 {
		limit = 10
	}
	_ = id
	rows, err := s.Pool.Query(r.Context(), `SELECT id::text,make,model,year,price,fuel,transmission FROM vehicles WHERE status='AVAILABLE' ORDER BY created_at DESC OFFSET $1 LIMIT $2`, offset, limit)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var vid, make, model, fuel, trans string
		var year, price int
		_ = rows.Scan(&vid, &make, &model, &year, &price, &fuel, &trans)
		out = append(out, map[string]any{"vehicle_id": vid, "make": make, "model": model, "year": year, "price": price, "fuel": fuel, "transmission": trans})
	}
	writeJSON(w, out)
}

func (s *Server) listNegotiations(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Pool.Query(r.Context(), `SELECT n.id::text,c.phone,v.make,v.model,n.original_price,n.customer_offer,n.sales_offer,n.final_price,n.notes FROM negotiations n JOIN leads l ON l.id=n.lead_id JOIN customers c ON c.id=l.customer_id JOIN vehicles v ON v.id=n.vehicle_id ORDER BY n.created_at DESC LIMIT 50`)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, phone, make, model, notes string
		var op, co, so, fp int
		_ = rows.Scan(&id, &phone, &make, &model, &op, &co, &so, &fp, &notes)
		out = append(out, map[string]any{"id": id, "phone": phone, "vehicle": make + " " + model, "original_price": op, "customer_offer": co, "sales_offer": so, "final_price": fp, "notes": notes})
	}
	writeJSON(w, out)
}

func (s *Server) createNegotiation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LeadID        string `json:"lead_id"`
		VehicleID     string `json:"vehicle_id"`
		OriginalPrice int    `json:"original_price"`
		CustomerOffer int    `json:"customer_offer"`
		SalesOffer    int    `json:"sales_offer"`
		FinalPrice    int    `json:"final_price"`
		Notes         string `json:"notes"`
	}
	if err := readJSON(r, &in); err != nil || in.LeadID == "" || in.VehicleID == "" {
		http.Error(w, `{"error":"lead_id+vehicle_id required"}`, 400)
		return
	}
	if badUUID(w, in.LeadID, in.VehicleID) {
		return
	}
	if in.OriginalPrice == 0 {
		_ = s.Pool.QueryRow(r.Context(), `SELECT price FROM vehicles WHERE id=$1`, in.VehicleID).Scan(&in.OriginalPrice)
	}
	id := uuid.NewString()
	if _, err := s.Pool.Exec(r.Context(), `INSERT INTO negotiations(id,lead_id,vehicle_id,original_price,customer_offer,sales_offer,final_price,notes) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, in.LeadID, in.VehicleID, in.OriginalPrice, in.CustomerOffer, in.SalesOffer, in.FinalPrice, in.Notes); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

func (s *Server) patchNegotiation(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "/api/negotiations/")
	if badUUID(w, id) {
		return
	}
	var in map[string]any
	if err := readJSON(r, &in); err != nil || id == "" {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	allowed := map[string]bool{"customer_offer": true, "sales_offer": true, "final_price": true, "notes": true}
	actor := ""
	if cl := auth.Current(r); cl != nil {
		actor = cl.Email
	}
	for k, v := range in {
		if !allowed[k] {
			continue
		}
		var prev any
		_ = s.Pool.QueryRow(r.Context(), `SELECT `+k+` FROM negotiations WHERE id=$1`, id).Scan(&prev)
		if _, err := s.Pool.Exec(r.Context(), `UPDATE negotiations SET `+k+`=$1, updated_at=now() WHERE id=$2`, v, id); err != nil {
			http.Error(w, `{"error":"db"}`, 500)
			return
		}
		audit(r.Context(), s.Pool, actor, "negotiation."+k, "negotiation", id, fmt.Sprintf("%v", prev), fmt.Sprintf("%v", v))
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) listFinance(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Pool.Query(r.Context(), `SELECT f.id::text,c.phone,f.loan_amount,f.tenure_months,f.employment,f.income,f.status FROM finance_requests f JOIN leads l ON l.id=f.lead_id JOIN customers c ON c.id=l.customer_id ORDER BY f.created_at DESC LIMIT 50`)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, phone, emp, status string
		var amt, ten, inc int
		_ = rows.Scan(&id, &phone, &amt, &ten, &emp, &inc, &status)
		out = append(out, map[string]any{"id": id, "phone": phone, "loan_amount": amt, "tenure_months": ten, "employment": emp, "income": inc, "status": status})
	}
	writeJSON(w, out)
}

func (s *Server) createFinance(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LeadID       string `json:"lead_id"`
		LoanAmount   int    `json:"loan_amount"`
		TenureMonths int    `json:"tenure_months"`
		Employment   string `json:"employment"`
		Income       int    `json:"income"`
	}
	if err := readJSON(r, &in); err != nil || in.LeadID == "" {
		http.Error(w, `{"error":"lead_id required"}`, 400)
		return
	}
	if badUUID(w, in.LeadID) {
		return
	}
	id := uuid.NewString()
	if _, err := s.Pool.Exec(r.Context(), `INSERT INTO finance_requests(id,lead_id,loan_amount,tenure_months,employment,income) VALUES($1,$2,$3,$4,$5,$6)`,
		id, in.LeadID, in.LoanAmount, in.TenureMonths, in.Employment, in.Income); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

func sellID(r *http.Request, suffix string) (string, bool) {
	p := r.URL.Path[len("/api/sell-requests/"):]
	if !strings.HasSuffix(p, suffix) {
		return "", false
	}
	id := p[:len(p)-len(suffix)]
	if id == "" {
		return "", false
	}
	return id, true
}

// POST /api/sell-requests/{id}/accept {price} — valuation accepted: the car
// enters inventory as AVAILABLE and the customer's WhatsApp photos are linked.
func (s *Server) acceptSell(w http.ResponseWriter, r *http.Request) {
	id, ok := sellID(r, "/accept")
	if !ok || badUUID(w, id) {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	var in struct {
		Price int `json:"price"`
	}
	_ = readJSON(r, &in)
	if in.Price < 0 {
		http.Error(w, `{"error":"price must be >= 0"}`, 400)
		return
	}
	ctx := r.Context()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer tx.Rollback(ctx)
	var leadID, brand, model, reg, fuel, trans, cond, loc string
	var year, km, photos int
	var status string
	err = tx.QueryRow(ctx, `SELECT lead_id::text,brand,model,year,registration,km,fuel,transmission,condition,location,photo_count,status
		FROM sell_requests WHERE id=$1 FOR UPDATE`, id).Scan(
		&leadID, &brand, &model, &year, &reg, &km, &fuel, &trans, &cond, &loc, &photos, &status)
	if err != nil {
		http.Error(w, `{"error":"sell request not found"}`, 404)
		return
	}
	if status != "VALUATION_PENDING" {
		http.Error(w, `{"error":"only VALUATION_PENDING can be accepted"}`, 409)
		return
	}
	vid := uuid.NewString()
	desc := strings.TrimSpace(cond + " " + loc + " " + reg)
	if _, err := tx.Exec(ctx, `INSERT INTO vehicles(id,make,model,year,price,fuel,transmission,km,status,description)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'AVAILABLE',$9)`,
		vid, brand, model, year, in.Price, fuel, trans, km, desc); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	// Link the customer's WhatsApp photos to the new vehicle. Collect first:
	// pgx forbids new queries on a tx while rows are still open.
	var photoPaths []string
	rows, err := tx.Query(ctx, `SELECT DISTINCT m.media_path FROM messages m
		JOIN conversations c ON c.id=m.conversation_id
		WHERE c.lead_id=$1 AND m.media_path<>'' ORDER BY m.media_path`, leadID)
	if err != nil {
		log.Printf("[acceptSell] photo query: %v", err)
	} else {
		for rows.Next() {
			var mp string
			if err := rows.Scan(&mp); err == nil {
				photoPaths = append(photoPaths, mp)
			}
		}
		rows.Close()
	}
	for n, mp := range photoPaths {
		_, _ = tx.Exec(ctx, `INSERT INTO vehicle_images(vehicle_id,path,sort_order) VALUES($1,$2,$3)`, vid, mp, n)
	}
	if _, err := tx.Exec(ctx, `UPDATE sell_requests SET status='ACCEPTED' WHERE id=$1`, id); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	actor := ""
	if cl := auth.Current(r); cl != nil {
		actor = cl.Email
	}
	audit(ctx, s.Pool, actor, "sell.accept", "sell_request", id, "VALUATION_PENDING", "ACCEPTED")
	writeJSON(w, map[string]any{"id": id, "vehicle_id": vid})
}

// POST /api/sell-requests/{id}/reject — back to the customer with a no.
func (s *Server) rejectSell(w http.ResponseWriter, r *http.Request) {
	id, ok := sellID(r, "/reject")
	if !ok || badUUID(w, id) {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	var prev string
	_ = s.Pool.QueryRow(r.Context(), `SELECT status FROM sell_requests WHERE id=$1`, id).Scan(&prev)
	if prev == "" {
		http.Error(w, `{"error":"sell request not found"}`, 404)
		return
	}
	if _, err := s.Pool.Exec(r.Context(), `UPDATE sell_requests SET status='REJECTED' WHERE id=$1`, id); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	actor := ""
	if cl := auth.Current(r); cl != nil {
		actor = cl.Email
	}
	audit(r.Context(), s.Pool, actor, "sell.reject", "sell_request", id, prev, "REJECTED")
	writeJSON(w, map[string]any{"ok": true})
}

// POST /api/sell-requests/{id}/reopen — back to the valuation queue.
func (s *Server) reopenSell(w http.ResponseWriter, r *http.Request) {
	id, ok := sellID(r, "/reopen")
	if !ok || badUUID(w, id) {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	if _, err := s.Pool.Exec(r.Context(), `UPDATE sell_requests SET status='VALUATION_PENDING' WHERE id IN ($1) AND status IN ('REJECTED','ACCEPTED')`, id); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) listSellRequests(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Pool.Query(r.Context(), `SELECT s.id::text,c.phone,s.brand,s.model,s.year,s.registration,s.km,s.fuel,s.transmission,s.condition,s.location,s.photo_count,s.status FROM sell_requests s JOIN leads l ON l.id=s.lead_id JOIN customers c ON c.id=l.customer_id ORDER BY s.created_at DESC LIMIT 50`)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, phone, brand, model, reg, fuel, trans, cond, loc, status string
		var year, km, photos int
		_ = rows.Scan(&id, &phone, &brand, &model, &year, &reg, &km, &fuel, &trans, &cond, &loc, &photos, &status)
		out = append(out, map[string]any{"id": id, "phone": phone, "brand": brand, "model": model, "year": year, "registration": reg, "km": km, "fuel": fuel, "transmission": trans, "condition": cond, "location": loc, "photo_count": photos, "status": status})
	}
	writeJSON(w, out)
}

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Pool.Query(r.Context(), `SELECT r.id::text,c.phone,r.rating,r.review,r.referral_source,r.referred_customer FROM reviews r JOIN bookings b ON b.id=r.booking_id JOIN leads l ON l.id=b.lead_id JOIN customers c ON c.id=l.customer_id ORDER BY r.created_at DESC LIMIT 50`)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, phone, review, src, ref string
		var rating int
		_ = rows.Scan(&id, &phone, &rating, &review, &src, &ref)
		out = append(out, map[string]any{"id": id, "phone": phone, "rating": rating, "review": review, "referral_source": src, "referred_customer": ref})
	}
	writeJSON(w, out)
}

func (s *Server) createReview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BookingID       string `json:"booking_id"`
		Rating          int    `json:"rating"`
		Review          string `json:"review"`
		ReferralSource  string `json:"referral_source"`
		ReferredCustomer string `json:"referred_customer"`
	}
	if err := readJSON(r, &in); err != nil || in.BookingID == "" {
		http.Error(w, `{"error":"booking_id required"}`, 400)
		return
	}
	if badUUID(w, in.BookingID) {
		return
	}
	if in.Rating < 1 || in.Rating > 5 {
		http.Error(w, `{"error":"rating must be 1..5"}`, 400)
		return
	}
	id := uuid.NewString()
	if _, err := s.Pool.Exec(r.Context(), `INSERT INTO reviews(id,booking_id,rating,review,referral_source,referred_customer) VALUES($1,$2,$3,$4,$5,$6)`,
		id, in.BookingID, in.Rating, in.Review, in.ReferralSource, in.ReferredCustomer); err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

func (s *Server) listOutbox(w http.ResponseWriter, r *http.Request) {
	limit := qInt(r, "limit", 50)
	rows, err := s.Pool.Query(r.Context(), `SELECT id::text,phone,body,status,attempts,created_at FROM outbox ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		http.Error(w, `{"error":"db"}`, 500)
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, phone, body, status string
		var att int
		var ts any
		_ = rows.Scan(&id, &phone, &body, &status, &att, &ts)
		out = append(out, map[string]any{"id": id, "phone": phone, "body": body, "status": status, "attempts": att, "created_at": ts})
	}
	writeJSON(w, out)
}

// PATCH /api/conversations?id= — human takeover toggle / close (STATE-007).
func (s *Server) patchConversation(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	var in struct {
		BotEnabled *bool  `json:"bot_enabled"`
		Status     string `json:"status"`
	}
	if err := readJSON(r, &in); err != nil || id == "" {
		http.Error(w, `{"error":"id query + json required"}`, 400)
		return
	}
	if badUUID(w, id) {
		return
	}
	actor := ""
	if cl := auth.Current(r); cl != nil {
		actor = cl.Email
	}
	if in.BotEnabled != nil {
		var prev bool
		_ = s.Pool.QueryRow(r.Context(), `SELECT bot_enabled FROM conversations WHERE id=$1`, id).Scan(&prev)
		if _, err := s.Pool.Exec(r.Context(), `UPDATE conversations SET bot_enabled=$1 WHERE id=$2`, *in.BotEnabled, id); err != nil {
			http.Error(w, `{"error":"db"}`, 500)
			return
		}
		audit(r.Context(), s.Pool, actor, "conversation.takeover", "conversation", id, fmt.Sprintf("%v", prev), fmt.Sprintf("%v", *in.BotEnabled))
	}
	if in.Status != "" {
		_, _ = s.Pool.Exec(r.Context(), `UPDATE conversations SET status=$1 WHERE id=$2`, in.Status, id)
	}
	writeJSON(w, map[string]any{"ok": true})
}
