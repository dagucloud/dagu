// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package oauthconfig defines SMTP OAuth configuration.
package oauthconfig

import (
	"errors"
	"fmt"
	"strings"
)

type Provider = string

const (
	ProviderMicrosoft            Provider = "microsoft"
	ProviderGoogleServiceAccount Provider = "google_service_account"
	ProviderGoogleRefresh        Provider = "google_refresh"

	microsoftSMTPHost = "smtp.office365.com"
	googleSMTPHost    = "smtp.gmail.com"
	smtpPort          = "587"
)

// Config contains the provider credentials needed to acquire SMTP access tokens.
type Config struct {
	Provider           Provider `json:"provider,omitempty" yaml:"provider,omitempty"`
	TenantID           string   `json:"tenantId,omitempty" yaml:"tenant_id,omitempty"`
	ClientID           string   `json:"clientId,omitempty" yaml:"client_id,omitempty"`
	ClientSecret       string   `json:"clientSecret,omitempty" yaml:"client_secret,omitempty"`
	ServiceAccountJSON string   `json:"serviceAccountJson,omitempty" yaml:"service_account_json,omitempty"`
	RefreshToken       string   `json:"refreshToken,omitempty" yaml:"refresh_token,omitempty"`
}

// Destination is an SMTP submission endpoint.
type Destination struct {
	Host string
	Port string
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

type configField struct {
	name  string
	value string
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
