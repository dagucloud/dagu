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
)

type Provider = string

const (
	ProviderMicrosoft            Provider = "microsoft"
	ProviderGoogleServiceAccount Provider = "google_service_account"
	ProviderGoogleRefresh        Provider = "google_refresh"

	microsoftSMTPHost = "smtp.office365.com"
	googleSMTPHost    = "smtp.gmail.com"
	smtpPort          = "587"

	microsoftScope  = "https://outlook.office365.com/.default"
	googleMailScope = "https://mail.google.com/"
	googleTokenURL  = "https://oauth2.googleapis.com/token" //nolint:gosec // Fixed provider endpoint, not a credential.

	maxCacheEntries     = 32
	tokenRequestTimeout = 15 * time.Second
)

var tokenHTTPClient = &http.Client{Timeout: tokenRequestTimeout}

// Config contains the provider credentials needed to acquire SMTP access tokens.
type Config struct {
	Provider           Provider `json:"provider,omitempty" yaml:"provider,omitempty"`
	TenantID           string   `json:"tenantId,omitempty" yaml:"tenant_id,omitempty"`
	ClientID           string   `json:"clientId,omitempty" yaml:"client_id,omitempty"`
	ClientSecret       string   `json:"clientSecret,omitempty" yaml:"client_secret,omitempty"`
	ServiceAccountJSON string   `json:"serviceAccountJson,omitempty" yaml:"service_account_json,omitempty"`
	RefreshToken       string   `json:"refreshToken,omitempty" yaml:"refresh_token,omitempty"`
}

// Destination is the provider's SMTP submission endpoint.
type Destination struct {
	Host string
	Port string
}

// TokenFunc returns an access token using the supplied operation context.
type TokenFunc func(context.Context) (*oauth2.Token, error)

type configField struct {
	name  string
	value string
}

// SMTPDestination returns the fixed SMTP endpoint for a provider.
func SMTPDestination(provider Provider) (Destination, error) {
	switch provider {
	case ProviderMicrosoft:
		return Destination{Host: microsoftSMTPHost, Port: smtpPort}, nil
	case ProviderGoogleServiceAccount, ProviderGoogleRefresh:
		return Destination{Host: googleSMTPHost, Port: smtpPort}, nil
	default:
		return Destination{}, fmt.Errorf("unsupported SMTP OAuth provider %q", provider)
	}
}

// ValidateStructure validates provider selection and credential field ownership.
func ValidateStructure(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	provider := strings.TrimSpace(cfg.Provider)
	if _, err := SMTPDestination(provider); err != nil {
		return err
	}

	require := func(fields ...configField) error {
		for _, field := range fields {
			if strings.TrimSpace(field.value) == "" {
				return fmt.Errorf("%s is required for SMTP OAuth provider %q", field.name, provider)
			}
		}
		return nil
	}
	reject := func(fields ...configField) error {
		for _, field := range fields {
			if strings.TrimSpace(field.value) != "" {
				return fmt.Errorf("%s is not valid for SMTP OAuth provider %q", field.name, provider)
			}
		}
		return nil
	}

	switch provider {
	case ProviderMicrosoft:
		if err := require(
			configField{"tenant_id", cfg.TenantID},
			configField{"client_id", cfg.ClientID},
			configField{"client_secret", cfg.ClientSecret},
		); err != nil {
			return err
		}
		return reject(
			configField{"service_account_json", cfg.ServiceAccountJSON},
			configField{"refresh_token", cfg.RefreshToken},
		)
	case ProviderGoogleServiceAccount:
		if err := require(configField{"service_account_json", cfg.ServiceAccountJSON}); err != nil {
			return err
		}
		return reject(
			configField{"tenant_id", cfg.TenantID},
			configField{"client_id", cfg.ClientID},
			configField{"client_secret", cfg.ClientSecret},
			configField{"refresh_token", cfg.RefreshToken},
		)
	case ProviderGoogleRefresh:
		if err := require(
			configField{"client_id", cfg.ClientID},
			configField{"client_secret", cfg.ClientSecret},
			configField{"refresh_token", cfg.RefreshToken},
		); err != nil {
			return err
		}
		return reject(
			configField{"tenant_id", cfg.TenantID},
			configField{"service_account_json", cfg.ServiceAccountJSON},
		)
	default:
		return errors.New("SMTP OAuth provider is required")
	}
}

// NewTokenFunc validates resolved credentials and returns a process-cached token function.
func NewTokenFunc(username string, cfg *Config) (TokenFunc, error) {
	if cfg == nil {
		return nil, errors.New("SMTP OAuth configuration is required")
	}
	cfgCopy := normalizedConfig(*cfg)
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("SMTP username is required with OAuth")
	}
	if err := ValidateStructure(&cfgCopy); err != nil {
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

func normalizedConfig(cfg Config) Config {
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.TenantID = strings.TrimSpace(cfg.TenantID)
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	return cfg
}

type refreshFunc func(context.Context, *oauth2.Token) (*oauth2.Token, error)

func providerRefresh(username string, cfg Config) (refreshFunc, error) {
	switch cfg.Provider {
	case ProviderMicrosoft:
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
	case ProviderGoogleServiceAccount:
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
	case ProviderGoogleRefresh:
		oauthConfig := oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint: oauth2.Endpoint{
				TokenURL: googleTokenURL,
			},
		}
		return func(ctx context.Context, cached *oauth2.Token) (*oauth2.Token, error) {
			seed := cached
			if seed == nil {
				seed = &oauth2.Token{RefreshToken: cfg.RefreshToken}
			} else if seed.RefreshToken == "" {
				copy := *seed
				copy.RefreshToken = cfg.RefreshToken
				seed = &copy
			}
			return oauthConfig.TokenSource(tokenHTTPContext(ctx), seed).Token()
		}, nil
	default:
		return nil, fmt.Errorf("unsupported SMTP OAuth provider %q", cfg.Provider)
	}
}

func tokenHTTPContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, tokenHTTPClient)
}

func cacheKey(username string, cfg Config) ([sha256.Size]byte, error) {
	data, err := json.Marshal(struct {
		Username string `json:"username"`
		Config
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
		return nil, errors.New("SMTP OAuth provider returned an empty access token")
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
