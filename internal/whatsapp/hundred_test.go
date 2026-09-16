package whatsapp

// 100-case hermetic suite: pure state-machine / parser coverage.
// No DB, no network. Run: go test ./internal/whatsapp/ -run TestHundred -v
// Each TCxx is one business rule so failures point at the exact rule.

import (
	"strings"
	"testing"
)

func TestHundred(t *testing.T) {
	// helper: run Next with fresh data copy, return state/patch/reply/intent/status
	run := func(state, body string, data map[string]string) (string, string, string, string, map[string]string) {
		if data == nil {
			data = map[string]string{}
		}
		ns, reply, intent, status, patch := Next(state, body, data)
		return ns, reply, intent, status, patch
	}
	withData := func(kv ...string) map[string]string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}

	cases := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// ---------- ParseBudget TC01-TC20 ----------
		{"TC01_budget_single_lakh", func(t *testing.T) {
			mn, mx := ParseBudget("4 lakh")
			if mn != 320000 || mx != 400000 {
				t.Fatalf("got %d,%d want 320000,400000", mn, mx)
			}
		}},
		{"TC02_budget_range_lakh", func(t *testing.T) {
			mn, mx := ParseBudget("3-5 lakh")
			if mn != 300000 || mx != 500000 {
				t.Fatalf("got %d,%d", mn, mx)
			}
		}},
		{"TC03_budget_rm_comma", func(t *testing.T) {
			mn, mx := ParseBudget("RM 90,000")
			if mn != 72000 || mx != 90000 {
				t.Fatalf("got %d,%d", mn, mx)
			}
		}},
		{"TC04_budget_rm_k", func(t *testing.T) {
			mn, mx := ParseBudget("RM150k")
			if mn != 120000 || mx != 150000 {
				t.Fatalf("got %d,%d", mn, mx)
			}
		}},
		{"TC05_budget_range_k", func(t *testing.T) {
			mn, mx := ParseBudget("100-200k")
			if mn != 100000 || mx != 200000 {
				t.Fatalf("got %d,%d", mn, mx)
			}
		}},
		{"TC06_budget_indian_grouping", func(t *testing.T) {
			mn, mx := ParseBudget("₹15,00,000")
			if mn != 1200000 || mx != 1500000 {
				t.Fatalf("got %d,%d", mn, mx)
			}
		}},
		{"TC07_budget_shorthand_L", func(t *testing.T) {
			mn, mx := ParseBudget("15L")
			if mn != 1200000 || mx != 1500000 {
				t.Fatalf("got %d,%d", mn, mx)
			}
		}},
		{"TC08_budget_shorthand_5l", func(t *testing.T) {
			mn, mx := ParseBudget("5l")
			if mn != 400000 || mx != 500000 {
				t.Fatalf("got %d,%d", mn, mx)
			}
		}},
		{"TC09_budget_plain_number", func(t *testing.T) {
			mn, mx := ParseBudget("500000")
			if mn != 400000 || mx != 500000 {
				t.Fatalf("got %d,%d", mn, mx)
			}
		}},
		{"TC10_budget_garbage", func(t *testing.T) {
			if _, mx := ParseBudget("hello"); mx != 0 {
				t.Fatalf("want 0 got %d", mx)
			}
		}},
		{"TC11_budget_modelname_not_budget", func(t *testing.T) {
			if _, mx := ParseBudget("X1"); mx != 0 {
				t.Fatalf("X1 must not be budget, got %d", mx)
			}
		}},
		{"TC12_budget_bare_small", func(t *testing.T) {
			if _, mx := ParseBudget("5"); mx != 0 {
				t.Fatalf("want 0 got %d", mx)
			}
		}},
		{"TC13_budget_negative", func(t *testing.T) {
			if _, mx := ParseBudget("-10 lakh"); mx != 0 {
				t.Fatalf("want 0 got %d", mx)
			}
		}},
		{"TC14_budget_zero_symbol", func(t *testing.T) {
			if _, mx := ParseBudget("₹0"); mx != 0 {
				t.Fatalf("want 0 got %d", mx)
			}
		}},
		{"TC15_budget_zero_plain", func(t *testing.T) {
			if _, mx := ParseBudget("0"); mx != 0 {
				t.Fatalf("want 0 got %d", mx)
			}
		}},
		{"TC16_budget_range_10_15", func(t *testing.T) {
			mn, mx := ParseBudget("10-15 lakh")
			if mn != 1000000 || mx != 1500000 {
				t.Fatalf("got %d,%d", mn, mx)
			}
		}},
		{"TC17_budget_rs_prefix", func(t *testing.T) {
			mn, mx := ParseBudget("Rs 15 lakh")
			if mn != 1200000 || mx != 1500000 {
				t.Fatalf("got %d,%d", mn, mx)
			}
		}},
		{"TC18_budget_km_figure", func(t *testing.T) {
			mn, mx := ParseBudget("55000km")
			if mn != 44000 || mx != 55000 {
				t.Fatalf("got %d,%d", mn, mx)
			}
		}},
		{"TC19_budget_under_phrase", func(t *testing.T) {
			mn, mx := ParseBudget("under 5 lakh")
			if mn != 400000 || mx != 500000 {
				t.Fatalf("got %d,%d", mn, mx)
			}
		}},
		{"TC20_budget_to_phrase", func(t *testing.T) {
			mn, mx := ParseBudget("3 to 6 lakh")
			if mn != 300000 || mx != 600000 {
				t.Fatalf("got %d,%d", mn, mx)
			}
		}},
		// ---------- fuel/trans TC21-TC30 ----------
		{"TC21_fuel_diesel", func(t *testing.T) {
			if got := findFuel("Diesel"); got != "DIESEL" {
				t.Fatalf("got %q", got)
			}
		}},
		{"TC22_fuel_petrol_sentence", func(t *testing.T) {
			if got := findFuel("i want petrol car"); got != "PETROL" {
				t.Fatalf("got %q", got)
			}
		}},
		{"TC23_fuel_rejects_trans", func(t *testing.T) {
			if got := findFuel("Automatic"); got != "" {
				t.Fatalf("got %q want empty", got)
			}
		}},
		{"TC24_fuel_electric", func(t *testing.T) {
			if got := findFuel("electric variant please"); got != "ELECTRIC" {
				t.Fatalf("got %q", got)
			}
		}},
		{"TC25_fuel_cng", func(t *testing.T) {
			if got := findFuel("CNG please"); got != "CNG" {
				t.Fatalf("got %q", got)
			}
		}},
		{"TC26_trans_auto", func(t *testing.T) {
			if got := findTrans("Automatic"); got != "AUTOMATIC" {
				t.Fatalf("got %q", got)
			}
		}},
		{"TC27_trans_manual", func(t *testing.T) {
			if got := findTrans("manual gearbox"); got != "MANUAL" {
				t.Fatalf("got %q", got)
			}
		}},
		{"TC28_trans_amt", func(t *testing.T) {
			if got := findTrans("AMT version"); got != "AMT" {
				t.Fatalf("got %q", got)
			}
		}},
		{"TC29_trans_rejects_fuel", func(t *testing.T) {
			if got := findTrans("Diesel"); got != "" {
				t.Fatalf("got %q want empty", got)
			}
		}},
		{"TC30_trans_cvt", func(t *testing.T) {
			if got := findTrans("cvt"); got != "CVT" {
				t.Fatalf("got %q", got)
			}
		}},
		// ---------- intents TC31-TC45 ----------
		{"TC31_buy_need_car", func(t *testing.T) {
			if !wantsBuy(norm("I need a car")) {
				t.Fatal("must route to BUY")
			}
		}},
		{"TC32_buy_looking", func(t *testing.T) {
			if !wantsBuy(norm("Looking for a car")) {
				t.Fatal("must route to BUY")
			}
		}},
		{"TC33_buy_venum", func(t *testing.T) {
			if !wantsBuy(norm("bro BMW venum")) {
				t.Fatal("must route to BUY")
			}
		}},
		{"TC34_buy_chahiye", func(t *testing.T) {
			if !wantsBuy(norm("gaadi chahiye")) {
				t.Fatal("must route to BUY")
			}
		}},
		{"TC35_buy_rejects_greeting", func(t *testing.T) {
			if wantsBuy(norm("hello")) {
				t.Fatal("hello must not route to BUY")
			}
		}},
		{"TC36_sell_detect", func(t *testing.T) {
			if !wantsSell(norm("i want to sell my car")) {
				t.Fatal("must detect SELL")
			}
		}},
		{"TC37_sell_rejects_buy", func(t *testing.T) {
			if wantsSell(norm("buy a car")) {
				t.Fatal("buy must not detect SELL")
			}
		}},
		{"TC38_exchange_detect", func(t *testing.T) {
			if !wantsExchange(norm("exchange my car")) {
				t.Fatal("must detect EXCHANGE")
			}
		}},
		{"TC39_exchange_replace", func(t *testing.T) {
			if !wantsExchange(norm("replace my car")) {
				t.Fatal("must detect EXCHANGE via replace")
			}
		}},
		{"TC40_switch_buy_to_sell", func(t *testing.T) {
			if detectIntentSwitch("actually i want to sell", "BUY") != "SELL" {
				t.Fatal("BUY->SELL switch not detected")
			}
		}},
		{"TC41_switch_buy_to_exchange", func(t *testing.T) {
			if detectIntentSwitch("i want exchange", "BUY") != "EXCHANGE" {
				t.Fatal("BUY->EXCHANGE switch not detected")
			}
		}},
		{"TC42_switch_no_false_on_fuel", func(t *testing.T) {
			if detectIntentSwitch("diesel", "BUY") != "" {
				t.Fatal("fuel answer must not switch intent")
			}
		}},
		{"TC43_switch_unknown_stays", func(t *testing.T) {
			if detectIntentSwitch("i want to buy", "UNKNOWN") != "" {
				t.Fatal("UNKNOWN must not switch")
			}
		}},
		{"TC44_switch_sell_to_buy", func(t *testing.T) {
			if detectIntentSwitch("i want to buy a car", "SELL") != "BUY" {
				t.Fatal("SELL->BUY switch not detected")
			}
		}},
		{"TC45_greeting_tamil", func(t *testing.T) {
			if !isGreeting("vanakkam") {
				t.Fatal("vanakkam must be greeting")
			}
		}},
		// ---------- BUY TC46-TC70 ----------
		{"TC46_new_hi_menu", func(t *testing.T) {
			ns, reply, _, _, _ := run("NEW", "hi", nil)
			if ns != "ASK_INTENT" || reply == "" {
				t.Fatalf("got %s %q", ns, reply)
			}
		}},
		{"TC47_new_direct_buy", func(t *testing.T) {
			ns, _, _, _, _ := run("NEW", "I want to buy a car", nil)
			if ns != "BUY_BUDGET" {
				t.Fatalf("got %s want BUY_BUDGET", ns)
			}
		}},
		{"TC48_ask_buy", func(t *testing.T) {
			ns, _, intent, _, _ := run("ASK_INTENT", "BUY", nil)
			if ns != "BUY_BUDGET" || intent != "BUY" {
				t.Fatalf("got %s %s", ns, intent)
			}
		}},
		{"TC49_ask_sell", func(t *testing.T) {
			ns, _, _, _, _ := run("ASK_INTENT", "sell", nil)
			if ns != "SELL_CAR" {
				t.Fatalf("got %s", ns)
			}
		}},
		{"TC50_ask_exchange", func(t *testing.T) {
			ns, _, intent, _, _ := run("ASK_INTENT", "exchange my car", nil)
			if ns != "EXCHANGE_CURRENT" || intent != "EXCHANGE" {
				t.Fatalf("got %s %s", ns, intent)
			}
		}},
		{"TC51_ask_garbage_stays", func(t *testing.T) {
			ns, _, _, _, _ := run("ASK_INTENT", "blah blah", nil)
			if ns != "ASK_INTENT" {
				t.Fatalf("got %s", ns)
			}
		}},
		{"TC52_budget_dontknow", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_BUDGET", "i dont know", nil)
			if ns != "BUY_BRAND" || patch["budget_unknown"] != "1" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC53_budget_dontknow_apos", func(t *testing.T) {
			ns, _, _, _, _ := run("BUY_BUDGET", "don't know", nil)
			if ns != "BUY_BRAND" {
				t.Fatalf("got %s", ns)
			}
		}},
		{"TC54_budget_range", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_BUDGET", "3-5 lakh", withData())
			if ns != "BUY_BRAND" || patch["budget_max"] != "500000" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC55_budget_rm", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_BUDGET", "RM 90,000", withData())
			if ns != "BUY_BRAND" || patch["budget_max"] != "90000" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC56_budget_garbage_reprompt", func(t *testing.T) {
			ns, reply, _, _, _ := run("BUY_BUDGET", "hello", withData())
			if ns != "BUY_BUDGET" || reply == "" {
				t.Fatalf("got %s %q", ns, reply)
			}
		}},
		{"TC57_brand_any", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_BRAND", "any", withData("budget_max", "500000"))
			if ns != "BUY_MODEL" || patch["brand"] != "ANY" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC58_brand_model_both", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_BRAND", "BMW X1", withData())
			if ns != "BUY_FUEL" || patch["brand"] == "" || patch["model"] == "" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC59_brand_single_with_budget", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_BRAND", "BMW", withData("budget_max", "500000"))
			if ns != "BUY_MODEL" || patch["brand"] == "" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC60_brand_fuel_outoforder", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_BRAND", "petrol", withData("budget_max", "500000", "brand", "BMW"))
			if ns != "BUY_MODEL" || patch["fuel"] != "PETROL" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC61_model_swift", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_MODEL", "Swift", withData("budget_max", "500000", "brand", "Maruti"))
			if ns != "BUY_FUEL" || patch["model"] == "" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC62_model_any", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_MODEL", "any", withData("budget_max", "500000", "brand", "Maruti"))
			if ns != "BUY_FUEL" || patch["model"] != "ANY" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC63_model_fuel_stays", func(t *testing.T) {
			ns, _, _, _, _ := run("BUY_MODEL", "petrol", withData("budget_max", "500000", "brand", "Maruti"))
			if ns != "BUY_MODEL" {
				t.Fatalf("fuel at model step must stay, got %s", ns)
			}
		}},
		{"TC64_fuel_diesel", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_FUEL", "Diesel", withData("budget_max", "500000", "brand", "Maruti", "model", "Swift"))
			if ns != "BUY_TRANS" || patch["fuel"] != "DIESEL" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC65_fuel_any", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_FUEL", "any", withData("budget_max", "500000", "brand", "Maruti", "model", "Swift"))
			if ns != "BUY_TRANS" || patch["fuel"] != "ANY" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC66_fuel_trans_stays", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_FUEL", "Automatic", withData())
			if ns != "BUY_FUEL" || patch["transmission"] != "AUTOMATIC" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC67_trans_auto", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_TRANS", "Automatic", withData("budget_max", "500000", "brand", "Maruti", "model", "Swift", "fuel", "DIESEL"))
			if ns != "BUY_YEAR" || patch["transmission"] != "AUTOMATIC" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC68_trans_any", func(t *testing.T) {
			ns, _, _, _, _ := run("BUY_TRANS", "any", withData("budget_max", "500000", "brand", "Maruti", "model", "Swift", "fuel", "DIESEL"))
			if ns != "BUY_YEAR" {
				t.Fatalf("got %s", ns)
			}
		}},
		{"TC69_year_number", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_YEAR", "2020", withData())
			if ns != "BUY_RESULTS" || patch["year_min"] != "2020" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC70_year_any", func(t *testing.T) {
			ns, _, _, _, _ := run("BUY_YEAR", "any", withData())
			if ns != "BUY_RESULTS" {
				t.Fatalf("got %s", ns)
			}
		}},
		// ---------- SELL TC71-TC82 ----------
		{"TC71_sell_car_split", func(t *testing.T) {
			ns, _, _, _, patch := run("SELL_CAR", "Swift VDI", withData())
			if ns != "SELL_DETAILS" || patch["sell_brand"] == "" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC72_sell_details_full", func(t *testing.T) {
			ns, _, _, _, patch := run("SELL_DETAILS", "2018, MH12AB1234, 55000km", withData())
			if ns != "SELL_SPECS" || patch["sell_year"] != "2018" || patch["sell_km"] != "55000" || patch["sell_reg"] == "" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC73_sell_details_garbage", func(t *testing.T) {
			ns, _, _, _, _ := run("SELL_DETAILS", "not a car at all", withData())
			if ns != "SELL_DETAILS" {
				t.Fatalf("got %s", ns)
			}
		}},
		{"TC74_sell_specs", func(t *testing.T) {
			ns, _, _, _, _ := run("SELL_SPECS", "Diesel, Manual, Good, Pune", withData())
			if ns != "SELL_PHOTOS" {
				t.Fatalf("got %s", ns)
			}
		}},
		{"TC75_sell_photos_text_stays", func(t *testing.T) {
			ns, _, _, _, _ := run("SELL_PHOTOS", "hello", withData())
			if ns != "SELL_PHOTOS" {
				t.Fatalf("got %s", ns)
			}
		}},
		{"TC76_sell_photos_done", func(t *testing.T) {
			ns, _, _, status, _ := run("SELL_PHOTOS", "DONE", withData())
			if ns != "DONE" || status != "FOLLOWUP" {
				t.Fatalf("got %s %s", ns, status)
			}
		}},
		{"TC77_sell_photos_cap", func(t *testing.T) {
			ns, reply, _, _, _ := run("SELL_PHOTOS", "another", withData("sell_photos", "10"))
			if ns != "SELL_PHOTOS" || !strings.Contains(reply, "DONE") {
				t.Fatalf("got %s %q", ns, reply)
			}
		}},
		{"TC78_ask_sell_phrase", func(t *testing.T) {
			ns, _, _, _, _ := run("ASK_INTENT", "sell my Swift", nil)
			if ns != "SELL_CAR" {
				t.Fatalf("got %s", ns)
			}
		}},
		{"TC79_sell_year_compat", func(t *testing.T) {
			ns, _, _, _, _ := run("SELL_YEAR", "2018, 50000km", withData())
			if ns != "SELL_SPECS" {
				t.Fatalf("got %s", ns)
			}
		}},
		{"TC80_sell_details_year_only", func(t *testing.T) {
			ns, _, _, _, _ := run("SELL_DETAILS", "2019", withData())
			if ns != "SELL_SPECS" {
				t.Fatalf("got %s", ns)
			}
		}},
		{"TC81_sell_specs_location", func(t *testing.T) {
			ns, _, _, _, patch := run("SELL_SPECS", "Petrol, Automatic, Excellent, Kuala Lumpur", withData())
			if ns != "SELL_PHOTOS" || patch["sell_location"] != "Kuala Lumpur" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC82_sell_done_followup", func(t *testing.T) {
			ns, _, _, status, _ := run("SELL_PHOTOS", "DONE", withData("sell_brand", "Maruti"))
			if ns != "DONE" || status != "FOLLOWUP" {
				t.Fatalf("got %s %s", ns, status)
			}
		}},
		// ---------- global/finance/td/done TC83-TC100 ----------
		{"TC83_results_select_number", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_RESULTS", "2", withData())
			if ns != "BUY_RESULTS" || patch["select_idx"] != "2" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC84_results_select_carN", func(t *testing.T) {
			_, _, _, _, patch := run("BUY_RESULTS", "car 3", withData())
			if patch["select_idx"] != "3" {
				t.Fatalf("got %+v", patch)
			}
		}},
		{"TC85_results_more_cars", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_RESULTS", "show me more cars", withData())
			if ns != "BUY_RESULTS" || patch["page"] != "next" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC86_results_more_photos", func(t *testing.T) {
			ns, _, _, _, patch := run("BUY_RESULTS", "more photos", withData())
			if ns != "BUY_RESULTS" || patch["more_photos"] != "1" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC87_results_not_interested", func(t *testing.T) {
			ns, _, _, status, patch := run("BUY_RESULTS", "not interested", withData())
			if ns != "DONE" || status != "LOST" || patch["interest"] != "NOT_INTERESTED" {
				t.Fatalf("got %s %s %+v", ns, status, patch)
			}
		}},
		{"TC88_results_thinking", func(t *testing.T) {
			ns, _, _, status, patch := run("BUY_RESULTS", "i will think later", withData())
			if ns != "BUY_RESULTS" || status != "FOLLOWUP" || patch["interest"] != "THINKING" {
				t.Fatalf("got %s %s %+v", ns, status, patch)
			}
		}},
		{"TC89_results_interested", func(t *testing.T) {
			_, _, _, status, patch := run("BUY_RESULTS", "interested", withData())
			if status != "QUALIFIED" || patch["interest"] != "INTERESTED" {
				t.Fatalf("got %s %+v", status, patch)
			}
		}},
		{"TC90_results_yes_confirm", func(t *testing.T) {
			_, _, _, _, patch := run("BUY_RESULTS", "yes", withData())
			if patch["interest"] != "INTERESTED" {
				t.Fatalf("got %+v", patch)
			}
		}},
		{"TC91_budget_yes_no_confirm", func(t *testing.T) {
			_, _, _, _, patch := run("BUY_BUDGET", "yes", withData())
			if patch["interest"] == "INTERESTED" {
				t.Fatalf("yes outside results must not confirm: %+v", patch)
			}
		}},
		{"TC92_results_testdrive", func(t *testing.T) {
			ns, _, _, _, _ := run("BUY_RESULTS", "book a test drive please", withData())
			if ns != "TESTDRIVE_ASK" {
				t.Fatalf("got %s", ns)
			}
		}},
		{"TC93_results_finance", func(t *testing.T) {
			ns, _, _, _, _ := run("BUY_RESULTS", "i need finance loan", withData())
			if ns != "FINANCE_INFO" {
				t.Fatalf("got %s", ns)
			}
		}},
		{"TC94_finance_capture", func(t *testing.T) {
			ns, _, _, status, patch := run("FINANCE_INFO", "RM 200000, 60 months, salaried", withData())
			if ns != "DONE" || status != "FOLLOWUP" || patch["finance_raw"] == "" {
				t.Fatalf("got %s %s %+v", ns, status, patch)
			}
		}},
		{"TC95_testdrive_capture", func(t *testing.T) {
			ns, _, _, status, patch := run("TESTDRIVE_ASK", "Swift tomorrow 10am", withData())
			if ns != "DONE" || status != "TEST_DRIVE" || patch["testdrive_raw"] == "" {
				t.Fatalf("got %s %s %+v", ns, status, patch)
			}
		}},
		{"TC96_exchange_current", func(t *testing.T) {
			ns, _, _, _, patch := run("EXCHANGE_CURRENT", "Alto 2016, 60000km", withData())
			if ns != "EXCHANGE_WANT" || patch["exchange_current"] == "" {
				t.Fatalf("got %s %+v", ns, patch)
			}
		}},
		{"TC97_exchange_want_done", func(t *testing.T) {
			ns, _, _, status, _ := run("EXCHANGE_WANT", "Creta under RM 200,000", withData())
			if ns != "DONE" || status != "FOLLOWUP" {
				t.Fatalf("got %s %s", ns, status)
			}
		}},
		{"TC98_done_buy_restart", func(t *testing.T) {
			data := withData("budget_max", "500000", "brand", "Maruti")
			ns, _, intent, _, _ := Next("DONE", "buy", data)
			if ns != "BUY_BUDGET" || intent != "BUY" {
				t.Fatalf("got %s %s", ns, intent)
			}
			if len(data) != 0 {
				t.Fatalf("stale data not wiped: %+v", data)
			}
		}},
		{"TC99_done_garbage_menu", func(t *testing.T) {
			ns, _, _, _, _ := run("DONE", "blah blah", withData())
			if ns != "ASK_INTENT" {
				t.Fatalf("got %s", ns)
			}
		}},
		{"TC100_invalid_state_recovery", func(t *testing.T) {
			ns, reply, _, status, _ := run("BROKEN_STATE", "hi", withData())
			if ns != "ASK_INTENT" || reply == "" || status != "CONTACTED" {
				t.Fatalf("got %s %q %s", ns, reply, status)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, tc.fn)
	}
	if len(cases) != 100 {
		t.Fatalf("suite must hold exactly 100 cases, got %d", len(cases))
	}
}
