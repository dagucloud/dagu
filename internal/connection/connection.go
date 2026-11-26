// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connection

import (
	"fmt"
	"regexp"
	"time"

	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// Key identifies a connection. A name is unique in its workspace.
type Key struct {
	// Workspace is a workspace name, secret.GlobalWorkspace for the
	// connections of DAGs without a workspace label, or empty for the
	// notification connection.
	Workspace string
	Name      string
}

// Connection is the metadata of one authorized account. Its tokens are in
// its TokenState.
type Connection struct {
	// ID tells a connection apart from an earlier one with the same Key.
	// The store assigns it when the connection is created.
	ID        string
	Workspace string
	Name      string
	Provider  Provider
	// App is the ID of the App that holds the client.
	App      string
	Grant    Grant
	Services []Service
	// Scopes are requested beyond those of the services' presets.
	Scopes []string
	// Account is the account's name read after consent, shown to people.
	Account string
	// AccountID is the provider's immutable ID for the account. Accounts
	// are compared by it, never by Account.
	AccountID string
	// Subject is the user a service account impersonates, or the mailbox a
	// Microsoft client credentials connection sends from.
	Subject string
	// BoundHosts may receive the token beyond the services' presets.
	BoundHosts []string
	// Resource is the canonical server URL of an MCP connection.
	Resource         string
	AllowTokenExport bool
	Holder           Holder
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Service is a preset service a connection uses, such as gmail.
type Service struct {
	Name   string
	Access Access
}

// Grant is the OAuth grant a connection obtains its tokens with.
type Grant string

const (
	GrantAuthorizationCode Grant = "authorization_code"
	GrantDeviceCode        Grant = "device_code"
	GrantClientCredentials Grant = "client_credentials"
	GrantServiceAccount    Grant = "service_account"
)

// Access is the level of access a connection asks of a service.
type Access string

const (
	AccessRead  Access = "read"
	AccessWrite Access = "write"
)

// Holder says who keeps a connection's tokens.
type Holder string

const (
	// HolderLocal is this server.
	HolderLocal Holder = "local"
	// HolderCloud is reserved for a connection Dagu Cloud keeps. It is
	// refused.
	HolderCloud Holder = "cloud"
)

// namePattern holds lowercase letters, digits, and hyphens, starting with a
// letter or digit so that a name never reads as a command-line flag.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Key returns the key that identifies c.
func (c *Connection) Key() Key {
	return Key{Workspace: c.Workspace, Name: c.Name}
}

// Validate reports whether the connection can be stored. A reserved holder
// fails with ErrReserved; any other invalid field fails with ErrInvalid.
func (c *Connection) Validate() error {
	if err := c.Key().Validate(); err != nil {
		return err
	}
	if err := validateProvider(c.Provider); err != nil {
		return err
	}
	if c.App == "" {
		return invalidf("app is required")
	}
	switch c.Grant {
	case GrantAuthorizationCode, GrantDeviceCode, GrantClientCredentials, GrantServiceAccount:
	default:
		return invalidf("unknown grant %q", c.Grant)
	}
	for _, svc := range c.Services {
		if err := ValidateName(svc.Name); err != nil {
			return fmt.Errorf("service: %w", err)
		}
		if svc.Access != AccessRead && svc.Access != AccessWrite {
			return invalidf("service %q: unknown access %q", svc.Name, svc.Access)
		}
	}
	switch c.Holder {
	case HolderLocal:
	case HolderCloud:
		return fmt.Errorf("%w: holder %q", ErrReserved, c.Holder)
	default:
		return invalidf("unknown holder %q", c.Holder)
	}
	return nil
}

// Validate reports whether k names a connection, failing with ErrInvalid
// when it does not.
func (k Key) Validate() error {
	switch k.Workspace {
	case "", secret.GlobalWorkspace:
	default:
		if err := workspace.ValidateName(k.Workspace); err != nil {
			return invalidf("workspace: %v", err)
		}
	}
	return ValidateName(k.Name)
}

// ValidateName reports whether name is a valid connection or service name:
// lowercase letters, digits, and hyphens, up to 63 characters, starting
// with a letter or digit. It fails with ErrInvalid.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return invalidf("name %q must be up to 63 lowercase letters, digits, and hyphens, starting with a letter or digit", name)
	}
	return nil
}
