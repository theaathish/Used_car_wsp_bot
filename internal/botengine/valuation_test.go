package botengine

import (
	"testing"
)

func TestEstimateVehicleValuation(t *testing.T) {
	// 1. Luxury brand (BMW)
	bmwVal := EstimateVehicleValuation("BMW", "X1", 2023, 25000, "Excellent", 0)
	if bmwVal < 1500000 || bmwVal > 3500000 {
		t.Fatalf("unexpected BMW valuation: %d", bmwVal)
	}

	// 2. Mainstream brand (Honda)
	hondaVal := EstimateVehicleValuation("Honda", "City", 2020, 50000, "Good", 0)
	if hondaVal < 400000 || hondaVal > 850000 {
		t.Fatalf("unexpected Honda valuation: %d", hondaVal)
	}

	// 3. High mileage and poor condition
	poorVal := EstimateVehicleValuation("Maruti", "Swift", 2015, 120000, "Poor", 0)
	if poorVal < 75000 || poorVal > 350000 {
		t.Fatalf("unexpected poor condition valuation: %d", poorVal)
	}

	// 4. Expected price calibration
	calibratedVal := EstimateVehicleValuation("Toyota", "Vios", 2021, 30000, "Good", 500000)
	if calibratedVal < 300000 || calibratedVal > 700000 {
		t.Fatalf("unexpected calibrated valuation: %d", calibratedVal)
	}
}

func TestFormatPrice(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{500, "500"},
		{5000, "5,000"},
		{50000, "50,000"},
		{500000, "5,00,000"},
		{1500000, "15,00,000"},
		{12500000, "1,25,00,000"},
	}
	for _, tc := range cases {
		got := FormatPrice(tc.in)
		if got != tc.want {
			t.Errorf("FormatPrice(%d) = %s, want %s", tc.in, got, tc.want)
		}
	}
}
