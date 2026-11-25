// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
)

// Source supplies the access tokens of one set of credentials. Sources with
// the same credentials share one cached token for the life of the process.
type Source struct {
	state   *tokenState
	refresh refreshFunc
}

// NewSource returns the token source of resolved credentials for calling an
// HTTP API: a google_refresh or microsoft_refresh refresh token, or a
// google_service_account key. Scopes narrow a Google refresh token's grant;
// a Microsoft refresh token and a service account require them.
func NewSource(cfg *oauthconfig.Config) (*Source, error) {
	if cfg == nil {
		return nil, errors.New("OAuth configuration is required")
	}
	cfgCopy := normalizedConfig(*cfg)
	refresh, err := apiRefresh(cfgCopy, googleTokenURL, microsoftTokenURL(cfgCopy.TenantID))
	if err != nil {
		return nil, err
	}
	key, err := cacheKey("", cfgCopy)
	if err != nil {
		return nil, err
	}
	return &Source{state: cachedState(key), refresh: refresh}, nil
}

// NewFetchSource returns a source whose tokens come from fetch. Sources with
// the same key share one cached token, fetched again once it expires.
func NewFetchSource(key string, fetch func(context.Context) (*oauth2.Token, error)) *Source {
	return &Source{
		state: cachedState(sha256.Sum256([]byte("fetch\x00" + key))),
		refresh: func(ctx context.Context, _ *oauth2.Token) (*oauth2.Token, error) {
			return fetch(ctx)
		},
	}
}

// Token returns the cached access token, or a new one when the cached token
// has expired or was invalidated.
func (s *Source) Token(ctx context.Context) (*oauth2.Token, error) {
	return s.state.get(ctx, s.refresh)
}

// Invalidate makes the next Token call get a new token, unless token was
// already replaced.
func (s *Source) Invalidate(token *oauth2.Token) {
	s.state.invalidate(token)
}

// apiRefresh returns how cfg's provider issues a token at the given token
// endpoints.
func apiRefresh(cfg oauthconfig.Config, googleURL, microsoftURL string) (refreshFunc, error) {
	switch cfg.Provider {
	case oauthconfig.ProviderGoogleRefresh:
		return refreshTokenGrant(cfg, googleURL, cfg.Scopes), nil
	case oauthconfig.ProviderMicrosoftRefresh:
		return refreshTokenGrant(cfg, microsoftURL, microsoftScopes(cfg)), nil
	case oauthconfig.ProviderGoogleServiceAccount:
		return serviceAccountGrant(cfg.ServiceAccountJSON, googleURL, "", cfg.Scopes)
	default:
		return nil, fmt.Errorf("unsupported OAuth provider %q", cfg.Provider)
	}
}
