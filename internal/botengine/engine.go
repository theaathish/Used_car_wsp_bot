package botengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

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
	q, err := e.loadQuestion(ctx, tx, *currentQuestionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Review Focus 3: Question deleted mid-conversation -> restart flow gracefully
			return e.restartFlow(ctx, tx, convID, currentFlowID)
		}
		return "", err
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

	// 8. Circular flow guard (Review Focus 5): prevent infinite loops
	visited := map[string]int{q.ID: 1}
	chainCount := 0
	targetQID := *nextQuestionID

	for {
		chainCount++
		if chainCount > 10 || visited[targetQID] >= 3 {
			// Loop detected -> break and complete flow safely
			return e.completeFlow(ctx, tx, convID, leadID, currentFlowID)
		}
		visited[targetQID]++

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

	// Check if this flow triggers vehicle matching
	if flowID != nil && *flowID != "" && leadID != "" {
		var triggerMatching bool
		err := tx.QueryRow(ctx, `SELECT trigger_matching FROM bot_flows WHERE id=$1`, *flowID).Scan(&triggerMatching)
		if err == nil && triggerMatching {
			var extracted map[string]any
			var rawData []byte
			_ = tx.QueryRow(ctx, `SELECT extracted_data FROM leads WHERE id=$1`, leadID).Scan(&rawData)
			if len(rawData) > 0 {
				_ = json.Unmarshal(rawData, &extracted)
			}
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

// Dummy sql.NullString helper to suppress unused import if needed
var _ = sql.NullString{}
