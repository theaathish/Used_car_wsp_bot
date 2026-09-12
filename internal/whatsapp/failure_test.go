package whatsapp

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/context"
)

// Failure accounting must never deadlock and must flip to "expired" after
// sustained failures. Needs DATABASE_URL (migrated); skips otherwise.
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
		done <- w.Status()
	}()
	select {
	case st := <-done:
		if st["status"] != "expired" {
			t.Fatalf("want expired, got %v", st)
		}
		if st["fail_count"] != 16 {
			t.Fatalf("want 16 fails, got %v", st)
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
