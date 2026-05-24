// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// loginMaxAttempts is the number of failed login attempts allowed per IP within loginWindow.
	loginMaxAttempts = 5
	// loginWindow is the sliding window duration for login rate limiting.
	loginWindow = 15 * time.Minute
)

// loginRateLimiter tracks failed login attempts per IP using a sliding window counter.
// Stale attempts older than loginWindow are pruned inline on every call to isBlocked
// and recordFailure. A periodic eviction pass (every 256 calls) deletes map entries
// whose entire window has expired, bounding memory growth under many distinct IPs.
// No background goroutine is used, so instances are safe to create in tests.
type loginRateLimiter struct {
	mu          sync.Mutex
	attempts    map[string][]time.Time
	maxAttempts int
	window      time.Duration
	callCount   int // for periodic stale-key eviction
}

func newLoginRateLimiter() *loginRateLimiter {
	return &loginRateLimiter{
		attempts:    make(map[string][]time.Time),
		maxAttempts: loginMaxAttempts,
		window:      loginWindow,
	}
}

// isBlocked returns true if ip has exceeded the failure limit within the window.
// When true it also returns the time until the oldest in-window failure expires.
func (l *loginRateLimiter) isBlocked(ip string) (bool, time.Duration) {
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	// Prune stale entries for this IP.
	prev := l.attempts[ip]
	valid := prev[:0]
	for _, t := range prev {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	if len(valid) == 0 {
		delete(l.attempts, ip)
	} else {
		l.attempts[ip] = valid
	}

	// Periodically evict fully-expired keys from other IPs to bound map growth.
	l.callCount++
	if l.callCount&0xFF == 0 {
		for k, v := range l.attempts {
			if len(v) > 0 && !v[len(v)-1].After(cutoff) {
				delete(l.attempts, k)
			}
		}
	}

	if len(valid) >= l.maxAttempts {
		retryAfter := max(valid[0].Add(l.window).Sub(now), 0)
		return true, retryAfter
	}
	return false, 0
}

// recordFailure records one failed login attempt for ip.
func (l *loginRateLimiter) recordFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[ip] = append(l.attempts[ip], time.Now())
}

// LoginRateLimitMiddleware returns chi-compatible middleware that rate-limits
// failed POST attempts to loginPath based on the client's IP address. Only
// HTTP 401 responses from downstream are counted as failures; successful logins
// do not consume the brute-force budget, preventing lockout of shared egress
// IPs (office NAT, VPN).
//
// The rate-limit key is derived from the raw TCP source address (RemoteAddr),
// which clients cannot spoof. X-Forwarded-For/X-Real-IP are trusted only when
// RemoteAddr is a loopback address, indicating a same-host reverse proxy.
//
// Loopback exemption: when the resolved key is a loopback address no failure
// is recorded. This exempts local dev and E2E test traffic that originates
// directly from the same machine.
func LoginRateLimitMiddleware(loginPath string) func(http.Handler) http.Handler {
	limiter := newLoginRateLimiter()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == loginPath {
				ip := rateLimitKey(r)
				if !isLoopbackIP(ip) {
					if blocked, retryAfter := limiter.isBlocked(ip); blocked {
						secs := max(int(math.Ceil(retryAfter.Seconds())), 1)
						w.Header().Set("Retry-After", strconv.Itoa(secs))
						writeAuthError(w, http.StatusTooManyRequests, "rate_limited", "Too many login attempts. Please try again later.")
						return
					}
					rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
					next.ServeHTTP(rec, r)
					if rec.status == http.StatusUnauthorized {
						limiter.recordFailure(ip)
					}
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// rateLimitKey returns the IP address to use as the rate-limit bucket key.
// It uses RemoteAddr (the actual TCP connection source, which clients cannot
// forge) as the primary key. X-Forwarded-For and X-Real-IP are consulted only
// when RemoteAddr is a loopback address, indicating a same-host reverse proxy
// that has set the header on behalf of an external client.
func rateLimitKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			raw := xff
			if before, _, ok := strings.Cut(xff, ","); ok {
				raw = before
			}
			return stripPort(strings.TrimSpace(raw))
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return stripPort(strings.TrimSpace(xri))
		}
	}
	return host
}

// isLoopbackIP reports whether addr is a loopback address.
func isLoopbackIP(addr string) bool {
	ip := net.ParseIP(addr)
	return ip != nil && ip.IsLoopback()
}

// statusRecorder wraps http.ResponseWriter to capture the HTTP status code
// written by the downstream handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
