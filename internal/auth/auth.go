package auth

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// loginLimiter blocks brute force (AUTH-111): >10 failures in 5 min => 429.
var loginLimiter = struct {
	sync.Mutex
	fails map[string][]time.Time
	ban   map[string]time.Time
}{fails: map[string][]time.Time{}, ban: map[string]time.Time{}}

func clientIP(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

func loginBlocked(ip string) bool {
	loginLimiter.Lock()
	defer loginLimiter.Unlock()
	if until, ok := loginLimiter.ban[ip]; ok {
		if time.Now().Before(until) {
			return true
		}
		delete(loginLimiter.ban, ip)
	}
	return false
}

func loginFail(ip string) {
	loginLimiter.Lock()
	defer loginLimiter.Unlock()
	now := time.Now()
	keep := loginLimiter.fails[ip][:0]
	for _, t := range loginLimiter.fails[ip] {
		if now.Sub(t) < 5*time.Minute {
			keep = append(keep, t)
		}
	}
	keep = append(keep, now)
	loginLimiter.fails[ip] = keep
	if len(keep) >= 10 {
		loginLimiter.ban[ip] = now.Add(5 * time.Minute)
		delete(loginLimiter.fails, ip)
	}
}

func loginOK(ip string) {
	loginLimiter.Lock()
	defer loginLimiter.Unlock()
	delete(loginLimiter.fails, ip)
}

type Claims struct {
	UserID string `json:"uid"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

func Hash(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func Check(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func Sign(secret, uid, email, role string) (string, error) {
	cl := Claims{UserID: uid, Email: email, Role: role, RegisteredClaims: jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, cl)
	return t.SignedString([]byte(secret))
}

func Verify(secret, token string) (*Claims, error) {
	t, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	cl, ok := t.Claims.(*Claims)
	if !ok || !t.Valid {
		return nil, jwt.ErrTokenInvalidClaims
	}
	return cl, nil
}

type ctxKey string

const userKey ctxKey = "user"

func Middleware(secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		cl, err := Verify(secret, strings.TrimPrefix(h, "Bearer "))
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, cl)))
	})
}

// RequireRole enforces JWT plus one of the given roles (AUTH-004: 403 on wrong role).
func RequireRole(secret string, roles []string, next http.Handler) http.Handler {
	return Middleware(secret, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cl, _ := r.Context().Value(userKey).(*Claims)
		if cl == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		for _, ro := range roles {
			if cl.Role == ro {
				next.ServeHTTP(w, r)
				return
			}
		}
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
	}))
}

// Current returns the caller's claims or nil.
func Current(r *http.Request) *Claims {
	cl, _ := r.Context().Value(userKey).(*Claims)
	return cl
}

func LoginHandler(pool *pgxpool.Pool, secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if loginBlocked(ip) {
			http.Error(w, `{"error":"too many attempts, try later"}`, http.StatusTooManyRequests)
			return
		}
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}
		var id, hash, role string
		err := pool.QueryRow(r.Context(), `SELECT id::text,password_hash,role FROM users WHERE email=$1`, in.Email).Scan(&id, &hash, &role)
		if err != nil || !Check(hash, in.Password) {
			loginFail(ip)
			http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
			return
		}
		loginOK(ip)
		tok, err := Sign(secret, id, in.Email, role)
		if err != nil {
			http.Error(w, `{"error":"token failed"}`, http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"token": tok, "email": in.Email, "role": role})
	}
}

func MeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cl, _ := r.Context().Value(userKey).(*Claims)
		if cl == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"uid": cl.UserID, "email": cl.Email, "role": cl.Role})
	}
}
