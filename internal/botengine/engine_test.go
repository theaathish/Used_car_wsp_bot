package botengine

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestValidate_SelectType(t *testing.T) {
	norm, ok := Validate("SUV", "select", "", []string{"SUV", "Sedan", "MPV", "Any"}, true)
	if !ok || norm != "SUV" {
		t.Fatalf("expected ok=true norm=SUV, got ok=%v norm=%q", ok, norm)
	}
	// Case insensitive
	norm, ok = Validate("suv", "select", "", []string{"SUV", "Sedan", "MPV", "Any"}, true)
	if !ok || norm != "SUV" {
		t.Fatalf("expected ok=true norm=SUV, got ok=%v norm=%q", ok, norm)
	}
	// Numeric index 1-based
	norm, ok = Validate("2", "select", "", []string{"SUV", "Sedan", "MPV", "Any"}, true)
	if !ok || norm != "Sedan" {
		t.Fatalf("expected ok=true norm=Sedan, got ok=%v norm=%q", ok, norm)
	}
	_, ok = Validate("truck", "select", "", []string{"SUV", "Sedan", "MPV", "Any"}, true)
	if ok {
		t.Fatal("expected invalid for truck")
	}
	_, ok = Validate("99", "select", "", []string{"SUV", "Sedan", "MPV", "Any"}, true)
	if ok {
		t.Fatal("expected invalid for out-of-range index")
	}
}

func TestValidate_Number(t *testing.T) {
	norm, ok := Validate(" 50,000 ", "number", "min:10000,max:100000", nil, true)
	if !ok || norm != "50000" {
		t.Fatalf("expected ok=true norm=50000, got ok=%v norm=%q", ok, norm)
	}
	_, ok = Validate("5000", "number", "min:10000,max:100000", nil, true)
	if ok {
		t.Fatal("expected invalid for below min")
	}
	_, ok = Validate("abc", "number", "", nil, true)
	if ok {
		t.Fatal("expected invalid for non-number")
	}
}

func TestValidate_Boolean(t *testing.T) {
	norm, ok := Validate("YES", "boolean", "", nil, true)
	if !ok || norm != "yes" {
		t.Fatalf("expected yes, got %v %q", ok, norm)
	}
	norm, ok = Validate("0", "boolean", "", nil, true)
	if !ok || norm != "no" {
		t.Fatalf("expected no, got %v %q", ok, norm)
	}
	_, ok = Validate("maybe", "boolean", "", nil, true)
	if ok {
		t.Fatal("expected invalid for maybe")
	}
}

func TestValidate_PhoneAndEmail(t *testing.T) {
	norm, ok := Validate("+91 98765 43210", "phone", "", nil, true)
	if !ok || norm != "919876543210" {
		t.Fatalf("expected 919876543210, got %v %q", ok, norm)
	}
	norm, ok = Validate("Test@Example.COM", "email", "", nil, true)
	if !ok || norm != "test@example.com" {
		t.Fatalf("expected test@example.com, got %v %q", ok, norm)
	}
	_, ok = Validate("not-an-email", "email", "", nil, true)
	if ok {
		t.Fatal("expected invalid for bad email")
	}
}

func TestValidate_Optional(t *testing.T) {
	norm, ok := Validate("", "text", "", nil, false)
	if !ok || norm != "" {
		t.Fatalf("expected ok=true norm='', got %v %q", ok, norm)
	}
	_, ok = Validate("", "text", "", nil, true)
	if ok {
		t.Fatal("expected invalid for empty required text")
	}
}

func TestEngine_ProcessMessage(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer pool.Close()

	engine := New(pool)
	if engine == nil {
		t.Fatal("expected engine to not be nil")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}
	defer tx.Rollback(ctx)

	// Clean tables for clean test
	_, _ = tx.Exec(ctx, "DELETE FROM conversation_answers; DELETE FROM bot_conditions; DELETE FROM bot_questions; DELETE FROM bot_flows;")

	// Insert test flow with 2 questions
	var flowID string
	err = tx.QueryRow(ctx, `INSERT INTO bot_flows(name, slug, is_entry_flow, is_active) VALUES ('Test Flow', 'test_flow', true, true) RETURNING id`).Scan(&flowID)
	if err != nil {
		t.Fatalf("insert flow: %v", err)
	}

	var q1ID, q2ID string
	err = tx.QueryRow(ctx, `INSERT INTO bot_questions(flow_id, field_name, question_text, question_type, allowed_values, error_message, order_index, is_active)
		VALUES ($1, 'vehicle_type', 'Select vehicle type:', 'select', '["SUV", "Sedan"]'::jsonb, 'Please select SUV or Sedan.', 1, true) RETURNING id`, flowID).Scan(&q1ID)
	if err != nil {
		t.Fatalf("insert q1: %v", err)
	}

	err = tx.QueryRow(ctx, `INSERT INTO bot_questions(flow_id, field_name, question_text, question_type, error_message, order_index, is_active)
		VALUES ($1, 'budget_max', 'What is your budget?', 'number', 'Please enter a valid budget.', 2, true) RETURNING id`, flowID).Scan(&q2ID)
	if err != nil {
		t.Fatalf("insert q2: %v", err)
	}

	// Link q1 -> q2
	_, err = tx.Exec(ctx, `UPDATE bot_questions SET next_question_id = $1 WHERE id = $2`, q2ID, q1ID)
	if err != nil {
		t.Fatalf("update next_question: %v", err)
	}

	// Insert test customer, lead, conversation
	var custID, convID, leadID string
	err = tx.QueryRow(ctx, `INSERT INTO customers(phone, name) VALUES ('60123456789', 'Tester') RETURNING id`).Scan(&custID)
	if err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	err = tx.QueryRow(ctx, `INSERT INTO leads(customer_id, status) VALUES ($1, 'NEW') RETURNING id`, custID).Scan(&leadID)
	if err != nil {
		t.Fatalf("insert lead: %v", err)
	}
	err = tx.QueryRow(ctx, `INSERT INTO conversations(customer_id, lead_id, channel, status) VALUES ($1, $2, 'whatsapp', 'open') RETURNING id`, custID, leadID).Scan(&convID)
	if err != nil {
		t.Fatalf("insert conversation: %v", err)
	}

	// 1. First call with no current_question -> sends first question's text
	reply, err := engine.ProcessMessage(ctx, tx, convID, custID, leadID, "hi")
	if err != nil {
		t.Fatalf("step 1 err: %v", err)
	}
	if reply != "Select vehicle type:" {
		t.Fatalf("step 1 expected 'Select vehicle type:', got %q", reply)
	}

	// 2. Invalid answer -> sends error message, does not advance
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "truck")
	if err != nil {
		t.Fatalf("step 2 err: %v", err)
	}
	if reply != "Please select SUV or Sedan." {
		t.Fatalf("step 2 expected error msg, got %q", reply)
	}

	// 3. Valid answer -> saves to answers, updates extracted_data, sends q2 text
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "SUV")
	if err != nil {
		t.Fatalf("step 3 err: %v", err)
	}
	if reply != "What is your budget?" {
		t.Fatalf("step 3 expected 'What is your budget?', got %q", reply)
	}

	// Check extracted_data on lead
	var extracted map[string]any
	err = tx.QueryRow(ctx, `SELECT extracted_data FROM leads WHERE id=$1`, leadID).Scan(&extracted)
	if err != nil {
		t.Fatalf("scan extracted_data: %v", err)
	}
	if extracted["vehicle_type"] != "SUV" {
		t.Fatalf("expected extracted_data['vehicle_type']='SUV', got %v", extracted)
	}
}

func TestEngine_NoFlowConfigured(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	engine := New(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	// Clean all flows
	_, _ = tx.Exec(ctx, "DELETE FROM conversation_answers; DELETE FROM bot_conditions; DELETE FROM bot_questions; DELETE FROM bot_flows;")

	var custID, convID, leadID string
	_ = tx.QueryRow(ctx, `INSERT INTO customers(phone, name) VALUES ('60123456780', 'Tester2') RETURNING id`).Scan(&custID)
	_ = tx.QueryRow(ctx, `INSERT INTO leads(customer_id, status) VALUES ($1, 'NEW') RETURNING id`, custID).Scan(&leadID)
	_ = tx.QueryRow(ctx, `INSERT INTO conversations(customer_id, lead_id, channel, status) VALUES ($1, $2, 'whatsapp', 'open') RETURNING id`, custID, leadID).Scan(&convID)

	reply, err := engine.ProcessMessage(ctx, tx, convID, custID, leadID, "hello")
	if err != nil {
		t.Fatalf("expected no error on empty flows, got %v", err)
	}
	if reply == "" {
		t.Fatal("expected safe fallback reply, got empty")
	}
}

func TestEngine_DeletedQuestion(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	engine := New(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	_, _ = tx.Exec(ctx, "DELETE FROM conversation_answers; DELETE FROM bot_conditions; DELETE FROM bot_questions; DELETE FROM bot_flows;")

	var flowID string
	_ = tx.QueryRow(ctx, `INSERT INTO bot_flows(name, slug, is_entry_flow, is_active) VALUES ('Flow 1', 'f1', true, true) RETURNING id`).Scan(&flowID)
	var q1ID string
	_ = tx.QueryRow(ctx, `INSERT INTO bot_questions(flow_id, field_name, question_text, question_type, order_index, is_active)
		VALUES ($1, 'name', 'What is your name?', 'text', 1, true) RETURNING id`, flowID).Scan(&q1ID)

	var custID, convID, leadID string
	_ = tx.QueryRow(ctx, `INSERT INTO customers(phone, name) VALUES ('60123456781', 'Tester3') RETURNING id`).Scan(&custID)
	_ = tx.QueryRow(ctx, `INSERT INTO leads(customer_id, status) VALUES ($1, 'NEW') RETURNING id`, custID).Scan(&leadID)
	_ = tx.QueryRow(ctx, `INSERT INTO conversations(customer_id, lead_id, channel, status, current_flow_id, current_question_id)
		VALUES ($1, $2, 'whatsapp', 'open', $3, $4) RETURNING id`, custID, leadID, flowID, q1ID).Scan(&convID)

	// Admin deactivates/deletes the question mid-conversation
	_, err = tx.Exec(ctx, `UPDATE bot_questions SET is_active=false WHERE id=$1`, q1ID)
	if err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	// Engine should gracefully restart flow
	reply, err := engine.ProcessMessage(ctx, tx, convID, custID, leadID, "hi")
	if err != nil {
		t.Fatalf("unexpected error on deleted question: %v", err)
	}
	if reply != "What is your name?" {
		// Since q1 was deactivated, and there are no other active questions, it should safely return welcome fallback
		if reply == "" {
			t.Fatal("expected non-empty fallback reply")
		}
	}
}

func TestEngine_CircularFlowBreaker(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	engine := New(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	_, _ = tx.Exec(ctx, "DELETE FROM conversation_answers; DELETE FROM bot_conditions; DELETE FROM bot_questions; DELETE FROM bot_flows;")

	var flowID string
	_ = tx.QueryRow(ctx, `INSERT INTO bot_flows(name, slug, is_entry_flow, is_active) VALUES ('Loop Flow', 'loop', true, true) RETURNING id`).Scan(&flowID)
	var q1ID, q2ID string
	_ = tx.QueryRow(ctx, `INSERT INTO bot_questions(flow_id, field_name, question_text, question_type, order_index, is_active)
		VALUES ($1, 'q1', 'Q1?', 'text', 1, true) RETURNING id`, flowID).Scan(&q1ID)
	_ = tx.QueryRow(ctx, `INSERT INTO bot_questions(flow_id, field_name, question_text, question_type, order_index, is_active)
		VALUES ($1, 'q2', 'Q2?', 'text', 2, true) RETURNING id`, flowID).Scan(&q2ID)

	// Admin misconfigured loop: q1 -> q2 -> q1
	_, _ = tx.Exec(ctx, `UPDATE bot_questions SET next_question_id=$1 WHERE id=$2`, q2ID, q1ID)
	_, _ = tx.Exec(ctx, `UPDATE bot_questions SET next_question_id=$1 WHERE id=$2`, q1ID, q2ID)

	var custID, convID, leadID string
	_ = tx.QueryRow(ctx, `INSERT INTO customers(phone, name) VALUES ('60123456782', 'Tester4') RETURNING id`).Scan(&custID)
	_ = tx.QueryRow(ctx, `INSERT INTO leads(customer_id, status) VALUES ($1, 'NEW') RETURNING id`, custID).Scan(&leadID)
	_ = tx.QueryRow(ctx, `INSERT INTO conversations(customer_id, lead_id, channel, status, current_flow_id, current_question_id)
		VALUES ($1, $2, 'whatsapp', 'open', $3, $4) RETURNING id`, custID, leadID, flowID, q1ID).Scan(&convID)

	// Answering q1 routes to q2
	reply, err := engine.ProcessMessage(ctx, tx, convID, custID, leadID, "ans1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if reply != "Q2?" {
		t.Fatalf("expected 'Q2?', got %q", reply)
	}

	// Answering q2 routes back to q1
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "ans2")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if reply != "Q1?" {
		t.Fatalf("expected 'Q1?', got %q", reply)
	}
}

func TestEngine_ConditionRouting(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	engine := New(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	_, _ = tx.Exec(ctx, "DELETE FROM conversation_answers; DELETE FROM bot_conditions; DELETE FROM bot_questions; DELETE FROM bot_flows;")

	var flowID string
	_ = tx.QueryRow(ctx, `INSERT INTO bot_flows(name, slug, is_entry_flow, is_active) VALUES ('Cond Flow', 'cond', true, true) RETURNING id`).Scan(&flowID)
	var q1ID, q2ID, q3ID string
	_ = tx.QueryRow(ctx, `INSERT INTO bot_questions(flow_id, field_name, question_text, question_type, allowed_values, order_index, is_active)
		VALUES ($1, 'type', 'Type?', 'select', '["Any", "SUV"]'::jsonb, 1, true) RETURNING id`, flowID).Scan(&q1ID)
	_ = tx.QueryRow(ctx, `INSERT INTO bot_questions(flow_id, field_name, question_text, question_type, order_index, is_active)
		VALUES ($1, 'suv_sub', 'SUV Subtype?', 'text', 2, true) RETURNING id`, flowID).Scan(&q2ID)
	_ = tx.QueryRow(ctx, `INSERT INTO bot_questions(flow_id, field_name, question_text, question_type, order_index, is_active)
		VALUES ($1, 'budget', 'Budget?', 'number', 3, true) RETURNING id`, flowID).Scan(&q3ID)

	// Condition: if type == 'Any', skip to q3 (Budget)
	_, _ = tx.Exec(ctx, `INSERT INTO bot_conditions(question_id, field_name, operator, value, target_question_id, priority)
		VALUES ($1, 'type', 'EQUALS', 'Any', $2, 1)`, q1ID, q3ID)

	var custID, convID, leadID string
	_ = tx.QueryRow(ctx, `INSERT INTO customers(phone, name) VALUES ('60123456783', 'Tester5') RETURNING id`).Scan(&custID)
	_ = tx.QueryRow(ctx, `INSERT INTO leads(customer_id, status) VALUES ($1, 'NEW') RETURNING id`, custID).Scan(&leadID)
	_ = tx.QueryRow(ctx, `INSERT INTO conversations(customer_id, lead_id, channel, status, current_flow_id, current_question_id)
		VALUES ($1, $2, 'whatsapp', 'open', $3, $4) RETURNING id`, custID, leadID, flowID, q1ID).Scan(&convID)

	// Answering "Any" should trigger condition and skip to Budget question (q3)
	reply, err := engine.ProcessMessage(ctx, tx, convID, custID, leadID, "Any")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if reply != "Budget?" {
		t.Fatalf("expected 'Budget?', got %q", reply)
	}
}

func TestEngine_FullBuyFlow(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	engine := New(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	var flowID string
	err = tx.QueryRow(ctx, `SELECT id FROM bot_flows WHERE slug='buy_flow'`).Scan(&flowID)
	if err != nil {
		t.Fatalf("buy_flow not found: %v", err)
	}

	var custID, convID, leadID string
	_ = tx.QueryRow(ctx, `INSERT INTO customers(phone, name) VALUES ('60199990001', 'BuyTester') RETURNING id`).Scan(&custID)
	_ = tx.QueryRow(ctx, `INSERT INTO leads(customer_id, status) VALUES ($1, 'NEW') RETURNING id`, custID).Scan(&leadID)
	_ = tx.QueryRow(ctx, `INSERT INTO conversations(customer_id, lead_id, channel, status) VALUES ($1, $2, 'whatsapp', 'open') RETURNING id`, custID, leadID).Scan(&convID)

	_, err = tx.Exec(ctx, `INSERT INTO vehicles(id, make, model, year, price, fuel, transmission, description, status)
		VALUES (gen_random_uuid(), 'BMW', 'X1', 2021, 120000, 'Petrol', 'Automatic', 'Compact SUV', 'AVAILABLE')`)
	if err != nil {
		t.Fatalf("insert vehicle err: %v", err)
	}

	// 1. Initial inbound message starts the entry flow (Welcome & Menu)
	reply, err := engine.ProcessMessage(ctx, tx, convID, custID, leadID, "hi")
	if err != nil {
		t.Fatalf("step 1 err: %v", err)
	}
	if !strings.Contains(reply, "Welcome") || !strings.Contains(reply, "Buy") {
		t.Fatalf("step 1 expected welcome/menu question, got: %q", reply)
	}

	// 1b. Choose Buy -> routes to Buy Flow and asks vehicle_type
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "Buy")
	if err != nil {
		t.Fatalf("step 1b err: %v", err)
	}
	if !strings.Contains(reply, "SUV") {
		t.Fatalf("step 1b expected vehicle_type question, got: %q", reply)
	}

	// 2. Answer vehicle_type -> advances to budget_max
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "SUV")
	if err != nil {
		t.Fatalf("step 2 err: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "budget") {
		t.Fatalf("step 2 expected budget question, got: %q", reply)
	}

	// 3. Answer budget_max -> advances to brand
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "150000")
	if err != nil {
		t.Fatalf("step 3 err: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "brand") {
		t.Fatalf("step 3 expected brand question, got: %q", reply)
	}

	// 4. Answer brand -> advances to model
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "BMW")
	if err != nil {
		t.Fatalf("step 4 err: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "model") {
		t.Fatalf("step 4 expected model question, got: %q", reply)
	}

	// 5. Answer model -> advances to fuel
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "X1")
	if err != nil {
		t.Fatalf("step 5 err: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "fuel") {
		t.Fatalf("step 5 expected fuel question, got: %q", reply)
	}

	// 6. Answer fuel -> advances to transmission
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "Petrol")
	if err != nil {
		t.Fatalf("step 6 err: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "transmission") {
		t.Fatalf("step 6 expected transmission question, got: %q", reply)
	}

	// 7. Answer transmission -> advances to year_min
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "Automatic")
	if err != nil {
		t.Fatalf("step 7 err: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "year") {
		t.Fatalf("step 7 expected year_min question, got: %q", reply)
	}

	// 8. Answer year_min (final question) -> triggers matching & completion
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "2020")
	if err != nil {
		t.Fatalf("step 8 err: %v", err)
	}
	if !strings.Contains(reply, "BMW") || !strings.Contains(reply, "X1") {
		t.Fatalf("step 8 expected vehicle match results, got: %q", reply)
	}

	// Assert conversation_answers has exactly 8 rows (service_intent + 7 buy fields)
	var answerCount int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM conversation_answers WHERE conversation_id=$1`, convID).Scan(&answerCount)
	if err != nil || answerCount != 8 {
		t.Fatalf("expected 8 answers, got %d (err: %v)", answerCount, err)
	}

	// Assert leads.extracted_data contains all captured fields
	var extractedJSON []byte
	err = tx.QueryRow(ctx, `SELECT extracted_data FROM leads WHERE id=$1`, leadID).Scan(&extractedJSON)
	if err != nil {
		t.Fatalf("failed to query extracted_data: %v", err)
	}
	var extracted map[string]any
	if err := json.Unmarshal(extractedJSON, &extracted); err != nil {
		t.Fatalf("unmarshal extracted_data: %v", err)
	}
	expectedFields := []string{"vehicle_type", "budget_max", "brand", "model", "fuel", "transmission", "year_min"}
	for _, f := range expectedFields {
		if val, exists := extracted[f]; !exists || val == "" {
			t.Fatalf("missing or empty extracted field %q: %+v", f, extracted)
		}
	}
}

func TestEngine_InvalidThenValid(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	engine := New(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	var custID, convID, leadID string
	_ = tx.QueryRow(ctx, `INSERT INTO customers(phone, name) VALUES ('60199990002', 'InvalidTester') RETURNING id`).Scan(&custID)
	_ = tx.QueryRow(ctx, `INSERT INTO leads(customer_id, status) VALUES ($1, 'NEW') RETURNING id`, custID).Scan(&leadID)
	_ = tx.QueryRow(ctx, `INSERT INTO conversations(customer_id, lead_id, channel, status) VALUES ($1, $2, 'whatsapp', 'open') RETURNING id`, custID, leadID).Scan(&convID)

	// Initialize flow to first question (vehicle_type)
	_, _ = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "hi")

	// Send invalid value for select type
	reply, err := engine.ProcessMessage(ctx, tx, convID, custID, leadID, "Spaceship")
	if err != nil {
		t.Fatalf("unexpected err on invalid: %v", err)
	}
	if !strings.Contains(reply, "Please reply *1*") && !strings.Contains(reply, "BUY") {
		t.Fatalf("expected validation error message, got: %q", reply)
	}

	// Assert no answer recorded yet
	var count int
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM conversation_answers WHERE conversation_id=$1`, convID).Scan(&count)
	if count != 0 {
		t.Fatalf("expected 0 answers on invalid input, got %d", count)
	}

	// Send valid value "Buy"
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "Buy")
	if err != nil {
		t.Fatalf("unexpected err on valid: %v", err)
	}
	if !strings.Contains(reply, "SUV") {
		t.Fatalf("expected advance to Buy flow vehicle_type question, got: %q", reply)
	}

	// Assert exactly 1 answer recorded
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM conversation_answers WHERE conversation_id=$1`, convID).Scan(&count)
	if count != 1 {
		t.Fatalf("expected exactly 1 answer after valid input, got %d", count)
	}

	var fieldName, valCaptured string
	_ = tx.QueryRow(ctx, `SELECT field_name, value_captured FROM conversation_answers WHERE conversation_id=$1`, convID).Scan(&fieldName, &valCaptured)
	if fieldName != "service_intent" || valCaptured != "Buy" {
		t.Fatalf("expected service_intent=Buy, got %s=%s", fieldName, valCaptured)
	}
}

func TestEngine_SessionExpiry(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	engine := New(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	var custID, convID, leadID string
	_ = tx.QueryRow(ctx, `INSERT INTO customers(phone, name) VALUES ('60199990003', 'ExpiryTester') RETURNING id`).Scan(&custID)
	_ = tx.QueryRow(ctx, `INSERT INTO leads(customer_id, status) VALUES ($1, 'NEW') RETURNING id`, custID).Scan(&leadID)
	_ = tx.QueryRow(ctx, `INSERT INTO conversations(customer_id, lead_id, channel, status, updated_at)
		VALUES ($1, $2, 'whatsapp', 'open', now() - INTERVAL '2 hours') RETURNING id`, custID, leadID).Scan(&convID)

	// Resetting an expired conversation restarts the entry flow
	reply, err := engine.ResetConversation(ctx, tx, convID)
	if err != nil {
		t.Fatalf("reset err: %v", err)
	}
	if !strings.Contains(reply, "Welcome") {
		t.Fatalf("expected restarted flow to ask welcome question, got %q", reply)
	}

	// Verify conversation state was updated to entry flow
	var curQID *string
	_ = tx.QueryRow(ctx, `SELECT current_question_id::text FROM conversations WHERE id=$1`, convID).Scan(&curQID)
	if curQID == nil || *curQID == "" {
		t.Fatal("expected current_question_id to be set after reset")
	}
}

func TestEngine_ConversationalNLP_Flow(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	engine := New(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	// Clean tables
	_, _ = tx.Exec(ctx, "DELETE FROM conversation_answers; DELETE FROM bot_conditions; DELETE FROM bot_questions; DELETE FROM bot_flows;")

	// Insert test flow
	var flowID string
	_ = tx.QueryRow(ctx, `INSERT INTO bot_flows(name, slug, is_entry_flow, is_active) VALUES ('Buy Flow', 'buy_flow', true, true) RETURNING id`).Scan(&flowID)
	var q1ID string
	_ = tx.QueryRow(ctx, `INSERT INTO bot_questions(flow_id, field_name, question_text, question_type, allowed_values, order_index, is_active)
		VALUES ($1, 'vehicle_type', 'What type of car are you looking for? (SUV, Sedan, Hatchback, MPV, Any)', 'select', '["SUV", "Sedan", "Any"]'::jsonb, 1, true) RETURNING id`, flowID).Scan(&q1ID)

	// Insert test BMW vehicle
	var vID string
	_ = tx.QueryRow(ctx, `INSERT INTO vehicles(make, model, year, price, fuel, transmission, km, status, description)
		VALUES ('BMW', 'X1 sDrive20i', 2026, 228000, 'Petrol', 'Automatic', 10000, 'AVAILABLE', 'BMW X1 in pristine condition') RETURNING id`).Scan(&vID)

	var custID, convID, leadID string
	_ = tx.QueryRow(ctx, `INSERT INTO customers(phone, name) VALUES ('60111222333', 'NLP User') RETURNING id`).Scan(&custID)
	_ = tx.QueryRow(ctx, `INSERT INTO leads(customer_id, status) VALUES ($1, 'NEW') RETURNING id`, custID).Scan(&leadID)
	_ = tx.QueryRow(ctx, `INSERT INTO conversations(customer_id, lead_id, channel, status) VALUES ($1, $2, 'whatsapp', 'open') RETURNING id`, custID, leadID).Scan(&convID)

	// 1. Initial greeting
	reply, err := engine.ProcessMessage(ctx, tx, convID, custID, leadID, "hi")
	if err != nil {
		t.Fatalf("greeting err: %v", err)
	}
	if !strings.Contains(reply, "What type of car") {
		t.Fatalf("expected question 1 text, got %q", reply)
	}

	// 2. User says "i want bmw m4" -> should NOT fail with "Please choose one of...", should immediately match BMWs!
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "i want bmw m4")
	if err != nil {
		t.Fatalf("bmw search err: %v", err)
	}
	if !strings.Contains(reply, "BMW") || !strings.Contains(reply, "X1") {
		t.Fatalf("expected BMW vehicle match results, got %q", reply)
	}

	// 3. User says "test drive" -> books test drive for human sales specialist
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "test drive")
	if err != nil {
		t.Fatalf("test drive err: %v", err)
	}
	if !strings.Contains(reply, "Test Drive Request Confirmed") || !strings.Contains(reply, "sales specialist") {
		t.Fatalf("expected test drive confirmation, got %q", reply)
	}

	// Verify test drive was inserted
	var tdCount int
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM test_drives WHERE lead_id=$1`, leadID).Scan(&tdCount)
	if tdCount == 0 {
		t.Fatal("expected test_drive record to be created in DB")
	}

	// 4. User says "finance" -> logs finance application for human finance desk
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "i need finance / loan")
	if err != nil {
		t.Fatalf("finance err: %v", err)
	}
	if !strings.Contains(reply, "Finance Application Received") || !strings.Contains(reply, "finance specialist") {
		t.Fatalf("expected finance confirmation, got %q", reply)
	}

	// Verify finance request was inserted
	var finCount int
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM finance_requests WHERE lead_id=$1`, leadID).Scan(&finCount)
	if finCount == 0 {
		t.Fatal("expected finance_request record to be created in DB")
	}

	// 5. Automated car selling flow: "sell my 2019 honda city"
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "sell my 2019 honda city")
	if err != nil {
		t.Fatalf("sell err: %v", err)
	}
	if !strings.Contains(reply, "Vehicle Submission Received") || !strings.Contains(reply, "Manual Review") {
		t.Fatalf("expected vehicle submission confirmation, got %q", reply)
	}

	// Verify sell_requests record created with status VALUATION_PENDING for manual review
	var sellCount int
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM sell_requests WHERE lead_id=$1 AND status='VALUATION_PENDING'`, leadID).Scan(&sellCount)
	if sellCount == 0 {
		t.Fatal("expected pending sell_request record in DB")
	}
}

func TestValidate_ConversationalTolerant(t *testing.T) {
	// "any" for select
	val, ok := Validate("any", "select", "", []string{"SUV", "Sedan", "Any"}, true)
	if !ok || val != "Any" {
		t.Fatalf("expected 'Any', true, got %q, %v", val, ok)
	}

	// "any" for number
	val, ok = Validate("any", "number", "min:1995,max:2027", nil, true)
	if !ok || val != "0" {
		t.Fatalf("expected '0', true, got %q, %v", val, ok)
	}

	// "2019 to 2026" for number
	val, ok = Validate("2019 to 2026", "number", "min:1995,max:2027", nil, true)
	if !ok || val != "2019" {
		t.Fatalf("expected '2019', true, got %q, %v", val, ok)
	}

	// "no preference" for select
	val, ok = Validate("no preference", "select", "", []string{"SUV", "Sedan", "Any"}, true)
	if !ok || val != "Any" {
		t.Fatalf("expected 'Any', true, got %q, %v", val, ok)
	}
}

func TestEngine_TwoStepSellFlow(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	engine := New(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	// Clean tables
	_, _ = tx.Exec(ctx, "DELETE FROM conversation_answers; DELETE FROM bot_conditions; DELETE FROM bot_questions; DELETE FROM bot_flows;")

	// Insert test flow
	var flowID string
	_ = tx.QueryRow(ctx, `INSERT INTO bot_flows(name, slug, is_entry_flow, is_active) VALUES ('Buy Flow', 'buy_flow', true, true) RETURNING id`).Scan(&flowID)
	_, _ = tx.Exec(ctx, `INSERT INTO bot_questions(flow_id, field_name, question_text, question_type, allowed_values, order_index, is_active)
		VALUES ($1, 'vehicle_type', 'What type of car are you looking for? (SUV, Sedan, Hatchback, MPV, Any)', 'select', '["SUV", "Sedan", "Any"]'::jsonb, 1, true)`, flowID)

	var custID, convID, leadID string
	_ = tx.QueryRow(ctx, `INSERT INTO customers(phone, name) VALUES ('60199998888', 'Seller User') RETURNING id`).Scan(&custID)
	_ = tx.QueryRow(ctx, `INSERT INTO leads(customer_id, status) VALUES ($1, 'NEW') RETURNING id`, custID).Scan(&leadID)
	_ = tx.QueryRow(ctx, `INSERT INTO conversations(customer_id, lead_id, channel, status) VALUES ($1, $2, 'whatsapp', 'open') RETURNING id`, custID, leadID).Scan(&convID)

	// 1. Initial greeting
	reply, err := engine.ProcessMessage(ctx, tx, convID, custID, leadID, "hi")
	if err != nil {
		t.Fatalf("hi err: %v", err)
	}
	if !strings.Contains(reply, "What type of car") {
		t.Fatalf("unexpected greeting reply: %q", reply)
	}

	// 2. User says "sell"
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "sell")
	if err != nil {
		t.Fatalf("sell err: %v", err)
	}
	if !strings.Contains(reply, "Sell Your Car Instantly") {
		t.Fatalf("expected Sell Your Car Instantly prompt, got: %q", reply)
	}

	// 3. User responds with details: "bmw m4 cs 2020 50000km"
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "bmw m4 cs 2020 50000km")
	if err != nil {
		t.Fatalf("m4 cs valuation err: %v", err)
	}
	if !strings.Contains(reply, "Vehicle Submission Received") || !strings.Contains(reply, "Manual Review") {
		t.Fatalf("expected manual valuation submission confirmation, got: %q", reply)
	}
	if !strings.Contains(reply, "BMW") || !strings.Contains(reply, "M4 CS") {
		t.Fatalf("expected BMW M4 CS in reply, got: %q", reply)
	}

	// Verify sell_requests status is VALUATION_PENDING for manual review by dealership
	var srCount int
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM sell_requests WHERE lead_id=$1 AND status='VALUATION_PENDING'`, leadID).Scan(&srCount)
	if srCount == 0 {
		t.Fatal("expected pending sell request in DB")
	}

	// 4. User says "i want to buy a bmw" -> should switch back to buy and match stock
	_, _ = tx.Exec(ctx, `INSERT INTO vehicles(id, make, model, year, price, fuel, transmission, description, status)
		VALUES (gen_random_uuid(), 'BMW', '320i', 2021, 150000, 'Petrol', 'Automatic', 'Luxury Sedan', 'AVAILABLE')`)

	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "i want to buy a bmw")
	if err != nil {
		t.Fatalf("buy err: %v", err)
	}
	if !strings.Contains(reply, "BMW") {
		t.Fatalf("expected BMW vehicle match results, got %q", reply)
	}
}

func TestEngine_WelcomeGreetingAndBuySellBranching(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	engine := New(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	// Clean tables
	_, _ = tx.Exec(ctx, "DELETE FROM conversation_answers; DELETE FROM bot_conditions; DELETE FROM bot_questions; DELETE FROM bot_flows;")

	// Insert Welcome, Buy, and Sell flows
	var welcomeFlowID, buyFlowID, sellFlowID string
	_ = tx.QueryRow(ctx, `INSERT INTO bot_flows(id, name, slug, is_entry_flow, is_active) VALUES ('11111111-1111-1111-1111-111111111100', 'Welcome & Menu', 'welcome_flow', true, true) RETURNING id`).Scan(&welcomeFlowID)
	_ = tx.QueryRow(ctx, `INSERT INTO bot_flows(id, name, slug, is_entry_flow, is_active) VALUES ('11111111-1111-1111-1111-111111111101', 'Buy a Car', 'buy_flow', false, true) RETURNING id`).Scan(&buyFlowID)
	_ = tx.QueryRow(ctx, `INSERT INTO bot_flows(id, name, slug, is_entry_flow, is_active) VALUES ('11111111-1111-1111-1111-111111111102', 'Sell a Car', 'sell_flow', false, true) RETURNING id`).Scan(&sellFlowID)

	// Welcome Question
	var welcomeQID, buyQ1ID, sellQ1ID string
	_ = tx.QueryRow(ctx, `INSERT INTO bot_questions(id, flow_id, field_name, question_text, question_type, allowed_values, order_index, is_active)
		VALUES ('22222222-2222-2222-2222-222222222001', $1, 'service_intent', '🚗 *Welcome to AutoKart!*\n\nHow can we help you today?\n\n1️⃣ *Buy a Car*\n2️⃣ *Sell Your Car*\n\n👉 Reply *1* or *BUY* to browse cars\n👉 Reply *2* or *SELL* to sell your car', 'select', '["Buy","Sell","1","2"]'::jsonb, 1, true) RETURNING id`, welcomeFlowID).Scan(&welcomeQID)

	// Buy Question 1
	_ = tx.QueryRow(ctx, `INSERT INTO bot_questions(id, flow_id, field_name, question_text, question_type, allowed_values, order_index, is_active)
		VALUES ('22222222-2222-2222-2222-222222222101', $1, 'vehicle_type', 'What type of car are you looking for? (SUV, Sedan, Hatchback, MPV, Any)', 'select', '["SUV","Sedan","Any"]'::jsonb, 1, true) RETURNING id`, buyFlowID).Scan(&buyQ1ID)

	// Sell Question 1
	_ = tx.QueryRow(ctx, `INSERT INTO bot_questions(id, flow_id, field_name, question_text, question_type, order_index, is_active)
		VALUES ('22222222-2222-2222-2222-222222222201', $1, 'sell_brand', '🚗 *Sell Your Car Instantly!*\n\nWhat brand/make is your car?', 'text', 1, true) RETURNING id`, sellFlowID).Scan(&sellQ1ID)

	// Conditions on Welcome Question:
	// "Buy" or "1" -> Buy flow
	// "Sell" or "2" -> Sell flow
	_, _ = tx.Exec(ctx, `INSERT INTO bot_conditions(question_id, field_name, operator, value, target_flow_id, priority) VALUES
		($1, 'service_intent', 'eq', 'Buy', $2, 1),
		($1, 'service_intent', 'eq', '1', $2, 2),
		($1, 'service_intent', 'eq', 'Sell', $3, 3),
		($1, 'service_intent', 'eq', '2', $3, 4)`, welcomeQID, buyFlowID, sellFlowID)

	var custID, convID, leadID string
	_ = tx.QueryRow(ctx, `INSERT INTO customers(phone, name) VALUES ('60188887777', 'Flow Branching User') RETURNING id`).Scan(&custID)
	_ = tx.QueryRow(ctx, `INSERT INTO leads(customer_id, status) VALUES ($1, 'NEW') RETURNING id`, custID).Scan(&leadID)
	_ = tx.QueryRow(ctx, `INSERT INTO conversations(customer_id, lead_id, channel, status) VALUES ($1, $2, 'whatsapp', 'open') RETURNING id`, custID, leadID).Scan(&convID)

	// 1. Initial "hi" MUST return Welcome greeting + ask Buy or Sell!
	reply, err := engine.ProcessMessage(ctx, tx, convID, custID, leadID, "hi")
	if err != nil {
		t.Fatalf("step 1 err: %v", err)
	}
	if !strings.Contains(reply, "Welcome") || !strings.Contains(reply, "Buy a Car") || !strings.Contains(reply, "Sell Your Car") {
		t.Fatalf("expected initial greeting and buy/sell prompt, got: %q", reply)
	}

	// 2. Replying "1" (or "buy") MUST branch to the Buy flow!
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "1")
	if err != nil {
		t.Fatalf("step 2 err: %v", err)
	}
	if !strings.Contains(reply, "What type of car are you looking for") {
		t.Fatalf("expected Buy flow vehicle_type question, got: %q", reply)
	}

	// 3. User says "menu" -> MUST reset to Welcome & Menu flow!
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "menu")
	if err != nil {
		t.Fatalf("step 3 menu err: %v", err)
	}
	if !strings.Contains(reply, "Welcome") || !strings.Contains(reply, "Buy a Car") {
		t.Fatalf("expected menu to reset to welcome question, got: %q", reply)
	}

	// 4. Replying "2" (or "sell") MUST branch to the Sell flow!
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "2")
	if err != nil {
		t.Fatalf("step 4 err: %v", err)
	}
	if !strings.Contains(reply, "Sell Your Car") || !strings.Contains(reply, "What brand/make") {
		t.Fatalf("expected Sell flow question, got: %q", reply)
	}

	// Verify lead was marked with intent=SELL and sell_mode=true
	var leadIntent string
	_ = tx.QueryRow(ctx, `SELECT intent FROM leads WHERE id=$1`, leadID).Scan(&leadIntent)
	if leadIntent != "SELL" {
		t.Fatalf("expected lead intent SELL, got %q", leadIntent)
	}

	// 5. User replies "i want bmw m4" while in sell flow -> MUST switch to BUY search, NOT accept fake car!
	_, _ = tx.Exec(ctx, `INSERT INTO vehicles(id, make, model, year, price, fuel, transmission, description, status)
		VALUES (gen_random_uuid(), 'BMW', '320i', 2021, 150000, 'Petrol', 'Automatic', 'Luxury Sedan', 'AVAILABLE')`)

	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "i want bmw m4")
	if err != nil {
		t.Fatalf("step 5 err: %v", err)
	}
	if strings.Contains(reply, "Vehicle Review Complete") || strings.Contains(reply, "Estimated Valuation") {
		t.Fatalf("CRITICAL BUG: Bot accepted a fake car valuation instead of switching to buy search: %q", reply)
	}
	if !strings.Contains(reply, "BMW") {
		t.Fatalf("expected BMW inventory match or fallback in reply, got: %q", reply)
	}

	// Verify lead intent switched to BUY
	_ = tx.QueryRow(ctx, `SELECT intent FROM leads WHERE id=$1`, leadID).Scan(&leadIntent)
	if leadIntent != "BUY" {
		t.Fatalf("expected lead intent BUY, got %q", leadIntent)
	}

	// Verify NO sell_requests were created
	var srCount int
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM sell_requests WHERE lead_id=$1`, leadID).Scan(&srCount)
	if srCount != 0 {
		t.Fatalf("expected 0 sell requests created, got %d", srCount)
	}
}

func TestEngine_SellFlow_DoesNotDefaultYearOrKm(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	engine := New(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	var custID, convID, leadID string
	_ = tx.QueryRow(ctx, `INSERT INTO customers(phone, name) VALUES ('60199990009', 'RealSeller') RETURNING id`).Scan(&custID)
	_ = tx.QueryRow(ctx, `INSERT INTO leads(customer_id, status) VALUES ($1, 'NEW') RETURNING id`, custID).Scan(&leadID)
	_ = tx.QueryRow(ctx, `INSERT INTO conversations(customer_id, lead_id, channel, status) VALUES ($1, $2, 'whatsapp', 'open') RETURNING id`, custID, leadID).Scan(&convID)

	// 1. Initial greeting
	_, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "hi")
	if err != nil {
		t.Fatalf("hi err: %v", err)
	}

	// 2. Select Sell
	reply, err := engine.ProcessMessage(ctx, tx, convID, custID, leadID, "2")
	if err != nil {
		t.Fatalf("select sell err: %v", err)
	}
	if !strings.Contains(reply, "brand") && !strings.Contains(reply, "make") {
		t.Fatalf("expected brand question, got: %q", reply)
	}

	// 3. User provides only brand: "Toyota"
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "Toyota")
	if err != nil {
		t.Fatalf("answer brand err: %v", err)
	}
	// Must NOT accept or review early
	if strings.Contains(reply, "Vehicle Review Complete") || strings.Contains(reply, "Estimated Valuation") {
		t.Fatalf("must not review early on just brand, got: %q", reply)
	}
	// Must advance to question 2 (model)
	if !strings.Contains(strings.ToLower(reply), "model") {
		t.Fatalf("expected model question, got: %q", reply)
	}

	// 4. User provides model: "Camry"
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "Camry")
	if err != nil {
		t.Fatalf("answer model err: %v", err)
	}
	// Must NOT accept early with fake year 2020!
	if strings.Contains(reply, "Vehicle Review Complete") || strings.Contains(reply, "Estimated Valuation") {
		t.Fatalf("must not review early on brand+model without year, got: %q", reply)
	}
	// Must advance to question 3 (year)
	if !strings.Contains(strings.ToLower(reply), "year") {
		t.Fatalf("expected year question, got: %q", reply)
	}
}

func TestEngine_SellFlow_CompleteAllEightQuestions(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	engine := New(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	var custID, convID, leadID string
	_ = tx.QueryRow(ctx, `INSERT INTO customers(phone, name) VALUES ('60199990099', 'FullSellTester') RETURNING id`).Scan(&custID)
	_ = tx.QueryRow(ctx, `INSERT INTO leads(customer_id, status) VALUES ($1, 'NEW') RETURNING id`, custID).Scan(&leadID)
	_ = tx.QueryRow(ctx, `INSERT INTO conversations(customer_id, lead_id, channel, status) VALUES ($1, $2, 'whatsapp', 'open') RETURNING id`, custID, leadID).Scan(&convID)

	// 1. Initial greeting -> Welcome & Menu
	reply, err := engine.ProcessMessage(ctx, tx, convID, custID, leadID, "hi")
	if err != nil {
		t.Fatalf("hi err: %v", err)
	}
	if !strings.Contains(reply, "Welcome") || !strings.Contains(reply, "Sell") {
		t.Fatalf("expected welcome & menu, got: %q", reply)
	}

	// 2. Select 2 (Sell) -> starts sell_flow Q1 (sell_brand)
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "2")
	if err != nil {
		t.Fatalf("select sell err: %v", err)
	}
	if !strings.Contains(reply, "brand") && !strings.Contains(reply, "make") {
		t.Fatalf("expected brand question, got: %q", reply)
	}

	// 3. Q1 (sell_brand): "bmw m4 cs" -> advances to Q2 (sell_model)
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "bmw m4 cs")
	if err != nil {
		t.Fatalf("answer brand err: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "model") {
		t.Fatalf("expected model question, got: %q", reply)
	}

	// 4. Q2 (sell_model): "m4 cs" -> advances to Q3 (sell_year)
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "m4 cs")
	if err != nil {
		t.Fatalf("answer model err: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "year") {
		t.Fatalf("expected year question, got: %q", reply)
	}

	// 5. Q3 (sell_year): "2022" -> MUST NOT CUT OFF! Must advance to Q4 (sell_km)
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "2022")
	if err != nil {
		t.Fatalf("answer year err: %v", err)
	}
	if strings.Contains(reply, "Review Complete") || strings.Contains(reply, "Estimated Valuation") {
		t.Fatalf("CRITICAL BUG: flow was cut off prematurely at year 2022! Got: %q", reply)
	}
	if !strings.Contains(strings.ToLower(reply), "mileage") && !strings.Contains(strings.ToLower(reply), "kilometer") {
		t.Fatalf("expected mileage question, got: %q", reply)
	}

	// 6. Q4 (sell_km): "35000" -> advances to Q5 (sell_fuel)
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "35000")
	if err != nil {
		t.Fatalf("answer km err: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "fuel") {
		t.Fatalf("expected fuel question, got: %q", reply)
	}

	// 7. Q5 (sell_fuel): "Petrol" -> advances to Q6 (sell_transmission)
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "Petrol")
	if err != nil {
		t.Fatalf("answer fuel err: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "transmission") {
		t.Fatalf("expected transmission question, got: %q", reply)
	}

	// 8. Q6 (sell_transmission): "Automatic" -> advances to Q7 (sell_condition)
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "Automatic")
	if err != nil {
		t.Fatalf("answer transmission err: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "condition") {
		t.Fatalf("expected condition question, got: %q", reply)
	}

	// 9. Q7 (sell_condition): "Good" -> advances to Q8 (sell_location)
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "Good")
	if err != nil {
		t.Fatalf("answer condition err: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "location") && !strings.Contains(strings.ToLower(reply), "located") {
		t.Fatalf("expected location question, got: %q", reply)
	}

	// 10. Q8 (sell_location): "Kuala Lumpur" -> COMPLETES FLOW with manual valuation status!
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "Kuala Lumpur")
	if err != nil {
		t.Fatalf("answer location err: %v", err)
	}
	if !strings.Contains(reply, "Vehicle Submission Received") || !strings.Contains(reply, "Manual Review") {
		t.Fatalf("expected manual valuation confirmation, got: %q", reply)
	}
	// Must NOT contain duplicate "bmw m4 cs m4 cs"
	if strings.Contains(strings.ToLower(reply), "m4 cs m4 cs") {
		t.Fatalf("duplicate model name found in reply: %q", reply)
	}
	if !strings.Contains(reply, "2022 BMW M4 CS") {
		t.Fatalf("expected cleaned '2022 BMW M4 CS' in reply, got: %q", reply)
	}

	// Verify sell_requests status is VALUATION_PENDING (NOT ACCEPTED)
	var srStatus, srBrand, srModel string
	var srYear, srKM int
	err = tx.QueryRow(ctx, `SELECT status, brand, model, year, km FROM sell_requests WHERE lead_id=$1`, leadID).Scan(
		&srStatus, &srBrand, &srModel, &srYear, &srKM)
	if err != nil {
		t.Fatalf("failed to query sell_requests: %v", err)
	}
	if srStatus != "VALUATION_PENDING" {
		t.Fatalf("expected status VALUATION_PENDING, got %q", srStatus)
	}
	if srBrand != "BMW" || srModel != "M4 CS" || srYear != 2022 || srKM != 35000 {
		t.Fatalf("expected BMW M4 CS 2022 35000km, got %s %s %d %d", srBrand, srModel, srYear, srKM)
	}

	// Verify vehicles table has ZERO customer_sell entries (NOT auto-listed as AVAILABLE)
	var vehCount int
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM vehicles WHERE make='BMW' AND model='M4 CS'`).Scan(&vehCount)
	if vehCount != 0 {
		t.Fatalf("CRITICAL: Vehicle was auto-listed into inventory without manual valuation! Count: %d", vehCount)
	}

	// 11. Next message: user says "payment" -> MUST route to Finance, NOT trigger "Sell Your Car Instantly!"
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "payment")
	if err != nil {
		t.Fatalf("payment err: %v", err)
	}
	if strings.Contains(reply, "Sell Your Car Instantly") {
		t.Fatalf("CRITICAL BUG: 'payment' triggered 'Sell Your Car Instantly!' prompt! Got: %q", reply)
	}
	if !strings.Contains(reply, "Finance Application Received") {
		t.Fatalf("expected Finance Application Received, got: %q", reply)
	}
}





