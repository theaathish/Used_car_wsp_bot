package whatsapp

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// WA-004: same provider message id processed exactly once.
// Needs DATABASE_URL (migrated); skips otherwise so unit runs stay hermetic.
func TestInboundDedup(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	w := New(pool, t.TempDir(), false)
	first, err := w.HandleInbound(ctx, "918888888888", "Dup", "hi", "wa-msg-dedup-1")
	if err != nil || first == "" {
		t.Fatalf("first delivery: %q %v", first, err)
	}
	second, err := w.HandleInbound(ctx, "918888888888", "Dup", "hi", "wa-msg-dedup-1")
	if err != nil || second != "" {
		t.Fatalf("duplicate must be no-op: %q %v", second, err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM leads l JOIN customers c ON c.id=l.customer_id WHERE c.phone='918888888888'`).Scan(&n)
	if n != 1 {
		t.Fatalf("exactly one lead, got %d", n)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM customers WHERE phone='918888888888'`)
	_, _ = pool.Exec(ctx, `DELETE FROM processed_messages WHERE wa_msg_id='wa-msg-dedup-1'`)
}
