// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
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
// Attempts older than loginWindow are discarded on each check.
type loginRateLimiter struct {
	mu          sync.Mutex
	attempts    map[string][]time.Time
	maxAttempts int
	window      time.Duration
	stop        chan struct{}
}

func newLoginRateLimiter() *loginRateLimiter {
	l := &loginRateLimiter{
		attempts:    make(map[string][]time.Time),
		maxAttempts: loginMaxAttempts,
		window:      loginWindow,
		stop:        make(chan struct{}),
	}
	go l.cleanup()
	return l
}

// Close stops the background cleanup goroutine. Safe to call once.
func (l *loginRateLimiter) Close() {
	close(l.stop)
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

// cleanup periodically removes IPs whose last attempt has expired from the window.
// It exits when Close is called.
func (l *loginRateLimiter) cleanup() {
	ticker := time.NewTicker(l.window)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
		}
		cutoff := time.Now().Add(-l.window)
		l.mu.Lock()
		for ip, attempts := range l.attempts {
			if len(attempts) == 0 || !attempts[len(attempts)-1].After(cutoff) {
				delete(l.attempts, ip)
			}
		}
		l.mu.Unlock()
	}
}

// LoginRateLimitMiddleware returns chi-compatible middleware that rate-limits POST
// requests to loginPath. Requests to all other paths pass through unaffected.
// The limiter is created once per middleware instance and shared across requests.
func LoginRateLimitMiddleware(loginPath string) func(http.Handler) http.Handler {
	limiter := newLoginRateLimiter()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == loginPath {
				if allowed, retryAfter := limiter.allow(GetClientIP(r)); !allowed {
					secs := max(int(retryAfter.Seconds()), 1)
					w.Header().Set("Retry-After", strconv.Itoa(secs))
					writeAuthError(w, http.StatusTooManyRequests, "rate_limited", "Too many login attempts. Please try again later.")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
