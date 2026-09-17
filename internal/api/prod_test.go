package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/leads", nil))
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "SAMEORIGIN",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q; want %q", k, got, want)
		}
	}
}

func TestRecoverer(t *testing.T) {
	h := recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/vehicles", nil)) // must not propagate
	if rec.Code != 500 {
		t.Fatalf("code = %d; want 500", rec.Code)
	}
}

func TestIPLimiter(t *testing.T) {
	l := newIPLimiter(3, time.Minute)
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !l.allow("1.2.3.4", now) {
			t.Fatalf("hit %d blocked", i)
		}
	}
	if l.allow("1.2.3.4", now) {
		t.Fatal("4th hit in window must block")
	}
	if !l.allow("5.6.7.8", now) {
		t.Fatal("other IP must pass")
	}
	if !l.allow("1.2.3.4", now.Add(61*time.Second)) {
		t.Fatal("next window must pass")
	}
}

func TestRateLimitExemptsHealth(t *testing.T) {
	l := newIPLimiter(1, time.Minute)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	h := rateLimit(l, ok)
	srv := httptest.NewServer(h)
	defer srv.Close()
	get := func(p string) int {
		r, _ := http.Get(srv.URL + p)
		defer r.Body.Close()
		return r.StatusCode
	}
	if get("/api/health") != 200 || get("/api/health") != 200 || get("/api/metrics") != 200 {
		t.Fatal("health/metrics must never limit")
	}
	if get("/api/leads") != 200 || get("/api/leads") == 200 {
		t.Fatal("second guarded hit must 429")
	}
}

func TestAccessLogPassthrough(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) })
	for _, p := range []string{"/x", "/api/health"} {
		rec := httptest.NewRecorder()
		accessLog(inner).ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 201 {
			t.Fatalf("%s passthrough broken: %d", p, rec.Code)
		}
	}
}
