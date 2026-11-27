// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connection

import "time"

// TokenState is a connection's tokens, status, and connect session.
type TokenState struct {
	// ConnectionID is the ID of the connection the state belongs to.
	ConnectionID string
	// Tokens are the live tokens that runs use.
	Tokens
	Status       TokenStatus
	StatusReason string
	// Generation increases each time a connect or reconnect is confirmed.
	Generation int64
	// Session is the connect session in progress, or nil.
	Session *Session
	// ConfirmedSession is the ID of the last session that was confirmed,
	// which its starter reads as connected after the session is cleared.
	ConfirmedSession string
	RefreshedAt      time.Time
	// RefreshErrors are keyed by resource and hold the outcome of failed
	// refreshes of each.
	RefreshErrors map[string]RefreshError
}

// RefreshError tells the processes that waited on a refresh that it
// failed, and why.
type RefreshError struct {
	// Reason is the provider's error of the last refresh, without
	// secrets. It is empty once a refresh succeeds.
	Reason string
	// Failures counts the failed refreshes. It never decreases, so a
	// process that read it before waiting tells a new failure from an
	// earlier one.
	Failures int64
}

// Tokens are the tokens one consent produced, with the account it signed
// in.
type Tokens struct {
	// Account is the account's name, such as an email address, shown to
	// people.
	Account string
	// AccountID is the provider's immutable ID for the account. Accounts
	// are compared by it, never by Account.
	AccountID    string
	RefreshToken string
	// RefreshTokenExpiresAt is zero unless the provider reports it.
	RefreshTokenExpiresAt time.Time
	// AccessTokens are keyed by the resource each is for. A connection
	// with one resource, such as a Google, OAuth2, or MCP connection, keeps
	// its token under the empty key; a Microsoft connection has one per
	// Microsoft resource it calls.
	AccessTokens map[string]AccessToken
	// GrantedScopes can be fewer than were requested.
	GrantedScopes []string
}

// AccessToken is an access token and when it expires.
type AccessToken struct {
	Token string
	// ExpiresAt is zero when the provider reported no expiry.
	ExpiresAt time.Time
	// IssuedAt is zero when the token's lifetime is unknown.
	IssuedAt time.Time
}

// Lifetime returns how long the token was issued for, or zero when that is
// unknown.
func (t AccessToken) Lifetime() time.Duration {
	if t.IssuedAt.IsZero() || t.ExpiresAt.IsZero() {
		return 0
	}
	return t.ExpiresAt.Sub(t.IssuedAt)
}

// Session is a connect or reconnect in progress. Its tokens go live only
// when the user who started it confirms the account.
type Session struct {
	ID string
	// User is the user who started the session.
	User      string
	Phase     SessionPhase
	ExpiresAt time.Time
	// ClaimedAt is when a callback began to complete the waiting session,
	// or zero. While the claim holds, other callbacks are refused, so a
	// code is exchanged once.
	ClaimedAt time.Time
	// Reason is the error of a failed session.
	Reason string
	// Tokens are those consent produced, with the account read after it,
	// before they are confirmed.
	Tokens Tokens
	// Probes hold the result of each service's check after consent.
	Probes []Probe
}

// Probe is the result of one service's check after consent.
type Probe struct {
	Service string
	// Error is the provider's error, without secrets, or empty when the
	// service answered.
	Error string
}

// TokenStatus is the state of a connection's authorization.
type TokenStatus string

const (
	TokenStatusPending        TokenStatus = "pending"
	TokenStatusConnected      TokenStatus = "connected"
	TokenStatusNeedsReconnect TokenStatus = "needs_reconnect"
)

// SessionPhase is the step a connect session has reached.
type SessionPhase string

const (
	SessionPhaseWaiting SessionPhase = "waiting"
	SessionPhaseConfirm SessionPhase = "confirm"
	SessionPhaseFailed  SessionPhase = "failed"
)
