package whatsapp

import (
	"testing"
	"time"
)

func TestParseTestDrive(t *testing.T) {
	now := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC) // Fri 14:30 IST
	cands := []tdVehicle{{id: "v1", make: "Maruti", model: "Swift"}, {id: "v2", make: "Hyundai", model: "Creta"}}

	cases := []struct {
		name     string
		body     string
		vid      string
		iso      string // expected UTC prefix
		needVeh  bool
		needDate bool
		needTime bool
		past     bool
	}{
		{"full", "Swift, tomorrow 10am", "v1", "2026-09-12T04:30:00Z", false, false, false, false},
		{"leadnum", "2, Saturday 4pm", "v2", "2026-09-12T10:30:00Z", false, false, false, false},
		{"num-select", "1, tomorrow 10am", "v1", "2026-09-12T04:30:00Z", false, false, false, false},
		{"today-eve", "Creta today 5pm", "v2", "2026-09-11T11:30:00Z", false, false, false, false},
		{"no-time", "Swift tomorrow", "v1", "", false, false, true, false},
		{"no-date", "Swift 10am", "v1", "", false, true, false, false},
		{"no-car", "tomorrow 10am", "", "", true, false, false, false},
		{"weekday", "Swift Monday 11am", "v1", "2026-09-14T05:30:00Z", false, false, false, false},
		{"past", "Swift today 6am", "v1", "", false, false, false, true},
		{"24h", "Swift tomorrow 15:00", "v1", "2026-09-12T09:30:00Z", false, false, false, false},
		{"slash-date", "Swift 12/09 10am", "v1", "2026-09-12T04:30:00Z", false, false, false, false},
	}
	for _, tc := range cases {
		r := parseTestDrive(tc.body, cands, now)
		if r.vehicleID != tc.vid || r.needVehicle != tc.needVeh || r.needDate != tc.needDate || r.needTime != tc.needTime || r.past != tc.past {
			t.Errorf("%s: %+v", tc.name, r)
			continue
		}
		if tc.iso != "" && r.at.UTC().Format(time.RFC3339) != tc.iso {
			t.Errorf("%s: at=%s want %s", tc.name, r.at.UTC().Format(time.RFC3339), tc.iso)
		}
	}

	// single candidate auto-picked even when unnamed
	r := parseTestDrive("tomorrow 10am", cands[:1], now)
	if r.vehicleID != "v1" || !r.at.Equal(time.Date(2026, 9, 12, 4, 30, 0, 0, time.UTC)) {
		t.Errorf("single-candidate: %+v", r)
	}
	// "2" alone is a selection, not a time
	r = parseTestDrive("2", cands, now)
	if r.vehicleID != "v2" || !r.needDate {
		t.Errorf("bare number: %+v", r)
	}
}
