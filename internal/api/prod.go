package api

// Production middleware: security headers, panic recovery, per-IP rate
// limiting and access logging. All pure/std-only and covered in prod_test.

import (
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// securityHeaders adds baseline browser protections. SAMEORIGIN (not DENY)
// so the admin still works if served behind a same-origin frame.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// recoverer turns a handler panic into a 500 instead of killing the whole
// single-service process (one bad request must never drop WhatsApp + API).
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[api] panic %s %s: %v", r.Method, r.URL.Path, rec)
				http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ipLimiter is a fixed-window per-IP counter. Zero value is unusable;
// build with newIPLimiter.
type ipLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]*ipWindow
}

type ipWindow struct {
	n     int
	reset time.Time
}

func newIPLimiter(limit int, window time.Duration) *ipLimiter {
	return &ipLimiter{limit: limit, window: window, hits: map[string]*ipWindow{}}
}

// allow reports whether ip may proceed, counting this hit.
func (l *ipLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.hits[ip]
	if !ok || !now.Before(w.reset) {
		l.hits[ip] = &ipWindow{n: 1, reset: now.Add(l.window)}
		return true
	}
	w.n++
	return w.n <= l.limit
}

func ipOf(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// rateLimit guards /api/* (600 req/min/IP default). Health and metrics are
// exempt so Railway healthchecks and soak monitors can never 429.
func rateLimit(l *ipLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" || r.URL.Path == "/api/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		if !l.allow(ipOf(r), time.Now()) {
			http.Error(w, `{"error":"too many requests"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// accessLog records method/path/status/latency at most once per request.
// Health checks are skipped: Railway polls them every few seconds.
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		sw := &statusWriter{ResponseWriter: w, status: 200}
		start := time.Now()
		next.ServeHTTP(sw, r)
		log.Printf("[api] %s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	})
}
