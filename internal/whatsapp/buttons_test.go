package whatsapp

// QR-friendly numbered menus ("button mathiri"): 1/2/3 + 0=any.
// Real WhatsApp interactive buttons can't be sent via whatsmeow companion
// (deprecated by WhatsApp — Cloud API only), so the bot uses tap-style
// numbers. Inbound button/list taps (if any) arrive as text via messageText
// and flow through the same matching. Run: go test -run TestButtonChoices -v

import "testing"

func TestButtonChoices(t *testing.T) {
	run := func(state, body string, data map[string]string) (string, map[string]string) {
		if data == nil {
			data = map[string]string{}
		}
		ns, _, _, _, patch := Next(state, body, data)
		return ns, patch
	}
	mk := func(kv ...string) map[string]string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}

	cases := []struct {
		name      string
		state     string
		body      string
		data      map[string]string
		wantState string
		wantKey   string
		wantVal   string
	}{
		{"menu_1_buy", "ASK_INTENT", "1", nil, "BUY_BUDGET", "", ""},
		{"menu_2_sell", "ASK_INTENT", "2", nil, "SELL_CAR", "", ""},
		{"menu_3_exchange", "ASK_INTENT", "3", nil, "EXCHANGE_CURRENT", "", ""},
		{"menu_1_dot", "ASK_INTENT", "1.", nil, "BUY_BUDGET", "", ""},
		{"menu_option2", "ASK_INTENT", "option 2", nil, "SELL_CAR", "", ""},
		{"menu_word_still_works", "ASK_INTENT", "buy", nil, "BUY_BUDGET", "", ""},
		{"fuel_1_petrol", "BUY_FUEL", "1", mk("budget_max", "500000", "brand", "M", "model", "S"), "BUY_TRANS", "fuel", "PETROL"},
		{"fuel_2_diesel", "BUY_FUEL", "2", mk("budget_max", "500000", "brand", "M", "model", "S"), "BUY_TRANS", "fuel", "DIESEL"},
		{"fuel_0_any", "BUY_FUEL", "0", mk("budget_max", "500000", "brand", "M", "model", "S"), "BUY_TRANS", "fuel", "ANY"},
		{"fuel_word_still_works", "BUY_FUEL", "diesel", mk("budget_max", "500000", "brand", "M", "model", "S"), "BUY_TRANS", "fuel", "DIESEL"},
		{"trans_1_manual", "BUY_TRANS", "1", mk("budget_max", "1", "brand", "M", "model", "S", "fuel", "DIESEL"), "BUY_YEAR", "transmission", "MANUAL"},
		{"trans_2_auto", "BUY_TRANS", "2", mk("budget_max", "1", "brand", "M", "model", "S", "fuel", "DIESEL"), "BUY_YEAR", "transmission", "AUTOMATIC"},
		{"trans_0_any", "BUY_TRANS", "0", mk("budget_max", "1", "brand", "M", "model", "S", "fuel", "DIESEL"), "BUY_YEAR", "transmission", "ANY"},
		{"brand_0_any", "BUY_BRAND", "0", mk("budget_max", "500000"), "BUY_MODEL", "brand", "ANY"},
		{"model_0_any", "BUY_MODEL", "0", mk("budget_max", "500000", "brand", "M"), "BUY_FUEL", "model", "ANY"},
		{"budget_0_skip", "BUY_BUDGET", "0", nil, "BUY_BRAND", "budget_unknown", "1"},
		{"sellphoto_1_done", "SELL_PHOTOS", "1", nil, "SELL_INSPECTION", "", ""},
		{"sellphoto_done_still_works", "SELL_PHOTOS", "DONE", nil, "SELL_INSPECTION", "", ""},
		{"sellinspection_books", "SELL_INSPECTION", "tomorrow 11am", nil, "DONE", "inspection_raw", "tomorrow 11am"},
		{"results_number_still_selects_car", "BUY_RESULTS", "2", nil, "BUY_RESULTS", "select_idx", "2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns, patch := run(tc.state, tc.body, tc.data)
			if ns != tc.wantState {
				t.Fatalf("state: got %s want %s (patch %+v)", ns, tc.wantState, patch)
			}
			if tc.wantKey != "" && patch[tc.wantKey] != tc.wantVal {
				t.Fatalf("patch[%s]: got %q want %q (full %+v)", tc.wantKey, patch[tc.wantKey], tc.wantVal, patch)
			}
		})
	}
}
