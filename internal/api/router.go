package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"sellingbot/internal/auth"
	"sellingbot/internal/images"
	"sellingbot/internal/whatsapp"
)

type Server struct {
	Pool      *pgxpool.Pool
	Secret    string
	WA        *whatsapp.Worker
	Images    *images.Store
	DataDir   string
	StartedAt time.Time
	Version   string // git sha (Railway) or "dev" — surfaced in /api/health
	Logger    *zerolog.Logger
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func idParam(r *http.Request, prefix string) string {
	p := strings.TrimPrefix(r.URL.Path, prefix)
	p = strings.Trim(p, "/")
	if i := strings.Index(p, "/"); i >= 0 {
		return p[:i]
	}
	return p
}

// badUUID writes 400 when any non-empty value is not a UUID (§V3-39).
func badUUID(w http.ResponseWriter, vals ...string) bool {
	for _, v := range vals {
		if v == "" {
			continue
		}
		if _, err := uuid.Parse(v); err != nil {
			http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
			return true
		}
	}
	return false
}

// badTime writes 400 when s is not RFC3339 (§V3-39).
func badTime(w http.ResponseWriter, s string) bool {
	if _, err := time.Parse(time.RFC3339, s); err != nil {
		http.Error(w, `{"error":"invalid datetime, use RFC3339"}`, http.StatusBadRequest)
		return true
	}
	return false
}

// Router wires all routes. Auth required except health/login/static/images.
func (s *Server) Router(webFS http.FileSystem) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("POST /api/auth/login", auth.LoginHandler(s.Pool, s.Secret))
	authed := auth.Middleware(s.Secret, http.HandlerFunc(s.authedRoutes))
	mux.Handle("/api/auth/me", auth.Middleware(s.Secret, auth.MeHandler()))
	mux.Handle("/api/", authed)

	// images (public read so WhatsApp/admin <img> works; upload stays authed)
	mux.Handle("GET /images/", http.StripPrefix("/images/", http.FileServer(http.Dir(filepath.Join(s.DataDir, "images")))))
	mux.Handle("GET /media/", http.StripPrefix("/media/", http.FileServer(http.Dir(filepath.Join(s.DataDir, "media")))))

	// static admin
	if webFS != nil {
		mux.Handle("/", http.FileServer(webFS))
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("sellingbot ok — web/dist missing, API at /api/health"))
		})
	}
	// CORS wrapper
	cors := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		if r.Method == "OPTIONS" {
			return
		}
		mux.ServeHTTP(w, r)
	})
	// Production chain (inner → outer): rate limit (health/metrics exempt)
	// → access log → security headers → panic recovery.
	var h http.Handler = cors
	h = rateLimit(newIPLimiter(600, time.Minute), h)
	h = accessLog(h)
	h = securityHeaders(h)
	return recoverer(h)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbOK := s.Pool.Ping(ctx) == nil
	disk := ""
	diskPct := -1
	if st, err := os.Stat(s.DataDir); err == nil && st.IsDir() {
		disk = "ok"
		var fs syscall.Statfs_t
		if err := syscall.Statfs(s.DataDir, &fs); err == nil && fs.Blocks > 0 {
			diskPct = int(100 * (fs.Blocks - fs.Bavail) / fs.Blocks)
		}
	}
	writeJSON(w, map[string]any{
		"ok": true, "db": dbOK, "disk": disk, "disk_used_pct": diskPct,
		"timezone": whatsapp.ZoneName(), "version": s.Version,
		"uptime_seconds": int64(time.Since(s.StartedAt).Seconds()),
		"whatsapp": s.WA.Status(), "time": time.Now().UTC(),
	})
}

// metrics exposes runtime vitals for long-run/stability monitoring (§30).
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	st := s.Pool.Stat()
	var pend, outbox int
	_ = s.Pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM followups WHERE status IN ('pending','sending')`).Scan(&pend)
	_ = s.Pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM outbox WHERE status='PENDING'`).Scan(&outbox)
	writeJSON(w, map[string]any{
		"uptime_seconds":   int64(time.Since(s.StartedAt).Seconds()),
		"goroutines":       runtime.NumGoroutine(),
		"db_total_conns":   st.TotalConns(),
		"db_acquired":      st.AcquiredConns(),
		"db_idle":          st.IdleConns(),
		"pending_followups": pend,
		"outbox_pending":   outbox,
		"whatsapp":         s.WA.Status(),
	})
}

func (s *Server) authedRoutes(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/api/customers" && r.Method == "GET":
		s.listCustomers(w, r)
	case p == "/api/customers" && r.Method == "POST":
		s.createCustomer(w, r)
	case strings.HasPrefix(p, "/api/customers/") && r.Method == "PATCH":
		s.patchCustomer(w, r)
	case strings.HasPrefix(p, "/api/customers/") && r.Method == "DELETE":
		s.deleteCustomer(w, r)
	case p == "/api/leads" && r.Method == "GET":
		s.listLeads(w, r)
	case strings.HasPrefix(p, "/api/leads/") && strings.HasSuffix(p, "/assign") && r.Method == "PATCH":
		s.assignLead(w, r)
	case strings.HasPrefix(p, "/api/leads/") && strings.HasSuffix(p, "/interest") && r.Method == "PATCH":
		s.leadInterest(w, r)
	case strings.HasPrefix(p, "/api/leads/") && r.Method == "DELETE":
		s.deleteLead(w, r)
	case p == "/api/vehicles" && r.Method == "GET":
		s.listVehicles(w, r)
	case p == "/api/vehicles/count" && r.Method == "GET":
		s.vehicleCounts(w, r)
	case p == "/api/vehicles" && r.Method == "POST":
		s.createVehicle(w, r)
	case p == "/api/vehicles/import" && r.Method == "POST":
		s.importVehicles(w, r)
	case strings.HasPrefix(p, "/api/vehicles/") && strings.HasSuffix(p, "/images") && r.Method == "POST":
		s.uploadVehicleImage(w, r)
	case strings.HasPrefix(p, "/api/vehicles/") && r.Method == "PATCH":
		s.patchVehicle(w, r)
	case strings.HasPrefix(p, "/api/vehicles/") && r.Method == "DELETE":
		s.deleteVehicle(w, r)
	case p == "/api/requirements" && r.Method == "GET":
		s.listRequirements(w, r)
	case strings.HasPrefix(p, "/api/leads/") && strings.HasSuffix(p, "/match") && r.Method == "POST":
		s.matchLead(w, r)
	case strings.HasPrefix(p, "/api/leads/") && strings.HasSuffix(p, "/more") && r.Method == "GET":
		s.moreCars(w, r)
	case p == "/api/users" && r.Method == "POST":
		s.createUser(w, r)
	case p == "/api/users" && r.Method == "GET":
		s.listUsers(w, r)
	case strings.HasPrefix(p, "/api/users/") && r.Method == "PATCH":
		s.patchUser(w, r)
	case strings.HasPrefix(p, "/api/users/") && r.Method == "DELETE":
		s.deleteUser(w, r)
	case p == "/api/negotiations" && r.Method == "GET":
		s.listNegotiations(w, r)
	case p == "/api/negotiations" && r.Method == "POST":
		s.createNegotiation(w, r)
	case strings.HasPrefix(p, "/api/negotiations/") && r.Method == "PATCH":
		s.patchNegotiation(w, r)
	case p == "/api/finance" && r.Method == "GET":
		s.listFinance(w, r)
	case p == "/api/finance" && r.Method == "POST":
		s.createFinance(w, r)
	case p == "/api/sell-requests" && r.Method == "GET":
		s.listSellRequests(w, r)
	case strings.HasPrefix(p, "/api/sell-requests/") && strings.HasSuffix(p, "/accept") && r.Method == "POST":
		s.acceptSell(w, r)
	case strings.HasPrefix(p, "/api/sell-requests/") && strings.HasSuffix(p, "/reject") && r.Method == "POST":
		s.rejectSell(w, r)
	case strings.HasPrefix(p, "/api/sell-requests/") && strings.HasSuffix(p, "/reopen") && r.Method == "POST":
		s.reopenSell(w, r)
	case p == "/api/reviews" && r.Method == "GET":
		s.listReviews(w, r)
	case p == "/api/reviews" && r.Method == "POST":
		s.createReview(w, r)
	case strings.HasPrefix(p, "/api/reviews/") && r.Method == "DELETE":
		s.deleteReview(w, r)
	case p == "/api/finance" && r.Method == "POST":
		s.createFinance(w, r)
	case strings.HasPrefix(p, "/api/finance/") && r.Method == "PATCH":
		s.patchFinance(w, r)
	case p == "/api/outbox" && r.Method == "GET":
		s.listOutbox(w, r)
	case p == "/api/test-drives" && r.Method == "GET":
		s.listTestDrives(w, r)
	case p == "/api/test-drives" && r.Method == "POST":
		s.createTestDrive(w, r)
	case strings.HasPrefix(p, "/api/test-drives/") && r.Method == "PATCH":
		s.patchTestDrive(w, r)
	case p == "/api/followups" && r.Method == "GET":
		s.listFollowups(w, r)
	case p == "/api/followups" && r.Method == "POST":
		s.createFollowup(w, r)
	case strings.HasPrefix(p, "/api/followups/") && r.Method == "PATCH":
		s.patchFollowup(w, r)
	case strings.HasPrefix(p, "/api/followups/") && r.Method == "DELETE":
		s.deleteFollowup(w, r)
	case p == "/api/bookings" && r.Method == "GET":
		s.listBookings(w, r)
	case p == "/api/bookings" && r.Method == "POST":
		s.createBooking(w, r)
	case strings.HasPrefix(p, "/api/bookings/") && r.Method == "PATCH":
		s.patchBooking(w, r)
	case p == "/api/payments" && r.Method == "GET":
		s.listPayments(w, r)
	case p == "/api/payments" && r.Method == "POST":
		s.createPayment(w, r)
	case strings.HasPrefix(p, "/api/payments/") && r.Method == "PATCH":
		s.patchPayment(w, r)
	case p == "/api/exchange-valuations" && r.Method == "GET":
		s.listExchangeValuations(w, r)
	case strings.HasPrefix(p, "/api/exchange-valuations/") && strings.HasSuffix(p, "/accept") && r.Method == "POST":
		s.acceptExchange(w, r)
	case strings.HasPrefix(p, "/api/exchange-valuations/") && strings.HasSuffix(p, "/reject") && r.Method == "POST":
		s.rejectExchange(w, r)
	case strings.HasPrefix(p, "/api/exchange-valuations/") && strings.HasSuffix(p, "/reopen") && r.Method == "POST":
		s.reopenExchange(w, r)
	case p == "/api/inspections" && r.Method == "GET":
		s.listInspections(w, r)
	case strings.HasPrefix(p, "/api/inspections/") && r.Method == "PATCH":
		s.patchInspection(w, r)
	case p == "/api/conversations" && r.Method == "GET":
		s.listConversations(w, r)
	case p == "/api/messages" && r.Method == "GET":
		s.listMessages(w, r)
	case p == "/api/dashboard" && r.Method == "GET":
		s.dashboard(w, r)
	case p == "/api/metrics" && r.Method == "GET":
		s.metrics(w, r)
	case p == "/api/conversations" && r.Method == "PATCH":
		s.patchConversation(w, r)
	case p == "/api/settings" && r.Method == "GET":
		s.listSettings(w, r)
	case p == "/api/settings" && r.Method == "PATCH":
		s.patchSettings(w, r)
	case p == "/api/whatsapp/status" && r.Method == "GET":
		writeJSON(w, s.WA.Status())
	case p == "/api/whatsapp/qr" && r.Method == "GET":
		png, err := s.WA.QRPNG()
		if err != nil {
			http.Error(w, `{"error":"no qr"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	case p == "/api/whatsapp/send" && r.Method == "POST":
		var in struct {
			Phone   string `json:"phone"`
			Message string `json:"message"`
		}
		if err := readJSON(r, &in); err != nil || in.Phone == "" || in.Message == "" {
			http.Error(w, `{"error":"phone+message required"}`, http.StatusBadRequest)
			return
		}
		if err := s.WA.Send(r.Context(), in.Phone, in.Message); err != nil {
			http.Error(w, `{"error":"send failed"}`, http.StatusBadGateway)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	case p == "/api/whatsapp/logout" && r.Method == "POST":
		_ = s.WA.Logout(r.Context())
		writeJSON(w, map[string]any{"ok": true})
	case p == "/api/whatsapp/reconnect" && r.Method == "POST":
		_ = s.WA.Reconnect(r.Context())
		writeJSON(w, map[string]any{"ok": true})
	case p == "/api/whatsapp/simulate" && r.Method == "POST":
		// Local tester without a phone: runs the same state machine path.
		var in struct {
			Phone string `json:"phone"`
			Name  string `json:"name"`
			Body  string `json:"body"`
		}
		if err := readJSON(r, &in); err != nil || in.Body == "" {
			http.Error(w, `{"error":"body required"}`, http.StatusBadRequest)
			return
		}
		if in.Phone == "" {
			in.Phone = "9999999999"
		}
		reply, err := s.WA.HandleInbound(r.Context(), in.Phone, in.Name, in.Body)
		if err != nil {
			s.Logger.Error().Msgf("[api] simulate %s %q: %v", in.Phone, in.Body, err)
			http.Error(w, `{"error":"handler failed"}`, http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"reply": reply})
	default:
		http.NotFound(w, r)
	}
}
