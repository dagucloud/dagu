// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package stepoauth authorizes the HTTP requests of a step with an OAuth
// access token that is replaced as it expires.
package stepoauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
	"github.com/dagucloud/dagu/v2/internal/cmn/oauth"
	"github.com/dagucloud/dagu/v2/internal/license"
)

// Providers a step's oauth block accepts.
const (
	providerGoogle    = "google"
	providerMicrosoft = "microsoft"
	providerDaguCloud = "dagu-cloud"
)

// Config is the oauth block of a step.
type Config struct {
	Provider string `json:"provider" mapstructure:"provider"`
	// Account is the Google account Dagu Cloud issues tokens for.
	Account            string   `json:"account" mapstructure:"account"`
	TenantID           string   `json:"tenant_id" mapstructure:"tenant_id"`
	ClientID           string   `json:"client_id" mapstructure:"client_id"`
	ClientSecret       string   `json:"client_secret" mapstructure:"client_secret"`
	RefreshToken       string   `json:"refresh_token" mapstructure:"refresh_token"`
	ServiceAccountJSON string   `json:"service_account_json" mapstructure:"service_account_json"`
	Scopes             []string `json:"scopes" mapstructure:"scopes"`
}

// field is a named credential value of the block.
type field struct {
	name  string
	value string
}

// Validate checks the block of a step whose requests also send headers. Field
// values may still be unresolved references.
func (c *Config) Validate(headers map[string]string) error {
	for name := range headers {
		if strings.EqualFold(strings.TrimSpace(name), "Authorization") {
			return errors.New("oauth cannot be combined with an Authorization header")
		}
	}
	var (
		account      = field{"account", c.Account}
		tenantID     = field{"tenant_id", c.TenantID}
		clientID     = field{"client_id", c.ClientID}
		clientSecret = field{"client_secret", c.ClientSecret}
		refreshToken = field{"refresh_token", c.RefreshToken}
		keyJSON      = field{"service_account_json", c.ServiceAccountJSON}
	)
	provider := strings.TrimSpace(c.Provider)
	switch provider {
	case providerDaguCloud:
		if err := requireFields(provider, account); err != nil {
			return err
		}
		if len(c.Scopes) == 0 {
			return fmt.Errorf("oauth.scopes is required for provider %q", provider)
		}
		if err := rejectFields(provider, tenantID, clientID, clientSecret, refreshToken, keyJSON); err != nil {
			return err
		}
	case providerGoogle:
		hasKey := strings.TrimSpace(c.ServiceAccountJSON) != ""
		switch hasRefresh := strings.TrimSpace(c.RefreshToken) != ""; {
		case hasKey && hasRefresh:
			return errors.New("oauth.refresh_token and oauth.service_account_json cannot both be set")
		case hasKey:
			if len(c.Scopes) == 0 {
				return errors.New("oauth.scopes is required with oauth.service_account_json")
			}
			if err := rejectFields(provider, account, tenantID, clientID, clientSecret); err != nil {
				return err
			}
		case hasRefresh:
			if err := requireFields(provider, clientID, clientSecret); err != nil {
				return err
			}
			if err := rejectFields(provider, account, tenantID); err != nil {
				return err
			}
		default:
			return fmt.Errorf("oauth.refresh_token or oauth.service_account_json is required for provider %q", provider)
		}
	case providerMicrosoft:
		if err := requireFields(provider, clientID, refreshToken); err != nil {
			return err
		}
		if len(c.Scopes) == 0 {
			return fmt.Errorf("oauth.scopes is required for provider %q", provider)
		}
		if err := rejectFields(provider, account, keyJSON); err != nil {
			return err
		}
	default:
		return fmt.Errorf("oauth.provider must be %s, %s, or %s", providerGoogle, providerMicrosoft, providerDaguCloud)
	}
	for _, scope := range c.Scopes {
		if strings.TrimSpace(scope) == "" {
			return errors.New("oauth.scopes must not contain an empty scope")
		}
	}
	return nil
}

func requireFields(provider string, fields ...field) error {
	for _, f := range fields {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("oauth.%s is required for provider %q", f.name, provider)
		}
	}
	return nil
}

func rejectFields(provider string, fields ...field) error {
	for _, f := range fields {
		if strings.TrimSpace(f.value) != "" {
			return fmt.Errorf("oauth.%s is not valid for provider %q", f.name, provider)
		}
	}
	return nil
}

// NewTransport returns a transport that sends each request through base with
// an access token for a validated block. A request the server rejects with
// HTTP 401 is sent once more with a new token when its body can be read
// again. A request for which no token can be obtained fails with a
// *TokenError. The dagu-cloud provider reads this server's Dagu Cloud
// credentials from ctx.
func NewTransport(ctx context.Context, cfg *Config, base http.RoundTripper) (http.RoundTripper, error) {
	source, err := cfg.source(ctx)
	if err != nil {
		return nil, fmt.Errorf("oauth: %w", err)
	}
	return &transport{base: base, source: source}, nil
}

func (c *Config) source(ctx context.Context) (*oauth.Source, error) {
	switch strings.TrimSpace(c.Provider) {
	case providerDaguCloud:
		creds, ok := license.ContextCloudCredentials(ctx)
		if !ok {
			return nil, errors.New("provider dagu-cloud needs this server connected to Dagu Cloud; connect it on the License page")
		}
		return cloudSource(creds, strings.TrimSpace(c.Account), c.Scopes), nil
	case providerMicrosoft:
		return oauth.NewSource(c.providerConfig(oauthconfig.ProviderMicrosoftRefresh))
	default:
		if strings.TrimSpace(c.ServiceAccountJSON) != "" {
			return oauth.NewSource(c.providerConfig(oauthconfig.ProviderGoogleServiceAccount))
		}
		return oauth.NewSource(c.providerConfig(oauthconfig.ProviderGoogleRefresh))
	}
}

func (c *Config) providerConfig(provider oauthconfig.Provider) *oauthconfig.Config {
	return &oauthconfig.Config{
		Provider:           provider,
		TenantID:           c.TenantID,
		ClientID:           c.ClientID,
		ClientSecret:       c.ClientSecret,
		ServiceAccountJSON: c.ServiceAccountJSON,
		RefreshToken:       c.RefreshToken,
		Scopes:             c.Scopes,
	}
}

// Schema returns the JSON schema of the oauth block.
func Schema() *jsonschema.Schema {
	text := func(description string) *jsonschema.Schema {
		return &jsonschema.Schema{Type: "string", Description: description}
	}
	return &jsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
		Required:             []string{"provider"},
		Description: "Send Authorization: Bearer with an OAuth access token that is refreshed as it expires. " +
			"A request rejected with HTTP 401 is retried once with a new token. Cannot be combined with an Authorization header.",
		Properties: map[string]*jsonschema.Schema{
			"provider": {
				Type:        "string",
				Enum:        []any{providerGoogle, providerMicrosoft, providerDaguCloud},
				Description: "google or microsoft uses the credentials in this block; dagu-cloud takes the token of a Google account connected in Dagu Console.",
			},
			"account":              text("dagu-cloud: Google account address whose token Dagu Console issues."),
			"client_id":            text("google, microsoft: OAuth client ID. Required with refresh_token."),
			"client_secret":        text("google, microsoft: OAuth client secret. Required for google with refresh_token."),
			"tenant_id":            text("microsoft: tenant. Defaults to common."),
			"refresh_token":        text("google, microsoft: refresh token exchanged for access tokens."),
			"service_account_json": text("google: service account key JSON, instead of a refresh token."),
			"scopes": {
				Type:        "array",
				Items:       &jsonschema.Schema{Type: "string", MinLength: new(1)},
				Description: "Scopes to request. Required for dagu-cloud, microsoft, and a Google service account; narrows a Google refresh token's grant. offline_access is always added for microsoft.",
			},
		},
	}
}
