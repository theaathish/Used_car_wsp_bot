package whatsapp

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/context"
)

// Failure accounting must never deadlock and must never escalate: sustained
// failures and flap churn keep the worker "connecting" with the error
// visible, so it can always recover on its own. Only a server-declared
// logout (not covered here) parks it. Needs DATABASE_URL (migrated); skips
// otherwise.
func TestFailureAccounting(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	w := New(pool, t.TempDir(), true, "")
	done := make(chan map[string]any, 1)
	go func() {
		for i := 0; i < 16; i++ {
			w.noteFailure(errors.New("dial tcp: connection refused"))
		}
		for i := 0; i < 8; i++ {
			w.noteFlap("socket dropped after 3s (test)")
		}
		done <- w.Status()
	}()
	select {
	case st := <-done:
		if st["status"] != "connecting" {
			t.Fatalf("want connecting (never expired), got %v", st)
		}
		if st["fail_count"] != 16 {
			t.Fatalf("want 16 fails, got %v", st)
		}
		if st["cycles_10m"] != 8 {
			t.Fatalf("want 8 cycles, got %v", st)
		}
		if st["last_error"] == "" {
			t.Fatal("want last_error set")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("noteFailure deadlocked")
	}
	w.noteSuccess("1@test")
	st := w.Status()
	if st["status"] != "connected" || st["fail_count"] != 0 || st["last_error"] != "" {
		t.Fatalf("success did not reset: %v", st)
	}
}

// Flap backoff must rise with churn (storm protection) and stay instant
// when healthy. Pure function — no DB needed.
func TestFlapBackoff(t *testing.T) {
	cases := []struct {
		cycles int
		want   time.Duration
	}{
		{0, 3 * time.Second},
		{2, 3 * time.Second},
		{3, 15 * time.Second},
		{5, 15 * time.Second},
		{6, time.Minute},
		{10, time.Minute},
		{11, 5 * time.Minute},
		{19, 5 * time.Minute},
	}
	for _, c := range cases {
		if got := flapBackoff(c.cycles); got != c.want {
			t.Fatalf("flapBackoff(%d) = %s, want %s", c.cycles, got, c.want)
		}
	}
}

// A worker without a pool (standalone / unit context) must fail open as
// leader so single-process behaviour is unchanged.
func TestClaimLeadershipNoPool(t *testing.T) {
	w := New(nil, t.TempDir(), true, "")
	if !w.claimLeadership(context.Background()) {
		t.Fatal("nil-pool worker must be leader")
	}
	st := w.Status()
	if st["leader"] != true || st["instance"] == "" {
		t.Fatalf("want leader+instance in status, got %v", st)
	}
}
