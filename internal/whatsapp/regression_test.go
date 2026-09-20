package whatsapp

import "testing"

// V3-41: permanent parser regression table. Every parsing bug ever found
// gets a row here. ParseBudget returns 0,0 for "not a budget".
func TestParserRegression(t *testing.T) {
	budgets := []struct {
		in       string
		min, max int
	}{
		{"15L", 1200000, 1500000},
		{"15 lakh", 1200000, 1500000},
		{"₹15 lakh", 1200000, 1500000},
		{"₹15,00,000", 1200000, 1500000},
		{"1500000", 1200000, 1500000},
		{"Rs 15 lakh", 1200000, 1500000},
		{"10-15 lakh", 1000000, 1500000},
		{"10–15 lakh", 1000000, 1500000}, // en dash
		{"X1", 0, 0},
		{"BMW", 0, 0},
		{"5", 0, 0},
		{"hello", 0, 0},
		{"-10 lakh", 0, 0},
		{"0", 0, 0},
		{"₹0", 0, 0},
		{"55000km", 44000, 55000}, // km figure still parses numerically; callers scope it
	}
	for _, tc := range budgets {
		mn, mx := ParseBudget(tc.in)
		if mn != tc.min || mx != tc.max {
			t.Errorf("ParseBudget(%q) = %d,%d; want %d,%d", tc.in, mn, mx, tc.min, tc.max)
		}
	}

	fuels := map[string]string{
		"Diesel": "DIESEL", "diesel car": "DIESEL", "petrol": "PETROL",
		"Automatic": "", "X1": "", "20 lakh": "",
	}
	for in, want := range fuels {
		if got := findFuel(in); got != want {
			t.Errorf("findFuel(%q) = %q; want %q", in, got, want)
		}
	}
	trans := map[string]string{
		"Automatic": "AUTOMATIC", "manual": "MANUAL", "AMT": "AMT",
		"Diesel": "", "X1": "", "any": "",
	}
	for in, want := range trans {
		if got := findTrans(in); got != want {
			t.Errorf("findTrans(%q) = %q; want %q", in, got, want)
		}
	}
}

// V3-40: state fuzz — every state x hostile input must never panic,
// must return a non-empty reply and a valid next state.
func TestStateFuzz(t *testing.T) {
	states := []string{"NEW", "ASK_INTENT", "BUY_BUDGET", "BUY_BRAND", "BUY_MODEL",
		"BUY_FUEL", "BUY_TRANS", "BUY_YEAR", "BUY_RESULTS", "FINANCE_INFO",
		"TESTDRIVE_ASK", "POST_TESTDRIVE_FOLLOWUP", "SELL_CAR", "SELL_YEAR", "SELL_DETAILS", "SELL_SPECS",
		"SELL_PHOTOS", "SELL_INSPECTION", "EXCHANGE_CURRENT", "EXCHANGE_WANT", "DONE", "BROKEN_STATE"}
	inputs := []string{
		"", " ", "null", "{}", "[]", "null null", "' OR 1=1 --", "<script>alert(1)</script>",
		"Automatic", "Diesel", "X1", "BMW", "20 lakh", "-5 lakh", "₹0", "999999999999999999999",
		"buy sell exchange", "BACK", "start again", "menu", "more cars more cars",
		"😊🚗💥", "பிஎம்டபிள்யூ வேணும்", "मुझे गाड़ी चाहिए", "test drive test drive",
		"I want to sell but also buy and exchange everything right now please",
		"2018, MH12AB1234, 55000km, Diesel, Manual, Good, Pune, 5 lakh, X1",
		"not interested", "INTERESTED", "i will think about it later maybe",
		"financE LoAn EMI", "DONE done done", "ok", "???", "..........",
	}
	bad := 0
	for _, st := range states {
		for _, in := range inputs {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("PANIC state=%q input=%q: %v", st, in, r)
						bad++
					}
				}()
				next, reply, _, _, _ := Next(st, in, map[string]string{})
				if reply == "" {
					t.Errorf("empty reply state=%q input=%q", st, in)
					bad++
				}
				if !validStates[next] {
					t.Errorf("invalid next=%q state=%q input=%q", next, st, in)
					bad++
				}
			}()
		}
	}
	// V3-40's key assertion: fuel/trans/model must never cross-contaminate.
	next, _, _, _, patch := Next("BUY_FUEL", "Automatic", map[string]string{})
	_ = next
	if patch["fuel"] == "AUTOMATIC" {
		t.Errorf("BUY_FUEL + 'Automatic' must NOT set fuel=AUTOMATIC: %+v", patch)
	}
	_, _, _, _, patch = Next("BUY_TRANS", "20 lakh", map[string]string{})
	if patch["transmission"] == "20 lakh" {
		t.Errorf("BUY_TRANS + '20 lakh' must NOT set transmission: %+v", patch)
	}
	if bad > 0 {
		t.Fatalf("%d fuzz violations", bad)
	}
}
