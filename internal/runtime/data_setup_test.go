// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataSetupResolvesStdoutArtifact(t *testing.T) {
	t.Parallel()

	artifactDir := t.TempDir()
	logDir := t.TempDir()
	step := core.Step{Name: "report", StdoutArtifact: "reports/report.md"}
	ctx := NewContext(
		context.Background(),
		&core.DAG{Name: "test"},
		"run-1",
		filepath.Join(logDir, "dag.log"),
		WithArtifactDir(artifactDir),
	)
	ctx = WithEnv(ctx, NewEnv(ctx, step))
	data := newSafeData(NodeData{Step: step})

	err := data.Setup(ctx, filepath.Join(logDir, "step"), time.Now())
	require.NoError(t, err)

	resolvedArtifactDir, err := filepath.EvalSymlinks(artifactDir)
	require.NoError(t, err)

	got := data.Step()
	assert.Equal(t, filepath.Join(resolvedArtifactDir, "reports", "report.md"), got.Stdout)
	_, err = os.Stat(filepath.Join(resolvedArtifactDir, "reports"))
	require.NoError(t, err)
}

func TestDataSetupResolvesStderrArtifact(t *testing.T) {
	t.Parallel()

	artifactDir := t.TempDir()
	logDir := t.TempDir()
	step := core.Step{Name: "report", StderrArtifact: "reports/report.err"}
	ctx := NewContext(
		context.Background(),
		&core.DAG{Name: "test"},
		"run-1",
		filepath.Join(logDir, "dag.log"),
		WithArtifactDir(artifactDir),
	)
	ctx = WithEnv(ctx, NewEnv(ctx, step))
	data := newSafeData(NodeData{Step: step})

	err := data.Setup(ctx, filepath.Join(logDir, "step"), time.Now())
	require.NoError(t, err)

	resolvedArtifactDir, err := filepath.EvalSymlinks(artifactDir)
	require.NoError(t, err)

	got := data.Step()
	assert.Equal(t, filepath.Join(resolvedArtifactDir, "reports", "report.err"), got.Stderr)
	_, err = os.Stat(filepath.Join(resolvedArtifactDir, "reports"))
	require.NoError(t, err)
}

func TestDataSetupRejectsStdoutArtifactWithoutArtifactDir(t *testing.T) {
	t.Parallel()

	logDir := t.TempDir()
	step := core.Step{Name: "report", StdoutArtifact: "reports/report.md"}
	ctx := NewContext(context.Background(), &core.DAG{Name: "test"}, "run-1", filepath.Join(logDir, "dag.log"))
	ctx = WithEnv(ctx, NewEnv(ctx, step))
	data := newSafeData(NodeData{Step: step})

	err := data.Setup(ctx, filepath.Join(logDir, "step"), time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DAG_RUN_ARTIFACTS_DIR is not set")
}

func TestDataSetupRejectsEscapingStdoutArtifact(t *testing.T) {
	t.Parallel()

	artifactDir := t.TempDir()
	logDir := t.TempDir()
	step := core.Step{Name: "report", StdoutArtifact: "../report.md"}
	ctx := NewContext(
		context.Background(),
		&core.DAG{Name: "test"},
		"run-1",
		filepath.Join(logDir, "dag.log"),
		WithArtifactDir(artifactDir),
	)
	ctx = WithEnv(ctx, NewEnv(ctx, step))
	data := newSafeData(NodeData{Step: step})

	err := data.Setup(ctx, filepath.Join(logDir, "step"), time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "artifact path must not contain parent directory segments")
}

func TestDataSetupRejectsSymlinkStdoutArtifact(t *testing.T) {
	t.Parallel()

	artifactDir := t.TempDir()
	logDir := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(artifactDir, "reports")); err != nil {
		t.Skipf("symlink creation is not available: %v", err)
	}

	step := core.Step{Name: "report", StdoutArtifact: "reports/report.md"}
	ctx := NewContext(
		context.Background(),
		&core.DAG{Name: "test"},
		"run-1",
		filepath.Join(logDir, "dag.log"),
		WithArtifactDir(artifactDir),
	)
	ctx = WithEnv(ctx, NewEnv(ctx, step))
	data := newSafeData(NodeData{Step: step})

	err := data.Setup(ctx, filepath.Join(logDir, "step"), time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes artifact directory")
}
