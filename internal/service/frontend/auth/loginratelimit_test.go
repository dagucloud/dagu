// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"net/http"
	"net/http/httptest"
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

func TestLoginRateLimiter(t *testing.T) {
	t.Parallel()

	t.Run("not blocked before max failures", func(t *testing.T) {
		t.Parallel()
		l := &loginRateLimiter{
			attempts:    make(map[string][]time.Time),
			maxAttempts: 3,
			window:      15 * time.Minute,
		}
		for range 3 {
			l.recordFailure("1.2.3.4")
		}
		// 3 failures recorded; isBlocked at exactly maxAttempts should block.
		blocked, _ := l.isBlocked("1.2.3.4")
		assert.True(t, blocked, "should be blocked after maxAttempts failures")
	})

	t.Run("not blocked before reaching max", func(t *testing.T) {
		t.Parallel()
		l := &loginRateLimiter{
			attempts:    make(map[string][]time.Time),
			maxAttempts: 3,
			window:      15 * time.Minute,
		}
		for range 2 {
			l.recordFailure("1.2.3.4")
		}
		blocked, _ := l.isBlocked("1.2.3.4")
		assert.False(t, blocked, "should not be blocked before maxAttempts failures")
	})

	t.Run("blocked after max failures with retry-after", func(t *testing.T) {
		t.Parallel()
		l := &loginRateLimiter{
			attempts:    make(map[string][]time.Time),
			maxAttempts: 3,
			window:      15 * time.Minute,
		}
		for range 3 {
			l.recordFailure("1.2.3.4")
		}
		blocked, retryAfter := l.isBlocked("1.2.3.4")
		assert.True(t, blocked)
		assert.Positive(t, retryAfter)
	})

	t.Run("different IPs have independent limits", func(t *testing.T) {
		t.Parallel()
		l := &loginRateLimiter{
			attempts:    make(map[string][]time.Time),
			maxAttempts: 2,
			window:      15 * time.Minute,
		}
		for range 2 {
			l.recordFailure("1.1.1.1")
		}
		blocked1, _ := l.isBlocked("1.1.1.1")
		blocked2, _ := l.isBlocked("2.2.2.2")
		assert.True(t, blocked1, "1.1.1.1 should be blocked")
		assert.False(t, blocked2, "2.2.2.2 should not be blocked")
	})

	t.Run("old failures outside window are pruned", func(t *testing.T) {
		t.Parallel()
		l := &loginRateLimiter{
			attempts:    make(map[string][]time.Time),
			maxAttempts: 2,
			window:      time.Second,
		}
		for range 2 {
			l.recordFailure("1.2.3.4")
		}
		blocked, _ := l.isBlocked("1.2.3.4")
		require.True(t, blocked, "should be blocked before window expires")

		time.Sleep(1100 * time.Millisecond)

		blocked, _ = l.isBlocked("1.2.3.4")
		assert.False(t, blocked, "should not be blocked after window expires")
	})

	t.Run("retry-after points to when oldest failure expires", func(t *testing.T) {
		t.Parallel()
		window := 10 * time.Second
		l := &loginRateLimiter{
			attempts:    make(map[string][]time.Time),
			maxAttempts: 1,
			window:      window,
		}
		l.recordFailure("1.2.3.4")
		blocked, retryAfter := l.isBlocked("1.2.3.4")
		assert.True(t, blocked)
		assert.InDelta(t, window.Seconds(), retryAfter.Seconds(), 1.0)
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
			assert.Equal(t, http.StatusUnauthorized, w.Code, "GET to login path must pass through")
		}
	})

	t.Run("failed logins up to limit are passed through", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(failHandler) // handler returns 401

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
		handler := mw(failHandler) // handler returns 401

		// Exhaust the budget with 401 responses.
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
		handler := mw(okHandler) // handler returns 200 (successful login)

		for range loginMaxAttempts * 3 {
			r := httptest.NewRequest(http.MethodPost, loginPath, nil)
			r.RemoteAddr = "7.7.7.7:1234"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assert.Equal(t, http.StatusOK, w.Code, "successful logins must never trigger rate limit")
		}
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
			assert.Equal(t, http.StatusUnauthorized, w.Code, "loopback should never be rate-limited")
		}
	})

	t.Run("loopback with forwarded header is rate-limited", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(failHandler) // handler returns 401

		// Same-host proxy scenario: RemoteAddr is loopback, proxy sets X-Forwarded-For.
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

	t.Run("spoofed X-Forwarded-For header does not affect key for direct clients", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(failHandler)

		// Direct client (non-loopback RemoteAddr) tries to evade rate limit by
		// rotating X-Forwarded-For values. The limiter must use RemoteAddr, not the header.
		for range loginMaxAttempts {
			r := httptest.NewRequest(http.MethodPost, loginPath, nil)
			r.RemoteAddr = "8.8.8.8:1234"
			r.Header.Set("X-Forwarded-For", "1.1.1.1") // spoofed; different each time would still use RemoteAddr
			handler.ServeHTTP(httptest.NewRecorder(), r)
		}

		// Now try with a different spoofed header — should still be blocked by RemoteAddr.
		r := httptest.NewRequest(http.MethodPost, loginPath, nil)
		r.RemoteAddr = "8.8.8.8:1234"
		r.Header.Set("X-Forwarded-For", "9.9.9.9")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assert.Equal(t, http.StatusTooManyRequests, w.Code, "RemoteAddr key must not be bypassable via X-Forwarded-For")
	})
}
