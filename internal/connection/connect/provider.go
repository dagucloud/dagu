// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connect

import (
	"context"
	"time"

	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/connection"
)

// Provider is what the engine needs of one OAuth provider. Each method may
// call the provider; none may log or return a token, code, or verifier in
// an error.
type Provider interface {
	// ParseClientFile returns the app that a client file issued by the
	// provider describes, such as Google's client JSON. The app has no ID
	// and no status. A provider may refuse, with connection.ErrInvalid, a
	// file whose redirect URIs lack the one that opts choose for the app.
	ParseClientFile(data []byte, opts RedirectOptions) (*connection.App, error)
	// Endpoint returns the authorization and token URLs of app.
	Endpoint(app *connection.App) (oauth2.Endpoint, error)
	// AuthorizationParams returns the parameters that an authorization
	// request for conn sends beyond the client, redirect URI, scopes,
	// state, and PKCE challenge, such as Google's access_type=offline.
	AuthorizationParams(conn *connection.Connection) []oauth2.AuthCodeOption
	// Exchange trades code for the tokens of a consent. cfg holds the
	// client, endpoint, redirect URI, and requested scopes; opts carry the
	// PKCE verifier.
	Exchange(ctx context.Context, cfg *oauth2.Config, conn *connection.Connection, code string, opts ...oauth2.AuthCodeOption) (*oauth2.Token, error)
	// Identity returns the account that token was issued for.
	Identity(ctx context.Context, app *connection.App, token *oauth2.Token) (Identity, error)
	// Probe checks each of conn's services with token, issued to app, and
	// returns one result per service. It returns once ctx ends, reporting
	// each service it did not finish as failed.
	Probe(ctx context.Context, app *connection.App, conn *connection.Connection, token *oauth2.Token) []connection.Probe
}

// Advisor is a Provider that tells people how to fix what a consent runs
// into. Its texts hold no secret.
type Advisor interface {
	// ConsentFix returns what to do about the consent error code, such as
	// access_denied, of a session of app with redirectURI, or "" when the
	// provider has no advice.
	ConsentFix(code string, app *connection.App, redirectURI string) string
	// RefreshTokenFix returns what to do about a refresh token whose end
	// the provider reports with refresh_token_expires_in, given how long it
	// stays valid, or "".
	RefreshTokenFix(remaining time.Duration) string
}

// Revoker is a Provider that can revoke refresh tokens. No token of a
// provider that does not implement it is revoked.
type Revoker interface {
	// RevokeScope states what revoking one refresh token ends at the
	// provider.
	RevokeScope() RevokeScope
	// Revoke revokes refreshToken, which was issued to app's client.
	Revoke(ctx context.Context, app *connection.App, refreshToken string) error
}

// RevokeScope is what revoking one refresh token ends at a provider. A
// removed connection's token is revoked under any scope. A token that a
// reconnect replaced, or that a session held and never confirmed, is
// revoked only under RevokesToken, since the account's other tokens for
// the client must keep working. Any other value counts as RevokesGrant.
type RevokeScope string

const (
	// RevokesGrant means revoking a refresh token may end the account's
	// whole grant to the client, with every token of it.
	RevokesGrant RevokeScope = "grant"
	// RevokesToken means revoking a refresh token ends that token alone.
	// It also promises that no two connections hold one refresh token,
	// since the engine compares a dropped token only with its own
	// connection's live one. A provider that may return the same refresh
	// token to each consent of an account and client declares
	// RevokesGrant.
	RevokesToken RevokeScope = "token"
)

// Identity is the account that a consent signed in.
type Identity struct {
	// Account is shown to people, such as an email address.
	Account string
	// AccountID is the provider's immutable ID for the account.
	AccountID string
	// VouchedEmail is the email address the provider vouches for: one it
	// reports as verified, or one in the tenant the app names. It is empty
	// when the provider vouches for none.
	VouchedEmail string
}
