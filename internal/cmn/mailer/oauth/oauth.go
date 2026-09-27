// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"golang.org/x/oauth2/google"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
)

const (
	microsoftScope  = "https://outlook.office365.com/.default"
	googleMailScope = "https://mail.google.com/"
	// microsoftMailScope covers the IMAP and SMTP permissions a person granted.
	microsoftMailScope = "https://outlook.office.com/.default"
	googleTokenURL     = "https://oauth2.googleapis.com/token" //nolint:gosec // Fixed provider endpoint, not a credential.

	maxCacheEntries     = 32
	tokenRequestTimeout = 15 * time.Second
	maxTokenResponse    = 1 << 20
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

	var refresh refreshFunc
	switch cfgCopy.Provider {
	case oauthconfig.ProviderGoogleRefresh:
		refresh = refreshTokenGrant(cfgCopy, googleTokenURL, nil)
	case oauthconfig.ProviderMicrosoftRefresh:
		refresh = refreshTokenGrant(cfgCopy, microsoftTokenURL(cfgCopy.TenantID), []string{microsoftMailScope, "offline_access"})
	}
	key, err := cacheKey(strings.TrimSpace(username), cfgCopy)
	if err != nil {
		return nil, err
	}
	return cachedTokenFunc(key, refresh), nil
}

// TokenError is an OAuth error response from a token endpoint, such as
// invalid_grant for a revoked or expired refresh token.
type TokenError struct {
	Code        string
	Description string
}

func (e *TokenError) Error() string {
	if e.Description == "" {
		return "token endpoint returned " + e.Code
	}
	return fmt.Sprintf("token endpoint returned %s: %s", e.Code, e.Description)
}

func microsoftTokenURL(tenant string) string {
	if tenant == "" {
		tenant = "common"
	}
	return "https://login.microsoftonline.com/" + url.PathEscape(tenant) + "/oauth2/v2.0/token"
}

// refreshTokenGrant exchanges a refresh token for an access token. It sends
// the scope explicitly because Microsoft requires it on refresh requests.
func refreshTokenGrant(cfg oauthconfig.Config, tokenURL string, scopes []string) refreshFunc {
	return func(ctx context.Context, cached *oauth2.Token) (*oauth2.Token, error) {
		refreshToken := cfg.RefreshToken
		if cached != nil && cached.RefreshToken != "" {
			refreshToken = cached.RefreshToken
		}
		form := url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {refreshToken},
			"client_id":     {cfg.ClientID},
		}
		if cfg.ClientSecret != "" {
			form.Set("client_secret", cfg.ClientSecret)
		}
		if len(scopes) > 0 {
			form.Set("scope", strings.Join(scopes, " "))
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		resp, err := tokenHTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("token request failed: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponse))
		if err != nil {
			return nil, fmt.Errorf("read token response: %w", err)
		}

		var body struct {
			AccessToken      string `json:"access_token"`
			RefreshToken     string `json:"refresh_token"`
			TokenType        string `json:"token_type"`
			ExpiresIn        int64  `json:"expires_in"`
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			return nil, fmt.Errorf("token endpoint returned status %d", resp.StatusCode)
		}
		if body.Error != "" {
			description, _, _ := strings.Cut(strings.TrimSpace(body.ErrorDescription), "\n")
			return nil, &TokenError{Code: body.Error, Description: strings.TrimSpace(description)}
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("token endpoint returned status %d", resp.StatusCode)
		}

		token := &oauth2.Token{
			AccessToken:  body.AccessToken,
			TokenType:    body.TokenType,
			RefreshToken: body.RefreshToken,
		}
		if token.RefreshToken == "" {
			token.RefreshToken = refreshToken
		}
		if body.ExpiresIn > 0 {
			token.Expiry = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
		}
		return token, nil
	}
}

func normalizedConfig(cfg oauthconfig.Config) oauthconfig.Config {
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.TenantID = strings.TrimSpace(cfg.TenantID)
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	return cfg
}

type refreshFunc func(context.Context, *oauth2.Token) (*oauth2.Token, error)

func providerRefresh(username string, cfg oauthconfig.Config) (refreshFunc, error) {
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

type tokenState struct {
	mu    sync.Mutex
	token *oauth2.Token
	gate  chan struct{}
}

func newTokenState() *tokenState {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &tokenState{gate: gate}
}

func (s *tokenState) current() *oauth2.Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

func (s *tokenState) set(token *oauth2.Token) {
	s.mu.Lock()
	s.token = token
	s.mu.Unlock()
}

func (s *tokenState) get(ctx context.Context, refresh refreshFunc) (*oauth2.Token, error) {
	if token := s.current(); token.Valid() {
		return token, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.gate:
	}
	defer func() { s.gate <- struct{}{} }()

	cached := s.current()
	if cached.Valid() {
		return cached, nil
	}
	token, err := refresh(ctx, cached)
	if err != nil {
		return nil, err
	}
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		return nil, errors.New("OAuth provider returned an empty access token")
	}
	s.set(token)
	return token, nil
}

var tokenCache = struct {
	sync.Mutex
	entries map[[sha256.Size]byte]*tokenState
}{entries: make(map[[sha256.Size]byte]*tokenState)}

func cachedTokenFunc(key [sha256.Size]byte, refresh refreshFunc) TokenFunc {
	tokenCache.Lock()
	state := tokenCache.entries[key]
	if state == nil {
		if len(tokenCache.entries) >= maxCacheEntries {
			for cachedKey := range tokenCache.entries {
				delete(tokenCache.entries, cachedKey)
				break
			}
		}
		state = newTokenState()
		tokenCache.entries[key] = state
	}
	tokenCache.Unlock()

	return func(ctx context.Context) (*oauth2.Token, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		return state.get(ctx, refresh)
	}
}
