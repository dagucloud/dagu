// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connection

import (
	"fmt"
	"time"
)

// App is an OAuth client registration that several connections share.
type App struct {
	ID       string
	Name     string
	Provider Provider
	Kind     AppKind
	ClientID string
	// ClientSecret is empty for a public client. For a service account it
	// is the account's private key.
	ClientSecret string
	ClientType   ClientType
	// Agent records the admin's attestation that the client is set up for
	// use by an AI-powered agent.
	Agent bool
	// Tenant is set only for ProviderMicrosoft: "common", "organizations",
	// or a tenant ID.
	Tenant string
	// Endpoints is set only for ProviderOAuth2 and ProviderMCP.
	Endpoints    *Endpoints
	Status       AppStatus
	StatusReason string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Endpoints are the URLs of a provider that Dagu does not know in advance.
type Endpoints struct {
	Authorize string
	Token     string
	Revoke    string
	Device    string
}

// Provider is the identity provider a client and its connections belong to.
type Provider string

const (
	ProviderGoogle    Provider = "google"
	ProviderMicrosoft Provider = "microsoft"
	ProviderOAuth2    Provider = "oauth2"
	ProviderMCP       Provider = "mcp"
)

// AppKind says who owns a client.
type AppKind string

const (
	// AppKindBYO is a client the user registered.
	AppKindBYO AppKind = "byo"
	// AppKindCIMD is this server's own Client ID Metadata Document.
	AppKindCIMD AppKind = "cimd"
	// AppKindDCR is a client Dagu registered through RFC 7591.
	AppKindDCR AppKind = "dcr"
	// AppKindHosted is reserved for a client Dagu owns. It is refused.
	AppKindHosted AppKind = "hosted"
)

// ClientType decides which grants and redirect modes a client supports.
type ClientType string

const (
	ClientTypeWeb            ClientType = "web"
	ClientTypeDesktop        ClientType = "desktop"
	ClientTypePublic         ClientType = "public"
	ClientTypeServiceAccount ClientType = "service_account"
)

// AppStatus says whether the provider accepts a client.
type AppStatus string

const (
	AppStatusOK      AppStatus = "ok"
	AppStatusInvalid AppStatus = "invalid"
)

// Validate reports whether the app can be stored. A reserved kind fails
// with ErrReserved; any other invalid field fails with ErrInvalid.
func (a *App) Validate() error {
	if a.Name == "" {
		return invalidf("app name is required")
	}
	if err := validateProvider(a.Provider); err != nil {
		return err
	}
	switch a.Kind {
	case AppKindBYO, AppKindCIMD, AppKindDCR:
	case AppKindHosted:
		return fmt.Errorf("%w: app kind %q", ErrReserved, a.Kind)
	default:
		return invalidf("unknown app kind %q", a.Kind)
	}
	if a.ClientID == "" {
		return invalidf("client ID is required")
	}
	switch a.ClientType {
	case ClientTypeWeb, ClientTypeDesktop:
	case ClientTypePublic:
		if a.ClientSecret != "" {
			return invalidf("a public client has no secret")
		}
	case ClientTypeServiceAccount:
		if a.ClientSecret == "" {
			return invalidf("a service account needs its key")
		}
	default:
		return invalidf("unknown client type %q", a.ClientType)
	}
	if a.Tenant != "" && a.Provider != ProviderMicrosoft {
		return invalidf("a tenant is set only for %s", ProviderMicrosoft)
	}
	if a.Endpoints != nil && a.Provider != ProviderOAuth2 && a.Provider != ProviderMCP {
		return invalidf("endpoints are set only for %s and %s", ProviderOAuth2, ProviderMCP)
	}
	switch a.Status {
	case AppStatusOK, AppStatusInvalid:
	default:
		return invalidf("unknown app status %q", a.Status)
	}
	return nil
}

func validateProvider(p Provider) error {
	switch p {
	case ProviderGoogle, ProviderMicrosoft, ProviderOAuth2, ProviderMCP:
		return nil
	default:
		return invalidf("unknown provider %q", p)
	}
}
