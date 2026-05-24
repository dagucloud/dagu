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

func TestLoginRateLimiter_Allow(t *testing.T) {
	t.Parallel()

	t.Run("allows up to max attempts", func(t *testing.T) {
		t.Parallel()
		l := &loginRateLimiter{
			attempts:    make(map[string][]time.Time),
			maxAttempts: 3,
			window:      15 * time.Minute,
		}
		for i := range 3 {
			allowed, _ := l.allow("1.2.3.4")
			assert.True(t, allowed, "attempt %d should be allowed", i+1)
		}
	})

	t.Run("blocks after max attempts", func(t *testing.T) {
		t.Parallel()
		l := &loginRateLimiter{
			attempts:    make(map[string][]time.Time),
			maxAttempts: 3,
			window:      15 * time.Minute,
		}
		for range 3 {
			_, _ = l.allow("1.2.3.4")
		}
		allowed, retryAfter := l.allow("1.2.3.4")
		assert.False(t, allowed)
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
			_, _ = l.allow("1.1.1.1")
		}
		// 1.1.1.1 is blocked; 2.2.2.2 is not
		allowed1, _ := l.allow("1.1.1.1")
		allowed2, _ := l.allow("2.2.2.2")
		assert.False(t, allowed1)
		assert.True(t, allowed2)
	})

	t.Run("old attempts outside window are pruned", func(t *testing.T) {
		t.Parallel()
		l := &loginRateLimiter{
			attempts:    make(map[string][]time.Time),
			maxAttempts: 2,
			window:      time.Second,
		}
		// Exhaust limit
		for range 2 {
			_, _ = l.allow("1.2.3.4")
		}
		blocked, _ := l.allow("1.2.3.4")
		require.False(t, blocked, "should be blocked before window expires")

		// Let the window expire
		time.Sleep(1100 * time.Millisecond)

		allowed, _ := l.allow("1.2.3.4")
		assert.True(t, allowed, "should be allowed after window expires")
	})

	t.Run("retry-after points to when oldest attempt expires", func(t *testing.T) {
		t.Parallel()
		window := 10 * time.Second
		l := &loginRateLimiter{
			attempts:    make(map[string][]time.Time),
			maxAttempts: 1,
			window:      window,
		}
		_, _ = l.allow("1.2.3.4")
		_, retryAfter := l.allow("1.2.3.4")
		// retryAfter should be close to window (oldest attempt was just now)
		assert.InDelta(t, window.Seconds(), retryAfter.Seconds(), 1.0)
	})
}

func TestLoginRateLimitMiddleware(t *testing.T) {
	t.Parallel()

	loginPath := "/api/v1/auth/login"
	noop := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("passes through non-login paths", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(noop)

		for range 20 {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/dags", nil)
			r.RemoteAddr = "1.2.3.4:1234"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assert.Equal(t, http.StatusOK, w.Code)
		}
	})

	t.Run("passes through GET to login path", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(noop)

		for range 20 {
			r := httptest.NewRequest(http.MethodGet, loginPath, nil)
			r.RemoteAddr = "1.2.3.4:1234"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assert.Equal(t, http.StatusOK, w.Code)
		}
	})

	t.Run("allows up to limit on login path", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(noop)

		for i := range loginMaxAttempts {
			r := httptest.NewRequest(http.MethodPost, loginPath, nil)
			r.RemoteAddr = "5.5.5.5:1234"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assert.Equal(t, http.StatusOK, w.Code, "attempt %d should pass", i+1)
		}
	})

	t.Run("returns 429 after limit exceeded", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(noop)

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

	t.Run("loopback never rate-limited", func(t *testing.T) {
		t.Parallel()
		mw := LoginRateLimitMiddleware(loginPath)
		handler := mw(noop)

		for range loginMaxAttempts * 3 {
			r := httptest.NewRequest(http.MethodPost, loginPath, nil)
			r.RemoteAddr = "127.0.0.1:1234"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assert.Equal(t, http.StatusOK, w.Code, "loopback should never be rate-limited")
		}
	})
}
