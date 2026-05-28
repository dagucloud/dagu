// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func newMemoryAgentConfigStore() *store.AgentConfigStore {
	col := testutil.NewMemoryBackend().Collection("agent")
	return store.NewAgentConfigStore(col)
}

func newFileAgentConfigStore(t *testing.T, dir string) *store.AgentConfigStore {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	return store.NewAgentConfigStore(file.NewCollection(dir, file.WithIndentedJSON()))
}

func TestAgentConfigStore_Load_DefaultWhenMissing(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentConfigStore()
	cfg, err := s.Load(context.Background())
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, agent.DefaultConfig().Enabled, cfg.Enabled)
}

func TestAgentConfigStore_SaveLoad_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentConfigStore()
	ctx := context.Background()

	cfg := agent.DefaultConfig()
	cfg.Enabled = true
	require.NoError(t, s.Save(ctx, cfg))

	got, err := s.Load(ctx)
	require.NoError(t, err)
	assert.True(t, got.Enabled)
}

func TestAgentConfigStore_EnvOverride(t *testing.T) {
	s := newMemoryAgentConfigStore()
	ctx := context.Background()

	cfg := agent.DefaultConfig()
	cfg.Enabled = false
	require.NoError(t, s.Save(ctx, cfg))

	t.Setenv("DAGU_AGENT_ENABLED", "true")
	got, err := s.Load(ctx)
	require.NoError(t, err)
	assert.True(t, got.Enabled, "env override must apply on top of file value")
}

func TestAgentConfigStore_IsEnabled(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentConfigStore()
	ctx := context.Background()

	require.NoError(t, s.Save(ctx, &agent.Config{Enabled: true}))
	assert.True(t, s.IsEnabled(ctx))
}

// On-disk bytes equal json.MarshalIndent(cfg, "", "  ") at {dir}/config.json.
func TestAgentConfigStore_File_OnDiskFormatMatchesReleasedJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileAgentConfigStore(t, dir)
	cfg := agent.DefaultConfig()
	cfg.Enabled = true
	require.NoError(t, s.Save(context.Background(), cfg))

	got, err := os.ReadFile(filepath.Join(dir, "config.json"))
	require.NoError(t, err)
	expected, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	assert.True(t, bytes.Equal(got, expected),
		"on-disk bytes must equal json.MarshalIndent output")
}
