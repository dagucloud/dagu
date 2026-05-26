// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	legacymodel "github.com/dagucloud/dagu/internal/persis/legacy/model"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateHistoryCommand(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	dagRunsDir := filepath.Join(tempDir, "dag-runs")
	dagsDir := filepath.Join(tempDir, "dags")

	require.NoError(t, os.MkdirAll(dataDir, 0750))
	require.NoError(t, os.MkdirAll(dagRunsDir, 0750))
	require.NoError(t, os.MkdirAll(dagsDir, 0750))

	// Create legacy data
	legacyDagDir := filepath.Join(dataDir, "test-dag-abc123")
	require.NoError(t, os.MkdirAll(legacyDagDir, 0750))

	// Create legacy status
	legacyStatus := legacymodel.Status{
		RequestID:  "req123",
		Name:       "test-dag",
		Status:     core.Succeeded,
		StartedAt:  time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
		FinishedAt: time.Now().Add(-30 * time.Minute).Format(time.RFC3339),
		Nodes: []*legacymodel.Node{
			{
				Step:       core.Step{Name: "step1"},
				Status:     core.NodeSucceeded,
				StartedAt:  time.Now().Add(-50 * time.Minute).Format(time.RFC3339),
				FinishedAt: time.Now().Add(-40 * time.Minute).Format(time.RFC3339),
			},
		},
	}

	// Write legacy data file
	statusData, _ := json.Marshal(legacyStatus)
	datFile := filepath.Join(legacyDagDir, "test-dag.20240101.100000.req123.dat")
	require.NoError(t, os.WriteFile(datFile, statusData, 0600))

	// Create DAG file
	dagPath := filepath.Join(dagsDir, "test-dag.yaml")
	require.NoError(t, os.WriteFile(dagPath, []byte("name: test-dag\nsteps:\n  - name: step1\n    command: echo test"), 0600))

	// Create a test context
	cfg := &config.Config{
		Paths: config.PathsConfig{
			DataDir:    dataDir,
			DAGRunsDir: dagRunsDir,
			DAGsDir:    dagsDir,
		},
	}

	// Create stores
	dagRunStore := dagrun.New(dagRunsDir)

	// Create command context
	cmd := &cobra.Command{}
	ctx := &Context{
		Context:     context.Background(),
		Command:     cmd,
		Config:      cfg,
		DAGRunStore: dagRunStore,
	}

	t.Run("SuccessfulMigration", func(t *testing.T) {
		err := runMigration(ctx)
		require.NoError(t, err)

		// Verify migration
		attempt, err := dagRunStore.FindAttempt(context.Background(), exec.NewDAGRunRef("test-dag", "req123"))
		require.NoError(t, err)
		require.NotNil(t, attempt)

		dagRunStatus, err := attempt.ReadStatus(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "req123", dagRunStatus.DAGRunID)
		assert.Equal(t, "test-dag", dagRunStatus.Name)
		assert.Equal(t, core.Succeeded, dagRunStatus.Status)

		// Verify legacy directory was moved
		_, err = os.Stat(legacyDagDir)
		assert.True(t, os.IsNotExist(err))

		// Verify archive exists
		entries, err := os.ReadDir(dataDir)
		require.NoError(t, err)

		archiveFound := false
		for _, entry := range entries {
			if entry.IsDir() && len(entry.Name()) > 17 && entry.Name()[:17] == "history_migrated_" {
				archiveFound = true
			}
		}
		assert.True(t, archiveFound)
	})
}

func TestMigrateCommand_NoLegacyData(t *testing.T) {
	// Create temporary directories
	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	dagRunsDir := filepath.Join(tempDir, "dag-runs")
	dagsDir := filepath.Join(tempDir, "dags")

	require.NoError(t, os.MkdirAll(dataDir, 0750))
	require.NoError(t, os.MkdirAll(dagRunsDir, 0750))
	require.NoError(t, os.MkdirAll(dagsDir, 0750))

	// Create a test context
	cfg := &config.Config{
		Paths: config.PathsConfig{
			DataDir:    dataDir,
			DAGRunsDir: dagRunsDir,
			DAGsDir:    dagsDir,
		},
	}

	// Create stores
	dagRunStore := dagrun.New(dagRunsDir)

	// Create command context
	cmd := &cobra.Command{}
	ctx := &Context{
		Context:     context.Background(),
		Command:     cmd,
		Config:      cfg,
		DAGRunStore: dagRunStore,
	}

	// Run migration with no legacy data
	err := runMigration(ctx)
	require.NoError(t, err)

	// Should complete without errors
}

func TestCmdMigrate(t *testing.T) {
	cmd := Migrate()
	assert.NotNil(t, cmd)
	assert.Equal(t, "migrate", cmd.Use)
	assert.True(t, cmd.HasSubCommands())

	// Check for history subcommand
	historyCmd := cmd.Commands()[0]
	assert.Equal(t, "history", historyCmd.Use)
}
