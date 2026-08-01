// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import "time"

// Status describes the current license state without exposing license credentials.
type Status struct {
	Valid       bool
	Plan        string
	Expiry      time.Time
	Features    []string
	GracePeriod bool
	GraceEndsAt time.Time
	Community   bool
	Source      DiscoverySource
	WarningCode string
	Failure     string
}

// StatusFor returns the current status reported by checker.
func StatusFor(checker Checker) Status {
	status := Status{
		Community: true,
		Features:  []string{},
	}
	if checker == nil {
		return status
	}

	claims := checker.Claims()
	if claims == nil {
		return status
	}

	now := time.Now()
	status.Community = false
	status.Plan = claims.Plan
	status.Features = make([]string, len(claims.Features))
	copy(status.Features, claims.Features)
	status.WarningCode = claims.WarningCode
	status.Valid = claims.ExpiresAt == nil || claims.ExpiresAt.After(now)
	if claims.ExpiresAt != nil {
		status.Expiry = claims.ExpiresAt.Time
		status.GraceEndsAt = claims.ExpiresAt.Add(graceDurationForClaims(claims))
		status.GracePeriod = !status.Valid && now.Before(status.GraceEndsAt)
	}

	return status
}

// Status returns the current license status.
func (m *Manager) Status() Status {
	status := StatusFor(m.Checker())
	status.Source = m.Source()
	status.Failure = m.Failure()
	return status
}
