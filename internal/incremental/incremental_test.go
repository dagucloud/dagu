// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package incremental_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/incremental"
	"github.com/dagucloud/dagu/v2/internal/persis/file/materialization"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareCommitAndReuse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workingDir := t.TempDir()
	inputPath := filepath.Join(workingDir, "input.txt")
	secondInputPath := filepath.Join(workingDir, "second.txt")
	outputPath := filepath.Join(workingDir, "output.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))
	require.NoError(t, os.WriteFile(secondInputPath, []byte("second"), 0o600))

	store := materialization.New(filepath.Join(t.TempDir(), "materializations"))
	request := prepareRequest(workingDir, inputPath, outputPath)
	request.Step.Inputs = append(request.Step.Inputs, core.StepInputDeclaration{Name: "second", Path: secondInputPath})
	request.Environment[exec.EnvKeyDAGRunID] = "run-1"

	first, err := incremental.Prepare(ctx, store, request)
	require.NoError(t, err)
	require.NoError(t, first.Evaluate(ctx))
	assert.Equal(t, incremental.DecisionExecute, first.Metadata().Decision)
	assert.Equal(t, "manifest_missing", first.Metadata().Reason)

	outputs, staging, err := first.NewAttempt(0)
	require.NoError(t, err)
	assert.Equal(t, staging, outputs["artifact"])
	require.NoError(t, os.WriteFile(staging, []byte("result"), 0o600))
	require.NoError(t, first.Commit(ctx, staging))
	require.NoError(t, first.Close(staging))

	request.DAGRunID = "run-2"
	request.AttemptID = "attempt-2"
	request.Environment[exec.EnvKeyDAGRunID] = "run-2"
	request.Step.Inputs[0], request.Step.Inputs[1] = request.Step.Inputs[1], request.Step.Inputs[0]
	second, err := incremental.Prepare(ctx, store, request)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close("")) })
	require.NoError(t, second.Evaluate(ctx))

	assert.True(t, second.Reused())
	assert.Equal(t, "matched", second.Metadata().Reason)
	assert.Equal(t, exec.NewDAGRunRef("incremental-test", "run-1"), second.Metadata().ProducerRun)
	assert.Equal(t, outputPath, second.PublishedOutputs()["artifact"])
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Equal(t, "result", string(content))
}

func TestPrepareExplainsWhyExecutionIsRequired(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workingDir := t.TempDir()
	inputPath := filepath.Join(workingDir, "input.txt")
	outputPath := filepath.Join(workingDir, "output.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))
	store := materialization.New(filepath.Join(t.TempDir(), "materializations"))
	request := prepareRequest(workingDir, inputPath, outputPath)

	first, err := incremental.Prepare(ctx, store, request)
	require.NoError(t, err)
	require.NoError(t, first.Evaluate(ctx))
	_, staging, err := first.NewAttempt(0)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(staging, []byte("result"), 0o600))
	require.NoError(t, first.Commit(ctx, staging))
	require.NoError(t, first.Close(staging))

	t.Run("input changed", func(t *testing.T) {
		require.NoError(t, os.WriteFile(inputPath, []byte("changed"), 0o600))
		session, err := incremental.Prepare(ctx, store, request)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, incremental.DecisionExecute, session.Metadata().Decision)
		assert.Equal(t, "input_changed", session.Metadata().Reason)
	})

	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))
	t.Run("recipe changed", func(t *testing.T) {
		changed := request
		changed.Environment = map[string]string{"MODE": "other"}
		session, err := incremental.Prepare(ctx, store, changed)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, "recipe_changed", session.Metadata().Reason)
	})

	t.Run("step environment changed", func(t *testing.T) {
		changed := request
		changed.Step.Env = []string{"TARGET=${outputs.artifact}"}
		session, err := incremental.Prepare(ctx, store, changed)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, "recipe_changed", session.Metadata().Reason)
	})

	t.Run("effective shell changed", func(t *testing.T) {
		changed := request
		changed.Shell = []string{"bash", "-e"}
		session, err := incremental.Prepare(ctx, store, changed)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, "recipe_changed", session.Metadata().Reason)
	})

	t.Run("reuse disabled", func(t *testing.T) {
		disabled := request
		disabled.NoReuse = true
		session, err := incremental.Prepare(ctx, store, disabled)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, "reuse_disabled", session.Metadata().Reason)
	})

	t.Run("secret consuming step", func(t *testing.T) {
		secret := request
		secret.HasSecrets = true
		session, err := incremental.Prepare(ctx, store, secret)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, incremental.DecisionAlways, session.Metadata().Decision)
		assert.Equal(t, "ineligible", session.Metadata().Reason)
		assert.Empty(t, session.Metadata().Fingerprint)
	})
}

func TestPrepareDryRunDoesNotAcquirePathLocks(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	inputPath := filepath.Join(workingDir, "input.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))
	store := &previewStore{}
	request := prepareRequest(workingDir, inputPath, filepath.Join(workingDir, "output.txt"))
	request.Dry = true

	session, err := incremental.Prepare(context.Background(), store, request)
	require.NoError(t, err)
	require.NoError(t, session.Evaluate(context.Background()))
	assert.False(t, store.acquireCalled)
	assert.Equal(t, incremental.DecisionExecute, session.Metadata().Decision)
	assert.Equal(t, "manifest_missing", session.Metadata().Reason)
}

func TestComparisonKeyUsesFilesystemCaseSemantics(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	upper := filepath.Join(dir, "Artifact.txt")
	lower := filepath.Join(dir, "artifact.txt")
	require.NoError(t, os.WriteFile(upper, []byte("output"), 0o600))

	upperInfo, err := os.Lstat(upper)
	require.NoError(t, err)
	lowerInfo, lowerErr := os.Lstat(lower)
	if lowerErr == nil && os.SameFile(upperInfo, lowerInfo) {
		assert.Equal(t, incremental.ComparisonKey(upper), incremental.ComparisonKey(lower))
		return
	}
	require.ErrorIs(t, lowerErr, os.ErrNotExist)
	assert.NotEqual(t, incremental.ComparisonKey(upper), incremental.ComparisonKey(lower))
}

func TestResolvePathRejectsExistingOutputDirectory(t *testing.T) {
	t.Parallel()

	outputPath := filepath.Join(t.TempDir(), "output")
	require.NoError(t, os.Mkdir(outputPath, 0o750))

	_, err := incremental.ResolvePath(outputPath, "", true)
	require.ErrorContains(t, err, "must be a regular file")
}

type previewStore struct {
	acquireCalled bool
}

func (s *previewStore) Get(context.Context, string) (*exec.Materialization, error) {
	return nil, exec.ErrMaterializationNotFound
}

func (s *previewStore) AcquirePaths(context.Context, []exec.PathLockRequest) (exec.MaterializationLock, error) {
	s.acquireCalled = true
	return nil, nil
}

func (*previewStore) Commit(context.Context, exec.MaterializationLock, exec.MaterializationCommit) error {
	return nil
}

func prepareRequest(workingDir, inputPath, outputPath string) incremental.PrepareRequest {
	return incremental.PrepareRequest{
		DAG: &core.DAG{
			Name:       "incremental-test",
			Type:       core.TypeIncremental,
			WorkingDir: workingDir,
		},
		Step: core.Step{
			ID:       "build",
			Name:     "build",
			Commands: []core.CommandEntry{{Command: "build"}},
			Inputs:   []core.StepInputDeclaration{{Name: "source", Path: inputPath}},
			Outputs:  []core.StepOutputDeclaration{{Name: "artifact", Path: outputPath}},
		},
		DAGRunID:   "run-1",
		AttemptID:  "attempt-1",
		WorkingDir: workingDir,
		Shell:      []string{"sh"},
		Environment: map[string]string{
			"MODE": "default",
		},
	}
}
