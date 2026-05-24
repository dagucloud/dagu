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
	// loginMaxTrackedIPs is the maximum number of distinct IPs tracked in the failure map.
	// When the map is full, a random entry is evicted to make room for the new one.
	loginMaxTrackedIPs = 1000
)

// loginRateLimiter tracks failed login attempts per IP using a sliding window
// counter with atomic reserve-confirm-release semantics.
//
//   - failures: confirmed failed attempts within the window.
//   - pending: in-flight requests that have passed the pre-check but whose
//     outcome is not yet known. Pending slots count toward the limit so that
//     a burst of concurrent bad-password requests cannot all slip through the
//     pre-check before any failure is confirmed.
//
// On every reserve call, stale entries for the current IP are pruned. A
// periodic pass (every 256 calls) evicts fully-expired keys from the map to
// bound memory growth under many distinct source IPs.
// No background goroutine is used, making instances safe to create in tests.
type loginRateLimiter struct {
	mu            sync.Mutex
	failures      map[string][]time.Time
	pending       map[string]int
	maxAttempts   int
	maxTrackedIPs int
	window        time.Duration
	callCount     int
}

func newLoginRateLimiter() *loginRateLimiter {
	return &loginRateLimiter{
		failures:      make(map[string][]time.Time),
		pending:       make(map[string]int),
		maxAttempts:   loginMaxAttempts,
		maxTrackedIPs: loginMaxTrackedIPs,
		window:        loginWindow,
	}
}

// reserve atomically checks the per-IP limit and reserves a pending slot.
// If the combined count of confirmed failures + in-flight requests equals or
// exceeds maxAttempts, it returns (false, retryAfter); otherwise it
// increments pending[ip] and returns (true, 0).
func (l *loginRateLimiter) reserve(ip string) (bool, time.Duration) {
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	// Prune stale confirmed failures for this IP.
	prev := l.failures[ip]
	valid := prev[:0]
	for _, t := range prev {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	if len(valid) == 0 {
		delete(l.failures, ip)
	} else {
		l.failures[ip] = valid
	}

	// Periodic eviction of fully-expired keys to bound map size.
	l.callCount++
	if l.callCount&0xFF == 0 {
		for k, v := range l.failures {
			if len(v) > 0 && !v[len(v)-1].After(cutoff) {
				delete(l.failures, k)
			}
		}
	}

	total := len(valid) + l.pending[ip]
	if total >= l.maxAttempts {
		var retryAfter time.Duration
		if len(valid) > 0 {
			retryAfter = max(valid[0].Add(l.window).Sub(now), 0)
		}
		return false, retryAfter
	}

	l.pending[ip]++
	return true, 0
}

// confirmFailure converts a pending slot into a confirmed failure.
// Called when the downstream handler returns HTTP 401.
func (l *loginRateLimiter) confirmFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pending[ip] > 0 {
		l.pending[ip]--
		if l.pending[ip] == 0 {
			delete(l.pending, ip)
		}
	}
	// Enforce the IP cap: if this is a new IP and the map is full, evict one
	// random entry to bound memory growth under many distinct source addresses.
	if _, exists := l.failures[ip]; !exists && l.maxTrackedIPs > 0 && len(l.failures) >= l.maxTrackedIPs {
		for k := range l.failures {
			delete(l.failures, k)
			break
		}
	}
	l.failures[ip] = append(l.failures[ip], time.Now())
}

// releaseSlot removes a pending slot without recording a failure.
// Called when the downstream handler returns any non-401 status (successful login).
func (l *loginRateLimiter) releaseSlot(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pending[ip] > 0 {
		l.pending[ip]--
		if l.pending[ip] == 0 {
			delete(l.pending, ip)
		}
	}
}

// LoginRateLimitMiddleware returns chi-compatible middleware that rate-limits
// POST requests to loginPath per client IP. Only HTTP 401 responses from the
// downstream handler are counted as failures; successful logins release their
// reserved slot without consuming the budget, preventing lockout of shared
// egress IPs (office NAT, VPN).
//
// Concurrent-burst safety: reserve increments a pending counter atomically
// before forwarding the request, so a burst of parallel bad-password attempts
// cannot all pass the pre-check before any failure is recorded.
//
// Rate-limit key: rateLimitKey reads the raw TCP source address stored in
// context by PreserveRawRemoteAddr (which must run before chi's
// middleware.RealIP). This prevents clients from rotating X-Forwarded-For
// values to get new rate-limit buckets. When the raw address is a loopback
// address and a forwarded-IP header is present, the forwarded IP is used
// instead (same-host reverse proxy scenario).
//
// Loopback exemption: when the resolved key is a loopback address (no proxy
// headers present), the request is assumed to be local dev or E2E test traffic
// and is passed through without being counted. Note: a same-host reverse
// proxy that does not set X-Forwarded-For/X-Real-IP cannot be distinguished
// from direct local traffic; operators using such a setup must configure their
// proxy to forward the client IP for rate limiting to be effective.
func LoginRateLimitMiddleware(loginPath string) func(http.Handler) http.Handler {
	limiter := newLoginRateLimiter()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == loginPath {
				ip := rateLimitKey(r)
				if !isLoopbackIP(ip) {
					if allowed, retryAfter := limiter.reserve(ip); !allowed {
						secs := max(int(math.Ceil(retryAfter.Seconds())), 1)
						w.Header().Set("Retry-After", strconv.Itoa(secs))
						writeAuthError(w, http.StatusTooManyRequests, "rate_limited", "Too many login attempts. Please try again later.")
						return
					}
					rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
					defer func() {
						if rec.status == http.StatusUnauthorized {
							limiter.confirmFailure(ip)
						} else {
							limiter.releaseSlot(ip)
						}
					}()
					next.ServeHTTP(rec, r)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// rateLimitKey returns the IP address to use as the rate-limit bucket key.
//
// It reads the raw TCP RemoteAddr stored in context by PreserveRawRemoteAddr
// (before chi's middleware.RealIP can overwrite r.RemoteAddr from forwarded
// headers). This makes the key unspoofable for direct connections.
//
// Forwarded headers are consulted only when the raw TCP peer is a loopback
// address, which reliably identifies a same-host reverse proxy. Private-range
// peers (10.x, 192.168.x, …) are NOT treated as trusted proxies: direct
// LAN/VPN clients also originate from private addresses and could supply
// arbitrary forwarded headers to rotate their rate-limit bucket.
//
// Header priority (loopback peers only):
//  1. True-Client-IP – set by some CDN/proxy stacks (chi's RealIP checks this first).
//  2. X-Real-IP – set by nginx to the direct upstream address; single, canonical value.
//  3. X-Forwarded-For (rightmost entry) – the value the last trusted proxy appended.
//     The rightmost entry is used instead of the leftmost because proxies that append
//     to an existing header place the attacker-controlled leftmost value first; the
//     rightmost entry is what the trusted proxy itself added.
//
// Note: deployments where Dagu is behind a non-loopback reverse proxy (e.g. a
// separate-host nginx) cannot benefit from per-client granularity without a
// configurable trusted-proxy list. In that topology all clients share the proxy's
// raw TCP address as their rate-limit bucket.
func rateLimitKey(r *http.Request) string {
	rawAddr, _ := r.Context().Value(rawRemoteAddrKey{}).(string)
	if rawAddr == "" {
		rawAddr = r.RemoteAddr
	}

	host, _, err := net.SplitHostPort(rawAddr)
	if err != nil {
		host = rawAddr
	}

	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		if tci := r.Header.Get("True-Client-IP"); tci != "" {
			return stripPort(strings.TrimSpace(tci))
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return stripPort(strings.TrimSpace(xri))
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Use the rightmost entry: what the trusted loopback proxy appended.
			parts := strings.Split(xff, ",")
			if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
				return stripPort(last)
			}
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
