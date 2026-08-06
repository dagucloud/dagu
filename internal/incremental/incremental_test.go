// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package incremental_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	assert.Equal(t, exec.IncrementalDecisionExecute, first.Metadata().Decision)
	assert.Equal(t, exec.IncrementalReasonManifestMissing, first.Metadata().Reason)

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
	assert.Equal(t, exec.IncrementalReasonMatched, second.Metadata().Reason)
	assert.Equal(t, exec.NewDAGRunRef("incremental-test", "run-1"), second.Metadata().ProducerRun)
	assert.Equal(t, outputPath, second.PublishedOutputs()["artifact"])
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Equal(t, "result", string(content))
}

func TestPrepareReevaluatesAfterWaitingForOutputLock(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
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

	acquireStarted := make(chan struct{})
	secondResult := make(chan prepareResult, 1)
	go func() {
		secondRequest := request
		secondRequest.DAGRunID = "run-2"
		secondRequest.AttemptID = "attempt-2"
		session, prepareErr := incremental.Prepare(ctx, notifyingStore{
			MaterializationStore: store,
			acquireStarted:       acquireStarted,
		}, secondRequest)
		secondResult <- prepareResult{session: session, err: prepareErr}
	}()

	select {
	case <-acquireStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, first.Close(""))

	var result prepareResult
	select {
	case result = <-secondResult:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, result.err)
	require.NotNil(t, result.session)
	t.Cleanup(func() { require.NoError(t, result.session.Close("")) })
	require.NoError(t, result.session.Evaluate(ctx))
	assert.True(t, result.session.Reused())
	assert.Equal(t, exec.IncrementalReasonMatched, result.session.Metadata().Reason)
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
		assert.Equal(t, exec.IncrementalDecisionExecute, session.Metadata().Decision)
		assert.Equal(t, exec.IncrementalReasonInputChanged, session.Metadata().Reason)
	})

	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))
	t.Run("recipe changed", func(t *testing.T) {
		changed := request
		changed.Environment = map[string]string{"MODE": "other"}
		session, err := incremental.Prepare(ctx, store, changed)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, exec.IncrementalReasonRecipeChanged, session.Metadata().Reason)
	})

	t.Run("step environment changed", func(t *testing.T) {
		changed := request
		changed.Step.Env = []string{"TARGET=${outputs.artifact}"}
		session, err := incremental.Prepare(ctx, store, changed)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, exec.IncrementalReasonRecipeChanged, session.Metadata().Reason)
	})

	t.Run("effective shell changed", func(t *testing.T) {
		changed := request
		changed.Shell = []string{"bash", "-e"}
		session, err := incremental.Prepare(ctx, store, changed)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, exec.IncrementalReasonRecipeChanged, session.Metadata().Reason)
	})

	t.Run("reuse disabled", func(t *testing.T) {
		disabled := request
		disabled.NoReuse = true
		session, err := incremental.Prepare(ctx, store, disabled)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, exec.IncrementalReasonReuseDisabled, session.Metadata().Reason)
	})

	t.Run("secret consuming step", func(t *testing.T) {
		secret := request
		secret.HasSecrets = true
		session, err := incremental.Prepare(ctx, store, secret)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, exec.IncrementalDecisionAlways, session.Metadata().Decision)
		assert.Equal(t, exec.IncrementalReasonIneligible, session.Metadata().Reason)
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
	assert.Equal(t, exec.IncrementalDecisionExecute, session.Metadata().Decision)
	assert.Equal(t, exec.IncrementalReasonManifestMissing, session.Metadata().Reason)
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

func TestComparisonKeyResolvesExistingAncestorAliases(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	aliasDir := filepath.Join(dir, "alias")
	require.NoError(t, os.Mkdir(realDir, 0o750))
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Skipf("filesystem symlinks are unavailable: %v", err)
	}

	assert.Equal(t,
		incremental.ComparisonKey(filepath.Join(realDir, "artifact.txt")),
		incremental.ComparisonKey(filepath.Join(aliasDir, "artifact.txt")),
	)
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

type notifyingStore struct {
	exec.MaterializationStore
	acquireStarted chan struct{}
}

type prepareResult struct {
	session *incremental.Session
	err     error
}

func (s notifyingStore) AcquirePaths(
	ctx context.Context,
	requests []exec.PathLockRequest,
) (exec.MaterializationLock, error) {
	close(s.acquireStarted)
	return s.MaterializationStore.AcquirePaths(ctx, requests)
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
