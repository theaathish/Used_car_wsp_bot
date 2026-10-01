package botengine

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Intent types
type Intent string

const (
	IntentUnknown   Intent = "UNKNOWN"
	IntentGreeting  Intent = "GREETING"
	IntentBuy       Intent = "BUY"
	IntentSell      Intent = "SELL"
	IntentTestDrive Intent = "TEST_DRIVE"
	IntentFinance   Intent = "FINANCE"
	IntentHuman     Intent = "HUMAN_AGENT"
	IntentSelect    Intent = "SELECT"
	IntentAny       Intent = "ANY"
	IntentReset     Intent = "RESET"
)

// NLPEntities represents all structured information extracted from a free-text message.
type NLPEntities struct {
	Intent           Intent
	Brand            string
	Model            string
	BodyType         string
	Fuel             string
	Transmission     string
	BudgetMin        int
	BudgetMax        int
	YearMin          int
	YearMax          int
	KM               int
	SelectionIndex   int // 1-based index if user replied "1", "car 2", etc.
	IsAny            bool
	HasSearchSignals bool
	HasSellSignals   bool
	RawText          string
}

var (
	rxDigits    = regexp.MustCompile(`\d+`)
	rxYearRange = regexp.MustCompile(`\b(19\d\d|20\d\d)\s*(?:to|-|till|through|and)\s*(19\d\d|20\d\d)\b`)
	rxSingleYear = regexp.MustCompile(`\b(199\d|20[0-2]\d|2030)\b`)
	rxSelection = regexp.MustCompile(`(?i)^\s*(?:option|car|vehicle|no\.?|#)?\s*([1-9])\s*[\.\)]?\s*$`)
	rxSelectionWord = regexp.MustCompile(`(?i)\b(?:car|option|vehicle|#)\s*([1-9])\b`)
)

var knownBrands = []string{
	"BMW", "Mercedes-Benz", "Mercedes", "Benz", "Audi", "Toyota", "Honda",
	"Hyundai", "Maruti Suzuki", "Maruti", "Suzuki", "Tata", "Mahindra",
	"Kia", "Volkswagen", "VW", "Skoda", "Nissan", "Renault", "Ford",
	"Porsche", "Volvo", "MG", "Jeep", "Land Rover", "Jaguar", "Lexus",
	"Perodua", "Proton", "Mazda", "Subaru", "Mitsubishi", "Peugeot",
	"Chevrolet", "Fiat", "Mini", "Tesla",
}

var knownModels = []string{
	// BMW
	"M4", "M3", "M5", "M2", "X1", "X3", "X5", "X7", "218", "218i", "320i", "330i",
	"3 Series", "5 Series", "7 Series", "Z4", "i4", "iX",
	// Mercedes
	"C-Class", "C200", "C300", "E-Class", "E200", "E300", "S-Class", "GLA", "GLC", "GLE", "A-Class", "A200",
	// Audi
	"A4", "A6", "A8", "Q3", "Q5", "Q7", "Q8", "RS5",
	// Honda
	"City", "Civic", "Accord", "CR-V", "HR-V", "BR-V", "Jazz", "WR-V", "Amaze",
	// Toyota
	"Corolla", "Camry", "Fortuner", "Innova", "Yaris", "Vios", "Hilux", "Vellfire", "Urban Cruiser", "Glanza",
	// Maruti / Suzuki
	"Swift", "Baleno", "Dzire", "Brezza", "Ertiga", "Ciaz", "WagonR", "Alto", "Fronx", "Grand Vitara", "Jimny",
	// Hyundai
	"Creta", "Venue", "i20", "i10", "Verna", "Tucson", "Alcazar", "Kona", "Exter", "Aura",
	// Tata
	"Nexon", "Harrier", "Safari", "Punch", "Altroz", "Tiago", "Tigor", "Curvv",
	// Mahindra
	"Thar", "Scorpio", "Scorpio-N", "XUV700", "XUV300", "XUV3XO", "Bolero",
	// Kia
	"Seltos", "Sonet", "Carens", "EV6", "Carnival",
	// VW & Skoda
	"Polo", "Vento", "Taigun", "Virtus", "Tiguan", "Slavia", "Kushaq", "Octavia", "Superb", "Kodiaq",
	// Proton / Perodua
	"Myvi", "Axia", "Bezza", "Alza", "Ativa", "Aruz", "Saga", "Persona", "Iriz", "X50", "X70", "X90",
}

var knownBodyTypes = []string{
	"SUV", "Sedan", "Hatchback", "MPV", "Coupe", "Convertible", "Crossover", "Wagon", "Van", "Truck",
}

var knownFuels = []string{
	"Petrol", "Diesel", "CNG", "Electric", "Hybrid",
}

var knownTransmissions = []string{
	"Automatic", "Manual", "AMT", "CVT", "DCT",
}

// ParseMessageNLP extracts intents and entities from an inbound customer message.
func ParseMessageNLP(body string) NLPEntities {
	trimmed := strings.TrimSpace(body)
	lower := strings.ToLower(trimmed)

	res := NLPEntities{
		Intent:  IntentUnknown,
		RawText: trimmed,
	}

	if trimmed == "" {
		return res
	}

	// 1. Check for "Any" / Skip phrases
	if isAnyPhrase(lower) {
		res.IsAny = true
		res.Intent = IntentAny
	}

	// 2. Check for Greetings / Reset
	if isGreetingText(lower) {
		res.Intent = IntentGreeting
		return res
	}
	if isResetText(lower) {
		res.Intent = IntentReset
		return res
	}

	// 3. Check for Selection Number (e.g. "1", "2", "car 1", "option 3")
	if sel := extractSelection(trimmed); sel > 0 {
		res.SelectionIndex = sel
		res.Intent = IntentSelect
	}

	// 4. Check for Human Agent Escalation
	if isHumanRequest(lower) {
		res.Intent = IntentHuman
		return res
	}

	// 5. Check for Test Drive Intent
	if isTestDriveRequest(lower) {
		res.Intent = IntentTestDrive
		// Check if a vehicle number was mentioned along with test drive (e.g. "test drive for car 2")
		if sel := extractSelection(trimmed); sel > 0 {
			res.SelectionIndex = sel
		}
	}

	// 6. Check for Finance Intent
	if isFinanceRequest(lower) {
		res.Intent = IntentFinance
		if sel := extractSelection(trimmed); sel > 0 {
			res.SelectionIndex = sel
		}
	}

	// 7. Check for Sell Intent
	if isSellRequest(lower) {
		res.Intent = IntentSell
		res.HasSellSignals = true
	}

	// 8. Extract Brand & Make
	res.Brand = extractBrand(trimmed)

	// 9. Extract Model
	res.Model = extractModel(trimmed, res.Brand)

	// 10. Extract Body Type
	res.BodyType = extractBodyType(lower)

	// 11. Extract Fuel
	res.Fuel = extractFuel(lower)

	// 12. Extract Transmission
	res.Transmission = extractTransmission(lower)

	// 13. Extract Budget
	bMin, bMax := parseNLPBudget(lower)
	if bMax > 0 {
		res.BudgetMin = bMin
		res.BudgetMax = bMax
	}

	// 14. Extract Year Range or Single Year
	yMin, yMax := extractYearRange(trimmed)
	if yMin > 0 {
		res.YearMin = yMin
		res.YearMax = yMax
	}

	// 15. Extract Mileage / KM
	res.KM = extractKM(lower)

	// Check if this message contains search signals for buying cars
	if res.Brand != "" || res.Model != "" || res.BodyType != "" || res.Fuel != "" ||
		res.Transmission != "" || res.BudgetMax > 0 || res.YearMin > 0 || isBuySearchPhrase(lower) {
		res.HasSearchSignals = true
		if res.Intent == IntentUnknown || res.Intent == IntentAny {
			res.Intent = IntentBuy
		}
	}

	return res
}

func isAnyPhrase(s string) bool {
	clean := strings.Trim(s, "!.?,;:-_ ")
	switch clean {
	case "any", "anything", "all", "no preference", "doesn't matter", "doesnt matter",
		"skip", "none", "no", "na", "n/a", "flexible", "whatever", "don't care", "dont care", "both":
		return true
	}
	return false
}

func isGreetingText(s string) bool {
	clean := strings.Trim(strings.ToLower(strings.TrimSpace(s)), "!.?,;:-_~*\"'“”‘’ ")
	switch clean {
	case "hi", "hello", "hey", "hai", "vanakkam", "namaste", "namaskar",
		"good morning", "good afternoon", "good evening", "yo", "start":
		return true
	}
	return false
}

func isResetText(s string) bool {
	clean := strings.Trim(strings.ToLower(strings.TrimSpace(s)), "!.?,;:-_~*\"'“”‘’ ")
	switch clean {
	case "restart", "menu", "main menu", "start again", "home", "reset":
		return true
	}
	return false
}

func isHumanRequest(s string) bool {
	return strings.Contains(s, "human") ||
		strings.Contains(s, "agent") ||
		strings.Contains(s, "sales rep") ||
		strings.Contains(s, "salesperson") ||
		strings.Contains(s, "speak to someone") ||
		strings.Contains(s, "talk to someone") ||
		strings.Contains(s, "call me") ||
		strings.Contains(s, "customer service")
}

func isTestDriveRequest(s string) bool {
	return strings.Contains(s, "test drive") ||
		strings.Contains(s, "testdrive") ||
		strings.Contains(s, "schedule drive") ||
		strings.Contains(s, "book drive") ||
		strings.Contains(s, "take a drive") ||
		strings.Contains(s, "test car") ||
		(strings.Contains(s, "drive") && strings.Contains(s, "car"))
}

func isFinanceRequest(s string) bool {
	return strings.Contains(s, "finance") ||
		strings.Contains(s, "loan") ||
		strings.Contains(s, "emi") ||
		strings.Contains(s, "down payment") ||
		strings.Contains(s, "payment") ||
		strings.Contains(s, "payments") ||
		strings.Contains(s, "pay") ||
		strings.Contains(s, "installment") ||
		strings.Contains(s, "interest rate")
}

func isSellRequest(s string) bool {
	if strings.Contains(s, "sell") ||
		strings.Contains(s, "selling") ||
		strings.Contains(s, "valuation") ||
		strings.Contains(s, "trade in") ||
		strings.Contains(s, "exchange car") {
		return true
	}
	return false
}

func isBuySearchPhrase(s string) bool {
	return strings.Contains(s, "buy") ||
		strings.Contains(s, "looking for") ||
		strings.Contains(s, "i want") ||
		strings.Contains(s, "want a") ||
		strings.Contains(s, "need a") ||
		strings.Contains(s, "show me") ||
		strings.Contains(s, "cars") ||
		strings.Contains(s, "vehicles") ||
		strings.Contains(s, "find")
}

func extractSelection(body string) int {
	clean := strings.TrimSpace(body)
	if m := rxSelection.FindStringSubmatch(clean); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if m := rxSelectionWord.FindStringSubmatch(clean); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func extractBrand(body string) string {
	lower := strings.ToLower(body)
	for _, b := range knownBrands {
		lb := strings.ToLower(b)
		// Check word boundary
		pattern := `\b` + regexp.QuoteMeta(lb) + `\b`
		if matched, _ := regexp.MatchString(pattern, lower); matched {
			if lb == "mercedes" || lb == "benz" {
				return "Mercedes-Benz"
			}
			if lb == "vw" {
				return "Volkswagen"
			}
			return b
		}
	}
	return ""
}

func extractModel(body string, brandFound string) string {
	lower := strings.ToLower(body)
	// 1. Direct match from knownModels
	for _, m := range knownModels {
		lm := strings.ToLower(m)
		pattern := `\b` + regexp.QuoteMeta(lm) + `\b`
		if matched, _ := regexp.MatchString(pattern, lower); matched {
			fields := strings.Fields(body)
			for i, f := range fields {
				cleanF := strings.Trim(strings.ToLower(f), ".,!?*")
				if cleanF == lm && i+1 < len(fields) {
					nextWord := strings.Trim(fields[i+1], ".,!?*")
					switch strings.ToLower(nextWord) {
					case "cs", "csl", "gts", "gt", "sport", "competition", "comp", "line", "xline", "m-sport":
						return m + " " + strings.ToUpper(nextWord)
					}
				}
			}
			return m
		}
	}

	// 2. If brand was found, check if a token immediately after the brand is a model code (e.g. "BMW M4", "Honda City")
	if brandFound != "" {
		fields := strings.Fields(body)
		for i, f := range fields {
			cleanF := strings.Trim(strings.ToLower(f), ".,!?*")
			if cleanF == strings.ToLower(brandFound) || (strings.HasPrefix(strings.ToLower(brandFound), cleanF) && len(cleanF) > 2) {
				if i+1 < len(fields) {
					nextWord := strings.Trim(fields[i+1], ".,!?*")
					lNext := strings.ToLower(nextWord)
					// Avoid noise words
					if !isFillerWord(lNext) {
						if i+2 < len(fields) {
							subNext := strings.Trim(fields[i+2], ".,!?*")
							switch strings.ToLower(subNext) {
							case "cs", "csl", "gts", "gt", "sport", "competition", "comp":
								return nextWord + " " + strings.ToUpper(subNext)
							}
						}
						return nextWord
					}
				}
			}
		}
	}

	return ""
}

func extractBodyType(lower string) string {
	for _, bt := range knownBodyTypes {
		if strings.Contains(lower, strings.ToLower(bt)) {
			return bt
		}
	}
	return ""
}

func extractFuel(lower string) string {
	for _, f := range knownFuels {
		if strings.Contains(lower, strings.ToLower(f)) {
			return strings.ToUpper(f)
		}
	}
	return ""
}

func extractTransmission(lower string) string {
	if strings.Contains(lower, "auto") {
		return "AUTOMATIC"
	}
	if strings.Contains(lower, "manual") {
		return "MANUAL"
	}
	for _, tr := range knownTransmissions {
		if strings.Contains(lower, strings.ToLower(tr)) {
			return strings.ToUpper(tr)
		}
	}
	return ""
}

func parseNLPBudget(lower string) (int, int) {
	t := lower
	t = strings.ReplaceAll(t, "₹", " ")
	t = strings.ReplaceAll(t, "rs.", " ")
	t = strings.ReplaceAll(t, "rs", " ")
	t = strings.ReplaceAll(t, "rm", " ")
	t = strings.ReplaceAll(t, "myr", " ")
	t = strings.ReplaceAll(t, ",", "")

	mult := 1
	if strings.Contains(t, "lakh") || strings.Contains(t, "lac") {
		mult = 100000
	} else if strings.Contains(t, "k") && !strings.Contains(t, "km") {
		mult = 1000
	}

	// Range "5 to 10 lakh" or "5-10 lakh"
	rxRange := regexp.MustCompile(`(\d+)\s*(?:to|-)\s*(\d+)\s*(lakh|lac|l|k)?`)
	if m := rxRange.FindStringSubmatch(t); len(m) >= 3 {
		v1, _ := strconv.Atoi(m[1])
		v2, _ := strconv.Atoi(m[2])
		unitMult := mult
		if len(m) > 3 {
			switch strings.ToLower(m[3]) {
			case "lakh", "lac", "l":
				unitMult = 100000
			case "k":
				unitMult = 1000
			}
		}
		if unitMult == 1 && v1 < 100 {
			unitMult = 100000 // default bare small numbers like "5 to 10" to lakhs
		}
		if v1 > v2 {
			v1, v2 = v2, v1
		}
		return v1 * unitMult, v2 * unitMult
	}

	// Single "under 15 lakh" or "15l"
	rxUnder := regexp.MustCompile(`(?:under|below|max|upto|around|within)?\s*(\d+)\s*(lakh|lac|l|k)?`)
	nums := rxDigits.FindAllString(t, -1)
	if len(nums) > 0 {
		v, err := strconv.Atoi(nums[0])
		if err == nil {
			if strings.Contains(t, "lakh") || strings.Contains(t, "lac") || strings.Contains(t, "l") {
				mult = 100000
			}
			if mult == 1 && v < 100 && (strings.Contains(t, "under") || strings.Contains(t, "budget")) {
				mult = 100000
			}
			if v*mult >= 10000 {
				return 0, v * mult
			}
		}
	}
	_ = rxUnder
	return 0, 0
}

func extractYearRange(body string) (int, int) {
	// "2019 to 2026"
	if m := rxYearRange.FindStringSubmatch(body); len(m) == 3 {
		y1, _ := strconv.Atoi(m[1])
		y2, _ := strconv.Atoi(m[2])
		if y1 > y2 {
			y1, y2 = y2, y1
		}
		return y1, y2
	}

	// Single year "2020" or "from 2018"
	if m := rxSingleYear.FindStringSubmatch(body); len(m) == 2 {
		y, _ := strconv.Atoi(m[1])
		currentYear := time.Now().Year() + 2
		if y >= 1995 && y <= currentYear {
			return y, currentYear
		}
	}

	return 0, 0
}

func extractKM(lower string) int {
	rxKM := regexp.MustCompile(`(\d+)\s*(k|thousand)?\s*km\b`)
	if m := rxKM.FindStringSubmatch(lower); len(m) == 3 {
		v, _ := strconv.Atoi(m[1])
		if m[2] == "k" || m[2] == "thousand" {
			return v * 1000
		}
		return v
	}
	return 0
}

func isFillerWord(w string) bool {
	fillers := map[string]bool{
		"i": true, "want": true, "need": true, "a": true, "an": true, "the": true,
		"car": true, "cars": true, "for": true, "me": true, "my": true, "please": true,
		"looking": true, "buy": true, "sell": true, "show": true, "give": true, "under": true,
		"with": true, "and": true, "or": true, "in": true, "is": true, "to": true,
	}
	return fillers[w]
}

// CleanMakeAndModel normalizes brand and model, preventing duplications like "BMW M4 CS M4 CS".
func CleanMakeAndModel(rawBrand, rawModel string) (string, string) {
	b := strings.TrimSpace(rawBrand)
	m := strings.TrimSpace(rawModel)

	// If rawBrand contains a known brand (e.g. "BMW" from "bmw m4 cs"), normalize make
	if known := extractBrand(b); known != "" {
		if m == "" {
			m = extractModel(b, known)
		}
		b = known
	} else if knownM := extractBrand(m); knownM != "" && b == "" {
		b = knownM
		m = extractModel(m, knownM)
	}

	// If rawModel already starts with brand, strip brand prefix from model
	if b != "" && m != "" {
		lb := strings.ToLower(b)
		lm := strings.ToLower(m)
		if strings.HasPrefix(lm, lb) {
			m = strings.TrimSpace(m[len(b):])
		}
	}

	// If rawBrand ends with rawModel (e.g. brand "bmw m4 cs", model "m4 cs"), strip model from brand
	if m != "" && b != "" {
		lb := strings.ToLower(b)
		lm := strings.ToLower(m)
		if strings.HasSuffix(lb, lm) {
			b = strings.TrimSpace(b[:len(b)-len(m)])
		}
	}

	if b == "" {
		b = rawBrand
	}
	if m == "" {
		m = rawModel
	}

	// Normalize casing
	if len(b) > 0 {
		b = strings.ToUpper(b[:1]) + b[1:]
	}
	if len(m) > 0 {
		m = strings.ToUpper(m[:1]) + m[1:]
	}

	lb := strings.ToLower(b)
	switch lb {
	case "bmw":
		b = "BMW"
	case "mercedes", "mercedes-benz", "benz":
		b = "Mercedes-Benz"
	case "vw", "volkswagen":
		b = "Volkswagen"
	case "byd":
		b = "BYD"
	case "mg":
		b = "MG"
	}

	lm := strings.ToLower(m)
	if strings.HasPrefix(lm, "m4") {
		m = strings.ToUpper(m)
	} else if strings.Contains(lm, "cs") || strings.Contains(lm, "gt") {
		m = strings.ToUpper(m)
	}

	return b, m
}

