// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package remotenode

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

// Source indicates where a remote node is defined.
type Source string

const (
	SourceConfig Source = "config"
	SourceStore  Source = "store"
)

// ResolvedNode wraps a remote node with its source information.
type ResolvedNode struct {
	*RemoteNode
	Source Source
}

// Resolver merges remote nodes from config file and store.
// Store nodes take precedence over config nodes on name collision.
type Resolver struct {
	nodes map[string]*RemoteNode
	store Store
}

// NewResolver creates a Resolver from config-file nodes and an optional store.
func NewResolver(configNodes []config.RemoteNode, store Store) *Resolver {
	nodeMap := make(map[string]*RemoteNode, len(configNodes))
	for _, cn := range configNodes {
		nodeMap[cn.Name] = FromConfigNode(cn)
	}
	return &Resolver{
		nodes: nodeMap,
		store: store,
	}
}

// GetByName looks up a remote node by name, trying the store first (if available),
// then falling back to config.
func (r *Resolver) GetByName(ctx context.Context, name string) (*RemoteNode, error) {
	// Try store first (higher precedence)
	if r.store != nil {
		node, err := r.store.GetByName(ctx, name)
		if err == nil {
			return node, nil
		}
		// Only fall through if not found; other errors are real failures
		if !errors.Is(err, ErrRemoteNodeNotFound) {
			return nil, err
		}
	}

	// Fall back to config
	n, ok := r.nodes[name]
	if !ok {
		return nil, ErrRemoteNodeNotFound
	}
	return n, nil
}

// GetByID retrieves a store-managed remote node by ID.
// Config nodes use synthetic IDs (cfg:<name>) and are resolved via GetByName.
func (r *Resolver) GetByID(ctx context.Context, id string) (*RemoteNode, error) {
	if r.store == nil {
		return nil, ErrRemoteNodeNotFound
	}
	return r.store.GetByID(ctx, id)
}

// ListAll returns all nodes from both sources.
// Store nodes override config nodes on name collision.
func (r *Resolver) ListAll(ctx context.Context) ([]ResolvedNode, error) {
	seen := make(map[string]struct{})
	var result []ResolvedNode

	// Store nodes first (higher precedence)
	if r.store != nil {
		storeNodes, err := r.store.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list store remote nodes: %w", err)
		}
		for _, n := range storeNodes {
			seen[n.Name] = struct{}{}
			result = append(result, ResolvedNode{
				RemoteNode: n,
				Source:     SourceStore,
			})
		}
	}

	// Config nodes (skip if overridden by store)
	for _, n := range r.nodes {
		if _, ok := seen[n.Name]; ok {
			continue
		}
		result = append(result, ResolvedNode{
			RemoteNode: n,
			Source:     SourceConfig,
		})
	}

	return result, nil
}

// GetConfigByName looks up a config-sourced remote node by name.
// Unlike GetByName, this only checks config nodes and never the store.
func (r *Resolver) GetConfigByName(name string) (*RemoteNode, error) {
	n, ok := r.nodes[name]
	if !ok {
		return nil, ErrRemoteNodeNotFound
	}
	return n, nil
}

// ListNames returns deduplicated node names from both sources.
func (r *Resolver) ListNames(ctx context.Context) ([]string, error) {
	nodes, err := r.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.Name)
	}
	return names, nil
}

// ConfigNodeIDPrefix is prepended to config node names to form a synthetic ID.
const ConfigNodeIDPrefix = "cfg:"

// ToConfigNode converts a domain RemoteNode to config.RemoteNode
// for backward compatibility with proxy/SSE middleware.
func ToConfigNode(n *RemoteNode) config.RemoteNode {
	var timeoutSec int
	if n.Timeout > 0 {
		timeoutSec = int(n.Timeout.Seconds())
	}
	return config.RemoteNode{
		Name:              n.Name,
		Description:       n.Description,
		APIBaseURL:        n.APIBaseURL,
		AuthType:          string(n.AuthType),
		BasicAuthUsername: n.BasicAuthUsername,
		BasicAuthPassword: n.BasicAuthPassword,
		AuthToken:         n.AuthToken,
		SkipTLSVerify:     n.SkipTLSVerify,
		Timeout:           timeoutSec,
	}
}

// FromConfigNode converts a config.RemoteNode to a domain RemoteNode.
// Config nodes receive a synthetic ID of "cfg:<name>".
func FromConfigNode(cn config.RemoteNode) *RemoteNode {
	authType := AuthType(cn.AuthType)
	if authType == "" {
		authType = AuthTypeNone
	}
	return &RemoteNode{
		ID:                ConfigNodeIDPrefix + cn.Name,
		Name:              cn.Name,
		Description:       cn.Description,
		APIBaseURL:        cn.APIBaseURL,
		AuthType:          authType,
		BasicAuthUsername: cn.BasicAuthUsername,
		BasicAuthPassword: cn.BasicAuthPassword,
		AuthToken:         cn.AuthToken,
		SkipTLSVerify:     cn.SkipTLSVerify,
		Timeout:           time.Duration(cn.Timeout) * time.Second,
	}
}
