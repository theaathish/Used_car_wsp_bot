package botengine

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMatchVehicles_BudgetAndType(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost/sellingbot_test?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	// Clean tables
	_, _ = tx.Exec(ctx, "DELETE FROM vehicle_matches; DELETE FROM vehicles;")

	// Insert test vehicles
	// 1. SUV under 100k (match)
	var v1ID string
	err = tx.QueryRow(ctx, `INSERT INTO vehicles(make, model, year, price, fuel, transmission, description, status)
		VALUES ('Toyota', 'RAV4 SUV', 2020, 80000, 'Petrol', 'Automatic', 'Compact SUV', 'AVAILABLE') RETURNING id`).Scan(&v1ID)
	if err != nil {
		t.Fatalf("insert v1: %v", err)
	}

	// 2. Sedan under 100k (wrong type)
	_, err = tx.Exec(ctx, `INSERT INTO vehicles(make, model, year, price, fuel, transmission, description, status)
		VALUES ('Honda', 'Civic Sedan', 2021, 75000, 'Petrol', 'Automatic', 'Sedan', 'AVAILABLE')`)
	if err != nil {
		t.Fatalf("insert v2: %v", err)
	}

	// 3. SUV over 100k (over budget)
	_, err = tx.Exec(ctx, `INSERT INTO vehicles(make, model, year, price, fuel, transmission, description, status)
		VALUES ('BMW', 'X5 SUV', 2022, 150000, 'Diesel', 'Automatic', 'Luxury SUV', 'AVAILABLE')`)
	if err != nil {
		t.Fatalf("insert v3: %v", err)
	}

	// Insert customer and lead
	var custID, leadID string
	_ = tx.QueryRow(ctx, `INSERT INTO customers(phone, name) VALUES ('60123456788', 'Buyer') RETURNING id`).Scan(&custID)
	_ = tx.QueryRow(ctx, `INSERT INTO leads(customer_id, status) VALUES ($1, 'NEW') RETURNING id`, custID).Scan(&leadID)

	data := map[string]any{
		"vehicle_type": "SUV",
		"budget_max":   "100000",
	}

	matches, err := MatchVehicles(ctx, tx, leadID, data)
	if err != nil {
		t.Fatalf("match vehicles: %v", err)
	}

	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 match, got %d", len(matches))
	}
	if matches[0].ID != v1ID {
		t.Fatalf("expected match v1 (%s), got %s (%s %s)", v1ID, matches[0].ID, matches[0].Make, matches[0].Model)
	}
}
