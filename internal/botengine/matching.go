package botengine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// MatchResult represents a matched vehicle from stock.
type MatchResult struct {
	ID           string
	Make         string
	Model        string
	Year         int
	Price        int
	Fuel         string
	Transmission string
	KM           int
	Description  string
}

// MatchVehicles finds vehicles matching criteria from extracted_data.
func MatchVehicles(ctx context.Context, tx pgx.Tx, leadID string, data map[string]any) ([]MatchResult, error) {
	whereClauses := []string{"status = 'AVAILABLE'"}
	args := []any{}
	argIdx := 1

	// 1. vehicle_type
	if vt, ok := getString(data, "vehicle_type"); ok && vt != "" && !strings.EqualFold(vt, "any") {
		whereClauses = append(whereClauses, fmt.Sprintf("(model ILIKE $%d OR description ILIKE $%d)", argIdx, argIdx))
		args = append(args, "%"+vt+"%")
		argIdx++
	}

	// 2. budget_max
	if bmax, ok := getNumber(data, "budget_max"); ok && bmax > 0 {
		whereClauses = append(whereClauses, fmt.Sprintf("price <= $%d", argIdx))
		args = append(args, bmax)
		argIdx++
	}

	// 3. brand / make
	brand, hasBrand := getString(data, "brand")
	if !hasBrand {
		brand, hasBrand = getString(data, "make")
	}
	if hasBrand && brand != "" && !strings.EqualFold(brand, "any") {
		whereClauses = append(whereClauses, fmt.Sprintf("make ILIKE $%d", argIdx))
		args = append(args, "%"+brand+"%")
		argIdx++
	}

	// 4. model
	if mo, ok := getString(data, "model"); ok && mo != "" && !strings.EqualFold(mo, "any") {
		whereClauses = append(whereClauses, fmt.Sprintf("model ILIKE $%d", argIdx))
		args = append(args, "%"+mo+"%")
		argIdx++
	}

	// 5. fuel
	if f, ok := getString(data, "fuel"); ok && f != "" && !strings.EqualFold(f, "any") {
		whereClauses = append(whereClauses, fmt.Sprintf("fuel ILIKE $%d", argIdx))
		args = append(args, "%"+f+"%")
		argIdx++
	}

	// 6. transmission
	if tr, ok := getString(data, "transmission"); ok && tr != "" && !strings.EqualFold(tr, "any") {
		whereClauses = append(whereClauses, fmt.Sprintf("transmission ILIKE $%d", argIdx))
		args = append(args, "%"+tr+"%")
		argIdx++
	}

	// 7. year_min
	if ymin, ok := getNumber(data, "year_min"); ok && ymin > 0 {
		whereClauses = append(whereClauses, fmt.Sprintf("year >= $%d", argIdx))
		args = append(args, ymin)
		argIdx++
	}

	query := fmt.Sprintf(`SELECT id::text, make, model, year, price, fuel, transmission, km, description
		FROM vehicles
		WHERE %s
		ORDER BY CASE WHEN price >= 100000 THEN 0 ELSE 1 END, year DESC, price ASC
		LIMIT 5`, strings.Join(whereClauses, " AND "))

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query matching vehicles: %w", err)
	}
	defer rows.Close()

	var matches []MatchResult
	var matchIDs []string
	score := 100

	for rows.Next() {
		var m MatchResult
		if err := rows.Scan(&m.ID, &m.Make, &m.Model, &m.Year, &m.Price, &m.Fuel, &m.Transmission, &m.KM, &m.Description); err != nil {
			return nil, err
		}
		matches = append(matches, m)
		matchIDs = append(matchIDs, m.ID)
	}

	// Record matches into vehicle_matches table
	for i, m := range matches {
		s := score - (i * 10)
		_, _ = tx.Exec(ctx, `INSERT INTO vehicle_matches(lead_id, vehicle_id, score)
			VALUES ($1, $2, $3)
			ON CONFLICT (lead_id, vehicle_id) DO UPDATE SET score=EXCLUDED.score`, leadID, m.ID, s)
	}

	// Save match_ids to leads.extracted_data and schedule followup
	if len(matchIDs) > 0 && leadID != "" {
		idsJoined := strings.Join(matchIDs, ",")
		_, _ = tx.Exec(ctx, `UPDATE leads
			SET extracted_data = extracted_data || jsonb_build_object('match_ids', $1::text),
			    updated_at = now()
			WHERE id = $2`, idsJoined, leadID)

		_, _ = tx.Exec(ctx, `INSERT INTO followups(customer_id, lead_id, type, scheduled_at, message)
			SELECT customer_id, id, 'post_match', now() + interval '24 hours', 'Follow up on matched cars'
			FROM leads WHERE id = $1
			ON CONFLICT DO NOTHING`, leadID)
	}

	return matches, nil
}

// FormatMatchesWithHeader formats the match list with an optional custom header and clean action footer.
func FormatMatchesWithHeader(matches []MatchResult, customHeader string) string {
	if len(matches) == 0 {
		return "Sorry, we don't have any vehicles matching your criteria right now. Our team will follow up if new stock arrives."
	}

	var sb strings.Builder
	if customHeader != "" {
		sb.WriteString(strings.TrimSpace(customHeader))
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("🚗 *Here are vehicles matching your preferences:*\n\n")
	}

	icons := []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣"}
	for i, m := range matches {
		icon := fmt.Sprintf("%d.", i+1)
		if i < len(icons) {
			icon = icons[i]
		}
		priceStr := formatPrice(m.Price)
		sb.WriteString(fmt.Sprintf("%s *%d %s %s* — ₹%s\n", icon, m.Year, m.Make, m.Model, priceStr))
		details := []string{}
		if m.Fuel != "" {
			details = append(details, m.Fuel)
		}
		if m.Transmission != "" {
			details = append(details, m.Transmission)
		}
		if m.KM > 0 {
			details = append(details, fmt.Sprintf("%d km", m.KM))
		}
		if len(details) > 0 {
			sb.WriteString(fmt.Sprintf("   (%s)\n", strings.Join(details, ", ")))
		}
	}

	sb.WriteString("\n👉 Reply with vehicle number for details\n👉 Reply *TEST DRIVE* to schedule a test drive\n👉 Reply *FINANCE* for loan options")
	return sb.String()
}

// FormatMatches formats the match list into a user-friendly WhatsApp response.
func FormatMatches(matches []MatchResult) string {
	return FormatMatchesWithHeader(matches, "")
}

func formatPrice(price int) string {
	s := strconv.Itoa(price)
	n := len(s)
	if n <= 3 {
		return s
	}
	// Indian number formatting: last 3 digits, then groups of 2
	last3 := s[n-3:]
	rest := s[:n-3]
	var parts []string
	for len(rest) > 2 {
		parts = append([]string{rest[len(rest)-2:]}, parts...)
		rest = rest[:len(rest)-2]
	}
	if len(rest) > 0 {
		parts = append([]string{rest}, parts...)
	}
	parts = append(parts, last3)
	return strings.Join(parts, ",")
}

func getString(data map[string]any, key string) (string, bool) {
	if data == nil {
		return "", false
	}
	v, ok := data[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v), true
	}
	return strings.TrimSpace(s), true
}

func getNumber(data map[string]any, key string) (int64, bool) {
	if data == nil {
		return 0, false
	}
	v, ok := data[key]
	if !ok || v == nil {
		return 0, false
	}
	switch val := v.(type) {
	case int:
		return int64(val), true
	case int64:
		return val, true
	case float64:
		return int64(val), true
	case string:
		clean := strings.ReplaceAll(val, ",", "")
		clean = strings.ReplaceAll(clean, " ", "")
		num, err := strconv.ParseInt(clean, 10, 64)
		if err == nil {
			return num, true
		}
	}
	return 0, false
}
