// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/connection"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
)

// connectionAttentionMaxAge bounds how long one read of the connections
// serves callers, for a status change that no wake reported. A refresh of
// the attention topic may use a read up to this old, so such a change
// shows within the topic's maximum interval plus this age.
const connectionAttentionMaxAge = 5 * time.Minute

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

// ConnectionsChanged tells the API that a connection's status may have
// changed, so the next count reads the connections again.
func (a *API) ConnectionsChanged() {
	a.attention.invalidate()
}

// connectionAttention sums, over the workspaces the caller can see, the
// counts that one shared read of the connections found.
func (a *API) connectionAttention(ctx context.Context) (api.ConnectionAttention, error) {
	attention := api.ConnectionAttention{Workspaces: []string{}}
	if a.connectionStore == nil {
		return attention, nil
	}
	// The read serves other callers too, so one caller's cancel must not
	// end it.
	readCtx := context.WithoutCancel(ctx)
	counts, err := a.attention.get(func() (map[string]int, error) { return a.readNeedsReconnect(readCtx) })
	if err != nil {
		return attention, err
	}
	for ws, n := range counts {
		role, ok, err := a.effectiveRoleForWorkspace(ctx, ws)
		if err != nil {
			return attention, err
		}
		if !ok || !role.IsSet() {
			continue
		}
		attention.NeedsReconnect += n
		attention.Workspaces = append(attention.Workspaces, connectionWorkspaceToAPI(ws))
	}
	slices.Sort(attention.Workspaces)
	return attention, nil
}

// readNeedsReconnect counts the connections in needs_reconnect per
// workspace. It lists each workspace with the store's tolerant List, so an
// unreadable record hides only itself, and reads each status with the
// plain token state read: a missing or undecodable state is not one that
// needs reconnecting. The notification connection belongs to no workspace
// and is left out.
func (a *API) readNeedsReconnect(ctx context.Context) (map[string]int, error) {
	workspaces := []string{secretpkg.GlobalWorkspace}
	if a.workspaceStore != nil {
		list, err := a.workspaceStore.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list workspaces: %w", err)
		}
		for _, ws := range list {
			workspaces = append(workspaces, ws.Name)
		}
	}
	counts := make(map[string]int)
	for _, ws := range workspaces {
		conns, err := a.connectionStore.List(ctx, ws)
		if err != nil {
			return nil, fmt.Errorf("failed to list connections: %w", err)
		}
		for _, conn := range conns {
			state, err := a.connectionStore.TokenState(ctx, conn)
			if errors.Is(err, connection.ErrCorrupt) {
				logger.Warn(ctx, "Skipped an unreadable connection status",
					slog.String("connection", connectionLabel(conn)), tag.Error(err))
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("failed to read connection status: %w", err)
			}
			if state.Status == connection.TokenStatusNeedsReconnect {
				counts[ws]++
			}
		}
	}
	return counts, nil
}

// attentionCounts shares one read of the connections' statuses among the
// callers that ask for it until a change invalidates the read or it grows
// older than connectionAttentionMaxAge. A wake of the attention topic then
// costs one read, however many tabs subscribe.
type attentionCounts struct {
	// generation increases on each invalidation, without waiting for a
	// read in progress.
	generation atomic.Uint64
	// readMu lets one read run at a time; callers that wait reuse it.
	readMu sync.Mutex

	mu      sync.Mutex
	counts  map[string]int
	readGen uint64
	readAt  time.Time
}

func (c *attentionCounts) invalidate() {
	c.generation.Add(1)
}

func (c *attentionCounts) get(read func() (map[string]int, error)) (map[string]int, error) {
	if counts, ok := c.current(); ok {
		return counts, nil
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if counts, ok := c.current(); ok {
		return counts, nil
	}
	// A change during the read invalidates it, as it may have missed it.
	gen := c.generation.Load()
	counts, err := read()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.counts, c.readGen, c.readAt = counts, gen, time.Now()
	c.mu.Unlock()
	return counts, nil
}

func (c *attentionCounts) current() (map[string]int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readAt.IsZero() || c.readGen != c.generation.Load() || time.Since(c.readAt) >= connectionAttentionMaxAge {
		return nil, false
	}
	return c.counts, true
}
