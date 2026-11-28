// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/connection"
)

// GetConnectionAttention counts the connections that need reconnecting in
// the workspaces the caller can see.
func (a *API) GetConnectionAttention(ctx context.Context, _ api.GetConnectionAttentionRequestObject) (api.GetConnectionAttentionResponseObject, error) {
	attention, err := a.connectionAttention(ctx)
	if err != nil {
		return nil, err
	}
	return api.GetConnectionAttention200JSONResponse(attention), nil
}

// GetConnectionAttentionData returns the body of GetConnectionAttention for
// the connectionattention SSE topic, which takes no identifier.
func (a *API) GetConnectionAttentionData(ctx context.Context, _ string) (any, error) {
	return a.connectionAttention(ctx)
}

// connectionAttention counts the visible connections in needs_reconnect.
// It reads the token state of each, which is where the status is kept,
// with the store's plain read: a missing or undecodable state is not one
// that needs reconnecting.
func (a *API) connectionAttention(ctx context.Context) (api.ConnectionAttention, error) {
	var attention api.ConnectionAttention
	if a.connectionStore == nil {
		return attention, nil
	}
	conns, err := a.connectionStore.ListAll(ctx)
	if err != nil {
		return attention, fmt.Errorf("failed to list connections: %w", err)
	}
	visible := make(map[string]bool)
	for _, conn := range conns {
		// The notification connection belongs to no workspace.
		if conn.Workspace == "" {
			continue
		}
		seen, ok := visible[conn.Workspace]
		if !ok {
			role, found, err := a.effectiveRoleForWorkspace(ctx, conn.Workspace)
			if err != nil {
				return attention, err
			}
			seen = found && role.IsSet()
			visible[conn.Workspace] = seen
		}
		if !seen {
			continue
		}
		state, err := a.connectionStore.TokenState(ctx, conn)
		if errors.Is(err, connection.ErrCorrupt) {
			continue
		}
		if err != nil {
			return attention, fmt.Errorf("failed to read connection status: %w", err)
		}
		if state.Status == connection.TokenStatusNeedsReconnect {
			attention.NeedsReconnect++
		}
	}
	return attention, nil
}
