package whatsapp

// Regression: "BMW C400GT 2025" at the budget step banked a bogus RM400-2025
// budget (model digits + year), so matching filtered out the in-stock
// C400GT bikes and showed unrelated 218i cars with "(/)". Model codes and
// years must never become money. Run: go test -run TestModelSearchGuard -v

import (
	"strings"
	"testing"
)

func TestModelSearchGuard(t *testing.T) {
	// acceptBudget gating
	if _, _, ok := acceptBudget("BMW C400GT 2025"); ok {
		t.Fatal("model+year must not be a budget")
	}
	if _, _, ok := acceptBudget("2025"); ok {
		t.Fatal("bare year must not be a budget")
	}
	if _, _, ok := acceptBudget("55000km"); ok {
		t.Fatal("mileage must not be a budget")
	}
	if mn, mx, ok := acceptBudget("3-5 lakh"); !ok || mn != 300000 || mx != 500000 {
		t.Fatalf("range: got %d,%d,%v", mn, mx, ok)
	}
	if _, mx, ok := acceptBudget("RM 90,000"); !ok || mx != 90000 {
		t.Fatalf("rm: got %d,%v", mx, ok)
	}
	if _, mx, ok := acceptBudget("90000"); !ok || mx != 90000 {
		t.Fatalf("bare large figure: got %d,%v", mx, ok)
	}
	// word-boundary signals: "cars"/"replace"/"performance" carry no signal
	if hasBudgetSignal("more cars") {
		t.Fatal("cars must not signal budget")
	}
	if hasBudgetSignal("replace my car") {
		t.Fatal("replace must not signal budget")
	}
	if hasBudgetSignal("performance") {
		t.Fatal("performance must not signal budget (rm inside word)")
	}
	if !hasBudgetSignal("under 15 lakh") || !hasBudgetSignal("RM150k") || !hasBudgetSignal("Rs 15 lakh") {
		t.Fatal("real budget phrasing must signal")
	}

	// ExtractAll banks model/year, never budget, for a model search
	got := ExtractAll("BMW C400GT 2025")
	if _, ok := got["budget_max"]; ok {
		t.Fatalf("ExtractAll budget leak: %+v", got)
	}
	if !strings.EqualFold(got["year_min"], "2025") {
		t.Fatalf("year lost: %+v", got)
	}

	// BUY_BUDGET "BMW C400GT 2025": stays on budget, names the find,
	// never claims "Noted budget"
	data := map[string]string{}
	ns, reply, _, _, patch := Next("BUY_BUDGET", "BMW C400GT 2025", data)
	for k, v := range patch {
		data[k] = v
	}
	if ns != "BUY_BUDGET" {
		t.Fatalf("must re-ask budget, got %s (%q)", ns, reply)
	}
	if data["budget_max"] != "" {
		t.Fatalf("bogus budget stored: %+v", data)
	}
	if data["brand"] == "" {
		t.Fatalf("brand lost: %+v", data)
	}
	if !strings.Contains(strings.ToUpper(data["model"]), "C400GT") {
		t.Fatalf("model lost: %+v", data)
	}
	if strings.Contains(reply, "Noted budget") {
		t.Fatalf("must never claim Noted budget: %q", reply)
	}
	if !strings.Contains(reply, "budget") {
		t.Fatalf("must ask budget: %q", reply)
	}

	// display: blank fuel/trans never renders "(/)"
	if s := specSuffix("", ""); s != "" {
		t.Fatalf("empty spec: %q", s)
	}
	if s := specSuffix("PETROL", ""); s != " (PETROL)" {
		t.Fatalf("fuel only: %q", s)
	}
	if s := specSuffix("", "AUTOMATIC"); s != " (AUTOMATIC)" {
		t.Fatalf("trans only: %q", s)
	}
	if s := specSuffix("PETROL", "AUTOMATIC"); s != " (PETROL/AUTOMATIC)" {
		t.Fatalf("both: %q", s)
	}
}

func TestBudgetSkipKeepsFind(t *testing.T) {
	banked := map[string]string{"brand": "BMW", "model": "C400GT", "year_min": "2025"}

	// iPhone curly apostrophe counts as "don't know"
	if !unknownBudget(norm("don’t know")) {
		t.Fatal("curly don’t know must skip budget")
	}
	ns, _, _, _, patch := Next("BUY_BUDGET", "don’t know", copyMap(banked))
	if ns != "BUY_FUEL" || patch["budget_unknown"] != "1" {
		t.Fatalf("curly skip: got %s %+v, want BUY_FUEL", ns, patch)
	}

	// "0" skip keeps the banked find instead of re-asking brand
	data := copyMap(banked)
	ns, reply, _, _, patch := Next("BUY_BUDGET", "0", data)
	for k, v := range patch {
		data[k] = v
	}
	if ns != "BUY_FUEL" {
		t.Fatalf("skip with banked find: got %s (%q), want BUY_FUEL", ns, reply)
	}
	if data["brand"] != "BMW" || data["model"] != "C400GT" {
		t.Fatalf("banked find wiped: %+v", data)
	}

	// fresh "0" still goes to brand
	ns, _, _, _, patch = Next("BUY_BUDGET", "0", map[string]string{})
	if ns != "BUY_BRAND" || patch["budget_unknown"] != "1" {
		t.Fatalf("fresh skip: got %s %+v", ns, patch)
	}

	// space-insensitive model: typed C400GT finds stored "C 400 GT"
	if !strings.Contains(nospace("C 400 GT"), nospace("c400gt")) {
		t.Fatal("nospace model match broken")
	}
	if strings.Contains(nospace("C 400 GT"), nospace("218i")) {
		t.Fatal("nospace must not over-match unrelated models")
	}
}

func copyMap(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// modelMatches must find the bike no matter which column holds the name:
// model, spaced model, description-only, or make-glued rows all match;
// unrelated models never do.
func TestModelMatchesColumns(t *testing.T) {
	match := []struct{ make_, model, desc string }{
		{"BMW", "C400GT", "BMW C400GT Diamond White"},
		{"BMW", "C 400 GT", "BMW C 400 GT"},
		{"BMW", "", "BMW C400GT Diamond White"},
		{"BMW", "C400GT 2025", ""},
		{"BMW C400GT", "2025", ""},
	}
	for _, tc := range match {
		if !modelMatches(tc.make_, tc.model, tc.desc, "c400gt") {
			t.Errorf("must match make=%q model=%q desc=%q", tc.make_, tc.model, tc.desc)
		}
	}
	nomatch := []struct{ make_, model, desc string }{
		{"BMW", "218i Gran Coupe M Sport", "BMW 218i Gran Coupe M Sport"},
		{"BMW", "R1250GS Adventure", "BMW R1250GS Adventure"},
		{"Volvo", "XC90 T8 Reskin", "Volvo XC90"},
	}
	for _, tc := range nomatch {
		if modelMatches(tc.make_, tc.model, tc.desc, "c400gt") {
			t.Errorf("must NOT match make=%q model=%q desc=%q", tc.make_, tc.model, tc.desc)
		}
	}
}
