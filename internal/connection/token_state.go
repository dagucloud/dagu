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
	Session     *Session
	RefreshedAt time.Time
}

// Tokens are the tokens one consent produced.
type Tokens struct {
	RefreshToken string
	// RefreshTokenExpiresAt is zero unless the provider reports it.
	RefreshTokenExpiresAt time.Time
	// AccessTokens are keyed by the resource each is for. Google and MCP
	// connections have one resource; a Microsoft connection has one per
	// Microsoft resource it calls.
	AccessTokens map[string]AccessToken
	// GrantedScopes can be fewer than were requested.
	GrantedScopes []string
}

// AccessToken is an access token and when it expires.
type AccessToken struct {
	Token     string
	ExpiresAt time.Time
}

// Session is a connect or reconnect in progress. Its tokens go live only
// when the user who started it confirms the account.
type Session struct {
	ID string
	// User is the user who started the session.
	User      string
	Phase     SessionPhase
	ExpiresAt time.Time
	// Account and AccountID are read after consent.
	Account   string
	AccountID string
	// Reason is the error of a failed session.
	Reason string
	// Tokens are those consent produced, before they are confirmed.
	Tokens Tokens
}

// TokenStatus is the state of a connection's authorization.
type TokenStatus string

const (
	TokenStatusPending        TokenStatus = "pending"
	TokenStatusConnected      TokenStatus = "connected"
	TokenStatusNeedsReconnect TokenStatus = "needs_reconnect"
	TokenStatusRevoked        TokenStatus = "revoked"
)

// SessionPhase is the step a connect session has reached.
type SessionPhase string

const (
	SessionPhaseWaiting SessionPhase = "waiting"
	SessionPhaseConfirm SessionPhase = "confirm"
	SessionPhaseFailed  SessionPhase = "failed"
)
