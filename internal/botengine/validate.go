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

	switch qType {
	case "text":
		if validationRule != "" {
			re, err := regexp.Compile(validationRule)
			if err != nil {
				// If admin wrote an invalid regex, fallback to allowing non-empty
				return trimmed, true
			}
			if !re.MatchString(trimmed) {
				return "", false
			}
		}
		return trimmed, true

	case "number":
		clean := strings.ReplaceAll(trimmed, ",", "")
		clean = strings.ReplaceAll(clean, " ", "")
		num, err := strconv.ParseInt(clean, 10, 64)
		if err != nil {
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
		// Case-insensitive match against allowed values
		lowerVal := strings.ToLower(trimmed)
		for _, av := range allowedValues {
			if strings.ToLower(strings.TrimSpace(av)) == lowerVal {
				return av, true
			}
		}
		return "", false

	case "boolean":
		lower := strings.ToLower(trimmed)
		switch lower {
		case "yes", "y", "true", "1", "ya", "ha":
			return "yes", true
		case "no", "n", "false", "0", "nah", "na":
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
		// Default to plain non-empty text
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
