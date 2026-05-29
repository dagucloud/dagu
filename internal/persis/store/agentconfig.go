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
	rec *SingleRecord[agent.Config]
}

// NewAgentConfigStore creates an AgentConfigStore backed by col.
func NewAgentConfigStore(col persis.Collection) *AgentConfigStore {
	return &AgentConfigStore{rec: NewSingleRecord[agent.Config](col, agentConfigRecordID)}
}

// Load reads the agent configuration. Missing record falls back to
// [agent.DefaultConfig]. The DAGU_AGENT_ENABLED env var overrides the
// Enabled field after the file value is applied.
func (s *AgentConfigStore) Load(ctx context.Context) (*agent.Config, error) {
	cfg := agent.DefaultConfig()
	// Decode over the defaults: an absent record keeps them, and a stored
	// record overrides only the fields it contains.
	if _, err := s.rec.Load(ctx, cfg); err != nil {
		return nil, fmt.Errorf("agent-config store: load: %w", err)
	}
	applyAgentEnvOverrides(cfg)
	return cfg, nil
}

// Save writes the agent configuration.
func (s *AgentConfigStore) Save(ctx context.Context, cfg *agent.Config) error {
	if cfg == nil {
		return errors.New("agent-config store: config cannot be nil")
	}
	if err := s.rec.Save(ctx, cfg); err != nil {
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
