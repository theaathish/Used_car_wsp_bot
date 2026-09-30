package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"sellingbot/internal/auth"
)

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if cl := auth.Current(r); cl != nil && cl.Role != "admin" {
		http.Error(w, `{"error":"admin only"}`, http.StatusForbidden)
		return false
	}
	return true
}

// Flow Handlers

func (s *Server) listBotFlows(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Pool.Query(r.Context(), `
		SELECT f.id::text, f.name, f.slug, f.is_entry_flow, f.trigger_matching, f.is_active, f.created_at, f.updated_at,
		       COUNT(q.id) as question_count
		FROM bot_flows f
		LEFT JOIN bot_questions q ON q.flow_id = f.id
		GROUP BY f.id
		ORDER BY f.created_at ASC
	`)
	if err != nil {
		http.Error(w, `{"error":"db query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var id, name, slug string
		var isEntry, triggerMatch, isActive bool
		var createdAt, updatedAt any
		var qCount int
		if err := rows.Scan(&id, &name, &slug, &isEntry, &triggerMatch, &isActive, &createdAt, &updatedAt, &qCount); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"id":               id,
			"name":             name,
			"slug":             slug,
			"is_entry_flow":    isEntry,
			"trigger_matching": triggerMatch,
			"is_active":        isActive,
			"created_at":       createdAt,
			"updated_at":       updatedAt,
			"question_count":   qCount,
		})
	}
	writeJSON(w, out)
}

func (s *Server) createBotFlow(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var in struct {
		Name            string `json:"name"`
		Slug            string `json:"slug"`
		IsEntryFlow     bool   `json:"is_entry_flow"`
		TriggerMatching bool   `json:"trigger_matching"`
		IsActive        *bool  `json:"is_active"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
		return
	}
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		slug = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(in.Name), " ", "_"))
	}
	isActive := true
	if in.IsActive != nil {
		isActive = *in.IsActive
	}

	ctx := r.Context()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		http.Error(w, `{"error":"db transaction"}`, http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)

	if in.IsEntryFlow {
		_, _ = tx.Exec(ctx, `UPDATE bot_flows SET is_entry_flow=false`)
	}

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO bot_flows(name, slug, is_entry_flow, trigger_matching, is_active)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text
	`, in.Name, slug, in.IsEntryFlow, in.TriggerMatching, isActive).Scan(&id)
	if err != nil {
		http.Error(w, `{"error":"failed to create flow, slug may exist"}`, http.StatusBadRequest)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		http.Error(w, `{"error":"commit failed"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{"id": id, "name": in.Name, "slug": slug, "is_entry_flow": in.IsEntryFlow, "trigger_matching": in.TriggerMatching, "is_active": isActive})
}

func (s *Server) patchBotFlow(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := idParam(r, "/api/bot/flows/")
	if badUUID(w, id) {
		return
	}
	var in struct {
		Name            *string `json:"name"`
		Slug            *string `json:"slug"`
		IsEntryFlow     *bool   `json:"is_entry_flow"`
		TriggerMatching *bool   `json:"trigger_matching"`
		IsActive        *bool   `json:"is_active"`
	}
	if err := readJSON(r, &in); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		http.Error(w, `{"error":"db transaction"}`, http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)

	if in.IsEntryFlow != nil && *in.IsEntryFlow {
		_, _ = tx.Exec(ctx, `UPDATE bot_flows SET is_entry_flow=false WHERE id != $1`, id)
	}

	if in.Name != nil {
		_, _ = tx.Exec(ctx, `UPDATE bot_flows SET name=$1, updated_at=now() WHERE id=$2`, *in.Name, id)
	}
	if in.Slug != nil {
		_, _ = tx.Exec(ctx, `UPDATE bot_flows SET slug=$1, updated_at=now() WHERE id=$2`, *in.Slug, id)
	}
	if in.IsEntryFlow != nil {
		_, _ = tx.Exec(ctx, `UPDATE bot_flows SET is_entry_flow=$1, updated_at=now() WHERE id=$2`, *in.IsEntryFlow, id)
	}
	if in.TriggerMatching != nil {
		_, _ = tx.Exec(ctx, `UPDATE bot_flows SET trigger_matching=$1, updated_at=now() WHERE id=$2`, *in.TriggerMatching, id)
	}
	if in.IsActive != nil {
		_, _ = tx.Exec(ctx, `UPDATE bot_flows SET is_active=$1, updated_at=now() WHERE id=$2`, *in.IsActive, id)
	}

	if err := tx.Commit(ctx); err != nil {
		http.Error(w, `{"error":"update failed"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
}

func (s *Server) deleteBotFlow(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := idParam(r, "/api/bot/flows/")
	if badUUID(w, id) {
		return
	}
	tag, err := s.Pool.Exec(r.Context(), `DELETE FROM bot_flows WHERE id=$1`, id)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"flow not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// Question Handlers

func (s *Server) listBotQuestions(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/api/bot/flows/")
	flowID := strings.TrimSuffix(p, "/questions")
	if badUUID(w, flowID) {
		return
	}

	rows, err := s.Pool.Query(r.Context(), `
		SELECT id::text, flow_id::text, field_name, question_text, question_type,
		       validation_rule, allowed_values, error_message, COALESCE(next_question_id::text, ''),
		       is_required, order_index, is_active, created_at, updated_at
		FROM bot_questions
		WHERE flow_id=$1
		ORDER BY order_index ASC, created_at ASC
	`, flowID)
	if err != nil {
		http.Error(w, `{"error":"db query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var id, fID, fieldName, qText, qType, valRule, errMsg, nextQID string
		var rawAllowed []byte
		var isReq, isActive bool
		var createdAt, updatedAt any
		var orderIdx int
		if err := rows.Scan(&id, &fID, &fieldName, &qText, &qType, &valRule, &rawAllowed, &errMsg, &nextQID, &isReq, &orderIdx, &isActive, &createdAt, &updatedAt); err != nil {
			continue
		}
		var allowedVals []string
		_ = json.Unmarshal(rawAllowed, &allowedVals)
		out = append(out, map[string]any{
			"id":               id,
			"flow_id":          fID,
			"field_name":       fieldName,
			"question_text":    qText,
			"question_type":    qType,
			"validation_rule":  valRule,
			"allowed_values":   allowedVals,
			"error_message":    errMsg,
			"next_question_id": nextQID,
			"is_required":      isReq,
			"order_index":      orderIdx,
			"is_active":        isActive,
			"created_at":       createdAt,
			"updated_at":       updatedAt,
		})
	}
	writeJSON(w, out)
}

func (s *Server) createBotQuestion(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var in struct {
		FlowID         string   `json:"flow_id"`
		FieldName      string   `json:"field_name"`
		QuestionText   string   `json:"question_text"`
		QuestionType   string   `json:"question_type"`
		ValidationRule string   `json:"validation_rule"`
		AllowedValues  []string `json:"allowed_values"`
		ErrorMessage   string   `json:"error_message"`
		NextQuestionID *string  `json:"next_question_id"`
		IsRequired     *bool    `json:"is_required"`
		OrderIndex     int      `json:"order_index"`
		IsActive       *bool    `json:"is_active"`
	}
	if in.FlowID == "" && strings.HasPrefix(r.URL.Path, "/api/bot/flows/") {
		in.FlowID = idParam(r, "/api/bot/flows/")
	}
	if err := readJSON(r, &in); err != nil || in.FlowID == "" || strings.TrimSpace(in.QuestionText) == "" {
		http.Error(w, `{"error":"flow_id and question_text required"}`, http.StatusBadRequest)
		return
	}
	if in.QuestionType == "" {
		in.QuestionType = "text"
	}
	if in.ErrorMessage == "" {
		in.ErrorMessage = "Invalid input. Please try again."
	}
	isReq := true
	if in.IsRequired != nil {
		isReq = *in.IsRequired
	}
	isActive := true
	if in.IsActive != nil {
		isActive = *in.IsActive
	}
	allowedBytes, _ := json.Marshal(in.AllowedValues)
	if len(in.AllowedValues) == 0 {
		allowedBytes = []byte("[]")
	}

	var nextID *string
	if in.NextQuestionID != nil && strings.TrimSpace(*in.NextQuestionID) != "" {
		nextID = in.NextQuestionID
	}

	var id string
	err := s.Pool.QueryRow(r.Context(), `
		INSERT INTO bot_questions(flow_id, field_name, question_text, question_type, validation_rule, allowed_values, error_message, next_question_id, is_required, order_index, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id::text
	`, in.FlowID, in.FieldName, in.QuestionText, in.QuestionType, in.ValidationRule, allowedBytes, in.ErrorMessage, nextID, isReq, in.OrderIndex, isActive).Scan(&id)
	if err != nil {
		http.Error(w, `{"error":"failed to create question"}`, http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]any{"id": id, "flow_id": in.FlowID, "question_text": in.QuestionText})
}

func (s *Server) patchBotQuestion(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := idParam(r, "/api/bot/questions/")
	if badUUID(w, id) {
		return
	}
	var in struct {
		FieldName      *string   `json:"field_name"`
		QuestionText   *string   `json:"question_text"`
		QuestionType   *string   `json:"question_type"`
		ValidationRule *string   `json:"validation_rule"`
		AllowedValues  *[]string `json:"allowed_values"`
		ErrorMessage   *string   `json:"error_message"`
		NextQuestionID *string   `json:"next_question_id"`
		IsRequired     *bool     `json:"is_required"`
		OrderIndex     *int      `json:"order_index"`
		IsActive       *bool     `json:"is_active"`
	}
	if err := readJSON(r, &in); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	if in.FieldName != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_questions SET field_name=$1, updated_at=now() WHERE id=$2`, *in.FieldName, id)
	}
	if in.QuestionText != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_questions SET question_text=$1, updated_at=now() WHERE id=$2`, *in.QuestionText, id)
	}
	if in.QuestionType != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_questions SET question_type=$1, updated_at=now() WHERE id=$2`, *in.QuestionType, id)
	}
	if in.ValidationRule != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_questions SET validation_rule=$1, updated_at=now() WHERE id=$2`, *in.ValidationRule, id)
	}
	if in.AllowedValues != nil {
		b, _ := json.Marshal(*in.AllowedValues)
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_questions SET allowed_values=$1, updated_at=now() WHERE id=$2`, b, id)
	}
	if in.ErrorMessage != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_questions SET error_message=$1, updated_at=now() WHERE id=$2`, *in.ErrorMessage, id)
	}
	if in.NextQuestionID != nil {
		if *in.NextQuestionID == "" {
			_, _ = s.Pool.Exec(ctx, `UPDATE bot_questions SET next_question_id=NULL, updated_at=now() WHERE id=$1`, id)
		} else {
			_, _ = s.Pool.Exec(ctx, `UPDATE bot_questions SET next_question_id=$1, updated_at=now() WHERE id=$2`, *in.NextQuestionID, id)
		}
	}
	if in.IsRequired != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_questions SET is_required=$1, updated_at=now() WHERE id=$2`, *in.IsRequired, id)
	}
	if in.OrderIndex != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_questions SET order_index=$1, updated_at=now() WHERE id=$2`, *in.OrderIndex, id)
	}
	if in.IsActive != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_questions SET is_active=$1, updated_at=now() WHERE id=$2`, *in.IsActive, id)
	}

	writeJSON(w, map[string]any{"ok": true, "id": id})
}

func (s *Server) deleteBotQuestion(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := idParam(r, "/api/bot/questions/")
	if badUUID(w, id) {
		return
	}
	tag, err := s.Pool.Exec(r.Context(), `DELETE FROM bot_questions WHERE id=$1`, id)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"question not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// Condition Handlers

func (s *Server) listBotConditions(w http.ResponseWriter, r *http.Request) {
	qID := idParam(r, "/api/bot/conditions/")
	if qID == "" && strings.HasPrefix(r.URL.Path, "/api/bot/questions/") {
		qID = idParam(r, "/api/bot/questions/")
	}
	if badUUID(w, qID) {
		return
	}

	rows, err := s.Pool.Query(r.Context(), `
		SELECT id::text, question_id::text, field_name, operator, value,
		       COALESCE(target_question_id::text, ''), COALESCE(target_flow_id::text, ''),
		       priority, created_at
		FROM bot_conditions
		WHERE question_id=$1
		ORDER BY priority ASC, created_at ASC
	`, qID)
	if err != nil {
		http.Error(w, `{"error":"db query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var id, questionID, fieldName, op, val, targetQ, targetF string
		var prio int
		var createdAt any
		if err := rows.Scan(&id, &questionID, &fieldName, &op, &val, &targetQ, &targetF, &prio, &createdAt); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"id":                 id,
			"question_id":        questionID,
			"field_name":         fieldName,
			"operator":           op,
			"value":              val,
			"target_question_id": targetQ,
			"target_flow_id":     targetF,
			"priority":           prio,
			"created_at":         createdAt,
		})
	}
	writeJSON(w, out)
}

func (s *Server) createBotCondition(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var in struct {
		QuestionID        string  `json:"question_id"`
		FieldName         string  `json:"field_name"`
		Operator          string  `json:"operator"`
		ConditionOperator string  `json:"condition_operator"`
		Value             string  `json:"value"`
		ConditionValue    string  `json:"condition_value"`
		TargetQuestionID  *string `json:"target_question_id"`
		TargetFlowID      *string `json:"target_flow_id"`
		Priority          int     `json:"priority"`
	}
	if err := readJSON(r, &in); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	if in.QuestionID == "" && strings.HasPrefix(r.URL.Path, "/api/bot/questions/") {
		in.QuestionID = idParam(r, "/api/bot/questions/")
	}
	if in.QuestionID == "" {
		http.Error(w, `{"error":"question_id required"}`, http.StatusBadRequest)
		return
	}
	if in.Operator == "" && in.ConditionOperator != "" {
		in.Operator = in.ConditionOperator
	}
	if in.Operator == "" {
		in.Operator = "EQUALS"
	}
	if in.Value == "" && in.ConditionValue != "" {
		in.Value = in.ConditionValue
	}
	if in.FieldName == "" {
		_ = s.Pool.QueryRow(r.Context(), `SELECT field_name FROM bot_questions WHERE id=$1`, in.QuestionID).Scan(&in.FieldName)
	}
	var targetQ, targetF *string
	if in.TargetQuestionID != nil && strings.TrimSpace(*in.TargetQuestionID) != "" {
		targetQ = in.TargetQuestionID
	}
	if in.TargetFlowID != nil && strings.TrimSpace(*in.TargetFlowID) != "" {
		targetF = in.TargetFlowID
	}

	var id string
	err := s.Pool.QueryRow(r.Context(), `
		INSERT INTO bot_conditions(question_id, field_name, operator, value, target_question_id, target_flow_id, priority)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id::text
	`, in.QuestionID, in.FieldName, in.Operator, in.Value, targetQ, targetF, in.Priority).Scan(&id)
	if err != nil {
		http.Error(w, `{"error":"failed to create condition"}`, http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"id": id, "question_id": in.QuestionID})
}

func (s *Server) patchBotCondition(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := idParam(r, "/api/bot/conditions/")
	if badUUID(w, id) {
		return
	}
	var in struct {
		TargetQuestionID  *string `json:"target_question_id"`
		TargetFlowID      *string `json:"target_flow_id"`
		Operator          *string `json:"operator"`
		ConditionOperator *string `json:"condition_operator"`
		Value             *string `json:"value"`
		ConditionValue    *string `json:"condition_value"`
		Priority          *int    `json:"priority"`
	}
	if err := readJSON(r, &in); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if in.TargetQuestionID != nil {
		if *in.TargetQuestionID == "" {
			_, _ = s.Pool.Exec(ctx, `UPDATE bot_conditions SET target_question_id=NULL WHERE id=$1`, id)
		} else {
			_, _ = s.Pool.Exec(ctx, `UPDATE bot_conditions SET target_question_id=$1 WHERE id=$2`, *in.TargetQuestionID, id)
		}
	}
	if in.TargetFlowID != nil {
		if *in.TargetFlowID == "" {
			_, _ = s.Pool.Exec(ctx, `UPDATE bot_conditions SET target_flow_id=NULL WHERE id=$1`, id)
		} else {
			_, _ = s.Pool.Exec(ctx, `UPDATE bot_conditions SET target_flow_id=$1 WHERE id=$2`, *in.TargetFlowID, id)
		}
	}
	op := in.Operator
	if op == nil {
		op = in.ConditionOperator
	}
	if op != nil && strings.TrimSpace(*op) != "" {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_conditions SET operator=$1 WHERE id=$2`, strings.ToUpper(strings.TrimSpace(*op)), id)
	}
	val := in.Value
	if val == nil {
		val = in.ConditionValue
	}
	if val != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_conditions SET value=$1 WHERE id=$2`, *val, id)
	}
	if in.Priority != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_conditions SET priority=$1 WHERE id=$2`, *in.Priority, id)
	}
	writeJSON(w, map[string]any{"ok": true, "id": id})
}

func (s *Server) deleteBotCondition(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := idParam(r, "/api/bot/conditions/")
	if badUUID(w, id) {
		return
	}
	tag, err := s.Pool.Exec(r.Context(), `DELETE FROM bot_conditions WHERE id=$1`, id)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"condition not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// Response Handlers

func (s *Server) listBotResponses(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Pool.Query(r.Context(), `
		SELECT id::text, response_key, response_text, is_active, created_at, updated_at
		FROM bot_responses
		ORDER BY response_key ASC
	`)
	if err != nil {
		http.Error(w, `{"error":"db query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var id, key, text string
		var isActive bool
		var createdAt, updatedAt any
		if err := rows.Scan(&id, &key, &text, &isActive, &createdAt, &updatedAt); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"id":            id,
			"response_key":  key,
			"response_text": text,
			"message_text":  text,
			"is_active":     isActive,
			"created_at":    createdAt,
			"updated_at":    updatedAt,
		})
	}
	writeJSON(w, out)
}

func (s *Server) patchBotResponse(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := idParam(r, "/api/bot/responses/")
	if badUUID(w, id) {
		return
	}
	var in struct {
		ResponseText *string `json:"response_text"`
		MessageText  *string `json:"message_text"`
		IsActive     *bool   `json:"is_active"`
	}
	if err := readJSON(r, &in); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	txt := in.ResponseText
	if txt == nil {
		txt = in.MessageText
	}

	ctx := r.Context()
	if txt != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_responses SET response_text=$1, updated_at=now() WHERE id=$2`, *txt, id)
	}
	if in.IsActive != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_responses SET is_active=$1, updated_at=now() WHERE id=$2`, *in.IsActive, id)
	}

	writeJSON(w, map[string]any{"ok": true, "id": id})
}

// resetDefaultBotConfig restores the full baseline configuration: Buy & Sell flows,
// all baseline questions and branching rules, and greeting/welcome response templates.
func (s *Server) resetDefaultBotConfig(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		http.Error(w, `{"error":"db transaction failed"}`, http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)

	// 1. Seed/Reset default system responses with greeting
	defaultResponses := []struct {
		key, text string
	}{
		{"welcome", "Welcome to AutoKart! How can we help you today?"},
		{"greeting", "Hello! Welcome to AutoKart. Are you looking to Buy a Car, Sell a Car, or Book a Test Drive?"},
		{"invalid_input", "Sorry, I didn't understand that. Please select or type one of the options above."},
		{"no_matching_vehicle", "Sorry, we don't have any vehicles matching your criteria right now. Our team will follow up if new stock arrives."},
		{"flow_complete", "Thank you! Our sales team will review your preferences and follow up with you shortly."},
		{"session_expired", "Your session has expired. Send 'hi' to start fresh!"},
		{"bot_disabled", "A team member will respond to you shortly."},
	}

	for _, dr := range defaultResponses {
		if _, err := tx.Exec(ctx, `
			INSERT INTO bot_responses(response_key, response_text, is_active)
			VALUES ($1, $2, true)
			ON CONFLICT (response_key) DO UPDATE
			SET response_text = EXCLUDED.response_text, is_active = true, updated_at = now()
		`, dr.key, dr.text); err != nil {
			http.Error(w, `{"error":"response failed: `+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
	}

	// 2. Unset is_entry_flow on all existing flows
	if _, err := tx.Exec(ctx, `UPDATE bot_flows SET is_entry_flow=false`); err != nil {
		http.Error(w, `{"error":"unset entry failed: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	// 3. Upsert Buy a Car flow as entry flow
	var buyFlowID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM bot_flows WHERE slug='buy_flow'`).Scan(&buyFlowID)
	if err != nil {
		buyFlowID = "11111111-1111-1111-1111-111111111101"
		if _, err := tx.Exec(ctx, `
			INSERT INTO bot_flows(id, name, slug, is_entry_flow, trigger_matching, is_active)
			VALUES ($1, 'Buy a Car', 'buy_flow', true, true, true)
			ON CONFLICT (slug) DO UPDATE
			SET is_entry_flow = true, trigger_matching = true, is_active = true, updated_at = now()
		`, buyFlowID); err != nil {
			http.Error(w, `{"error":"buy flow insert failed: `+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
	} else {
		_, _ = tx.Exec(ctx, `UPDATE bot_flows SET is_entry_flow=true, trigger_matching=true, is_active=true, updated_at=now() WHERE id=$1`, buyFlowID)
	}

	// 4. Upsert Sell a Car flow
	var sellFlowID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM bot_flows WHERE slug='sell_flow'`).Scan(&sellFlowID)
	if err != nil {
		sellFlowID = "11111111-1111-1111-1111-111111111102"
		if _, err := tx.Exec(ctx, `
			INSERT INTO bot_flows(id, name, slug, is_entry_flow, trigger_matching, is_active)
			VALUES ($1, 'Sell a Car', 'sell_flow', false, false, true)
			ON CONFLICT (slug) DO UPDATE
			SET is_active = true, updated_at = now()
		`, sellFlowID); err != nil {
			http.Error(w, `{"error":"sell flow insert failed: `+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
	} else {
		_, _ = tx.Exec(ctx, `UPDATE bot_flows SET is_active=true, updated_at=now() WHERE id=$1`, sellFlowID)
	}

	// 5. Seed Buy a Car questions with sequential chaining
	type qDef struct {
		id, field, text, qtype, val, allowed, errMsg, nextID string
		order                                                int
		req                                                  bool
	}

	buyQuestions := []qDef{
		{
			id:      "22222222-2222-2222-2222-222222222101",
			field:   "vehicle_type",
			text:    "What type of car are you looking for? (SUV, Sedan, Hatchback, MPV, Any)",
			qtype:   "select",
			allowed: `["SUV","Sedan","Hatchback","MPV","Any"]`,
			errMsg:  "Please choose one of: SUV, Sedan, Hatchback, MPV, or Any.",
			order:   1,
			req:     true,
			nextID:  "22222222-2222-2222-2222-222222222102",
		},
		{
			id:      "22222222-2222-2222-2222-222222222102",
			field:   "budget_max",
			text:    "What is your maximum budget in RM? (e.g. 50000)",
			qtype:   "number",
			val:     "min:0,max:99999999",
			allowed: "[]",
			errMsg:  "Please enter a valid budget amount.",
			order:   2,
			req:     true,
			nextID:  "22222222-2222-2222-2222-222222222103",
		},
		{
			id:      "22222222-2222-2222-2222-222222222103",
			field:   "brand",
			text:    "Which brand/make do you prefer? (e.g. Toyota, Honda, BMW, or Any)",
			qtype:   "text",
			allowed: "[]",
			errMsg:  "Please specify a preferred brand or Any.",
			order:   3,
			req:     true,
			nextID:  "22222222-2222-2222-2222-222222222104",
		},
		{
			id:      "22222222-2222-2222-2222-222222222104",
			field:   "model",
			text:    "Any specific model? (e.g. Civic, Vios, or Any)",
			qtype:   "text",
			allowed: "[]",
			errMsg:  "Please specify a model or Any.",
			order:   4,
			req:     true,
			nextID:  "22222222-2222-2222-2222-222222222105",
		},
		{
			id:      "22222222-2222-2222-2222-222222222105",
			field:   "fuel",
			text:    "What fuel type do you prefer? (Petrol, Diesel, CNG, Electric, Hybrid, Any)",
			qtype:   "select",
			allowed: `["Petrol","Diesel","CNG","Electric","Hybrid","Any"]`,
			errMsg:  "Please choose one of: Petrol, Diesel, CNG, Electric, Hybrid, or Any.",
			order:   5,
			req:     true,
			nextID:  "22222222-2222-2222-2222-222222222106",
		},
		{
			id:      "22222222-2222-2222-2222-222222222106",
			field:   "transmission",
			text:    "Transmission preference? (Manual, Automatic, Any)",
			qtype:   "select",
			allowed: `["Manual","Automatic","Any"]`,
			errMsg:  "Please choose Manual, Automatic, or Any.",
			order:   6,
			req:     true,
			nextID:  "22222222-2222-2222-2222-222222222107",
		},
		{
			id:      "22222222-2222-2222-2222-222222222107",
			field:   "year_min",
			text:    "Minimum manufacturing year? (e.g. 2018 or Any)",
			qtype:   "number",
			val:     "min:1995,max:2027",
			allowed: "[]",
			errMsg:  "Please enter a valid year between 1995 and 2027.",
			order:   7,
			req:     false,
			nextID:  "",
		},
	}

	for _, q := range buyQuestions {
		var next *string
		if q.nextID != "" {
			next = &q.nextID
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO bot_questions(id, flow_id, field_name, question_text, question_type, validation_rule, allowed_values, error_message, next_question_id, is_required, order_index, is_active)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11, true)
			ON CONFLICT (id) DO UPDATE
			SET field_name = EXCLUDED.field_name, question_text = EXCLUDED.question_text, question_type = EXCLUDED.question_type,
			    validation_rule = EXCLUDED.validation_rule, allowed_values = EXCLUDED.allowed_values,
			    error_message = EXCLUDED.error_message, next_question_id = EXCLUDED.next_question_id,
			    is_required = EXCLUDED.is_required, order_index = EXCLUDED.order_index, is_active = true, updated_at = now()
		`, q.id, buyFlowID, q.field, q.text, q.qtype, q.val, q.allowed, q.errMsg, next, q.req, q.order); err != nil {
			http.Error(w, `{"error":"buy question insert failed: `+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
	}

	// 6. Branch condition on Buy flow
	condID := "33333333-3333-3333-3333-333333333101"
	targetBrandID := "22222222-2222-2222-2222-222222222103"
	if _, err := tx.Exec(ctx, `
		INSERT INTO bot_conditions(id, question_id, field_name, operator, value, target_question_id, priority)
		VALUES ($1, $2, 'vehicle_type', 'eq', 'Any', $3, 1)
		ON CONFLICT (id) DO UPDATE
		SET operator = EXCLUDED.operator, value = EXCLUDED.value, target_question_id = EXCLUDED.target_question_id
	`, condID, buyQuestions[0].id, targetBrandID); err != nil {
		http.Error(w, `{"error":"condition insert failed: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	// 7. Seed Sell a Car questions
	sellQuestions := []qDef{
		{id: "22222222-2222-2222-2222-222222222201", field: "sell_brand", text: "What brand/make is your car? (e.g. Toyota, Honda, Proton)", qtype: "text", allowed: "[]", errMsg: "Please enter the car brand.", order: 1, req: true, nextID: "22222222-2222-2222-2222-222222222202"},
		{id: "22222222-2222-2222-2222-222222222202", field: "sell_model", text: "What is the car model? (e.g. Myvi, City, Vios)", qtype: "text", allowed: "[]", errMsg: "Please enter the car model.", order: 2, req: true, nextID: "22222222-2222-2222-2222-222222222203"},
		{id: "22222222-2222-2222-2222-222222222203", field: "sell_year", text: "Which year was it manufactured? (e.g. 2019)", qtype: "number", val: "min:1990,max:2027", allowed: "[]", errMsg: "Please enter a valid year.", order: 3, req: true, nextID: "22222222-2222-2222-2222-222222222204"},
		{id: "22222222-2222-2222-2222-222222222204", field: "sell_km", text: "What is the current mileage in kilometers? (e.g. 45000)", qtype: "number", val: "min:0,max:999999", allowed: "[]", errMsg: "Please enter the mileage in kilometers.", order: 4, req: true, nextID: "22222222-2222-2222-2222-222222222205"},
		{id: "22222222-2222-2222-2222-222222222205", field: "sell_fuel", text: "What fuel type does it use? (Petrol, Diesel, Hybrid, Electric)", qtype: "select", allowed: `["Petrol","Diesel","Hybrid","Electric"]`, errMsg: "Please select: Petrol, Diesel, Hybrid, or Electric.", order: 5, req: true, nextID: "22222222-2222-2222-2222-222222222206"},
		{id: "22222222-2222-2222-2222-222222222206", field: "sell_transmission", text: "What transmission is it? (Automatic, Manual)", qtype: "select", allowed: `["Automatic","Manual"]`, errMsg: "Please select Automatic or Manual.", order: 6, req: true, nextID: "22222222-2222-2222-2222-222222222207"},
		{id: "22222222-2222-2222-2222-222222222207", field: "sell_condition", text: "What is the overall condition? (Excellent, Good, Fair, Poor)", qtype: "select", allowed: `["Excellent","Good","Fair","Poor"]`, errMsg: "Please select: Excellent, Good, Fair, or Poor.", order: 7, req: true, nextID: "22222222-2222-2222-2222-222222222208"},
		{id: "22222222-2222-2222-2222-222222222208", field: "sell_location", text: "Where is the car located? (e.g. Kuala Lumpur, Petaling Jaya)", qtype: "text", allowed: "[]", errMsg: "Please enter the location of the car.", order: 8, req: true, nextID: ""},
	}

	for _, q := range sellQuestions {
		var next *string
		if q.nextID != "" {
			next = &q.nextID
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO bot_questions(id, flow_id, field_name, question_text, question_type, validation_rule, allowed_values, error_message, next_question_id, is_required, order_index, is_active)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11, true)
			ON CONFLICT (id) DO UPDATE
			SET field_name = EXCLUDED.field_name, question_text = EXCLUDED.question_text, question_type = EXCLUDED.question_type,
			    validation_rule = EXCLUDED.validation_rule, allowed_values = EXCLUDED.allowed_values,
			    error_message = EXCLUDED.error_message, next_question_id = EXCLUDED.next_question_id,
			    is_required = EXCLUDED.is_required, order_index = EXCLUDED.order_index, is_active = true, updated_at = now()
		`, q.id, sellFlowID, q.field, q.text, q.qtype, q.val, q.allowed, q.errMsg, next, q.req, q.order); err != nil {
			http.Error(w, `{"error":"sell question insert failed: `+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{"ok": true, "message": "Default bot configuration applied successfully"})
}
