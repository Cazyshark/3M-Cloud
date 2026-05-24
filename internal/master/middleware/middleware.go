package middleware

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/multi-ops/internal/master/auth"
)

type contextKey string

const claimsKey contextKey = "claims"

// ContextWithClaims stores JWT claims in context
func ContextWithClaims(ctx context.Context, claims *auth.Claims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

// ClaimsFromContext extracts JWT claims from context
func ClaimsFromContext(ctx context.Context) *auth.Claims {
	if c, ok := ctx.Value(claimsKey).(*auth.Claims); ok {
		return c
	}
	return nil
}

// RequireAuth checks for a valid JWT
func RequireAuth(jwt *auth.JWTManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ""
			if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}
			if token == "" {
				if c, err := r.Cookie("token"); err == nil {
					token = c.Value
				}
			}
			if token == "" {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			claims, err := jwt.ValidateToken(token)
			if err != nil {
				// Generic error — never leak JWT validation details
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			ctx := ContextWithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole checks that the authenticated user has one of the allowed roles
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFromContext(r.Context())
			if claims == nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if !allowed[claims.Role] {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimiter provides per-IP rate limiting
type RateLimiter struct {
	limits map[string]*limiterEntry
	mu     sync.Mutex
	rate   int
	window time.Duration
}

type limiterEntry struct {
	count   int
	resetAt time.Time
}

func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		limits: make(map[string]*limiterEntry),
		rate:   rate,
		window: window,
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ExtractIP(r)
		if !rl.allow(ip) {
			http.Error(w, `{"error":"too many requests"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	entry, ok := rl.limits[ip]
	if !ok || now.After(entry.resetAt) {
		rl.limits[ip] = &limiterEntry{count: 1, resetAt: now.Add(rl.window)}
		return true
	}
	entry.count++
	return entry.count <= rl.rate
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, entry := range rl.limits {
			if now.After(entry.resetAt) {
				delete(rl.limits, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// NewLoginRateLimiter creates a strict rate limiter for login attempts (5/min per IP)
func NewLoginRateLimiter() *RateLimiter {
	return NewRateLimiter(5, time.Minute)
}

// SecurityHeaders adds security-related HTTP headers
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; connect-src 'self' ws: wss:; font-src 'self'; img-src 'self' data:;")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// IPWhitelist restricts access to allowed IPs
type IPWhitelist struct {
	allowed map[string]bool
	enabled bool
}

func NewIPWhitelist(ips []string) *IPWhitelist {
	wl := &IPWhitelist{
		allowed: make(map[string]bool),
		enabled: len(ips) > 0,
	}
	for _, ip := range ips {
		wl.allowed[ip] = true
	}
	return wl
}

func (wl *IPWhitelist) Middleware(next http.Handler) http.Handler {
	if !wl.enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ExtractIP(r)
		if !wl.allowed[ip] {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AuditLogger logs all requests
type AuditLogger struct {
	excludePaths map[string]bool
}

func NewAuditLogger() *AuditLogger {
	return &AuditLogger{
		excludePaths: map[string]bool{
			"/favicon.ico": true,
		},
	}
}

func (al *AuditLogger) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !al.excludePaths[r.URL.Path] {
			log.Printf("[AUDIT] %s %s ip=%s", r.Method, r.URL.Path, ExtractIP(r))
		}
		next.ServeHTTP(w, r)
	})
}

// ExtractIP gets the real client IP from request
func ExtractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Only trust X-Forwarded-For when connection is from a known private address
		if isPrivateAddr(r.RemoteAddr) {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isPrivateAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback()
}
