// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package providers

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
)

// serviceOption names the service whose resource the exported token is for.
const serviceOption = "service"

var errNoConnections = errors.New("connection tokens are not available in this run")

func init() {
	registerResolver(secretref.ProviderConnection, func(_ []string) Resolver {
		return &connectionResolver{}
	})
}

// Connections finds the connections that a run's DAG declares and hands
// out their tokens.
type Connections interface {
	broker.Source
	// Connection returns the connection named name in the run's project.
	Connection(ctx context.Context, name string) (*connection.Connection, error)
}

type connectionsKey struct{}

// WithConnections returns ctx carrying conns, from which connection secrets
// take their tokens.
func WithConnections(ctx context.Context, conns Connections) context.Context {
	return context.WithValue(ctx, connectionsKey{}, conns)
}

// connectionResolver exports the access token of the connection that key
// names (Spec 107 §Using a Connection).
type connectionResolver struct{}

func (r *connectionResolver) Name() string {
	return secretref.ProviderConnection
}

func (r *connectionResolver) Validate(ref secretref.Ref) error {
	if err := connection.ValidateName(ref.Key); err != nil {
		return fmt.Errorf("key: %w", err)
	}
	for name := range ref.Options {
		if name != serviceOption {
			return fmt.Errorf("unknown option %q; only %q is allowed", name, serviceOption)
		}
	}
	return nil
}

// CheckCapability reports that a check reads the connection's metadata,
// never its token.
func (r *connectionResolver) CheckCapability(secretref.Ref) CheckCapability {
	return CheckCapabilityMetadataOnly
}

func (r *connectionResolver) CheckAccessibility(ctx context.Context, ref secretref.Ref) error {
	_, err := exportedConnection(ctx, ref)
	return err
}

// Resolve returns a token that stays valid for broker.ExportMinTTL, or half
// the provider's token lifetime when that is shorter.
func (r *connectionResolver) Resolve(ctx context.Context, ref secretref.Ref) (string, error) {
	conn, err := exportedConnection(ctx, ref)
	if err != nil {
		return "", err
	}
	// Every provider but Microsoft serves one resource, so a service only
	// has to be one of the connection's services.
	token, _, err := connectionsFrom(ctx).Token(ctx, conn, "", broker.ExportMinTTL)
	if err != nil {
		return "", err
	}
	// On a worker, the token's answer carries the connection's current
	// binding, so an export disallowed since the check returns no token.
	if _, err := exportedConnection(ctx, ref); err != nil {
		return "", err
	}
	return token, nil
}

// exportedConnection returns the connection ref names when it allows
// token export and serves the service ref's options name.
func exportedConnection(ctx context.Context, ref secretref.Ref) (*connection.Connection, error) {
	conns := connectionsFrom(ctx)
	if conns == nil {
		return nil, fmt.Errorf("connection %q: %w", ref.Key, errNoConnections)
	}
	conn, err := conns.Connection(ctx, ref.Key)
	if err != nil {
		return nil, err
	}
	if !conn.AllowTokenExport {
		return nil, fmt.Errorf("connection %q does not allow token export; an admin can set allow_token_export on it", ref.Key)
	}
	// A Microsoft service selects one of several resources, which its
	// presets do not name yet.
	if conn.Provider == connection.ProviderMicrosoft {
		return nil, fmt.Errorf("connection %q: a Microsoft connection cannot export a token yet", ref.Key)
	}
	if service, ok := ref.Options[serviceOption]; ok {
		if !slices.ContainsFunc(conn.Services, func(svc connection.Service) bool { return svc.Name == service }) {
			return nil, fmt.Errorf("connection %q: service %q is not one of its services", ref.Key, service)
		}
	}
	return conn, nil
}

func connectionsFrom(ctx context.Context) Connections {
	conns, _ := ctx.Value(connectionsKey{}).(Connections)
	return conns
}
