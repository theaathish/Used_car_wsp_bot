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
		IsActive     *bool   `json:"is_active"`
	}
	if err := readJSON(r, &in); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	if in.ResponseText != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_responses SET response_text=$1, updated_at=now() WHERE id=$2`, *in.ResponseText, id)
	}
	if in.IsActive != nil {
		_, _ = s.Pool.Exec(ctx, `UPDATE bot_responses SET is_active=$1, updated_at=now() WHERE id=$2`, *in.IsActive, id)
	}

	writeJSON(w, map[string]any{"ok": true, "id": id})
}
