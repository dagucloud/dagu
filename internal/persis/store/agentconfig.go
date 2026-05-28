// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/persis"
)

const (
	agentConfigRecordID = "config"
	envAgentEnabled     = "DAGU_AGENT_ENABLED"
)

var _ agent.ConfigStore = (*AgentConfigStore)(nil)

// AgentConfigStore implements [agent.ConfigStore] over a single
// [persis.Collection] record.
type AgentConfigStore struct {
	col persis.Collection
}

// NewAgentConfigStore creates an AgentConfigStore backed by col.
func NewAgentConfigStore(col persis.Collection) *AgentConfigStore {
	return &AgentConfigStore{col: col}
}

// Load reads the agent configuration. Missing record falls back to
// [agent.DefaultConfig]. The DAGU_AGENT_ENABLED env var overrides the
// Enabled field after the file value is applied.
func (s *AgentConfigStore) Load(ctx context.Context) (*agent.Config, error) {
	cfg := agent.DefaultConfig()
	rec, err := s.col.Get(ctx, agentConfigRecordID)
	if err != nil && !errors.Is(err, persis.ErrNotFound) {
		return nil, fmt.Errorf("agent-config store: load: %w", err)
	}
	if rec != nil {
		if err := persis.Decode(rec, cfg); err != nil {
			return nil, fmt.Errorf("agent-config store: decode: %w", err)
		}
	}
	applyAgentEnvOverrides(cfg)
	return cfg, nil
}

// Save writes the agent configuration.
func (s *AgentConfigStore) Save(ctx context.Context, cfg *agent.Config) error {
	if cfg == nil {
		return errors.New("agent-config store: config cannot be nil")
	}
	data, err := persis.Encode(cfg)
	if err != nil {
		return fmt.Errorf("agent-config store: encode: %w", err)
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:   agentConfigRecordID,
		Data: data,
	}); err != nil {
		return fmt.Errorf("agent-config store: save: %w", err)
	}
	return nil
}

// IsEnabled returns whether the agent feature is enabled.
func (s *AgentConfigStore) IsEnabled(ctx context.Context) bool {
	cfg, err := s.Load(ctx)
	if err != nil {
		return false
	}
	return cfg.Enabled
}

func applyAgentEnvOverrides(cfg *agent.Config) {
	if v := os.Getenv(envAgentEnabled); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Enabled = enabled
		}
	}
}
