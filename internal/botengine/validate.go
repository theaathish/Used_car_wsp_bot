package botengine

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	digitsOnly = regexp.MustCompile(`[^\d]`)
)

// Validate checks and normalizes customer input based on question type and rules.
func Validate(value string, questionType string, validationRule string, allowedValues []string, isRequired bool) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if !isRequired {
			return "", true
		}
		return "", false
	}

	qType := strings.ToLower(strings.TrimSpace(questionType))

	// Universal conversational bypass: "any", "skip", "no preference", etc. are always valid
	if isAnyPhrase(trimmed) {
		switch qType {
		case "select":
			for _, av := range allowedValues {
				if strings.EqualFold(av, "any") {
					return av, true
				}
			}
			return "Any", true
		case "number":
			return "0", true
		default:
			return "Any", true
		}
	}

	switch qType {
	case "text":
		if validationRule != "" {
			re, err := regexp.Compile(validationRule)
			if err != nil {
				return trimmed, true
			}
			if !re.MatchString(trimmed) {
				return "", false
			}
		}
		return trimmed, true

	case "number":
		// Handle year ranges e.g. "2019 to 2026" or "2019-2026"
		if y1, _, ok := parseRangeOrYear(trimmed); ok {
			if validationRule != "" && !checkNumberBounds(int64(y1), validationRule) {
				return "", false
			}
			return strconv.Itoa(y1), true
		}

		// Handle budget formats e.g. "15 lakh", "50k", "150000"
		if bmin, bmax := parseNLPBudget(strings.ToLower(trimmed)); bmax > 0 {
			target := bmax
			if bmin > 0 {
				target = bmin
			}
			if validationRule != "" && !checkNumberBounds(int64(target), validationRule) {
				return "", false
			}
			return strconv.Itoa(target), true
		}

		clean := strings.ReplaceAll(trimmed, ",", "")
		clean = strings.ReplaceAll(clean, " ", "")
		num, err := strconv.ParseInt(clean, 10, 64)
		if err != nil {
			// Extract any first contiguous digits
			nums := rxDigits.FindAllString(clean, -1)
			if len(nums) > 0 {
				if n, err2 := strconv.ParseInt(nums[0], 10, 64); err2 == nil {
					if validationRule != "" && !checkNumberBounds(n, validationRule) {
						return "", false
					}
					return strconv.FormatInt(n, 10), true
				}
			}
			return "", false
		}
		if validationRule != "" {
			if !checkNumberBounds(num, validationRule) {
				return "", false
			}
		}
		return strconv.FormatInt(num, 10), true

	case "select":
		// Check numeric index: "1" corresponds to allowedValues[0]
		if idx, err := strconv.Atoi(trimmed); err == nil && idx >= 1 && idx <= len(allowedValues) {
			return allowedValues[idx-1], true
		}
		lowerVal := strings.ToLower(trimmed)
		// 1. Exact case-insensitive match against allowed values
		for _, av := range allowedValues {
			if strings.ToLower(strings.TrimSpace(av)) == lowerVal {
				return av, true
			}
		}
		// 2. Substring match (e.g. user typed "i prefer SUV" or "sedan please")
		for _, av := range allowedValues {
			avClean := strings.ToLower(strings.TrimSpace(av))
			if avClean != "" && avClean != "any" && strings.Contains(lowerVal, avClean) {
				return av, true
			}
		}
		// 3. Fallback: if "Any" is an allowed value, check if user said anything permissive
		for _, av := range allowedValues {
			if strings.EqualFold(av, "any") && (strings.Contains(lowerVal, "any") || strings.Contains(lowerVal, "all") || strings.Contains(lowerVal, "dont care")) {
				return av, true
			}
		}
		return "", false

	case "boolean":
		lower := strings.ToLower(trimmed)
		switch lower {
		case "yes", "y", "true", "1", "ya", "ha", "sure", "ok", "okay":
			return "yes", true
		case "no", "n", "false", "0", "nah", "na", "nope":
			return "no", true
		default:
			return "", false
		}

	case "phone":
		digits := digitsOnly.ReplaceAllString(trimmed, "")
		if len(digits) >= 7 && len(digits) <= 15 {
			return digits, true
		}
		return "", false

	case "email":
		if emailRegex.MatchString(trimmed) {
			return strings.ToLower(trimmed), true
		}
		return "", false

	default:
		return trimmed, true
	}
}

// checkNumberBounds parses rules like "min:0,max:999999" or "min:10"
func checkNumberBounds(val int64, rule string) bool {
	parts := strings.Split(rule, ",")
	for _, p := range parts {
		kv := strings.SplitN(strings.TrimSpace(p), ":", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(kv[0]))
		vStr := strings.TrimSpace(kv[1])
		limit, err := strconv.ParseInt(vStr, 10, 64)
		if err != nil {
			continue
		}
		if k == "min" && val < limit {
			return false
		}
		if k == "max" && val > limit {
			return false
		}
	}
	return true
}

// parseRangeOrYear checks if string contains a year range e.g. "2019 to 2026" or "2019-2026" or "2020"
func parseRangeOrYear(s string) (int, int, bool) {
	y1, y2 := extractYearRange(s)
	if y1 > 0 {
		return y1, y2, true
	}
	return 0, 0, false
}
