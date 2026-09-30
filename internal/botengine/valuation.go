package botengine

import (
	"strings"
)

// EstimateVehicleValuation estimates a vehicle's market value in INR based on
// make, model, year, km, condition, and optional expected price.
func EstimateVehicleValuation(brand, model string, year, km int, condition string, expectedPrice int) int {
	brandLower := strings.ToLower(strings.TrimSpace(brand))
	modelLower := strings.ToLower(strings.TrimSpace(model))

	// 1. Establish base value based on brand/model tier
	basePrice := 600000 // default ~6 Lakhs
	switch {
	case strings.Contains(modelLower, "m4") || strings.Contains(modelLower, "m3") || strings.Contains(modelLower, "m5") ||
		strings.Contains(modelLower, "amg") || strings.Contains(modelLower, "911") || strings.Contains(modelLower, "gt-r"):
		basePrice = 7500000
	case strings.Contains(brandLower, "bmw") || strings.Contains(brandLower, "mercedes") || strings.Contains(brandLower, "audi") ||
		strings.Contains(brandLower, "jaguar") || strings.Contains(brandLower, "land rover") || strings.Contains(brandLower, "porsche"):
		basePrice = 2800000
	case strings.Contains(brandLower, "toyota") && (strings.Contains(modelLower, "fortuner") || strings.Contains(modelLower, "innova") || strings.Contains(modelLower, "camry")):
		basePrice = 1800000
	case strings.Contains(brandLower, "jeep") || strings.Contains(brandLower, "skoda") || strings.Contains(brandLower, "volkswagen") || strings.Contains(brandLower, "mg"):
		basePrice = 1200000
	case strings.Contains(brandLower, "hyundai") || strings.Contains(brandLower, "kia") || strings.Contains(brandLower, "tata") || strings.Contains(brandLower, "mahindra") || strings.Contains(brandLower, "honda"):
		basePrice = 900000
	case strings.Contains(brandLower, "maruti") || strings.Contains(brandLower, "suzuki") || strings.Contains(brandLower, "renault") || strings.Contains(brandLower, "nissan") || strings.Contains(brandLower, "proton") || strings.Contains(brandLower, "perodua"):
		basePrice = 650000
	}

	// 2. Depreciation based on age (current year assumed ~2026)
	currentYear := 2026
	if year <= 1990 || year > currentYear {
		year = currentYear - 4 // default ~4 years old
	}
	age := currentYear - year
	if age < 0 {
		age = 0
	}
	depreciated := float64(basePrice)
	for i := 0; i < age && i < 15; i++ {
		depreciated *= 0.88 // 12% annual depreciation
	}

	// 3. Mileage adjustment (baseline 12,000 km per year)
	expectedKm := age * 12000
	if km > 0 {
		kmDiff := km - expectedKm
		if kmDiff > 0 {
			depreciated -= float64(kmDiff) * 0.80
		} else {
			depreciated += float64(-kmDiff) * 0.50
		}
	}

	// 4. Condition factor
	condLower := strings.ToLower(condition)
	switch {
	case strings.Contains(condLower, "excellent") || strings.Contains(condLower, "mint") || strings.Contains(condLower, "like new"):
		depreciated *= 1.10
	case strings.Contains(condLower, "poor") || strings.Contains(condLower, "rough") || strings.Contains(condLower, "damaged"):
		depreciated *= 0.75
	case strings.Contains(condLower, "fair") || strings.Contains(condLower, "average"):
		depreciated *= 0.90
	default:
		// good
		depreciated *= 1.00
	}

	finalVal := int(depreciated)

	// If customer specified a reasonable expected price (> 0), blend
	if expectedPrice > 50000 {
		if expectedPrice >= finalVal*7/10 && expectedPrice <= finalVal*13/10 {
			finalVal = int(float64(finalVal)*0.6 + float64(expectedPrice)*0.4)
		}
	}

	// Round to nearest Rs 5,000
	finalVal = (finalVal / 5000) * 5000

	// Sanity bounds
	if finalVal < 75000 {
		finalVal = 75000
	}
	if finalVal > 95000000 {
		finalVal = 95000000
	}

	return finalVal
}

// FormatPrice formats an integer price into Indian comma separated format (e.g. 15,00,000).
func FormatPrice(price int) string {
	return formatPrice(price)
}
