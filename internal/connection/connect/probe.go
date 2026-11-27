// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connect

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
)

// Test probes each of conn's services with its live access token, as the
// checks after consent do, and returns one result per service. A
// connection that cannot hand out a token fails with the token source's
// error, such as broker.ErrReconnectRequired. Without a token source, Test
// fails with ErrUnsupported.
func (e *Engine) Test(ctx context.Context, conn *connection.Connection) ([]connection.Probe, error) {
	if e.tokens == nil {
		return nil, fmt.Errorf("%w: no token source", ErrUnsupported)
	}
	provider, app, err := e.client(ctx, conn)
	if err != nil {
		return nil, err
	}
	access, expiresIn, err := e.tokens.Token(ctx, conn, "", broker.StepMinTTL)
	if err != nil {
		return nil, err
	}
	// Read after the token, so a reconnect between the two leaves granted
	// scopes that are at least as new as the token, and the probe's call
	// reports what the token lacks.
	state, err := e.store.TokenState(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("connection %q: %w", conn.Name, err)
	}
	token := &oauth2.Token{AccessToken: access}
	if expiresIn > 0 {
		token.Expiry = e.now().Add(expiresIn)
	}
	// A probe compares its service's scopes with the granted ones.
	token = token.WithExtra(map[string]any{"scope": strings.Join(state.GrantedScopes, " ")})
	callCtx, cancel := e.callContext(ctx)
	defer cancel()
	probes := provider.Probe(callCtx, app, conn, token)
	for i := range probes {
		probes[i].Error = redact(probes[i].Error, access, app.ClientSecret)
	}
	return probes, nil
}

// Providers returns the providers that the engine connects, sorted.
func (e *Engine) Providers() []connection.Provider {
	return slices.Sorted(maps.Keys(e.providers))
}

// Guide returns the Guide of provider, or nil when it gives none.
func (e *Engine) Guide(provider connection.Provider) Guide {
	guide, _ := e.providers[provider].(Guide)
	return guide
}
