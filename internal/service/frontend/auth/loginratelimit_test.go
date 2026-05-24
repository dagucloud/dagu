// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failHandler returns 401 Unauthorized — simulates a failed login attempt.
var failHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusUnauthorized)
})

// okHandler returns 200 OK — simulates a successful login attempt.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func newTestLimiter(maxAttempts int, window time.Duration) *loginRateLimiter {
	return &loginRateLimiter{
		failures:      make(map[string][]time.Time),
		pending:       make(map[string]int),
		maxAttempts:   maxAttempts,
		maxTrackedIPs: loginMaxTrackedIPs,
		window:        window,
	}
}

func TestLoginRateLimiter(t *testing.T) {
	t.Parallel()

	t.Run("not blocked before reaching max failures", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter(3, 15*time.Minute)
		for range 2 {
			ok, _ := l.reserve("1.2.3.4")
			require.True(t, ok)
			l.confirmFailure("1.2.3.4")
		}
		blocked, _ := l.reserve("1.2.3.4")
		assert.True(t, blocked, "should be allowed at attempt 3 (not yet blocked)")
		l.confirmFailure("1.2.3.4")
	})

	t.Run("blocked after max failures", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter(3, 15*time.Minute)
		for range 3 {
			ok, _ := l.reserve("1.2.3.4")
			require.True(t, ok)
			l.confirmFailure("1.2.3.4")
		}
		blocked, retryAfter := l.reserve("1.2.3.4")
		assert.False(t, blocked)
		assert.Positive(t, retryAfter)
	})

	t.Run("successful login releases slot without counting failure", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter(3, 15*time.Minute)
		for range 10 {
			ok, _ := l.reserve("1.2.3.4")
			require.True(t, ok, "should never be blocked on successful logins")
			l.releaseSlot("1.2.3.4") // simulate successful login
		}
		blocked, _ := l.reserve("1.2.3.4")
		assert.True(t, blocked, "successful logins must not consume failure budget")
	})

	t.Run("pending slots count toward limit to prevent burst bypass", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter(3, 15*time.Minute)
		// Reserve 3 slots without confirming — simulates concurrent in-flight requests.
		for range 3 {
			ok, _ := l.reserve("1.2.3.4")
			require.True(t, ok)
		}
		// A 4th concurrent request must be blocked even though no failure is confirmed.
		blocked, _ := l.reserve("1.2.3.4")
		assert.False(t, blocked, "pending slots must count toward limit")
	})

	t.Run("different IPs have independent limits", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter(2, 15*time.Minute)
		for range 2 {
			ok, _ := l.reserve("1.1.1.1")
			require.True(t, ok)
			l.confirmFailure("1.1.1.1")
		}
		blocked1, _ := l.reserve("1.1.1.1")
		blocked2, _ := l.reserve("2.2.2.2")
		assert.False(t, blocked1, "1.1.1.1 should be blocked")
		assert.True(t, blocked2, "2.2.2.2 should not be blocked")
		l.releaseSlot("2.2.2.2")
	})

	t.Run("old failures outside window are pruned", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter(2, time.Second)
		for range 2 {
			ok, _ := l.reserve("1.2.3.4")
			require.True(t, ok)
			l.confirmFailure("1.2.3.4")
		}
		blocked, _ := l.reserve("1.2.3.4")
		require.False(t, blocked, "should be blocked before window expires")
		// 4th reserve was blocked, nothing to confirm/release.

		time.Sleep(1100 * time.Millisecond)

		ok, _ := l.reserve("1.2.3.4")
		assert.True(t, ok, "should be allowed after window expires")
		l.releaseSlot("1.2.3.4")
	})

	t.Run("retry-after points to when oldest failure expires", func(t *testing.T) {
		t.Parallel()
		window := 10 * time.Second
		l := newTestLimiter(1, window)
		ok, _ := l.reserve("1.2.3.4")
		require.True(t, ok)
		l.confirmFailure("1.2.3.4")

		blocked, retryAfter := l.reserve("1.2.3.4")
		assert.False(t, blocked)
		assert.InDelta(t, window.Seconds(), retryAfter.Seconds(), 1.0)
	})

	t.Run("failure map capped at maxTrackedIPs", func(t *testing.T) {
		t.Parallel()
		const cap = 5
		l := &loginRateLimiter{
			failures:      make(map[string][]time.Time),
			pending:       make(map[string]int),
			maxAttempts:   10,
			maxTrackedIPs: cap,
			window:        15 * time.Minute,
		}
		// Insert cap+10 distinct IPs; each gets one confirmed failure.
		for i := range cap + 10 {
			ip := fmt.Sprintf("10.0.0.%d", i+1)
			ok, _ := l.reserve(ip)
			require.True(t, ok)
			l.confirmFailure(ip)
		}
		assert.LessOrEqual(t, len(l.failures), cap, "failure map must not exceed maxTrackedIPs")
	})
}

func TestLoginRateLimitMiddleware(t *testing.T) {
	t.Parallel()

	loginPath := "/api/v1/auth/login"

	t.Run("passes through non-login paths", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(failHandler)

		for range 20 {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/dags", nil)
			r.RemoteAddr = "1.2.3.4:1234"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assert.Equal(t, http.StatusUnauthorized, w.Code, "non-login path must pass through")
		}
	})

	t.Run("passes through GET to login path", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(failHandler)

		for range 20 {
			r := httptest.NewRequest(http.MethodGet, loginPath, nil)
			r.RemoteAddr = "1.2.3.4:1234"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assert.Equal(t, http.StatusUnauthorized, w.Code, "GET must pass through")
		}
	})

	t.Run("failed logins up to limit are passed through", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(failHandler)

		for i := range loginMaxAttempts {
			r := httptest.NewRequest(http.MethodPost, loginPath, nil)
			r.RemoteAddr = "5.5.5.5:1234"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assert.Equal(t, http.StatusUnauthorized, w.Code, "attempt %d should reach handler", i+1)
		}
	})

	t.Run("returns 429 after max failed attempts", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(failHandler)

		for range loginMaxAttempts {
			r := httptest.NewRequest(http.MethodPost, loginPath, nil)
			r.RemoteAddr = "6.6.6.6:1234"
			handler.ServeHTTP(httptest.NewRecorder(), r)
		}

		r := httptest.NewRequest(http.MethodPost, loginPath, nil)
		r.RemoteAddr = "6.6.6.6:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assert.Equal(t, http.StatusTooManyRequests, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
		assert.NotEmpty(t, w.Header().Get("Retry-After"))
		assert.Contains(t, w.Body.String(), "rate_limited")
	})

	t.Run("successful logins do not consume failure budget", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(okHandler)

		for range loginMaxAttempts * 3 {
			r := httptest.NewRequest(http.MethodPost, loginPath, nil)
			r.RemoteAddr = "7.7.7.7:1234"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assert.Equal(t, http.StatusOK, w.Code, "successful logins must not trigger rate limit")
		}
	})

	t.Run("concurrent burst requests blocked atomically", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)

		// Slow handler that gives concurrent requests time to pile up.
		var started sync.WaitGroup
		started.Add(loginMaxAttempts)
		slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			started.Done()
			started.Wait() // hold until all are in-flight
			w.WriteHeader(http.StatusUnauthorized)
		})
		handler := mw(slow)

		results := make([]int, loginMaxAttempts+2)
		var wg sync.WaitGroup
		for i := range loginMaxAttempts + 2 {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				r := httptest.NewRequest(http.MethodPost, loginPath, nil)
				r.RemoteAddr = "9.9.9.9:1234"
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				results[idx] = w.Code
			}(i)
		}
		wg.Wait()

		tooMany := 0
		for _, code := range results {
			if code == http.StatusTooManyRequests {
				tooMany++
			}
		}
		// At least the overflow requests must be blocked.
		assert.GreaterOrEqual(t, tooMany, 2, "concurrent burst beyond limit must be blocked")
	})

	t.Run("loopback never rate-limited", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(failHandler)

		for range loginMaxAttempts * 3 {
			r := httptest.NewRequest(http.MethodPost, loginPath, nil)
			r.RemoteAddr = "127.0.0.1:1234"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assert.Equal(t, http.StatusUnauthorized, w.Code, "loopback must never be rate-limited")
		}
	})

	t.Run("loopback with forwarded header is rate-limited", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(failHandler)

		for range loginMaxAttempts {
			r := httptest.NewRequest(http.MethodPost, loginPath, nil)
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header.Set("X-Forwarded-For", "203.0.113.1")
			handler.ServeHTTP(httptest.NewRecorder(), r)
		}

		r := httptest.NewRequest(http.MethodPost, loginPath, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("X-Forwarded-For", "203.0.113.1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assert.Equal(t, http.StatusTooManyRequests, w.Code, "proxied loopback should be rate-limited using forwarded IP")
	})

	t.Run("private-range peer XFF is ignored (LAN client spoofing prevention)", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(failHandler)

		// Direct LAN client at 10.x can set arbitrary XFF — must not be trusted.
		// All requests from the same private IP share one bucket regardless of XFF.
		for range loginMaxAttempts {
			r := httptest.NewRequest(http.MethodPost, loginPath, nil)
			r.RemoteAddr = "10.0.0.5:1234"
			r.Header.Set("X-Forwarded-For", "1.1.1.1") // spoofed — different each time
			handler.ServeHTTP(httptest.NewRecorder(), r)
		}

		// The 6th attempt is blocked by 10.0.0.5 (raw key), not 1.1.1.1.
		r := httptest.NewRequest(http.MethodPost, loginPath, nil)
		r.RemoteAddr = "10.0.0.5:1234"
		r.Header.Set("X-Forwarded-For", "9.9.9.9") // different spoofed value
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assert.Equal(t, http.StatusTooManyRequests, w.Code, "private peer XFF must not be trusted as rate-limit key")
	})

	t.Run("loopback proxy: rightmost XFF used (client-supplied leftmost ignored)", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(failHandler)

		// Client spoofs leftmost XFF; proxy appends real client IP as rightmost.
		for range loginMaxAttempts {
			r := httptest.NewRequest(http.MethodPost, loginPath, nil)
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header.Set("X-Forwarded-For", "spoofed-value, 203.0.113.7")
			handler.ServeHTTP(httptest.NewRecorder(), r)
		}

		// Blocked by 203.0.113.7 (rightmost, proxy-added value).
		r := httptest.NewRequest(http.MethodPost, loginPath, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("X-Forwarded-For", "different-spoof, 203.0.113.7")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assert.Equal(t, http.StatusTooManyRequests, w.Code, "rightmost XFF (proxy-added) must be used as rate-limit key")
	})

	t.Run("loopback proxy: True-Client-IP honored", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(failHandler)

		for range loginMaxAttempts {
			r := httptest.NewRequest(http.MethodPost, loginPath, nil)
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header.Set("True-Client-IP", "203.0.113.8")
			handler.ServeHTTP(httptest.NewRecorder(), r)
		}

		r := httptest.NewRequest(http.MethodPost, loginPath, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("True-Client-IP", "203.0.113.8")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assert.Equal(t, http.StatusTooManyRequests, w.Code, "True-Client-IP must be used as rate-limit key")
	})

	t.Run("panic in handler releases pending slot", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		panicHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			panic("simulated handler panic")
		})
		// Outer recoverer — mirrors chi's middleware.Recoverer position.
		recoverer := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer func() {
					if rec := recover(); rec != nil {
						w.WriteHeader(http.StatusInternalServerError)
					}
				}()
				next.ServeHTTP(w, r)
			})
		}
		handler := recoverer(mw(panicHandler))

		// Send more than maxAttempts panicking requests; each slot must be
		// released by the defer so pending[ip] never accumulates.
		for i := range loginMaxAttempts + 2 {
			r := httptest.NewRequest(http.MethodPost, loginPath, nil)
			r.RemoteAddr = "11.11.11.11:1234"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assert.Equal(t, http.StatusInternalServerError, w.Code, "panic must yield 500 not 429, attempt %d", i+1)
		}
	})

	t.Run("PreserveRawRemoteAddr prevents X-Forwarded-For spoofing", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)

		// simulateRealIP mimics chi's middleware.RealIP: rewrites RemoteAddr from XFF.
		simulateRealIP := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
					r.RemoteAddr = strings.TrimSpace(strings.Split(xff, ",")[0]) + ":0"
				}
				next.ServeHTTP(w, r)
			})
		}

		// Correct server chain: PreserveRawRemoteAddr → RealIP → rate limiter → handler.
		handler := PreserveRawRemoteAddr(simulateRealIP(mw(failHandler)))

		// Attacker from 8.8.8.8 exhausts limit with real TCP connection but
		// tries to use a different XFF each time. preserveRawRemoteAddr captures
		// the true RemoteAddr before simulateRealIP overwrites it.
		for range loginMaxAttempts {
			r := httptest.NewRequest(http.MethodPost, loginPath, nil)
			r.RemoteAddr = "8.8.8.8:1234"
			r.Header.Set("X-Forwarded-For", "1.1.1.1") // spoofed
			handler.ServeHTTP(httptest.NewRecorder(), r)
		}

		// Different spoofed XFF — should still be blocked because key = 8.8.8.8.
		r := httptest.NewRequest(http.MethodPost, loginPath, nil)
		r.RemoteAddr = "8.8.8.8:1234"
		r.Header.Set("X-Forwarded-For", "9.9.9.9")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assert.Equal(t, http.StatusTooManyRequests, w.Code, "spoofed XFF must not bypass the limit")
	})
}
