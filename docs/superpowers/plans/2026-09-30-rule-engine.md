# Dynamic Rule Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the hardcoded WhatsApp state machine with a fully dynamic, database-driven rule engine where all questions, validation, responses, and conversation flows are configured from the Admin Panel. Remove the Exchange feature entirely.

**Architecture:** A new migration adds `bot_flows`, `bot_questions`, `bot_conditions`, `bot_responses`, and `conversation_answers` tables. A new `internal/botengine` package reads these tables at inbound-message time and acts as the conversation state machine. The existing `statemachine.go` (hardcoded `Next()` function) is replaced by the engine. The WhatsApp `HandleInbound` in `worker.go` is rewritten to delegate to the engine. New API endpoints let the Admin CRUD flows/questions/conditions/responses. The Admin web UI gets new sections for configuring the bot. Exchange-related code is excised from router, flows.go, statemachine.go, worker.go, and the web UI.

**Tech Stack:** Go 1.27, PostgreSQL (pgxpool), vanilla JS admin UI

**Spec:** This plan implements the requirements from the user's 15-point specification for a rule-based WhatsApp automation engine.

## Global Constraints

- Zero AI: no LLM calls, no AI intent detection, no AI-generated responses anywhere
- All questions, answers, validation rules, error messages, and flow routing must live in the database, never hardcoded in Go
- Go application = automation engine; Admin Panel = business configuration
- Vehicle matching uses only real database inventory
- Exchange feature completely removed (API, DB references in logic, web UI)
- Existing WhatsApp transport layer (whatsmeow, QR pairing, send/receive, outbox, dedup) is preserved unchanged
- All state changes in a single PostgreSQL transaction
- Existing `CREATE TABLE IF NOT EXISTS` migration pattern preserved (idempotent)

## Review Focus

1. **Empty flow (no questions configured):** A customer messages before admin has set up any flow — engine must reply with a safe fallback message, not crash or go silent. Test: `TestEngine_NoFlowConfigured`
2. **Concurrent messages from same phone:** Two messages arrive before the first transaction commits — per-phone mutex in `HandleInbound` already serializes, but the engine must never advance two steps from one message. Test: existing `phoneLock` coverage + `TestEngine_IdempotentAdvance`
3. **Admin deletes a question mid-conversation:** Customer is on question 5, admin deletes it — engine must gracefully restart the flow. Test: `TestEngine_DeletedQuestion`
4. **Select question with input not in allowed_values:** Customer types "truck" when options are SUV/Sedan/MPV/Any — must send error_message and re-ask. Test: `TestEngine_InvalidSelectValue`
5. **Circular flow (question A → B → A):** Admin misconfigures a loop — engine must detect and break after N iterations. Test: `TestEngine_CircularFlowBreaker`

---

### Task 1: Database Migration — Rule Engine Tables + Exchange Removal

**Files:**
- Create: `migrations/10_rule_engine.sql`

**Interfaces:**
- Consumes: nothing
- Produces: tables `bot_flows`, `bot_questions`, `bot_conditions`, `bot_responses`, `conversation_answers`; columns `conversations.current_flow_id`, `conversations.current_question_id`; column `leads.extracted_data`

- [ ] **Step 1: Write migration `migrations/10_rule_engine.sql`**

```sql
-- bot_flows: groups of questions (e.g. "Welcome", "Vehicle Enquiry")
CREATE TABLE IF NOT EXISTS bot_flows (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL DEFAULT '',
  slug TEXT NOT NULL DEFAULT '',
  is_entry_flow BOOLEAN NOT NULL DEFAULT FALSE,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_bot_flow_slug ON bot_flows(slug);

-- bot_questions: each step in a flow
CREATE TABLE IF NOT EXISTS bot_questions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  flow_id UUID NOT NULL REFERENCES bot_flows(id) ON DELETE CASCADE,
  field_name TEXT NOT NULL DEFAULT '',
  question_text TEXT NOT NULL DEFAULT '',
  question_type TEXT NOT NULL DEFAULT 'text',
  validation_rule TEXT NOT NULL DEFAULT '',
  allowed_values JSONB NOT NULL DEFAULT '[]',
  error_message TEXT NOT NULL DEFAULT 'Invalid input. Please try again.',
  next_question_id UUID REFERENCES bot_questions(id) ON DELETE SET NULL,
  is_required BOOLEAN NOT NULL DEFAULT TRUE,
  order_index INT NOT NULL DEFAULT 0,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_bq_flow ON bot_questions(flow_id, order_index);

-- bot_conditions: conditional routing after a question
CREATE TABLE IF NOT EXISTS bot_conditions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  question_id UUID NOT NULL REFERENCES bot_questions(id) ON DELETE CASCADE,
  field_name TEXT NOT NULL DEFAULT '',
  operator TEXT NOT NULL DEFAULT 'EQUALS',
  value TEXT NOT NULL DEFAULT '',
  target_question_id UUID REFERENCES bot_questions(id) ON DELETE SET NULL,
  target_flow_id UUID REFERENCES bot_flows(id) ON DELETE SET NULL,
  priority INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_bc_question ON bot_conditions(question_id, priority);

-- bot_responses: admin-configurable response templates
CREATE TABLE IF NOT EXISTS bot_responses (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  response_key TEXT UNIQUE NOT NULL,
  response_text TEXT NOT NULL DEFAULT '',
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- conversation_answers: audit trail of every valid answer
CREATE TABLE IF NOT EXISTS conversation_answers (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  question_id UUID NOT NULL REFERENCES bot_questions(id) ON DELETE CASCADE,
  field_name TEXT NOT NULL DEFAULT '',
  value_captured TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ca_conv ON conversation_answers(conversation_id, created_at);

-- Add state tracking to conversations
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS current_flow_id UUID REFERENCES bot_flows(id) ON DELETE SET NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS current_question_id UUID REFERENCES bot_questions(id) ON DELETE SET NULL;

-- Add JSONB extracted_data to leads for fast vehicle matching
ALTER TABLE leads ADD COLUMN IF NOT EXISTS extracted_data JSONB NOT NULL DEFAULT '{}';

-- Seed default bot_responses for all admin-configurable response types
INSERT INTO bot_responses(response_key, response_text) VALUES
  ('welcome', 'Welcome! How can we help you today?'),
  ('invalid_input', 'Sorry, I didn''t understand that. Please try again.'),
  ('no_matching_vehicle', 'Sorry, we don''t have any vehicles matching your criteria right now. Our team will follow up if new stock arrives.'),
  ('flow_complete', 'Thank you! Our team will follow up with you shortly.'),
  ('session_expired', 'Your session has expired. Let''s start fresh!'),
  ('bot_disabled', 'A team member will respond to you shortly.'),
  ('greeting', 'Hello! Welcome back.')
ON CONFLICT (response_key) DO NOTHING;
```

- [ ] **Step 2: Verify migration runs idempotently**

Run: `go run ./cmd/server` (with DATABASE_URL pointing to a local Postgres)
Expected: Server boots, `SELECT count(*) FROM bot_flows` returns 0, `SELECT count(*) FROM bot_responses` returns 7.

- [ ] **Step 3: Commit**

```bash
git add migrations/10_rule_engine.sql
git commit -m "feat: add rule engine tables (bot_flows, bot_questions, bot_conditions, bot_responses, conversation_answers)"
```

---

### Task 2: Bot Engine Core — The Rule-Based State Machine

**Files:**
- Create: `internal/botengine/engine.go`
- Create: `internal/botengine/validate.go`
- Create: `internal/botengine/engine_test.go`

**Interfaces:**
- Consumes: `bot_flows`, `bot_questions`, `bot_conditions`, `bot_responses`, `conversation_answers` tables (Task 1)
- Produces:
  - `Engine` struct with `func New(pool *pgxpool.Pool) *Engine`
  - `func (e *Engine) ProcessMessage(ctx context.Context, tx pgx.Tx, convID, custID, leadID, body string) (reply string, err error)` — the main entry point
  - `func (e *Engine) GetResponse(ctx context.Context, key string) string` — fetches an admin-configured response template
  - `func Validate(value string, questionType string, validationRule string, allowedValues []string, isRequired bool) (normalized string, valid bool)` — pure function

- [ ] **Step 1: Write failing test `TestValidate_SelectType`**

```go
func TestValidate_SelectType(t *testing.T) {
    norm, ok := Validate("SUV", "select", "", []string{"SUV", "Sedan", "MPV", "Any"}, true)
    if !ok || norm != "SUV" { t.Fatalf("expected ok=true norm=SUV, got ok=%v norm=%q", ok, norm) }
    _, ok = Validate("truck", "select", "", []string{"SUV", "Sedan", "MPV", "Any"}, true)
    if ok { t.Fatal("expected invalid for truck") }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/botengine/ -run TestValidate_SelectType -v`
Expected: FAIL (package doesn't exist yet)

- [ ] **Step 3: Implement `validate.go`**

`Validate(value, questionType, validationRule, allowedValues, isRequired) (string, bool)`:
- `text`: any non-empty string if required; apply regex `validationRule` if set
- `number`: must parse as integer; apply min/max from validationRule if set (format: `min:0,max:999999`)
- `select`: case-insensitive match against `allowedValues`; also accept numeric index ("1" → first option)
- `boolean`: normalize yes/no/y/n/true/false → "yes"/"no"
- `phone`: digits only, 7-15 chars
- `email`: basic regex
- Empty value + `isRequired=false` → valid, return ""
- Empty value + `isRequired=true` → invalid

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/botengine/ -run TestValidate -v`
Expected: PASS

- [ ] **Step 5: Write failing test `TestEngine_ProcessMessage`**

Uses a test helper that inserts a flow with 2 questions into a test DB, then calls `ProcessMessage`. Asserts:
1. First call with no current_question → sends the first question's text
2. Valid answer → saves to `conversation_answers`, updates `leads.extracted_data`, sends next question
3. Invalid answer → sends `error_message`, does not advance

- [ ] **Step 6: Implement `engine.go`**

`ProcessMessage(ctx, tx, convID, custID, leadID, body)`:
1. Load conversation's `current_flow_id` and `current_question_id`
2. If no flow set: find the entry flow (`is_entry_flow=true`), set it, send its first question
3. If flow set but no question: send first active question in flow (by `order_index`)
4. If question set: call `Validate()` with the question's config
5. If invalid: return `error_message` (do not advance)
6. If valid:
   - `INSERT INTO conversation_answers`
   - Update `leads.extracted_data` JSONB: `extracted_data || jsonb_build_object(field_name, value)`
   - Evaluate `bot_conditions` for this question (ordered by priority): first match wins
   - If condition matches → set `current_question_id` (or `current_flow_id` if target_flow_id)
   - Else use `next_question_id`
   - If next is NULL → flow complete, fetch `bot_responses` key `flow_complete`, clear flow state
   - Send next question's `question_text`
7. Circular-flow guard: if the same question_id is hit 3 times in the same call chain, break with a safe fallback

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/botengine/ -v`
Expected: all PASS

- [ ] **Step 8: Commit**

```bash
git add internal/botengine/
git commit -m "feat: bot engine core — rule-based state machine with validation"
```

---

### Task 3: Wire Bot Engine into WhatsApp Worker

**Files:**
- Modify: `internal/whatsapp/worker.go` (HandleInbound, lines ~1321-1783)
- Modify: `cmd/server/main.go` (pass engine to worker)

**Interfaces:**
- Consumes: `botengine.Engine` (Task 2), `Worker` struct
- Produces: Updated `Worker` with field `Engine *botengine.Engine`; `HandleInbound` delegates to engine when bot is enabled

- [ ] **Step 1: Add `Engine` field to `Worker` struct and `New()` constructor**

In `worker.go`, add `engine *botengine.Engine` to the `Worker` struct. Update `New()` to accept and store it.

- [ ] **Step 2: Rewrite `HandleInbound` core logic**

Replace the block from line ~1413 (after `!botEnabled` check) through line ~1782 (before `tx.Commit`) with:
1. Keep: dedup, customer upsert, conversation open/expire, lead creation, message insert, human takeover check
2. Replace: the entire `validStates` / `Next()` / state-machine block with a single call: `reply, err := w.engine.ProcessMessage(ctx, tx, convID, custID, leadID, body)`
3. Keep: message out insert, tx.Commit, photo jobs

Special cases to preserve:
- `isGreetingOnly` → reset flow to entry flow (configurable via `bot_responses.greeting`)
- `norm(body) == "menu" || "restart"` → same reset
- Session timeout → same reset
- Human takeover (`!botEnabled`) → stay silent (already handled)

- [ ] **Step 3: Update `cmd/server/main.go` to create and pass `botengine.Engine`**

```go
import "sellingbot/internal/botengine"
// after pool is ready:
engine := botengine.New(pool)
wa := whatsapp.New(pool, cfg.DataDir, cfg.WhatsappEnabled, cfg.DatabaseURL, engine)
```

- [ ] **Step 4: Verify the server compiles and boots**

Run: `go build ./cmd/server && echo "OK"`
Expected: OK

- [ ] **Step 5: Commit**

```bash
git add internal/whatsapp/worker.go cmd/server/main.go
git commit -m "feat: wire bot engine into WhatsApp HandleInbound"
```

---

### Task 4: Vehicle Matching Integration

**Files:**
- Create: `internal/botengine/matching.go`
- Modify: `internal/botengine/engine.go` (call matching on flow completion)

**Interfaces:**
- Consumes: `leads.extracted_data` JSONB, `vehicles` table
- Produces: `func MatchVehicles(ctx context.Context, tx pgx.Tx, leadID string, data map[string]any) ([]MatchResult, error)`

- [ ] **Step 1: Write failing test `TestMatchVehicles_BudgetAndType`**

Insert test vehicles and a lead with `extracted_data = {"vehicle_type": "SUV", "budget_max": "100000"}`. Assert matching returns only SUVs under 100k.

- [ ] **Step 2: Implement `matching.go`**

`MatchVehicles` reads `extracted_data` from the lead, builds a dynamic WHERE clause against `vehicles` (status='AVAILABLE'), using configured field mappings:
- `vehicle_type` → `vehicles.model` ILIKE or a category column
- `budget_max` → `vehicles.price <= $x`
- `brand` → `vehicles.make ILIKE`
- `fuel` → `vehicles.fuel ILIKE`
- `transmission` → `vehicles.transmission ILIKE`
- `year_min` → `vehicles.year >= $x`

Returns `[]MatchResult{ID, Make, Model, Year, Price, Fuel, Transmission}`.

Format results as a numbered WhatsApp message. Insert into `vehicle_matches` table. Create a followup. Store `match_ids` in `extracted_data`.

- [ ] **Step 3: Hook matching into engine flow completion**

In `engine.go`, when a flow completes and the flow's slug indicates a buy/enquiry flow (configurable: `bot_flows` gets a `trigger_matching` boolean column — add to migration), call `MatchVehicles` and append results to the reply.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/botengine/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/botengine/matching.go internal/botengine/engine.go
git commit -m "feat: vehicle matching from extracted_data JSONB"
```

---

### Task 5: Admin API — Bot Flow & Question CRUD

**Files:**
- Create: `internal/api/bot_admin.go`
- Modify: `internal/api/router.go` (mount new routes)

**Interfaces:**
- Consumes: `bot_flows`, `bot_questions`, `bot_conditions`, `bot_responses` tables
- Produces:
  - `GET /api/bot/flows` — list all flows
  - `POST /api/bot/flows` — create flow
  - `PATCH /api/bot/flows/{id}` — update flow
  - `DELETE /api/bot/flows/{id}` — delete flow
  - `GET /api/bot/flows/{id}/questions` — list questions in a flow
  - `POST /api/bot/questions` — create question
  - `PATCH /api/bot/questions/{id}` — update question
  - `DELETE /api/bot/questions/{id}` — delete question
  - `GET /api/bot/conditions/{question_id}` — list conditions for a question
  - `POST /api/bot/conditions` — create condition
  - `DELETE /api/bot/conditions/{id}` — delete condition
  - `GET /api/bot/responses` — list all response templates
  - `PATCH /api/bot/responses/{id}` — update response text

- [ ] **Step 1: Implement `bot_admin.go` with all CRUD handlers**

Each handler follows the existing pattern in `crud.go` and `flows.go`: read JSON, validate, execute SQL, return JSON. Admin-only (role check via `auth.RoleFromCtx`).

- [ ] **Step 2: Mount routes in `router.go` `authedRoutes`**

Add cases for all `/api/bot/*` paths.

- [ ] **Step 3: Verify endpoints work**

Run: `go build ./cmd/server && echo "OK"`
Expected: compiles

- [ ] **Step 4: Commit**

```bash
git add internal/api/bot_admin.go internal/api/router.go
git commit -m "feat: admin API for bot flows, questions, conditions, responses"
```

---

### Task 6: Remove Exchange Feature

**Files:**
- Modify: `internal/api/router.go` (remove exchange routes, lines 257-264)
- Modify: `internal/api/flows.go` (remove exchange functions, lines ~738-905)
- Modify: `internal/whatsapp/statemachine.go` (remove EXCHANGE states, `wantsExchange`, exchange flow in `Next()`)
- Modify: `internal/whatsapp/worker.go` (remove exchange valuation inserts, lines ~1688-1742)
- Modify: `web/dist/index.html` (remove exchange section and references)
- Modify: `web/dist/app.js` (remove exchange functions and nav entry)

**Interfaces:**
- Consumes: nothing new
- Produces: codebase without any exchange references (the `exchange_valuations` table stays in the DB but is unused — no destructive migration)

- [ ] **Step 1: Remove exchange routes from `router.go`**

Delete the 4 cases for `/api/exchange-valuations`.

- [ ] **Step 2: Remove exchange handler functions from `flows.go`**

Delete `listExchangeValuations`, `exchangeID`, `acceptExchange`, `rejectExchange`, `reopenExchange`.

- [ ] **Step 3: Remove exchange states from `statemachine.go`**

Remove `EXCHANGE_CURRENT` and `EXCHANGE_WANT` from `validStates`. Remove `wantsExchange` function. Remove exchange cases from `Next()`. Remove exchange references from intent detection and menu text (change "BUY, SELL or EXCHANGE" to "BUY or SELL").

- [ ] **Step 4: Remove exchange valuation inserts from `worker.go`**

Delete the `EXCHANGE_CURRENT` → `EXCHANGE_WANT` insert block and the `EXCHANGE_WANT` → `BUY_RESULTS` matching block.

- [ ] **Step 5: Remove exchange from web UI**

In `index.html`: remove the exchange section (`s-exc`), remove "Exchange" button, remove EXCHANGE option from intent filter.
In `app.js`: remove `loadEXC`, `exchangeAccept`, `exchangeReject`, `exchangeReopen` functions, remove 'exc' from nav.

- [ ] **Step 6: Verify compilation and boot**

Run: `go build ./cmd/server && echo "OK"`
Expected: OK, no exchange references in `grep -r "exchange" internal/ web/` (only the DB table remains)

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat: remove exchange feature entirely"
```

---

### Task 7: Admin Panel — Bot Configuration UI

**Files:**
- Modify: `web/dist/index.html` (add Bot Config section)
- Modify: `web/dist/app.js` (add bot config UI logic)

**Interfaces:**
- Consumes: Bot Admin API endpoints (Task 5)
- Produces: Interactive admin UI sections for managing flows, questions, conditions, and responses

- [ ] **Step 1: Add Bot Config navigation and sections to `index.html`**

Add a new section `s-bot` with subsections for:
- Flows list with create/edit/delete
- Questions list (per flow) with create/edit/delete, drag-to-reorder
- Conditions list (per question) with create/delete
- Responses list with edit

- [ ] **Step 2: Add JavaScript functions to `app.js`**

Functions:
- `loadBotFlows()` — fetch and render flows
- `addBotFlow()` / `editBotFlow(id)` / `deleteBotFlow(id)`
- `loadBotQuestions(flowId)` — fetch and render questions for a flow
- `addBotQuestion(flowId)` — form with: question_text, field_name, question_type (dropdown), validation_rule, allowed_values (textarea, one per line), error_message, next_question_id (dropdown of questions), is_required, order_index
- `editBotQuestion(id)` / `deleteBotQuestion(id)`
- `loadBotConditions(questionId)` — fetch and render
- `addBotCondition(questionId)` — form with operator dropdown, value, target_question/flow dropdowns
- `deleteBotCondition(id)`
- `loadBotResponses()` — fetch and render editable list
- `editBotResponse(id)` — inline edit

- [ ] **Step 3: Update navigation to include "Bot Config" tab**

Add `['bot', 'Bot Config']` to the nav array.

- [ ] **Step 4: Verify the admin UI loads and shows the empty state**

Run server, open browser, navigate to Bot Config tab. Verify empty flows list renders.

- [ ] **Step 5: Commit**

```bash
git add web/dist/
git commit -m "feat: admin panel bot configuration UI"
```

---

### Task 8: Seed Default Buy & Sell Flows

**Files:**
- Modify: `migrations/10_rule_engine.sql` (add seed data at bottom)

**Interfaces:**
- Consumes: tables from Task 1
- Produces: Pre-configured "Buy a Car" and "Sell a Car" flows with questions that replicate the current bot behavior, but now fully editable from the Admin Panel

- [ ] **Step 1: Add seed INSERT statements to migration**

Seed a "Buy a Car" flow with questions:
1. `vehicle_type` (select: SUV/Sedan/Hatchback/MPV/Any)
2. `budget_max` (number, validation: min:0,max:99999999)
3. `brand` (text)
4. `model` (text)
5. `fuel` (select: Petrol/Diesel/CNG/Electric/Hybrid/Any)
6. `transmission` (select: Manual/Automatic/Any)
7. `year_min` (number, validation: min:1995,max:2027)

Seed a "Sell a Car" flow with questions:
1. `sell_brand` (text)
2. `sell_model` (text)
3. `sell_year` (number)
4. `sell_km` (number)
5. `sell_fuel` (select)
6. `sell_transmission` (select)
7. `sell_condition` (select: Excellent/Good/Fair/Poor)
8. `sell_location` (text)

Set buy flow as `is_entry_flow = true`, `trigger_matching = true`.

Add a condition on `vehicle_type = "Any"` → skip to brand question.

Seed bot_responses for welcome, greeting, invalid_input, no_matching_vehicle, flow_complete.

- [ ] **Step 2: Verify migration runs and seeds data**

Run: `go run ./cmd/server`
Expected: `SELECT count(*) FROM bot_questions` returns 15, `SELECT count(*) FROM bot_flows` returns 2

- [ ] **Step 3: Commit**

```bash
git add migrations/10_rule_engine.sql
git commit -m "feat: seed default Buy and Sell flows with questions"
```

---

### Task 9: End-to-End Testing & Cleanup

**Files:**
- Modify: `internal/botengine/engine_test.go` (add integration tests)
- Modify: `internal/whatsapp/worker.go` (remove dead code from old state machine references)

**Interfaces:**
- Consumes: everything from Tasks 1-8
- Produces: passing test suite, clean compilation

- [ ] **Step 1: Write integration test `TestEngine_FullBuyFlow`**

Simulates a complete buy conversation: welcome → vehicle_type(SUV) → budget(100000) → brand(BMW) → model(X1) → fuel(Petrol) → transmission(Automatic) → year(2020) → matching results. Assert `conversation_answers` has 7 rows. Assert `leads.extracted_data` contains all fields.

- [ ] **Step 2: Write test `TestEngine_InvalidThenValid`**

Send invalid value at select step → get error → send valid value → advance. Assert conversation_answers has exactly 1 row (the valid one).

- [ ] **Step 3: Write test `TestEngine_SessionExpiry`**

Simulate a conversation, wait beyond session timeout, send new message → should restart with welcome/entry flow.

- [ ] **Step 4: Clean up old statemachine.go**

The hardcoded `Next()` function, `validStates`, `promptFor`, and all the parsing helpers (`ParseBudget`, `extractBrandModel`, etc.) that are no longer called by any code path can be removed. Keep utility functions that are still used by other parts (`normalizePhone`, `norm`, `atoi`, `nospace`).

- [ ] **Step 5: Run full test suite**

Run: `go test ./... -v`
Expected: all pass, no compilation errors

- [ ] **Step 6: Verify the simulate endpoint still works**

Run: `curl -X POST http://localhost:8080/api/whatsapp/simulate -H 'Authorization: Bearer <token>' -d '{"body":"hi"}'`
Expected: returns the welcome message from `bot_responses`

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat: end-to-end tests, cleanup old hardcoded state machine"
```
