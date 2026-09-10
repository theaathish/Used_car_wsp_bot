package whatsapp

import (
	"strings"
	"testing"
)

func feed(state string, data map[string]string, msgs ...string) (string, map[string]string, map[string]string) {
	patch := map[string]string{}
	for _, m := range msgs {
		var p map[string]string
		var ni, ns string
		state, _, ni, ns, p = Next(state, m, data)
		_, _ = ni, ns
		for k, v := range p {
			data[k] = v
		}
		patch = p
	}
	return state, data, patch
}

func TestBuyFlow(t *testing.T) {
	data := map[string]string{}
	st, rep, intent, _, patch := Next("NEW", "hi", data)
	if st != "ASK_INTENT" || rep == "" {
		t.Fatalf("NEW: %s %s", st, rep)
	}
	for k, v := range patch {
		data[k] = v
	}
	st, _, intent, _, patch = Next(st, "BUY", data)
	if st != "BUY_BUDGET" || intent != "BUY" {
		t.Fatalf("intent: %s %s", st, intent)
	}
	st, _, _, _, patch = Next(st, "3-5 lakh", data)
	for k, v := range patch {
		data[k] = v
	}
	if st != "BUY_BRAND" || data["budget_max"] == "" {
		t.Fatalf("budget: %s %+v", st, data)
	}
	// BUY-004: brand+model in one message fills both
	st, _, _, _, patch = Next(st, "BMW X1", data)
	for k, v := range patch {
		data[k] = v
	}
	if data["brand"] == "" || data["model"] == "" {
		t.Fatalf("brand+model split: %+v", data)
	}
	if st != "BUY_FUEL" {
		t.Fatalf("after brand+model want FUEL, got %s", st)
	}
	st, _, _, _, patch = Next(st, "Diesel", data)
	for k, v := range patch {
		data[k] = v
	}
	if st != "BUY_TRANS" {
		t.Fatalf("fuel: %s", st)
	}
	st, _, _, _, patch = Next(st, "Automatic", data)
	for k, v := range patch {
		data[k] = v
	}
	if st != "BUY_YEAR" {
		t.Fatalf("trans: %s", st)
	}
	st, _, _, _, _ = Next(st, "2020", data)
	if st != "BUY_RESULTS" {
		t.Fatalf("year: %s", st)
	}
}

func TestBuyUnknownBudget(t *testing.T) {
	data := map[string]string{}
	st, _, _, _, _ := Next("BUY_BUDGET", "i dont know my budget", data)
	if st != "BUY_BRAND" {
		t.Fatalf("unknown budget should skip to brand, got %s", st)
	}
}

func TestBuyMultiField(t *testing.T) {
	// BUY-009: everything in one message
	data := map[string]string{}
	st, _, _, _, patch := Next("ASK_INTENT", "buy BMW X1 automatic diesel under 15 lakh", data)
	for k, v := range patch {
		data[k] = v
	}
	if data["budget_max"] == "" || data["fuel"] == "" || data["transmission"] == "" {
		t.Fatalf("multi extract failed: %+v (state %s)", data, st)
	}
	if data["brand"] != "BMW" || data["model"] != "X1" {
		t.Fatalf("brand/model extract failed: %+v (state %s)", data, st)
	}
	if st != "BUY_YEAR" {
		t.Fatalf("only year should remain, got %s (%+v)", st, data)
	}
}

func TestInterestStates(t *testing.T) {
	data := map[string]string{}
	_, _, _, _, p := Next("BUY_RESULTS", "i will think about it", data)
	if p["interest"] != "THINKING" {
		t.Fatalf("thinking: %+v", p)
	}
	_, _, _, _, p = Next("BUY_RESULTS", "not interested", data)
	if p["interest"] != "NOT_INTERESTED" {
		t.Fatalf("not interested: %+v", p)
	}
	_, _, _, _, p = Next("BUY_RESULTS", "show me more cars", data)
	if p["page"] != "next" {
		t.Fatalf("more cars: %+v", p)
	}
	ns, _, _, _, _ := Next("BUY_RESULTS", "i need finance loan emi", data)
	if ns != "FINANCE_INFO" {
		t.Fatalf("finance: %s", ns)
	}
}

func TestSellFlow(t *testing.T) {
	data := map[string]string{}
	st, _, _, _, _ := Next("ASK_INTENT", "sell", data)
	if st != "SELL_CAR" {
		t.Fatalf("sell: %s", st)
	}
	st, _, _, _, patch := Next(st, "Swift VDI", data)
	for k, v := range patch {
		data[k] = v
	}
	if st != "SELL_DETAILS" {
		t.Fatalf("sell car: %s", st)
	}
	st, _, _, _, patch = Next(st, "2018, MH12AB1234, 55000km", data)
	for k, v := range patch {
		data[k] = v
	}
	if data["sell_year"] != "2018" || data["sell_km"] != "55000" || data["sell_reg"] == "" {
		t.Fatalf("sell details parse: %+v", data)
	}
	st, _, _, _, _ = Next(st, "Diesel, Manual, Good, Pune", data)
	if st != "SELL_PHOTOS" {
		t.Fatalf("sell specs: %s", st)
	}
	st, _, _, status, _ := Next(st, "DONE", data)
	if st != "DONE" || status != "FOLLOWUP" {
		t.Fatalf("sell done: %s %s", st, status)
	}
}

func TestExchangeFlow(t *testing.T) {
	data := map[string]string{}
	st, _, intent, _, _ := Next("ASK_INTENT", "exchange my car", data)
	if st != "EXCHANGE_CURRENT" || intent != "EXCHANGE" {
		t.Fatalf("exchange: %s %s", st, intent)
	}
}

func TestParseBudget(t *testing.T) {
	mn, mx := ParseBudget("4 lakh")
	if mx != 400000 || mn == 0 {
		t.Fatalf("budget single: %d %d", mn, mx)
	}
	mn, mx = ParseBudget("3-5 lakh")
	if mn != 300000 || mx != 500000 {
		t.Fatalf("budget range: %d %d", mn, mx)
	}
	if _, mx := ParseBudget("hello"); mx != 0 {
		t.Fatalf("no budget should be 0, got %d", mx)
	}
	// BUY-104 currency formats normalize identically
	for _, s := range []string{"1500000", "15L", "15 lakh", "₹15 lakh", "₹15,00,000", "Rs 15 lakh"} {
		_, mx := ParseBudget(s)
		if mx != 1500000 {
			t.Fatalf("%q -> max %d, want 1500000", s, mx)
		}
	}
	// BUY-105/106 invalid budgets reprompt (0,0)
	if _, mx := ParseBudget("₹0"); mx != 0 {
		t.Fatalf("zero budget should be 0, got %d", mx)
	}
	if _, mx := ParseBudget("-10 lakh"); mx != 0 {
		t.Fatalf("negative budget should be 0, got %d", mx)
	}
}

func TestTolerantIntents(t *testing.T) {
	for _, s := range []string{"buy", "BUY", "I want to buy", "I need a car", "Looking for a car", "bro BMW venum", "car venum", "gaadi chahiye"} {
		if !wantsBuy(norm(s)) {
			t.Fatalf("should route to BUY: %q", s)
		}
	}
	if detectIntentSwitch("actually i want to sell", "BUY") != "SELL" {
		t.Fatal("intent switch BUY->SELL not detected")
	}
	if detectIntentSwitch("diesel", "BUY") != "" {
		t.Fatal("false intent switch on fuel answer")
	}
}

func TestSellValidation(t *testing.T) {
	data := map[string]string{}
	st, _, _, _, _ := Next("SELL_DETAILS", "not a car at all", data)
	if st != "SELL_DETAILS" {
		t.Fatalf("missing year+km must reprompt, got %s", st)
	}
	data2 := map[string]string{}
	st, _, _, _, _ = Next("SELL_PHOTOS", "anything", data2)
	_ = st
	// photo cap at 10
	dcap := map[string]string{"sell_photos": "10"}
	st, rep, _, _, _ := Next("SELL_PHOTOS", "another", dcap)
	if st != "SELL_PHOTOS" || !strings.Contains(rep, "DONE") {
		t.Fatalf("photo cap: %s %s", st, rep)
	}
}
