// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"sync"
	"time"
)

const gracePeriod = 14 * 24 * time.Hour

// State implements Checker with thread-safe access to license claims.
type State struct {
	mu     sync.RWMutex
	claims *LicenseClaims
	token  string
}

var _ Checker = (*State)(nil)

// Update atomically replaces the license state.
func (s *State) Update(claims *LicenseClaims, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims = cloneClaims(claims)
	s.token = token
}

// Token returns the raw JWT token string.
func (s *State) Token() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.token
}

// Claims returns a deep copy of the current license claims.
func (s *State) Claims() *LicenseClaims {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneClaims(s.claims)
}

// cloneClaims returns a deep copy of claims, or nil if claims is nil.
func cloneClaims(c *LicenseClaims) *LicenseClaims {
	if c == nil {
		return nil
	}
	cp := *c
	if c.ExpiresAt != nil {
		expiresAt := *c.ExpiresAt
		cp.ExpiresAt = &expiresAt
	}
	if c.NotBefore != nil {
		notBefore := *c.NotBefore
		cp.NotBefore = &notBefore
	}
	if c.IssuedAt != nil {
		issuedAt := *c.IssuedAt
		cp.IssuedAt = &issuedAt
	}
	if c.Features != nil {
		cp.Features = make([]string, len(c.Features))
		copy(cp.Features, c.Features)
	}
	if c.GraceDays != nil {
		graceDays := *c.GraceDays
		cp.GraceDays = &graceDays
	}
	return &cp
}

// IsFeatureEnabled returns true if the feature is available.
// Features remain active during the grace period after expiry. The free plan
// enables none.
func (s *State) IsFeatureEnabled(feature string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.claims == nil || s.claims.IsFree() {
		return false
	}
	if !s.claims.HasFeature(feature) {
		return false
	}

	// Perpetual tokens (nil ExpiresAt) have all claimed features enabled.
	if s.claims.ExpiresAt == nil {
		return true
	}

	// If the token has not expired, the feature is enabled.
	if s.claims.ExpiresAt.After(time.Now()) {
		return true
	}

	// If expired, check grace period.
	return s.isInGracePeriod()
}

// Plan returns the license plan name.
func (s *State) Plan() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.claims == nil {
		return ""
	}
	return s.claims.Plan
}

// IsGracePeriod returns true if the license is expired but within the grace period.
func (s *State) IsGracePeriod() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isInGracePeriod()
}

func (s *State) isInGracePeriod() bool {
	if s.claims == nil || s.claims.ExpiresAt == nil {
		return false
	}
	expiry := s.claims.ExpiresAt.Time
	if time.Now().Before(expiry) {
		return false // not expired yet
	}
	return time.Now().Before(expiry.Add(graceDurationForClaims(s.claims)))
}

func (s *State) isPastGracePeriod() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.claims == nil || s.claims.ExpiresAt == nil {
		return false
	}
	expiry := s.claims.ExpiresAt.Time
	return !time.Now().Before(expiry.Add(graceDurationForClaims(s.claims)))
}

func graceDurationForClaims(claims *LicenseClaims) time.Duration {
	if claims == nil || claims.GraceDays == nil {
		return gracePeriod
	}
	if *claims.GraceDays <= 0 {
		return 0
	}
	return time.Duration(*claims.GraceDays) * 24 * time.Hour
}

// WarningCode returns the warning code from the license claims.
func (s *State) WarningCode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.claims == nil {
		return ""
	}
	return s.claims.WarningCode
}

// IsCommunity returns true when no license is loaded.
func (s *State) IsCommunity() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.claims == nil
}
