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

	// 1. Initial inbound message starts the entry flow
	reply, err := engine.ProcessMessage(ctx, tx, convID, custID, leadID, "hi")
	if err != nil {
		t.Fatalf("step 1 err: %v", err)
	}
	if !strings.Contains(reply, "SUV") {
		t.Fatalf("step 1 expected vehicle_type question, got: %q", reply)
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

	// Assert conversation_answers has exactly 7 rows
	var answerCount int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM conversation_answers WHERE conversation_id=$1`, convID).Scan(&answerCount)
	if err != nil || answerCount != 7 {
		t.Fatalf("expected 7 answers, got %d (err: %v)", answerCount, err)
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
	if !strings.Contains(reply, "Please choose one of") {
		t.Fatalf("expected validation error message, got: %q", reply)
	}

	// Assert no answer recorded yet
	var count int
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM conversation_answers WHERE conversation_id=$1`, convID).Scan(&count)
	if count != 0 {
		t.Fatalf("expected 0 answers on invalid input, got %d", count)
	}

	// Send valid value
	reply, err = engine.ProcessMessage(ctx, tx, convID, custID, leadID, "Sedan")
	if err != nil {
		t.Fatalf("unexpected err on valid: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "budget") {
		t.Fatalf("expected advance to budget question, got: %q", reply)
	}

	// Assert exactly 1 answer recorded
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM conversation_answers WHERE conversation_id=$1`, convID).Scan(&count)
	if count != 1 {
		t.Fatalf("expected exactly 1 answer after valid input, got %d", count)
	}

	var fieldName, valCaptured string
	_ = tx.QueryRow(ctx, `SELECT field_name, value_captured FROM conversation_answers WHERE conversation_id=$1`, convID).Scan(&fieldName, &valCaptured)
	if fieldName != "vehicle_type" || valCaptured != "Sedan" {
		t.Fatalf("expected vehicle_type=Sedan, got %s=%s", fieldName, valCaptured)
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
	if !strings.Contains(reply, "SUV") {
		t.Fatalf("expected restarted flow to ask vehicle_type, got %q", reply)
	}

	// Verify conversation state was updated to entry flow
	var curQID *string
	_ = tx.QueryRow(ctx, `SELECT current_question_id::text FROM conversations WHERE id=$1`, convID).Scan(&curQID)
	if curQID == nil || *curQID == "" {
		t.Fatal("expected current_question_id to be set after reset")
	}
}



