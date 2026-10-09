// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"net/url"
	"time"
)

// Ways a license reaches this server, as reported by Status.ConnectedVia.
const (
	ConnectedViaConsole = "console"
	ConnectedViaKey     = "key"
	ConnectedViaEnv     = "env"
	ConnectedViaConfig  = "config"
	ConnectedViaFile    = "file"
)

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

	// ServerName identifies this server in Dagu Console.
	ServerName string
	// LicenseID and Workspace identify the loaded license.
	LicenseID string
	Workspace string
	// ConnectedVia is one of the ConnectedVia constants, or empty without a
	// license.
	ConnectedVia string
	// ServerID, LastCheckIn, and ConsoleURL are set for licenses that check
	// in with Dagu Console.
	ServerID    string
	LastCheckIn time.Time
	ConsoleURL  string
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
	status.LicenseID = claims.ID
	status.Workspace = claims.Workspace
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
	status.ServerName = m.serverName
	if status.Community {
		return status
	}

	ad := m.currentActivation()
	status.ConnectedVia = connectedVia(status.Source, ad)
	if ad != nil && status.Source.NeedsHeartbeat() {
		status.ServerID = ad.ServerID
		status.LastCheckIn = ad.CheckedInAt
		status.ConsoleURL = m.client.baseURL + "/servers?" + url.Values{"server": {ad.ServerID}}.Encode()
	}
	return status
}

func connectedVia(source DiscoverySource, ad *ActivationData) string {
	switch source {
	case SourceEnvInline, SourceEnvKey:
		return ConnectedViaEnv
	case SourceConfigKey:
		return ConnectedViaConfig
	case SourceFileJWT:
		return ConnectedViaFile
	case SourceActivationFile:
		if ad != nil && ad.Via == ConnectedViaConsole {
			return ConnectedViaConsole
		}
		return ConnectedViaKey
	case SourceNone:
	}
	return ""
}
