// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"golang.org/x/oauth2/google"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
	"github.com/dagucloud/dagu/v2/internal/connection/oauthrefresh"
)

const (
	microsoftScope  = "https://outlook.office365.com/.default"
	googleMailScope = "https://mail.google.com/"
	// microsoftMailScope covers the IMAP and SMTP permissions a person granted.
	microsoftMailScope = "https://outlook.office.com/.default"
	googleTokenURL     = "https://oauth2.googleapis.com/token" //nolint:gosec // Fixed provider endpoint, not a credential.

	maxCacheEntries     = 32
	tokenRequestTimeout = 15 * time.Second
)

var tokenHTTPClient = &http.Client{Timeout: tokenRequestTimeout}

// TokenFunc returns an access token using the supplied operation context.
type TokenFunc func(context.Context) (*oauth2.Token, error)

// NewTokenFunc validates resolved credentials and returns a process-cached token function.
func NewTokenFunc(username string, cfg *oauthconfig.Config) (TokenFunc, error) {
	if cfg == nil {
		return nil, errors.New("SMTP OAuth configuration is required")
	}
	cfgCopy := normalizedConfig(*cfg)
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("SMTP username is required with OAuth")
	}
	if err := oauthconfig.ValidateStructure(&cfgCopy); err != nil {
		return nil, err
	}

	refresh, err := providerRefresh(username, cfgCopy)
	if err != nil {
		return nil, err
	}
	key, err := cacheKey(username, cfgCopy)
	if err != nil {
		return nil, err
	}
	return cachedTokenFunc(key, refresh), nil
}

// NewRefreshTokenFunc validates a mail account's resolved OAuth credentials and
// returns a process-cached token function. A refresh token the provider returns
// replaces the configured one for the life of the process only.
func NewRefreshTokenFunc(username string, cfg *oauthconfig.Config) (TokenFunc, error) {
	if cfg == nil {
		return nil, errors.New("OAuth configuration is required")
	}
	cfgCopy := normalizedConfig(*cfg)
	if err := oauthconfig.ValidateMailAccount(&cfgCopy); err != nil {
		return nil, err
	}

	var refresh oauthrefresh.RefreshFunc
	switch cfgCopy.Provider {
	case oauthconfig.ProviderGoogleRefresh:
		refresh = refreshTokenGrant(cfgCopy, googleTokenURL, nil)
	case oauthconfig.ProviderMicrosoftRefresh:
		refresh = refreshTokenGrant(cfgCopy, oauthrefresh.MicrosoftTokenURL(cfgCopy.TenantID), microsoftMailScopes(cfgCopy))
	}
	key, err := cacheKey(strings.TrimSpace(username), cfgCopy)
	if err != nil {
		return nil, err
	}
	return cachedTokenFunc(key, refresh), nil
}

// microsoftMailScopes returns the configured scopes, or every mail permission
// the person granted, always with offline_access.
func microsoftMailScopes(cfg oauthconfig.Config) []string {
	if len(cfg.Scopes) == 0 {
		return oauthrefresh.MicrosoftScopes([]string{microsoftMailScope})
	}
	return oauthrefresh.MicrosoftScopes(cfg.Scopes)
}

// refreshTokenGrant refreshes with the configured client and refresh token.
func refreshTokenGrant(cfg oauthconfig.Config, tokenURL string, scopes []string) oauthrefresh.RefreshFunc {
	return oauthrefresh.Grant{
		TokenURL:     tokenURL,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RefreshToken: cfg.RefreshToken,
		Scopes:       scopes,
	}.Refresh
}

func normalizedConfig(cfg oauthconfig.Config) oauthconfig.Config {
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.TenantID = strings.TrimSpace(cfg.TenantID)
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	return cfg
}

func providerRefresh(username string, cfg oauthconfig.Config) (oauthrefresh.RefreshFunc, error) {
	switch cfg.Provider {
	case oauthconfig.ProviderMicrosoft:
		tokenURL := "https://login.microsoftonline.com/" + url.PathEscape(cfg.TenantID) + "/oauth2/v2.0/token"
		return func(ctx context.Context, _ *oauth2.Token) (*oauth2.Token, error) {
			ctx = tokenHTTPContext(ctx)
			return (&clientcredentials.Config{
				ClientID:     cfg.ClientID,
				ClientSecret: cfg.ClientSecret,
				TokenURL:     tokenURL,
				Scopes:       []string{microsoftScope},
			}).TokenSource(ctx).Token()
		}, nil
	case oauthconfig.ProviderGoogleServiceAccount:
		jwtConfig, err := google.JWTConfigFromJSON([]byte(cfg.ServiceAccountJSON), googleMailScope)
		if err != nil {
			return nil, fmt.Errorf("invalid Google service account JSON: %w", err)
		}
		if strings.TrimSpace(jwtConfig.Email) == "" || strings.TrimSpace(string(jwtConfig.PrivateKey)) == "" {
			return nil, errors.New("google service account JSON requires client_email and private_key")
		}
		jwtConfig.Subject = username
		jwtConfig.TokenURL = googleTokenURL
		return func(ctx context.Context, _ *oauth2.Token) (*oauth2.Token, error) {
			return jwtConfig.TokenSource(tokenHTTPContext(ctx)).Token()
		}, nil
	case oauthconfig.ProviderGoogleRefresh:
		return refreshTokenGrant(cfg, googleTokenURL, nil), nil
	default:
		return nil, fmt.Errorf("unsupported SMTP OAuth provider %q", cfg.Provider)
	}
}

func tokenHTTPContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, tokenHTTPClient)
}

func cacheKey(username string, cfg oauthconfig.Config) ([sha256.Size]byte, error) {
	data, err := json.Marshal(struct {
		Username string `json:"username"`
		oauthconfig.Config
	}{Username: username, Config: cfg})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode SMTP OAuth cache key: %w", err)
	}
	return sha256.Sum256(data), nil
}

var tokenCache = struct {
	sync.Mutex
	entries map[[sha256.Size]byte]*oauthrefresh.Gate
}{entries: make(map[[sha256.Size]byte]*oauthrefresh.Gate)}

func cachedTokenFunc(key [sha256.Size]byte, refresh oauthrefresh.RefreshFunc) TokenFunc {
	tokenCache.Lock()
	gate := tokenCache.entries[key]
	if gate == nil {
		if len(tokenCache.entries) >= maxCacheEntries {
			for cachedKey := range tokenCache.entries {
				delete(tokenCache.entries, cachedKey)
				break
			}
		}
		gate = oauthrefresh.NewGate()
		tokenCache.entries[key] = gate
	}
	tokenCache.Unlock()

	return func(ctx context.Context) (*oauth2.Token, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		return gate.Token(ctx, refresh)
	}
}
