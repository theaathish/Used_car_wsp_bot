package whatsapp

// Session timeout + "hi"-as-init regression. Your 3:17 "hi" resumed a stale
// 3:00 BUY_RESULTS search ("Reply more cars...") instead of the menu.
// Bare greetings now restart the menu; chats idle 30+ min auto-close.
// Run: go test -run TestSessionGreetingInit -v

import (
	"testing"
	"time"
)

func TestSessionGreetingInit(t *testing.T) {
	greet := map[string]bool{
		"hi": true, "Hi": true, "hi!": true, "  hello  ": true,
		"HEY": true, "vanakkam": true, "Vanakkam!": true,
		"good morning": true, "h": true,
		"hello i want bmw": false, // longer sentences flow through
		"hi, I need a car": false,
		"hi menu":          false, // handled by the menu-word path as before
		"ok":               false, "": false, "???": false, "1": false, "0": false,
		"yes": false, "more cars": false, "done bro": false,
	}
	for in, want := range greet {
		if got := isGreetingOnly(in); got != want {
			t.Errorf("isGreetingOnly(%q) = %v; want %v", in, got, want)
		}
	}

	now := time.Now()
	if !isSessionExpired(now.Add(-31*time.Minute), now) {
		t.Error("31min idle must expire")
	}
	if isSessionExpired(now.Add(-5*time.Minute), now) {
		t.Error("5min idle must stay live")
	}
	if isSessionExpired(now.Add(-29*time.Minute), now) {
		t.Error("29min idle must stay live")
	}
	if isSessionExpired(time.Time{}, now) {
		t.Error("zero time must never expire")
	}
	if sessionTimeout != 30*time.Minute {
		t.Errorf("timeout = %s; want 30m", sessionTimeout)
	}
}

func TestSendCooldown(t *testing.T) {
	now := time.Now()
	if got := sendCooldown(time.Time{}, now); got != 0 {
		t.Fatalf("zero last: got %s want 0", got)
	}
	if got := sendCooldown(now.Add(-1*time.Second), now); got < 900*time.Millisecond || got > 1100*time.Millisecond {
		t.Fatalf("1s ago: got %s want ~1s", got)
	}
	if got := sendCooldown(now.Add(-3*time.Second), now); got != 0 {
		t.Fatalf("3s ago: got %s want 0", got)
	}
	if got := sendCooldown(now.Add(-100*time.Millisecond), now); got < 1800*time.Millisecond {
		t.Fatalf("burst: got %s want ~1.9s", got)
	}
}
