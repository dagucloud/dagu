// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/dagstate"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewContext_InitializesConfiguredDAGStateStore(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	stateDir := filepath.Join(home, "custom-state")
	configPath := writeStateTestConfig(t, home, stateDir)

	command := &cobra.Command{Use: "status"}
	initFlags(command)
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))
	require.NoError(t, command.Flags().Set("config", configPath))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)
	require.NotNil(t, ctx.StateStore)

	ref := dagstate.Ref{Scope: dagstate.ScopeDAG, Namespace: "daily-agent", Key: "cursor"}
	value, err := dagstate.NormalizeValue([]byte(`{"last_id":123}`))
	require.NoError(t, err)
	_, err = ctx.StateStore.Put(ctx.Context, ref, value, dagstate.PutOptions{})
	require.NoError(t, err)

	recordID, err := ref.RecordID()
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(append([]string{stateDir}, strings.Split(recordID, "/")...)...) + ".json")
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"last_id":123`)

	got, err := ctx.StateStore.Get(ctx.Context, ref)
	require.NoError(t, err)
	assert.JSONEq(t, `{"last_id":123}`, string(got.Value))
}

func TestRunStart_StateActionPersistsInConfiguredStore(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	stateDir := filepath.Join(home, "custom-state")
	configPath := writeStateTestConfig(t, home, stateDir)

	dagFile := filepath.Join(home, "state-action.yaml")
	require.NoError(t, os.WriteFile(dagFile, []byte(`
name: state-action-start-test
steps:
  - name: save
    action: state.set
    with:
      key: cursor
      value:
        last_id: 123
`), 0o600))

	command := &cobra.Command{Use: "start"}
	initFlags(command, startFlags...)
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))
	require.NoError(t, command.Flags().Set("config", configPath))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)

	require.NoError(t, runStart(ctx, []string{dagFile}))

	got, err := ctx.StateStore.Get(ctx.Context, dagstate.Ref{
		Scope:     dagstate.ScopeDAG,
		Namespace: "state-action-start-test",
		Key:       "cursor",
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"last_id":123}`, string(got.Value))
}

func TestRunDry_StateActionUsesConfiguredStore(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	stateDir := filepath.Join(home, "custom-state")
	configPath := writeStateTestConfig(t, home, stateDir)

	dagFile := filepath.Join(home, "state-action-dry-test.yaml")
	require.NoError(t, os.WriteFile(dagFile, []byte(`
name: state-action-dry-test
steps:
  - name: load
    action: state.get
    with:
      key: cursor
      default:
        last_id: 0
`), 0o600))

	command := &cobra.Command{Use: "dry"}
	initFlags(command, dryFlags...)
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))
	require.NoError(t, command.Flags().Set("config", configPath))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)
	require.NoError(t, runDry(ctx, []string{dagFile}))
}

func writeStateTestConfig(t *testing.T, home, stateDir string) string {
	t.Helper()

	configPath := filepath.Join(home, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, fmt.Appendf(nil, `
paths:
  dag_state_dir: %q
`, stateDir), 0o600))
	return configPath
}
