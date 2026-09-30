package botengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MatcherFunc is called on flow completion if flow has trigger_matching=true.
type MatcherFunc func(ctx context.Context, tx pgx.Tx, leadID string, extracted map[string]any) (string, error)

// Engine runs the database-driven bot conversation state machine.
type Engine struct {
	pool    *pgxpool.Pool
	matcher MatcherFunc
}

// New creates a new bot engine.
func New(pool *pgxpool.Pool) *Engine {
	return &Engine{
		pool: pool,
	}
}

// SetMatcher configures a vehicle matcher callback for flow completion.
func (e *Engine) SetMatcher(fn MatcherFunc) {
	e.matcher = fn
}

var defaultResponses = map[string]string{
	"welcome":             "Welcome! How can we help you today?",
	"invalid_input":       "Sorry, I didn't understand that. Please try again.",
	"no_matching_vehicle": "Sorry, we don't have any vehicles matching your criteria right now. Our team will follow up if new stock arrives.",
	"flow_complete":       "Thank you! Our team will follow up with you shortly.",
	"session_expired":     "Your session has expired. Let's start fresh!",
	"bot_disabled":        "A team member will respond to you shortly.",
	"greeting":            "Hello! Welcome back.",
}

// GetResponse fetches an admin-configured response template from bot_responses,
// falling back to hardcoded defaults if not found.
func (e *Engine) GetResponse(ctx context.Context, key string) string {
	fallback := defaultResponses[key]
	if fallback == "" {
		fallback = "Thank you!"
	}
	if e.pool == nil {
		return fallback
	}
	var text string
	err := e.pool.QueryRow(ctx, `SELECT response_text FROM bot_responses WHERE response_key=$1 AND is_active=true`, key).Scan(&text)
	if err != nil || strings.TrimSpace(text) == "" {
		return fallback
	}
	return text
}

// Question models a question row from bot_questions.
type Question struct {
	ID             string
	FlowID         string
	FieldName      string
	QuestionText   string
	QuestionType   string
	ValidationRule string
	AllowedValues  []string
	ErrorMessage   string
	NextQuestionID *string
	IsRequired     bool
	OrderIndex     int
	IsActive       bool
}

// Condition models a conditional routing rule from bot_conditions.
type Condition struct {
	FieldName        string
	Operator         string
	Value            string
	TargetQuestionID *string
	TargetFlowID     *string
	Priority         int
}

// ProcessMessage processes an inbound message for a conversation in a transaction.
func (e *Engine) ProcessMessage(ctx context.Context, tx pgx.Tx, convID, custID, leadID, body string) (string, error) {
	var currentFlowID, currentQuestionID *string
	err := tx.QueryRow(ctx, `SELECT current_flow_id, current_question_id FROM conversations WHERE id=$1`, convID).Scan(&currentFlowID, &currentQuestionID)
	if err != nil {
		return "", fmt.Errorf("load conversation state: %w", err)
	}

	var activeQ *Question
	if currentQuestionID != nil && *currentQuestionID != "" {
		activeQ, _ = e.loadQuestion(ctx, tx, *currentQuestionID)
	}

	// Conversational NLP layer
	nlp := ParseMessageNLP(body)
	if reply, handled, err := e.handleConversationalNLP(ctx, tx, convID, custID, leadID, body, activeQ, nlp); err != nil {
		return "", err
	} else if handled {
		return reply, nil
	}

	// 1. If no flow set, find entry flow
	if currentFlowID == nil || *currentFlowID == "" {
		entryFlowID, err := e.findEntryFlow(ctx, tx)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Review Focus 1: Empty flow configured -> safe fallback response
				return e.GetResponse(ctx, "welcome"), nil
			}
			return "", err
		}
		currentFlowID = &entryFlowID
		_, err = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=$1, updated_at=now() WHERE id=$2`, entryFlowID, convID)
		if err != nil {
			return "", fmt.Errorf("set current flow: %w", err)
		}
	}

	// 2. If flow is set but no question is set, or if starting a new flow:
	if currentQuestionID == nil || *currentQuestionID == "" {
		firstQ, err := e.findFirstQuestion(ctx, tx, *currentFlowID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return e.GetResponse(ctx, "welcome"), nil
			}
			return "", err
		}
		_, err = tx.Exec(ctx, `UPDATE conversations SET current_question_id=$1, updated_at=now() WHERE id=$2`, firstQ.ID, convID)
		if err != nil {
			return "", fmt.Errorf("set current question: %w", err)
		}
		return firstQ.QuestionText, nil
	}

	// 3. Question is set: load current question
	q := activeQ
	if q == nil {
		q, err = e.loadQuestion(ctx, tx, *currentQuestionID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Review Focus 3: Question deleted mid-conversation -> restart flow gracefully
				return e.restartFlow(ctx, tx, convID, currentFlowID)
			}
			return "", err
		}
	}

	// 4. Validate input
	normVal, ok := Validate(body, q.QuestionType, q.ValidationRule, q.AllowedValues, q.IsRequired)
	if !ok {
		// Review Focus 4: Invalid select value or invalid format -> send error_message, do not advance
		errMsg := q.ErrorMessage
		if strings.TrimSpace(errMsg) == "" {
			errMsg = e.GetResponse(ctx, "invalid_input")
		}
		return errMsg, nil
	}

	// 5. Valid answer: record in conversation_answers
	_, err = tx.Exec(ctx, `INSERT INTO conversation_answers(conversation_id, question_id, field_name, value_captured)
		VALUES ($1, $2, $3, $4)`, convID, q.ID, q.FieldName, normVal)
	if err != nil {
		return "", fmt.Errorf("insert answer: %w", err)
	}

	// Update lead extracted_data JSONB if field_name is set
	if leadID != "" && q.FieldName != "" {
		_, err = tx.Exec(ctx, `UPDATE leads
			SET extracted_data = extracted_data || jsonb_build_object($1::text, $2::text),
			    updated_at = now()
			WHERE id = $3`, q.FieldName, normVal, leadID)
		if err != nil {
			return "", fmt.Errorf("update lead extracted_data: %w", err)
		}
	}

	// 6. Conditional routing
	nextQuestionID, nextFlowID, err := e.evaluateNextStep(ctx, tx, q, normVal)
	if err != nil {
		return "", fmt.Errorf("evaluate next step: %w", err)
	}

	// If routed to another flow
	if nextFlowID != nil && *nextFlowID != "" && (currentFlowID == nil || *nextFlowID != *currentFlowID) {
		currentFlowID = nextFlowID
		_, err = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=$1, updated_at=now() WHERE id=$2`, *nextFlowID, convID)
		if err != nil {
			return "", fmt.Errorf("update flow: %w", err)
		}
		if nextQuestionID == nil || *nextQuestionID == "" {
			firstQ, err := e.findFirstQuestion(ctx, tx, *nextFlowID)
			if err == nil {
				nextQuestionID = &firstQ.ID
			}
		}
	}

	// 7. Check if flow is complete
	if nextQuestionID == nil || *nextQuestionID == "" {
		return e.completeFlow(ctx, tx, convID, leadID, currentFlowID)
	}

	// 8. Circular flow guard (Review Focus 5): prevent infinite immediate loops
	targetQID := *nextQuestionID
	if targetQID == q.ID {
		return e.completeFlow(ctx, tx, convID, leadID, currentFlowID)
	}

	nextQ, err := e.loadQuestion(ctx, tx, targetQID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return e.completeFlow(ctx, tx, convID, leadID, currentFlowID)
		}
		return "", err
	}

	// Update conversation state to next question
	_, err = tx.Exec(ctx, `UPDATE conversations SET current_question_id=$1, updated_at=now() WHERE id=$2`, nextQ.ID, convID)
	if err != nil {
		return "", fmt.Errorf("update current question: %w", err)
	}

	return nextQ.QuestionText, nil
}

// ResetConversation resets flow state to start fresh (e.g. on greeting, menu, timeout).
func (e *Engine) ResetConversation(ctx context.Context, tx pgx.Tx, convID string) (string, error) {
	entryFlowID, err := e.findEntryFlow(ctx, tx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_, _ = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=NULL, current_question_id=NULL, updated_at=now() WHERE id=$1`, convID)
			return e.GetResponse(ctx, "welcome"), nil
		}
		return "", err
	}

	firstQ, err := e.findFirstQuestion(ctx, tx, entryFlowID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_, _ = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=$1, current_question_id=NULL, updated_at=now() WHERE id=$2`, entryFlowID, convID)
			return e.GetResponse(ctx, "welcome"), nil
		}
		return "", err
	}

	_, err = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=$1, current_question_id=$2, updated_at=now() WHERE id=$3`, entryFlowID, firstQ.ID, convID)
	if err != nil {
		return "", err
	}
	return firstQ.QuestionText, nil
}

func (e *Engine) findEntryFlow(ctx context.Context, tx pgx.Tx) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM bot_flows WHERE is_entry_flow=true AND is_active=true ORDER BY created_at ASC LIMIT 1`).Scan(&id)
	return id, err
}

func (e *Engine) findFirstQuestion(ctx context.Context, tx pgx.Tx, flowID string) (*Question, error) {
	var q Question
	var rawAllowed []byte
	err := tx.QueryRow(ctx, `SELECT id, flow_id, field_name, question_text, question_type, validation_rule, allowed_values, error_message, next_question_id, is_required, order_index, is_active
		FROM bot_questions
		WHERE flow_id=$1 AND is_active=true
		ORDER BY order_index ASC, created_at ASC LIMIT 1`, flowID).Scan(
		&q.ID, &q.FlowID, &q.FieldName, &q.QuestionText, &q.QuestionType, &q.ValidationRule,
		&rawAllowed, &q.ErrorMessage, &q.NextQuestionID, &q.IsRequired, &q.OrderIndex, &q.IsActive,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(rawAllowed, &q.AllowedValues)
	return &q, nil
}

func (e *Engine) loadQuestion(ctx context.Context, tx pgx.Tx, questionID string) (*Question, error) {
	var q Question
	var rawAllowed []byte
	err := tx.QueryRow(ctx, `SELECT id, flow_id, field_name, question_text, question_type, validation_rule, allowed_values, error_message, next_question_id, is_required, order_index, is_active
		FROM bot_questions
		WHERE id=$1 AND is_active=true`, questionID).Scan(
		&q.ID, &q.FlowID, &q.FieldName, &q.QuestionText, &q.QuestionType, &q.ValidationRule,
		&rawAllowed, &q.ErrorMessage, &q.NextQuestionID, &q.IsRequired, &q.OrderIndex, &q.IsActive,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(rawAllowed, &q.AllowedValues)
	return &q, nil
}

func (e *Engine) evaluateNextStep(ctx context.Context, tx pgx.Tx, q *Question, normVal string) (nextQID *string, nextFlowID *string, err error) {
	// 1. Evaluate bot_conditions in priority order
	rows, err := tx.Query(ctx, `SELECT field_name, operator, value, target_question_id, target_flow_id
		FROM bot_conditions
		WHERE question_id=$1
		ORDER BY priority ASC, created_at ASC`, q.ID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var cond Condition
			if err := rows.Scan(&cond.FieldName, &cond.Operator, &cond.Value, &cond.TargetQuestionID, &cond.TargetFlowID); err != nil {
				continue
			}
			if matchesCondition(normVal, cond.Operator, cond.Value) {
				return cond.TargetQuestionID, cond.TargetFlowID, nil
			}
		}
	}

	// 2. Next question explicitly set
	if q.NextQuestionID != nil && *q.NextQuestionID != "" {
		return q.NextQuestionID, nil, nil
	}

	// 3. Fallback to question with next higher order_index in same flow
	var nextID string
	err = tx.QueryRow(ctx, `SELECT id FROM bot_questions
		WHERE flow_id=$1 AND order_index > $2 AND is_active=true
		ORDER BY order_index ASC, created_at ASC LIMIT 1`, q.FlowID, q.OrderIndex).Scan(&nextID)
	if err == nil {
		return &nextID, nil, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	return nil, nil, err
}

func matchesCondition(inputVal, operator, condVal string) bool {
	op := strings.ToUpper(strings.TrimSpace(operator))
	switch op {
	case "EQUALS", "=", "EQ":
		return strings.EqualFold(strings.TrimSpace(inputVal), strings.TrimSpace(condVal))
	case "NOT_EQUALS", "!=", "NEQ":
		return !strings.EqualFold(strings.TrimSpace(inputVal), strings.TrimSpace(condVal))
	case "CONTAINS":
		return strings.Contains(strings.ToLower(inputVal), strings.ToLower(condVal))
	case "GREATER_THAN", ">", "GT":
		inNum, err1 := strconv.ParseFloat(strings.TrimSpace(inputVal), 64)
		condNum, err2 := strconv.ParseFloat(strings.TrimSpace(condVal), 64)
		return err1 == nil && err2 == nil && inNum > condNum
	case "LESS_THAN", "<", "LT":
		inNum, err1 := strconv.ParseFloat(strings.TrimSpace(inputVal), 64)
		condNum, err2 := strconv.ParseFloat(strings.TrimSpace(condVal), 64)
		return err1 == nil && err2 == nil && inNum < condNum
	case "GTE", ">=":
		inNum, err1 := strconv.ParseFloat(strings.TrimSpace(inputVal), 64)
		condNum, err2 := strconv.ParseFloat(strings.TrimSpace(condVal), 64)
		return err1 == nil && err2 == nil && inNum >= condNum
	case "LTE", "<=":
		inNum, err1 := strconv.ParseFloat(strings.TrimSpace(inputVal), 64)
		condNum, err2 := strconv.ParseFloat(strings.TrimSpace(condVal), 64)
		return err1 == nil && err2 == nil && inNum <= condNum
	case "IN":
		parts := strings.Split(condVal, ",")
		for _, p := range parts {
			if strings.EqualFold(strings.TrimSpace(inputVal), strings.TrimSpace(p)) {
				return true
			}
		}
		return false
	default:
		return strings.EqualFold(inputVal, condVal)
	}
}

func (e *Engine) restartFlow(ctx context.Context, tx pgx.Tx, convID string, currentFlowID *string) (string, error) {
	targetFlow := ""
	if currentFlowID != nil && *currentFlowID != "" {
		targetFlow = *currentFlowID
	} else {
		var err error
		targetFlow, err = e.findEntryFlow(ctx, tx)
		if err != nil {
			return e.GetResponse(ctx, "welcome"), nil
		}
	}
	firstQ, err := e.findFirstQuestion(ctx, tx, targetFlow)
	if err != nil {
		return e.GetResponse(ctx, "welcome"), nil
	}
	_, err = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=$1, current_question_id=$2, updated_at=now() WHERE id=$3`, targetFlow, firstQ.ID, convID)
	if err != nil {
		return "", err
	}
	return firstQ.QuestionText, nil
}

func (e *Engine) completeFlow(ctx context.Context, tx pgx.Tx, convID, leadID string, flowID *string) (string, error) {
	_, err := tx.Exec(ctx, `UPDATE conversations SET current_flow_id=NULL, current_question_id=NULL, updated_at=now() WHERE id=$1`, convID)
	if err != nil {
		return "", fmt.Errorf("clear conversation flow state: %w", err)
	}

	reply := e.GetResponse(ctx, "flow_complete")

	// Check if this flow is a Sell Car flow (or extracted data contains car sell fields)
	var flowSlug string
	if flowID != nil && *flowID != "" {
		_ = tx.QueryRow(ctx, `SELECT slug FROM bot_flows WHERE id=$1`, *flowID).Scan(&flowSlug)
	}

	var extracted map[string]any
	var rawData []byte
	if leadID != "" {
		_ = tx.QueryRow(ctx, `SELECT extracted_data FROM leads WHERE id=$1`, leadID).Scan(&rawData)
		if len(rawData) > 0 {
			_ = json.Unmarshal(rawData, &extracted)
		}
	}

	isSellFlow := flowSlug == "sell_flow"
	if !isSellFlow && extracted != nil {
		_, hasBrand := getString(extracted, "sell_brand")
		_, hasModel := getString(extracted, "sell_model")
		if hasBrand && hasModel {
			isSellFlow = true
		}
	}

	if isSellFlow && extracted != nil && leadID != "" {
		brand, _ := getString(extracted, "sell_brand")
		if brand == "" {
			brand, _ = getString(extracted, "brand")
		}
		model, _ := getString(extracted, "sell_model")
		if model == "" {
			model, _ = getString(extracted, "model")
		}
		yearNum, _ := getNumber(extracted, "sell_year")
		if yearNum == 0 {
			yearNum, _ = getNumber(extracted, "year")
		}
		kmNum, _ := getNumber(extracted, "sell_km")
		if kmNum == 0 {
			kmNum, _ = getNumber(extracted, "km")
		}
		fuel, _ := getString(extracted, "sell_fuel")
		if fuel == "" {
			fuel, _ = getString(extracted, "fuel")
		}
		trans, _ := getString(extracted, "sell_transmission")
		if trans == "" {
			trans, _ = getString(extracted, "transmission")
		}
		cond, _ := getString(extracted, "sell_condition")
		if cond == "" {
			cond, _ = getString(extracted, "condition")
		}
		loc, _ := getString(extracted, "sell_location")
		if loc == "" {
			loc, _ = getString(extracted, "location")
		}
		reg, _ := getString(extracted, "sell_reg")
		if reg == "" {
			reg, _ = getString(extracted, "registration")
		}
		expPrice, _ := getNumber(extracted, "expected_price")

		if brand != "" && model != "" {
			sID := uuid.NewString()
			vID := uuid.NewString()
			valPrice := EstimateVehicleValuation(brand, model, int(yearNum), int(kmNum), cond, int(expPrice))

			// 1. Insert sell_requests as ACCEPTED
			_, _ = tx.Exec(ctx, `INSERT INTO sell_requests (id, lead_id, brand, model, year, registration, km, fuel, transmission, condition, location, status)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'ACCEPTED')`,
				sID, leadID, brand, model, int(yearNum), reg, int(kmNum), fuel, trans, cond, loc)

			// 2. Insert into vehicles inventory as AVAILABLE
			desc := strings.TrimSpace(cond + " " + loc + " " + reg)
			if desc == "" {
				desc = "Verified pre-owned vehicle (auto-reviewed)"
			}
			_, _ = tx.Exec(ctx, `INSERT INTO vehicles (id, make, model, year, price, fuel, transmission, km, status, description, acquired_via)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'AVAILABLE', $9, 'customer_sell')`,
				vID, brand, model, int(yearNum), valPrice, fuel, trans, int(kmNum), desc)

			// 3. Link customer photos from this conversation
			_, _ = tx.Exec(ctx, `INSERT INTO vehicle_images(vehicle_id, path, sort_order)
				SELECT $1, m.media_path, row_number() over ()
				FROM messages m WHERE m.conversation_id = $2 AND m.media_path <> ''`,
				vID, convID)

			// 4. Update lead status
			_, _ = tx.Exec(ctx, `UPDATE leads SET status='QUALIFIED', intent='SELL', updated_at=now() WHERE id=$1`, leadID)

			// 5. Tailored completion message with automated valuation
			reply = fmt.Sprintf("🎉 *Vehicle Review Complete!*\n\nYour %d %s %s has been automatically evaluated and accepted into our inventory!\n\n📋 *Estimated Valuation*: ₹%s\n📍 *Status*: Verified & Listed as Available\n\nOur sales specialist will contact you shortly to coordinate vehicle inspection and paperwork.", int(yearNum), brand, model, FormatPrice(valPrice))
			return reply, nil
		}
	}

	// Check if this flow triggers vehicle matching
	if flowID != nil && *flowID != "" && leadID != "" {
		var triggerMatching bool
		err := tx.QueryRow(ctx, `SELECT trigger_matching FROM bot_flows WHERE id=$1`, *flowID).Scan(&triggerMatching)
		if err == nil && triggerMatching {
			if e.matcher != nil {
				matchMsg, err := e.matcher(ctx, tx, leadID, extracted)
				if err == nil && strings.TrimSpace(matchMsg) != "" {
					reply = reply + "\n\n" + matchMsg
				}
			} else {
				matches, err := MatchVehicles(ctx, tx, leadID, extracted)
				if err == nil {
					matchMsg := FormatMatches(matches)
					if strings.TrimSpace(matchMsg) != "" {
						reply = reply + "\n\n" + matchMsg
					}
				}
			}
		}
	}

	return reply, nil
}

func (e *Engine) loadLeadExtractedData(ctx context.Context, tx pgx.Tx, leadID string) (map[string]any, error) {
	out := map[string]any{}
	if leadID == "" {
		return out, nil
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT extracted_data FROM leads WHERE id=$1`, leadID).Scan(&raw)
	if err != nil {
		return out, nil
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out, nil
}

func (e *Engine) updateLeadExtractedData(ctx context.Context, tx pgx.Tx, leadID string, updates map[string]any) error {
	if leadID == "" || len(updates) == 0 {
		return nil
	}
	b, err := json.Marshal(updates)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE leads SET extracted_data = extracted_data || $1::jsonb, updated_at=now() WHERE id=$2`, b, leadID)
	return err
}

func isAnsweringCurrentQuestion(q *Question, nlp NLPEntities, body string) bool {
	if q == nil {
		return false
	}
	// Explicit action intents are never regular survey answers
	if nlp.Intent == IntentTestDrive || nlp.Intent == IntentFinance || nlp.Intent == IntentHuman || nlp.Intent == IntentSell {
		return false
	}
	// Direct search phrases are not simple survey answers
	lower := strings.ToLower(body)
	if strings.Contains(lower, "i want") || strings.Contains(lower, "looking for") || strings.Contains(lower, "show me") || strings.Contains(lower, "want a") || strings.Contains(lower, "need a") {
		return false
	}

	switch q.FieldName {
	case "vehicle_type":
		if nlp.Brand != "" || nlp.Model != "" || nlp.BudgetMax > 0 {
			return false
		}
		return true
	case "budget_max", "budget":
		if nlp.Brand != "" || nlp.Model != "" {
			return false
		}
		return true
	case "brand", "make":
		if nlp.Brand != "" && nlp.Model != "" {
			return false
		}
		return true
	case "model":
		return true
	case "fuel":
		if nlp.Brand != "" || nlp.Model != "" {
			return false
		}
		return true
	case "transmission":
		if nlp.Brand != "" || nlp.Model != "" {
			return false
		}
		return true
	case "year_min", "year":
		if nlp.Brand != "" || nlp.Model != "" {
			return false
		}
		return true
	default:
		return true
	}
}

func (e *Engine) handleConversationalNLP(ctx context.Context, tx pgx.Tx, convID, custID, leadID, body string, currentQuestion *Question, nlp NLPEntities) (string, bool, error) {
	_ = custID
	// If conversation just started with no active question and user sent a bare greeting, let entry flow initialize
	if currentQuestion == nil && isGreetingText(body) {
		return "", false, nil
	}

	// 1. Reset / Menu
	if nlp.Intent == IntentReset || (nlp.Intent == IntentGreeting && currentQuestion == nil) {
		_, _ = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=NULL, current_question_id=NULL, updated_at=now() WHERE id=$1`, convID)
		msg := "🚗 *Welcome to AutoKart!*\n" +
			"I can help you find, finance, or sell a pre-owned car with 100% automated matching & valuation.\n\n" +
			"You can simply type what you're looking for, e.g.:\n" +
			"• *\"BMW M4\"* or *\"Mercedes C-Class\"*\n" +
			"• *\"Petrol SUV under 15 Lakh\"*\n" +
			"• *\"Automatic hatchback\"*\n\n" +
			"Or choose an option:\n" +
			"1️⃣ *View All Available Cars*\n" +
			"2️⃣ *Sell Your Car* (Instant valuation & auto-listing)\n" +
			"3️⃣ *Book a Test Drive* (Sales specialist desk)\n" +
			"4️⃣ *Apply for Car Finance* (Finance specialist desk)"
		return msg, true, nil
	}

	// 2. Human Agent Request
	if nlp.Intent == IntentHuman {
		_, _ = tx.Exec(ctx, `UPDATE conversations SET bot_enabled=false, updated_at=now() WHERE id=$1`, convID)
		if leadID != "" {
			_, _ = tx.Exec(ctx, `UPDATE leads SET status='FOLLOWUP', updated_at=now() WHERE id=$1`, leadID)
		}
		return "👤 *Connecting you with a sales specialist...*\n\nA human representative from our team will reply to you directly in this chat shortly.", true, nil
	}

	// 3. Human Work: Test Drive
	if nlp.Intent == IntentTestDrive {
		data, _ := e.loadLeadExtractedData(ctx, tx, leadID)
		var targetVehID, targetMake, targetModel string

		if nlp.SelectionIndex > 0 {
			if matchIDsStr, ok := getString(data, "match_ids"); ok && matchIDsStr != "" {
				ids := strings.Split(matchIDsStr, ",")
				if nlp.SelectionIndex <= len(ids) {
					targetVehID = strings.TrimSpace(ids[nlp.SelectionIndex-1])
				}
			}
		}
		if targetVehID == "" {
			if svID, ok := getString(data, "selected_vehicle_id"); ok && svID != "" {
				targetVehID = svID
			}
		}
		if targetVehID == "" {
			if matchIDsStr, ok := getString(data, "match_ids"); ok && matchIDsStr != "" {
				ids := strings.Split(matchIDsStr, ",")
				if len(ids) > 0 {
					targetVehID = strings.TrimSpace(ids[0])
				}
			}
		}
		if targetVehID == "" && (nlp.Brand != "" || nlp.Model != "") {
			qBrand := "%" + nlp.Brand + "%"
			qModel := "%" + nlp.Model + "%"
			_ = tx.QueryRow(ctx, `SELECT id::text, make, model FROM vehicles WHERE (make ILIKE $1 OR model ILIKE $2) AND status='AVAILABLE' LIMIT 1`, qBrand, qModel).Scan(&targetVehID, &targetMake, &targetModel)
		}

		if targetVehID != "" {
			if targetMake == "" {
				_ = tx.QueryRow(ctx, `SELECT make, model FROM vehicles WHERE id=$1`, targetVehID).Scan(&targetMake, &targetModel)
			}
			tID := uuid.NewString()
			_, _ = tx.Exec(ctx, `INSERT INTO test_drives(id, lead_id, vehicle_id, scheduled_at, notes)
				VALUES ($1, $2, $3, now() + interval '1 day', 'WhatsApp booking - Human sales follow-up')`,
				tID, leadID, targetVehID)
			if leadID != "" {
				_, _ = tx.Exec(ctx, `UPDATE leads SET status='TEST_DRIVE', updated_at=now() WHERE id=$1`, leadID)
			}
			_, _ = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=NULL, current_question_id=NULL, updated_at=now() WHERE id=$1`, convID)
			reply := fmt.Sprintf("🚗 *Test Drive Request Confirmed!*\n\nWe have scheduled your test drive request for *%s %s*.\n\nOur human sales specialist has been assigned and will call you shortly to confirm your preferred time slot and location!", targetMake, targetModel)
			return reply, true, nil
		}

		return "🚗 *Schedule a Test Drive*\n\nWhich vehicle would you like to test drive? Please reply with the vehicle number (e.g. *1*) or model name!", true, nil
	}

	// 4. Human Work: Finance / Loan
	if nlp.Intent == IntentFinance {
		if leadID != "" {
			fID := uuid.NewString()
			loanAmt := nlp.BudgetMax
			if loanAmt == 0 {
				loanAmt = 150000
			}
			_, _ = tx.Exec(ctx, `INSERT INTO finance_requests(id, lead_id, loan_amount, tenure_months, employment, income, status)
				VALUES ($1, $2, $3, 60, 'Salaried', 0, 'NEW')`, fID, leadID, loanAmt)
			_, _ = tx.Exec(ctx, `UPDATE leads SET status='QUALIFIED',
				extracted_data = extracted_data || '{"finance_requested":"true"}',
				updated_at=now() WHERE id=$1`, leadID)
		}
		_, _ = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=NULL, current_question_id=NULL, updated_at=now() WHERE id=$1`, convID)
		return "💼 *Finance Application Received!*\n\nOur dedicated finance desk has been notified. A human finance specialist will contact you shortly to review your loan eligibility, zero-down-payment options, and customize low-interest EMI plans for you!", true, nil
	}

	// 5. Automated Selling Flow
	data, _ := e.loadLeadExtractedData(ctx, tx, leadID)
	isSellMode := false
	if sm, ok := getString(data, "sell_mode"); ok && (sm == "true" || sm == "1") {
		isSellMode = true
	}
	if leadID != "" && !isSellMode {
		var currentIntent string
		_ = tx.QueryRow(ctx, `SELECT intent FROM leads WHERE id=$1`, leadID).Scan(&currentIntent)
		if currentIntent == "SELL" {
			isSellMode = true
		}
	}

	// Check if user explicitly switches from sell mode to buy
	lowerBody := strings.ToLower(body)
	explicitBuy := strings.Contains(lowerBody, "buy") ||
		strings.Contains(lowerBody, "purchase") ||
		strings.Contains(lowerBody, "browse") ||
		strings.Contains(lowerBody, "looking to buy") ||
		strings.Contains(lowerBody, "want to buy")

	if isSellMode && explicitBuy {
		isSellMode = false
		if leadID != "" {
			_, _ = tx.Exec(ctx, `UPDATE leads SET intent='BUY', extracted_data = extracted_data - 'sell_mode', updated_at=now() WHERE id=$1`, leadID)
		}
	}

	if nlp.Intent == IntentSell || nlp.HasSellSignals || isSellMode {
		brand := nlp.Brand
		model := nlp.Model
		if brand == "" {
			brand, _ = getString(data, "sell_brand")
		}
		if model == "" {
			model, _ = getString(data, "sell_model")
		}

		year := nlp.YearMin
		if year == 0 {
			yNum, _ := getNumber(data, "sell_year")
			year = int(yNum)
		}
		km := nlp.KM
		if km == 0 {
			kNum, _ := getNumber(data, "sell_km")
			km = int(kNum)
		}

		if brand != "" && model != "" {
			if year == 0 {
				year = 2020
			}
			if km == 0 {
				km = 35000
			}
			fuel := nlp.Fuel
			if fuel == "" {
				fuel, _ = getString(data, "sell_fuel")
			}
			if fuel == "" {
				fuel = "Petrol"
			}
			trans := nlp.Transmission
			if trans == "" {
				trans, _ = getString(data, "sell_transmission")
			}
			if trans == "" {
				trans = "Automatic"
			}

			sID := uuid.NewString()
			vID := uuid.NewString()
			valPrice := EstimateVehicleValuation(brand, model, year, km, "Good", 0)

			_, _ = tx.Exec(ctx, `INSERT INTO sell_requests (id, lead_id, brand, model, year, km, fuel, transmission, condition, status)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'Good', 'ACCEPTED')`,
				sID, leadID, brand, model, year, km, fuel, trans)

			desc := fmt.Sprintf("Verified pre-owned %s %s (auto-reviewed & listed)", brand, model)
			_, _ = tx.Exec(ctx, `INSERT INTO vehicles (id, make, model, year, price, fuel, transmission, km, status, description, acquired_via)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'AVAILABLE', $9, 'customer_sell')`,
				vID, brand, model, year, valPrice, fuel, trans, km, desc)

			if leadID != "" {
				_, _ = tx.Exec(ctx, `UPDATE leads SET status='QUALIFIED', intent='SELL', extracted_data = extracted_data - 'sell_mode', updated_at=now() WHERE id=$1`, leadID)
			}
			_, _ = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=NULL, current_question_id=NULL, updated_at=now() WHERE id=$1`, convID)

			reply := fmt.Sprintf("🎉 *Vehicle Review Complete!*\n\nYour %d %s %s has been automatically evaluated and accepted into our inventory!\n\n📋 *Estimated Valuation*: ₹%s\n📍 *Status*: Verified & Listed as Available\n\nOur team will contact you shortly to coordinate vehicle pickup and paperwork.",
				year, brand, model, FormatPrice(valPrice))
			return reply, true, nil
		}

		// Save sell_mode and whatever partial fields are provided
		updates := map[string]any{"sell_mode": "true"}
		if brand != "" {
			updates["sell_brand"] = brand
		}
		if model != "" {
			updates["sell_model"] = model
		}
		if year > 0 {
			updates["sell_year"] = year
		}
		if km > 0 {
			updates["sell_km"] = km
		}
		if leadID != "" {
			_ = e.updateLeadExtractedData(ctx, tx, leadID, updates)
			_, _ = tx.Exec(ctx, `UPDATE leads SET intent='SELL', updated_at=now() WHERE id=$1`, leadID)
		}
		_, _ = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=NULL, current_question_id=NULL, updated_at=now() WHERE id=$1`, convID)

		return "🚗 *Sell Your Car Instantly!*\n\nPlease tell us your car's Brand, Model, Manufacturing Year, and approximate Mileage (e.g. *\"2019 Honda City, 45,000 km\"*).\n\nWe will evaluate your car automatically and list it in our inventory!", true, nil
	}

	// 6. Direct Vehicle Selection (#1, #2, etc.)
	data, _ = e.loadLeadExtractedData(ctx, tx, leadID)
	matchIDsStr, hasMatches := getString(data, "match_ids")

	if nlp.SelectionIndex > 0 && hasMatches && matchIDsStr != "" {
		ids := strings.Split(matchIDsStr, ",")
		if nlp.SelectionIndex <= len(ids) {
			vID := strings.TrimSpace(ids[nlp.SelectionIndex-1])
			var vMake, vModel, vFuel, vTrans, vDesc string
			var vYear, vPrice, vKM int
			err := tx.QueryRow(ctx, `SELECT make, model, year, price, fuel, transmission, km, description
				FROM vehicles WHERE id=$1`, vID).Scan(&vMake, &vModel, &vYear, &vPrice, &vFuel, &vTrans, &vKM, &vDesc)
			if err == nil {
				_ = e.updateLeadExtractedData(ctx, tx, leadID, map[string]any{"selected_vehicle_id": vID})
				_, _ = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=NULL, current_question_id=NULL, updated_at=now() WHERE id=$1`, convID)
				card := fmt.Sprintf("🚗 *%d %s %s*\n💰 *Price*: ₹%s\n⛽ *Fuel*: %s | ⚙️ *Transmission*: %s\n🛣️ *Mileage*: %d km\n📋 *Details*: %s\n\n👉 Reply *TEST DRIVE* to schedule a test drive with our sales team\n👉 Reply *FINANCE* to apply for EMI / Loan assistance\n👉 Or reply with another vehicle number or search query!",
					vYear, vMake, vModel, formatPrice(vPrice), vFuel, vTrans, vKM, vDesc)
				return card, true, nil
			}
		}
	}

	// 7. Check if user is merely answering the active question
	if isAnsweringCurrentQuestion(currentQuestion, nlp, body) {
		return "", false, nil
	}

	// 8. Instant Search & Inventory Match (Automated Buying)
	if nlp.HasSearchSignals {
		updates := map[string]any{}
		if nlp.Brand != "" {
			updates["brand"] = nlp.Brand
			updates["make"] = nlp.Brand
		}
		if nlp.Model != "" {
			updates["model"] = nlp.Model
		}
		if nlp.BodyType != "" {
			updates["vehicle_type"] = nlp.BodyType
		}
		if nlp.Fuel != "" {
			updates["fuel"] = nlp.Fuel
		}
		if nlp.Transmission != "" {
			updates["transmission"] = nlp.Transmission
		}
		if nlp.BudgetMax > 0 {
			updates["budget_max"] = nlp.BudgetMax
		}
		if nlp.YearMin > 0 {
			updates["year_min"] = nlp.YearMin
		}

		if len(updates) > 0 {
			_ = e.updateLeadExtractedData(ctx, tx, leadID, updates)
			for k, v := range updates {
				data[k] = v
			}
		}

		// Try primary match
		matches, err := MatchVehicles(ctx, tx, leadID, data)
		if err == nil && len(matches) > 0 {
			_, _ = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=NULL, current_question_id=NULL, updated_at=now() WHERE id=$1`, convID)
			return FormatMatches(matches), true, nil
		}

		// Fallback 1: Relax model and search by brand alone
		if nlp.Brand != "" {
			brandMatches, err2 := MatchVehicles(ctx, tx, leadID, map[string]any{"brand": nlp.Brand})
			if err2 == nil && len(brandMatches) > 0 {
				_, _ = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=NULL, current_question_id=NULL, updated_at=now() WHERE id=$1`, convID)
				leadNote := fmt.Sprintf("🚗 We don't have an exact *%s* in stock right now, but here are our top available *%s* vehicles:",
					strings.TrimSpace(nlp.Brand+" "+nlp.Model), nlp.Brand)
				return FormatMatchesWithHeader(brandMatches, leadNote), true, nil
			}
		}

		// Fallback 2: Show top available inventory
		allMatches, err3 := MatchVehicles(ctx, tx, leadID, map[string]any{})
		if err3 == nil && len(allMatches) > 0 {
			_, _ = tx.Exec(ctx, `UPDATE conversations SET current_flow_id=NULL, current_question_id=NULL, updated_at=now() WHERE id=$1`, convID)
			leadNote := "🚗 We don't have vehicles matching that exact search in stock right now, but here are our top featured vehicles:"
			return FormatMatchesWithHeader(allMatches, leadNote), true, nil
		}
	}

	return "", false, nil
}

// Dummy sql.NullString helper to suppress unused import if needed
var _ = sql.NullString{}
