// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connect

import (
	"context"

	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/connection"
)

// Provider is what the engine needs of one OAuth provider. Each method may
// call the provider; none may log or return a token, code, or verifier in
// an error.
type Provider interface {
	// ParseClientFile returns the app that a client file issued by the
	// provider describes, such as Google's client JSON. The app has no ID
	// and no status.
	ParseClientFile(data []byte) (*connection.App, error)
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
	// Probe checks each of conn's services with token, and returns one
	// result per service. It returns once ctx ends, reporting each service
	// it did not finish as failed.
	Probe(ctx context.Context, conn *connection.Connection, token *oauth2.Token) []connection.Probe
}

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
