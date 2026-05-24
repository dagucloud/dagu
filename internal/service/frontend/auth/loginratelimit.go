// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	// loginMaxAttempts is the number of login attempts allowed per IP within loginWindow.
	loginMaxAttempts = 5
	// loginWindow is the sliding window duration for login rate limiting.
	loginWindow = 15 * time.Minute
)

// loginRateLimiter tracks login attempts per IP using a sliding window counter.
// Stale attempts older than loginWindow are pruned inline on every call to allow(),
// so no background goroutine is required and instances are safe to create in tests.
type loginRateLimiter struct {
	mu          sync.Mutex
	attempts    map[string][]time.Time
	maxAttempts int
	window      time.Duration
}

func newLoginRateLimiter() *loginRateLimiter {
	return &loginRateLimiter{
		attempts:    make(map[string][]time.Time),
		maxAttempts: loginMaxAttempts,
		window:      loginWindow,
	}
}

// allow returns true if the IP is within the rate limit.
// When false, it also returns the duration until the limit resets (for Retry-After).
func (l *loginRateLimiter) allow(ip string) (bool, time.Duration) {
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	prev := l.attempts[ip]
	// Prune attempts outside the window, reusing the backing array.
	valid := prev[:0]
	for _, t := range prev {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= l.maxAttempts {
		l.attempts[ip] = valid
		// Retry-After = when the oldest in-window attempt falls out of the window.
		retryAfter := max(valid[0].Add(l.window).Sub(now), 0)
		return false, retryAfter
	}

	l.attempts[ip] = append(valid, now)
	return true, 0
}

// LoginRateLimitMiddleware returns chi-compatible middleware that rate-limits POST
// requests to loginPath. Requests to all other paths pass through unaffected.
// The limiter is created once per middleware instance and shared across requests.
//
// Loopback exemption: when the raw TCP connection comes directly from a loopback
// address (127.0.0.1, ::1, …) and no forwarded-IP header is present, the request
// is considered local dev/test traffic and is never counted. If a same-host reverse
// proxy is in use it must set X-Forwarded-For or X-Real-IP so the real client IP
// is available; without that header the limiter cannot distinguish clients and is
// intentionally a no-op (the alternative — all users sharing one bucket — would
// cause false lockouts).
func LoginRateLimitMiddleware(loginPath string) func(http.Handler) http.Handler {
	limiter := newLoginRateLimiter()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == loginPath {
				ip := GetClientIP(r)
				if !isTrulyLocal(r, ip) {
					if allowed, retryAfter := limiter.allow(ip); !allowed {
						secs := max(int(retryAfter.Seconds()), 1)
						w.Header().Set("Retry-After", strconv.Itoa(secs))
						writeAuthError(w, http.StatusTooManyRequests, "rate_limited", "Too many login attempts. Please try again later.")
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isTrulyLocal reports whether a request originates from a loopback address with
// no upstream proxy involved. It returns true only when:
//   - the resolved client IP (from GetClientIP) is a loopback address, AND
//   - neither X-Forwarded-For nor X-Real-IP is present (i.e., no proxy is
//     forwarding on behalf of an external client).
//
// When a same-host reverse proxy correctly sets forwarded headers, GetClientIP
// returns the real client IP and isTrulyLocal is false, so rate limiting applies.
func isTrulyLocal(r *http.Request, clientIP string) bool {
	ip := net.ParseIP(clientIP)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	// Loopback IP but proxy headers present → external client behind a local proxy.
	return r.Header.Get("X-Forwarded-For") == "" && r.Header.Get("X-Real-IP") == ""
}
