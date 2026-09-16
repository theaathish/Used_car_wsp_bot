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
