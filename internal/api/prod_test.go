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
		r, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("http.Get(%s) failed: %v", p, err)
			return 0
		}
		defer r.Body.Close()
		return r.StatusCode
	}
	if h1 := get("/api/health"); h1 != 200 {
		t.Fatalf("first health hit must be 200, got %d", h1)
	}
	if h2 := get("/api/health"); h2 != 200 {
		t.Fatalf("second health hit must not be limited, got %d", h2)
	}
	if m := get("/api/metrics"); m != 200 {
		t.Fatalf("metrics hit must be 200, got %d", m)
	}
	if l1 := get("/api/leads"); l1 != 200 {
		t.Fatalf("first guarded hit must be 200, got %d", l1)
	}
	if l2 := get("/api/leads"); l2 == 200 {
		t.Fatalf("second guarded hit must 429, got %d", l2)
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
