package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"sellingbot/internal/auth"
	"sellingbot/internal/botengine"
	"sellingbot/internal/whatsapp"
)

func setupTestServer(t *testing.T) (*Server, string, func()) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	secret := "test-secret-key-1234567890123456"
	engine := botengine.New(pool)
	wa := whatsapp.New(pool, t.TempDir(), false, dsn, engine)
	srv := &Server{
		Pool:   pool,
		Secret: secret,
		WA:     wa,
	}

	token, err := auth.Sign(secret, "admin-1", "admin@test.com", "admin")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	cleanup := func() {
		pool.Close()
	}
	return srv, token, cleanup
}

func TestBotAdmin_CRUD(t *testing.T) {
	srv, token, cleanup := setupTestServer(t)
	defer cleanup()

	handler := srv.Router(http.Dir(t.TempDir()))

	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req := httptest.NewRequest(method, path, &buf)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// 1. Create Flow
	rec := doReq("POST", "/api/bot/flows", map[string]any{
		"name":             "Admin Test Flow",
		"slug":             "admin_test_flow",
		"is_entry_flow":    false,
		"trigger_matching": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create flow code = %d; body = %s", rec.Code, rec.Body.String())
	}
	var flowResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &flowResp)
	flowID := flowResp["id"].(string)

	// 2. List Flows
	rec = doReq("GET", "/api/bot/flows", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list flows code = %d", rec.Code)
	}

	// 3. Patch Flow
	rec = doReq("PATCH", "/api/bot/flows/"+flowID, map[string]any{
		"name": "Updated Flow Name",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch flow code = %d", rec.Code)
	}

	// 4. Create Question
	rec = doReq("POST", "/api/bot/questions", map[string]any{
		"flow_id":        flowID,
		"field_name":     "budget",
		"question_text":  "What is your budget?",
		"question_type":  "number",
		"allowed_values": []string{},
		"order_index":    1,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create question code = %d; body = %s", rec.Code, rec.Body.String())
	}
	var qResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &qResp)
	qID := qResp["id"].(string)

	// 5. List Questions for Flow
	rec = doReq("GET", "/api/bot/flows/"+flowID+"/questions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list questions code = %d", rec.Code)
	}

	// 6. Create Condition
	rec = doReq("POST", "/api/bot/conditions", map[string]any{
		"question_id": qID,
		"field_name":  "budget",
		"operator":    "GREATER_THAN",
		"value":       "50000",
		"priority":    1,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create condition code = %d; body = %s", rec.Code, rec.Body.String())
	}
	var condResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &condResp)
	condID := condResp["id"].(string)

	// 7. List Conditions
	rec = doReq("GET", "/api/bot/conditions/"+qID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list conditions code = %d", rec.Code)
	}

	// 7b. Patch Condition
	rec = doReq("PATCH", "/api/bot/conditions/"+condID, map[string]any{
		"value": "60000",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch condition code = %d", rec.Code)
	}

	// 8. Delete Condition
	rec = doReq("DELETE", "/api/bot/conditions/"+condID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete condition code = %d", rec.Code)
	}

	// 9. Delete Question
	rec = doReq("DELETE", "/api/bot/questions/"+qID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete question code = %d", rec.Code)
	}

	// 10. Delete Flow
	rec = doReq("DELETE", "/api/bot/flows/"+flowID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete flow code = %d", rec.Code)
	}

	// 11. List Responses
	rec = doReq("GET", "/api/bot/responses", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list responses code = %d", rec.Code)
	}

	// 12. Reset Default Bot Config
	rec = doReq("POST", "/api/bot/reset-defaults", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset defaults code = %d; body = %s", rec.Code, rec.Body.String())
	}
}

func TestSimulateEndpoint(t *testing.T) {
	srv, token, cleanup := setupTestServer(t)
	defer cleanup()

	handler := srv.Router(http.Dir(t.TempDir()))

	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(map[string]any{
		"body": "hi",
	})
	req := httptest.NewRequest("POST", "/api/whatsapp/simulate", &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("simulate status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var res map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	reply, _ := res["reply"].(string)
	if reply == "" {
		t.Fatalf("expected non-empty reply, got %v", res)
	}
	if !strings.Contains(reply, "SUV") && !strings.Contains(reply, "Welcome") {
		t.Fatalf("expected welcome or entry question, got %q", reply)
	}
}

func TestSellRequests_AutoReview(t *testing.T) {
	srv, token, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()
	handler := srv.Router(http.Dir(t.TempDir()))

	// Create test customer & lead
	custID := "44444444-4444-4444-4444-444444444441"
	leadID := "44444444-4444-4444-4444-444444444442"
	sellID := "44444444-4444-4444-4444-444444444443"

	_, _ = srv.Pool.Exec(ctx, `INSERT INTO customers(id, name, phone, source) VALUES($1, 'Auto Tester', '9876543210', 'whatsapp') ON CONFLICT DO NOTHING`, custID)
	_, _ = srv.Pool.Exec(ctx, `INSERT INTO leads(id, customer_id, intent, status, state, source) VALUES($1, $2, 'SELL', 'QUALIFIED', 'DONE', 'whatsapp') ON CONFLICT DO NOTHING`, leadID, custID)
	_, _ = srv.Pool.Exec(ctx, `INSERT INTO sell_requests(id, lead_id, brand, model, year, registration, km, fuel, transmission, condition, location, status)
		VALUES($1, $2, 'Honda', 'City', 2021, 'MH01AA9999', 32000, 'Petrol', 'Automatic', 'Excellent', 'Mumbai', 'VALUATION_PENDING')
		ON CONFLICT (id) DO UPDATE SET status='VALUATION_PENDING'`, sellID, leadID)

	req := httptest.NewRequest("POST", "/api/sell-requests/auto-review", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("auto-review status = %d; body = %s", rec.Code, rec.Body.String())
	}

	var status string
	err := srv.Pool.QueryRow(ctx, `SELECT status FROM sell_requests WHERE id=$1`, sellID).Scan(&status)
	if err != nil || status != "ACCEPTED" {
		t.Fatalf("expected status ACCEPTED, got %s, err: %v", status, err)
	}

	var vehCount int
	_ = srv.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM vehicles WHERE make='Honda' AND model='City' AND acquired_via='customer_sell' AND status='AVAILABLE'`).Scan(&vehCount)
	if vehCount == 0 {
		t.Fatalf("expected vehicle created in vehicles inventory with AVAILABLE and customer_sell")
	}
}

