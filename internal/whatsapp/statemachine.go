package whatsapp

// Deterministic rule-based state machine. No AI.
// INPUT -> RULE -> STATE CHANGE -> DATABASE -> WHATSAPP RESPONSE.

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var numRe = regexp.MustCompile(`\d+`)

var fuels = []string{"petrol", "diesel", "cng", "electric", "hybrid"}
var transmissions = []string{"automatic", "manual", "amt", "cvt", "dct"}

// maxModelYear caps year input at next calendar year (BUY-107).
func maxModelYear() int { return time.Now().Year() + 1 }

// ParseBudget extracts min/max rupees from free text like "5 lakh", "5l",
// "500000", "3-6 lakh", "₹15,00,000". Returns 0,0 when absent or invalid
// (zero/negative) so callers reprompt instead of storing garbage.
func ParseBudget(s string) (int, int) {
	t := strings.ToLower(s)
	if regexp.MustCompile(`(^|\s)-\s*\d`).MatchString(t) {
		return 0, 0 // BUY-106 negative
	}
	// Normalize currency first: drop symbols and grouping commas so Indian
	// grouping ("15,00,000") parses as one number (BUY-104).
	t = strings.ReplaceAll(t, "₹", " ")
	t = strings.ReplaceAll(t, "rs.", " ")
	t = strings.ReplaceAll(t, "rs", " ")
	t = strings.ReplaceAll(t, ",", "")
	mult := 1
	if strings.Contains(t, "lakh") || strings.Contains(t, " lac") || strings.Contains(t, "lac") {
		mult = 100000
	} else if strings.Contains(t, "k") && !strings.Contains(t, "km") {
		mult = 1000
	}
	// "5l" shorthand: number directly followed by l (but not km handled above)
	shortL := regexp.MustCompile(`(\d+)\s*l\b`)
	if mult == 1 {
		if m := shortL.FindStringSubmatch(t); m != nil {
			if v, err := strconv.Atoi(m[1]); err == nil {
				return v * 80000, v * 100000
			}
		}
	}
	nums := numRe.FindAllString(t, -1)
	vals := []int{}
	for _, n := range nums {
		v, err := strconv.Atoi(n)
		if err != nil {
			continue
		}
		vals = append(vals, v*mult)
	}
	if len(vals) == 0 {
		return 0, 0
	}
	if len(vals) == 1 {
		v := vals[0]
		if mult == 1 && v < 1000 {
			return 0, 0 // bare small number ("X1", "5") is not a budget
		}
		return v * 8 / 10, v
	}
	a, b := vals[0], vals[1]
	if a > b {
		a, b = b, a
	}
	if mult == 1 && b < 1000 {
		return 0, 0 // unit-less small numbers ("1=1", "5") are not budgets
	}
	return a, b
}

func norm(s string) string { return strings.TrimSpace(strings.ToLower(s)) }

func hasBudget(s string) bool {
	_, mx := ParseBudget(s)
	return mx > 0
}

func unknownBudget(s string) bool {
	t := norm(s)
	for _, p := range []string{"don't know", "dont know", "not sure", "no idea", "any", "flexible", "skip"} {
		if strings.Contains(t, p) {
			return true
		}
	}
	return false
}

func findFuel(s string) string {
	t := norm(s)
	for _, f := range fuels {
		if strings.Contains(t, f) {
			return strings.ToUpper(f)
		}
	}
	return ""
}

func findTrans(s string) string {
	t := norm(s)
	for _, tr := range transmissions {
		if strings.Contains(t, tr) {
			return strings.ToUpper(tr)
		}
	}
	return ""
}

// ExtractAll pulls every structured field present in one message (BUY-009).
func ExtractAll(body string) map[string]string {
	out := map[string]string{}
	if mn, mx := ParseBudget(body); mx > 0 {
		out["budget_min"] = strconv.Itoa(mn)
		out["budget_max"] = strconv.Itoa(mx)
	}
	if f := findFuel(body); f != "" {
		out["fuel"] = f
	}
	if tr := findTrans(body); tr != "" {
		out["transmission"] = tr
	}
	if y := numRe.FindAllString(body, -1); len(y) > 0 {
		for _, c := range y {
			c = strings.ReplaceAll(c, ",", "")
			if v, err := strconv.Atoi(c); err == nil && v >= 1995 && v <= maxModelYear() {
				// avoid swallowing a budget number already used as lone year
				if _, mx := ParseBudget(body); !(mx == v || mx == v*100000) {
					out["year_min"] = strconv.Itoa(v)
					break
				} else if len(y) > 1 {
					out["year_min"] = strconv.Itoa(v)
					break
				}
			}
		}
	}
	return out
}

// SplitBrandModel splits "BMW X1" -> ("BMW","X1"). Single word -> brand only.
func SplitBrandModel(body string) (string, string) {
	parts := strings.Fields(strings.TrimSpace(body))
	if len(parts) == 0 {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}

// Tolerant intent routing (§5): natural phrasing, Tanglish/Hinglish mix.
func wantsBuy(b string) bool {
	if strings.Contains(b, "buy") {
		return true
	}
	for _, p := range []string{"i need a car", "need a car", "looking for", "need car", "venum", "chahiye", "i want a car", "want to buy", "purchase"} {
		if strings.Contains(b, p) {
			return true
		}
	}
	return false
}

func wantsSell(b string) bool { return strings.Contains(b, "sell") }

func wantsExchange(b string) bool {
	return strings.Contains(b, "exchange") || strings.Contains(b, "replace")
}

// detectIntentSwitch reports when a mid-flow message explicitly starts a
// different intent (P0-13). Returns the new intent or "".
func detectIntentSwitch(body, currentIntent string) string {
	b := norm(body)
	switch {
	case wantsExchange(b) && currentIntent != "EXCHANGE":
		return "EXCHANGE"
	case wantsSell(b) && !strings.Contains(b, "exchange") && currentIntent != "SELL":
		return "SELL"
	case wantsBuy(b) && currentIntent != "BUY" && currentIntent != "UNKNOWN" && currentIntent != "":
		return "BUY"
	default:
		return ""
	}
}
// after stripping intent words, fuels, transmissions and budget phrases.
func extractBrandModel(body string) (string, string) {
	t := norm(body)
	t = strings.Replace(t, "i want to buy", " ", 1)
	t = strings.Replace(t, "want to buy", " ", 1)
	t = strings.Replace(t, "buy", " ", 1)
	t = strings.Replace(t, "a car", " ", 1)
	for _, f := range fuels {
		t = strings.ReplaceAll(t, f, " ")
	}
	for _, tr := range transmissions {
		t = strings.ReplaceAll(t, tr, " ")
	}
	t = regexp.MustCompile(`under\s+[\d,]+\s*(lakh|lac|l|k)?`).ReplaceAllString(t, " ")
	t = regexp.MustCompile(`[\d,]+\s*(lakh|lac)`).ReplaceAllString(t, " ")
	t = regexp.MustCompile(`\b\d{4}\b`).ReplaceAllString(t, " ") // years aren't brand/model
	stop := map[string]bool{"car": true, "any": true, "with": true, "and": true, "or": true, "in": true, "under": true, "below": true, "around": true,
		"bro": true, "hey": true, "hello": true, "hi": true, "please": true, "da": true, "machi": true, "macha": true, "anna": true, "sir": true, "madam": true, "ji": true,
		"venum": true, "chahiye": true, "want": true, "need": true, "looking": true, "purchase": true, "gaadi": true, "vandi": true, "for": true, "me": true, "a": true}
	words := []string{}
	for _, wd := range strings.Fields(t) {
		if stop[wd] {
			continue
		}
		if !strings.ContainsAny(wd, "abcdefghijklmnopqrstuvwxyz0123456789") {
			continue // emoji/symbols are never brand or model
		}
		words = append(words, wd)
	}
	if len(words) == 0 {
		return "", ""
	}
	// restore original casing from body
	orig := strings.Fields(body)
	pick := func(want string) string {
		for _, o := range orig {
			if strings.EqualFold(o, want) {
				return o
			}
		}
		return want
	}
	if len(words) == 1 {
		return pick(words[0]), ""
	}
	return pick(words[0]), pick(words[1])
}

// stripKnown removes fuel/transmission/budget/year tokens, leaving the
// likely model/brand remainder for out-of-order messages (INT-004).
func stripKnown(body string) string {
	known := map[string]bool{}
	for _, f := range fuels {
		known[f] = true
	}
	for _, tr := range transmissions {
		known[tr] = true
	}
	for _, s := range []string{"car", "any", "no", "under", "below", "around", "with", "and", "or", "the", "a", "an", "my", "budget", "is", "of", "lakh", "lac", "l", "k", "rs"} {
		known[s] = true
	}
	kept := []string{}
	for _, o := range strings.Fields(body) {
		lw := strings.ToLower(strings.Trim(o, ".,!?₹"))
		if lw == "" || known[lw] {
			continue
		}
		if regexp.MustCompile(`^\d+$`).MatchString(lw) {
			continue // pure numbers are budget/year, not model names
		}
		if !strings.ContainsAny(lw, "abcdefghijklmnopqrstuvwxyz0123456789") {
			continue // emoji/symbols are never model names
		}
		kept = append(kept, o)
	}
	return strings.Join(kept, " ")
}
func nextMissingBuy(data map[string]string) string {
	if data["budget_max"] == "" && data["budget_unknown"] == "" {
		return "BUY_BUDGET"
	}
	if data["brand"] == "" {
		return "BUY_BRAND"
	}
	if data["model"] == "" {
		return "BUY_MODEL"
	}
	if data["fuel"] == "" {
		return "BUY_FUEL"
	}
	if data["transmission"] == "" {
		return "BUY_TRANS"
	}
	if data["year_min"] == "" {
		return "BUY_YEAR"
	}
	return "BUY_RESULTS"
}

func promptFor(state string) string {
	switch state {
	case "BUY_BUDGET":
		return "What's your budget? (e.g. 3-5 lakh, or reply *don't know*)"
	case "BUY_BRAND":
		return "Which brand? (e.g. Maruti, Hyundai, BMW, or *any*)"
	case "BUY_MODEL":
		return "Which model? (e.g. Swift, Creta, X1, or *any*)"
	case "BUY_FUEL":
		return "Fuel preference? *Petrol / Diesel / CNG / Electric / Any*"
	case "BUY_TRANS":
		return "Transmission? *Manual / Automatic / Any*"
	case "BUY_YEAR":
		return "Minimum model year? (e.g. 2018, or *any*)"
	default:
		return "Thanks! Let me find matching cars for you..."
	}
}

// Next advances the conversation. Returns nextState, reply, intent, leadStatus, dataPatch.
// patch["interest"] carries INTERESTED/THINKING/NOT_INTERESTED when set.
// Unknown states recover to the menu (STATE-004) — Next never returns invalid.
func Next(state, body string, data map[string]string) (string, string, string, string, map[string]string) {
	if data == nil {
		data = map[string]string{}
	}
	patch := map[string]string{}
	if !validStates[state] {
		return "ASK_INTENT", "Something got mixed up — let's start fresh. Are you looking to *BUY*, *SELL* or *EXCHANGE* a car?", "", "CONTACTED", patch
	}
	b := norm(body)

	// Global commands valid in any post-match state.
	switch {
	case strings.Contains(b, "more") && strings.Contains(b, "car"):
		patch["page"] = "next"
		return "BUY_RESULTS", "Showing more cars for you...", "", "", patch
	case strings.Contains(b, "finance") || strings.Contains(b, "loan") || strings.Contains(b, "emi"):
		return "FINANCE_INFO", "We offer loan assistance through partner banks. Reply with: loan amount, tenure (months), employment type and monthly income — e.g. *5 lakh, 60 months, salaried, 80000*. Our finance team will call you. (No payment is taken on WhatsApp.)", "", "FOLLOWUP", patch
	case strings.Contains(b, "not interested") || strings.Contains(b, "not intrested") || strings.Contains(b, "no thanks") || strings.Contains(b, "drop"):
		patch["interest"] = "NOT_INTERESTED"
		return "DONE", "No problem! We'll not follow up aggressively. Reply *BUY*, *SELL* or *EXCHANGE* anytime.", "", "LOST", patch
	case strings.Contains(b, "think") || strings.Contains(b, "decide") || strings.Contains(b, "later") || strings.Contains(b, "call me back"):
		patch["interest"] = "THINKING"
		return state, "Sure, take your time! When should we follow up — tomorrow or next week?", "", "FOLLOWUP", patch
	case strings.Contains(b, "interested") || strings.Contains(b, "intrested") || strings.Contains(b, "i like") || strings.Contains(b, "book") && strings.Contains(b, "test") == false && state == "BUY_RESULTS":
		patch["interest"] = "INTERESTED"
		return state, "Great! Our salesperson will call you shortly. You can also ask for a *test drive* with date/time.", "", "QUALIFIED", patch
	case strings.Contains(b, "test") && strings.Contains(b, "drive"):
		return "TESTDRIVE_ASK", "To book a test drive, reply with vehicle + preferred date/time — e.g. *Swift, tomorrow 10am*. Our team will confirm the slot.", "", "TEST_DRIVE", patch
	}

	switch state {
	case "NEW":
		// First message with a clear intent skips the menu.
		if wantsBuy(b) || wantsSell(b) || wantsExchange(b) {
			return Next("ASK_INTENT", body, data)
		}
		return "ASK_INTENT", "Welcome to AutoKart! Are you looking to *BUY*, *SELL* or *EXCHANGE* a car?", "", "CONTACTED", patch
	case "ASK_INTENT":
		switch {
		case wantsBuy(b):
			for k, v := range ExtractAll(body) {
				patch[k] = v
			}
			if br, mo := extractBrandModel(body); br != "" {
				patch["brand"] = br
				if mo != "" {
					patch["model"] = mo
				}
			}
			merged := merge(data, patch)
			nxt := nextMissingBuy(merged)
			if nxt == "BUY_RESULTS" {
				return nxt, "Thanks! Let me find matching cars for you...", "BUY", "QUALIFIED", patch
			}
			return nxt, "Great, you want to *BUY*! "+promptFor(nxt), "BUY", "QUALIFIED", patch
		case strings.Contains(b, "sell") && !strings.Contains(b, "exchange"):
			return "SELL_CAR", "Sure! Which car do you want to sell? (brand + model, e.g. *Swift VDI*)", "SELL", "QUALIFIED", patch
		case strings.Contains(b, "exchange") || strings.Contains(b, "replace"):
			return "EXCHANGE_CURRENT", "Got it. Which is your current car? (brand, model, year, km — e.g. *Alto 2016, 60000km*)", "EXCHANGE", "QUALIFIED", patch
		default:
			return "ASK_INTENT", "Please reply with *BUY*, *SELL* or *EXCHANGE*.", "", "CONTACTED", patch
		}
	case "BUY_BUDGET":
		if unknownBudget(body) {
			patch["budget_unknown"] = "1"
			return "BUY_BRAND", "No worries! Our sales team can help with budget. Meanwhile — which brand do you prefer? (or *any*)", "BUY", "QUALIFIED", patch
		}
		// multi-field single message (BUY-009) + interruption-safe (INT-001):
		// bank every structured hint; a short lettered message with no
		// budget fills the first missing slot (brand, then model).
		for k, v := range ExtractAll(body) {
			patch[k] = v
		}
		if br, mo := extractBrandModel(body); br != "" && !hasBudget(br) && strings.ContainsAny(br, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") && len(strings.Fields(body)) <= 4 && !strings.HasSuffix(strings.TrimSpace(body), "?") {
			switch {
			case data["brand"] == "" && patch["brand"] == "":
				patch["brand"] = br
				if mo != "" && data["model"] == "" && patch["model"] == "" {
					patch["model"] = mo
				}
			case data["model"] == "" && patch["model"] == "":
				patch["model"] = strings.TrimSpace(br + " " + mo)
			}
		}
		if _, mx := ParseBudget(body); mx == 0 {
			if len(patch) > 0 {
				merged := merge(data, patch)
				nxt := nextMissingBuy(merged)
				return nxt, "Got it — I'll come back to budget later. "+promptFor(nxt), "BUY", "QUALIFIED", patch
			}
			return "BUY_BUDGET", "I didn't catch the budget. Try e.g. *4 lakh*, *3-5 lakh*, or reply *don't know*.", "BUY", "QUALIFIED", patch
		}
		merged := merge(data, patch)
		nxt := nextMissingBuy(merged)
		if nxt == "BUY_BUDGET" {
			return nxt, promptFor(nxt), "BUY", "QUALIFIED", patch
		}
		if nxt == "BUY_RESULTS" {
			return nxt, "Thanks! Let me find matching cars for you...", "BUY", "QUALIFIED", patch
		}
		return nxt, "Noted budget. "+promptFor(nxt), "BUY", "QUALIFIED", patch
	case "BUY_BRAND":
		if b == "" || b == "any" || b == "no" {
			patch["brand"] = "ANY"
		} else if br, mo := SplitBrandModel(body); mo != "" {
			patch["brand"] = br // "BMW X1" in brand step fills both
			patch["model"] = mo
		} else {
			patch["brand"] = strings.TrimSpace(body)
		}
		merged := merge(data, patch)
		if merged["model"] != "" {
			if merged["fuel"] == "" {
				return "BUY_FUEL", "Noted. "+promptFor("BUY_FUEL"), "BUY", "QUALIFIED", patch
			}
		}
		nxt := nextMissingBuy(merged)
		if nxt == "BUY_RESULTS" {
			return nxt, "Thanks! Let me find matching cars for you...", "BUY", "QUALIFIED", patch
		}
		return nxt, promptFor(nxt), "BUY", "QUALIFIED", patch
	case "BUY_MODEL":
		// Out-of-order answers (INT-004): bank fuel/trans/budget/year hints
		// first; only the remainder counts as the model.
		for k, v := range ExtractAll(body) {
			patch[k] = v
		}
		if b == "" || b == "any" || b == "no" {
			patch["model"] = "ANY"
		} else if rem := stripKnown(body); rem != "" {
			patch["model"] = rem
		} else {
			return "BUY_MODEL", "Saved that. Which model exactly? (e.g. Swift, Creta, X1, or *any*)", "BUY", "QUALIFIED", patch
		}
		merged := merge(data, patch)
		nxt := nextMissingBuy(merged)
		if nxt == "BUY_RESULTS" {
			return nxt, "Thanks! Let me find matching cars for you...", "BUY", "QUALIFIED", patch
		}
		return nxt, "Noted. "+promptFor(nxt), "BUY", "QUALIFIED", patch
	case "BUY_FUEL":
		for k, v := range ExtractAll(body) {
			if k != "fuel" {
				patch[k] = v
			}
		}
		if f := findFuel(body); f != "" {
			patch["fuel"] = f
		} else if strings.Contains(b, "any") || b == "" || b == "no" || b == "skip" {
			patch["fuel"] = "ANY"
		} else if findTrans(body) != "" || hasBudget(body) {
			return "BUY_FUEL", "Saved that. Still need fuel — *Petrol / Diesel / CNG / Electric / Any*?", "BUY", "QUALIFIED", patch
		} else if !strings.Contains(b, "any") && b != "" {
			patch["fuel"] = strings.TrimSpace(body)
		}
		merged := merge(data, patch)
		nxt := nextMissingBuy(merged)
		if nxt == "BUY_RESULTS" {
			return nxt, "Thanks! Let me find matching cars for you...", "BUY", "QUALIFIED", patch
		}
		return nxt, promptFor(nxt), "BUY", "QUALIFIED", patch
	case "BUY_TRANS":
		for k, v := range ExtractAll(body) {
			if k != "transmission" {
				patch[k] = v
			}
		}
		if tr := findTrans(body); tr != "" {
			patch["transmission"] = tr
		} else if strings.Contains(b, "any") || b == "" || b == "no" || b == "skip" {
			patch["transmission"] = "ANY"
		} else if findFuel(body) != "" || hasBudget(body) {
			return "BUY_TRANS", "Saved that. Still need transmission — *Manual / Automatic / Any*?", "BUY", "QUALIFIED", patch
		} else if !strings.Contains(b, "any") && b != "" {
			patch["transmission"] = strings.TrimSpace(body)
		}
		merged := merge(data, patch)
		nxt := nextMissingBuy(merged)
		if nxt == "BUY_RESULTS" {
			return nxt, "Thanks! Let me find matching cars for you...", "BUY", "QUALIFIED", patch
		}
		return nxt, promptFor(nxt), "BUY", "QUALIFIED", patch
	case "BUY_YEAR":
		if y := numRe.FindString(b); y != "" && !strings.Contains(b, "any") {
			y = strings.ReplaceAll(y, ",", "")
			if v, err := strconv.Atoi(y); err == nil && v >= 1995 && v <= maxModelYear() {
				patch["year_min"] = strconv.Itoa(v)
			}
		}
		return "BUY_RESULTS", "Thanks! Let me find matching cars for you...", "BUY", "QUALIFIED", patch
	case "BUY_RESULTS":
		return "BUY_RESULTS", "Reply *more cars* for additional options, *test drive* to book, *finance* for loan help, or *interested* and our team will call you.", "BUY", "QUALIFIED", patch
	case "FINANCE_INFO":
		patch["finance_raw"] = strings.TrimSpace(body)
		return "DONE", "Thanks! Your finance enquiry is recorded. Our finance team will contact you with EMI options.", "", "FOLLOWUP", patch
	case "TESTDRIVE_ASK":
		patch["testdrive_raw"] = strings.TrimSpace(body)
		return "DONE", "Thanks! Your test drive request is recorded. We'll confirm the slot shortly.", "", "TEST_DRIVE", patch
	case "SELL_CAR":
		br, mo := SplitBrandModel(body)
		patch["sell_brand"] = br
		patch["sell_model"] = mo
		return "SELL_DETAILS", "Noted. Now share: manufacturing year, registration number and km driven — e.g. *2018, MH12AB1234, 55000km*", "SELL", "QUALIFIED", patch
	case "SELL_YEAR": // backwards compat -> SELL_DETAILS
		patch["sell_year_km"] = strings.TrimSpace(body)
		return "SELL_SPECS", "Got it. Now share: fuel, transmission, condition and location — e.g. *Diesel, Manual, Good, Pune*", "SELL", "QUALIFIED", patch
	case "SELL_DETAILS":
		patch["sell_year_km"] = strings.TrimSpace(body)
		if reg := regexp.MustCompile(`[A-Z]{2}\s?\d{1,2}\s?[A-Z]{1,3}\s?\d{3,4}`).FindString(strings.ToUpper(body)); reg != "" {
			patch["sell_reg"] = reg
		}
		for _, c := range numRe.FindAllString(body, -1) {
			clean := strings.ReplaceAll(c, ",", "")
			if v, err := strconv.Atoi(clean); err == nil && v >= 1995 && v <= maxModelYear() {
				patch["sell_year"] = strconv.Itoa(v)
				break
			}
		}
		if km := regexp.MustCompile(`([\d,]+)\s?km`).FindStringSubmatch(strings.ToLower(body)); len(km) == 2 {
			raw := strings.ReplaceAll(km[1], ",", "")
			if v, err := strconv.Atoi(raw); err == nil {
				switch {
				case v < 0:
					return "SELL_DETAILS", "KM can't be negative. Please resend year, registration and km — e.g. *2018, MH12AB1234, 55000km*", "SELL", "QUALIFIED", patch
				case v > 2000000:
					patch["sell_km_flag"] = "unrealistic"
				default:
					patch["sell_km"] = strconv.Itoa(v)
				}
			}
		}
		merged := merge(data, patch)
		if merged["sell_year"] == "" && merged["sell_km"] == "" {
			return "SELL_DETAILS", "I need at least the manufacturing year and km. Please resend — e.g. *2018, MH12AB1234, 55000km*", "SELL", "QUALIFIED", patch
		}
		return "SELL_SPECS", "Got it. Now share: fuel, transmission, condition and location — e.g. *Diesel, Manual, Good, Pune*", "SELL", "QUALIFIED", patch
	case "SELL_SPECS":
		if f := findFuel(body); f != "" {
			patch["sell_fuel"] = f
		}
		if tr := findTrans(body); tr != "" {
			patch["sell_trans"] = tr
		}
		patch["sell_specs"] = strings.TrimSpace(body)
		if parts := strings.Split(body, ","); len(parts) >= 2 {
			if loc := strings.TrimSpace(parts[len(parts)-1]); loc != "" {
				patch["sell_location"] = loc
			}
		}
		return "SELL_PHOTOS", "Please send car photos here on WhatsApp (front, rear, side, interior, dashboard, tyres). Reply *DONE* after sending.", "SELL", "QUALIFIED", patch
	case "SELL_PHOTOS":
		if strings.Contains(b, "done") {
			return "DONE", "Thank you! Your sell request is recorded with status VALUATION_PENDING. Our team will call you for free inspection & valuation.", "SELL", "FOLLOWUP", patch
		}
		if n, err := strconv.Atoi(data["sell_photos"]); err == nil && n >= 10 {
			return "SELL_PHOTOS", "10 photos are enough, thank you! Reply *DONE* and we'll proceed to valuation.", "SELL", "QUALIFIED", patch
		}
		if v, ok := data["sell_photos"]; ok && v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				patch["sell_photos"] = strconv.Itoa(n + 1)
			}
		} else {
			patch["sell_photos"] = "1"
		}
		return "SELL_PHOTOS", "Photo noted (reply *DONE* when finished, or keep sending).", "SELL", "QUALIFIED", patch
	case "EXCHANGE_CURRENT":
		patch["exchange_current"] = strings.TrimSpace(body)
		return "EXCHANGE_WANT", "What new car are you looking for? (budget + brand, e.g. Creta under 10 lakh)", "EXCHANGE", "QUALIFIED", patch
	case "EXCHANGE_WANT":
		patch["exchange_want"] = strings.TrimSpace(body)
		for k, v := range ExtractAll(body) {
			patch[k] = v
		}
		return "DONE", "Thanks! We'll arrange valuation of your old car + show matching cars. Our team will call you.", "EXCHANGE", "FOLLOWUP", patch
	default:
		if strings.Contains(b, "hi") || strings.Contains(b, "hello") || strings.Contains(b, "hey") {
			return state, "Hello! How can I help — *BUY*, *SELL* or *EXCHANGE*?", "", "", patch
		}
		return state, "Noted. Our sales team will follow up. Reply *BUY*, *SELL* or *EXCHANGE* to continue.", "", "", patch
	}
}

func merge(a, b map[string]string) map[string]string {
	m := map[string]string{}
	for k, v := range a {
		if v != "" {
			m[k] = v
		}
	}
	for k, v := range b {
		if v != "" {
			m[k] = v
		}
	}
	return m
}
