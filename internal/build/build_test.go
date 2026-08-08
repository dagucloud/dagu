// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package build_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runenv "github.com/dagucloud/dagu/v2/internal/runctx/env"

	"github.com/dagucloud/dagu/v2/internal/build"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
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
	request.Step.Inputs = append(request.Step.Inputs, ir.StepInputDeclaration{Name: "second", Path: secondInputPath})
	request.Environment[runenv.EnvKeyDAGRunID] = "run-1"

	first, err := build.Prepare(ctx, store, request)
	require.NoError(t, err)
	require.NoError(t, first.Evaluate(ctx))
	assert.Equal(t, dagrun.BuildDecisionExecute, first.Metadata().Decision)
	assert.Equal(t, dagrun.BuildReasonManifestMissing, first.Metadata().Reason)

	outputs, staging, err := first.NewAttempt(0)
	require.NoError(t, err)
	assert.Equal(t, staging, outputs["artifact"])
	require.NoError(t, os.WriteFile(staging, []byte("result"), 0o600))
	require.NoError(t, first.Commit(ctx, staging))
	require.NoError(t, first.Close(staging))

	request.DAGRunID = "run-2"
	request.AttemptID = "attempt-2"
	request.Environment[runenv.EnvKeyDAGRunID] = "run-2"
	request.Step.Inputs[0], request.Step.Inputs[1] = request.Step.Inputs[1], request.Step.Inputs[0]
	second, err := build.Prepare(ctx, store, request)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close("")) })
	require.NoError(t, second.Evaluate(ctx))

	assert.True(t, second.Reused())
	assert.Equal(t, dagrun.BuildReasonMatched, second.Metadata().Reason)
	assert.Equal(t, dagrun.NewDAGRunRef("build-test", "run-1"), second.Metadata().ProducerRun)
	assert.Equal(t, outputPath, second.PublishedOutputs()["artifact"])
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Equal(t, "result", string(content))
}

func TestImplicitRunWorkingDirectoryReusesMaterialization(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := t.TempDir()
	inputPath := filepath.Join(dataDir, "input.txt")
	outputPath := filepath.Join(dataDir, "output.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))

	store := materialization.New(filepath.Join(t.TempDir(), "materializations"))
	firstRunWorkDir := t.TempDir()
	request := prepareRequest(firstRunWorkDir, inputPath, outputPath)
	request.RunWorkDir = firstRunWorkDir
	request.Environment["PWD"] = firstRunWorkDir
	first, err := build.Prepare(ctx, store, request)
	require.NoError(t, err)
	require.NoError(t, first.Evaluate(ctx))
	_, staging, err := first.NewAttempt(0)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(staging, []byte("result"), 0o600))
	require.NoError(t, first.Commit(ctx, staging))
	require.NoError(t, first.Close(staging))

	secondRunWorkDir := t.TempDir()
	request.DAGRunID = "run-2"
	request.AttemptID = "attempt-2"
	request.WorkingDir = secondRunWorkDir
	request.RunWorkDir = secondRunWorkDir
	request.Environment["PWD"] = secondRunWorkDir
	second, err := build.Prepare(ctx, store, request)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close("")) })
	require.NoError(t, second.Evaluate(ctx))
	require.True(t, second.Reused())
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
	first, err := build.Prepare(ctx, store, request)
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
		session, prepareErr := build.Prepare(ctx, notifyingStore{
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
	assert.Equal(t, dagrun.BuildReasonMatched, result.session.Metadata().Reason)
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

	first, err := build.Prepare(ctx, store, request)
	require.NoError(t, err)
	require.NoError(t, first.Evaluate(ctx))
	_, staging, err := first.NewAttempt(0)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(staging, []byte("result"), 0o600))
	require.NoError(t, first.Commit(ctx, staging))
	require.NoError(t, first.Close(staging))

	t.Run("input changed", func(t *testing.T) {
		require.NoError(t, os.WriteFile(inputPath, []byte("changed"), 0o600))
		session, err := build.Prepare(ctx, store, request)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, dagrun.BuildDecisionExecute, session.Metadata().Decision)
		assert.Equal(t, dagrun.BuildReasonInputChanged, session.Metadata().Reason)
	})

	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))
	t.Run("recipe changed", func(t *testing.T) {
		changed := request
		changed.Environment = map[string]string{"MODE": "other"}
		session, err := build.Prepare(ctx, store, changed)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, dagrun.BuildReasonRecipeChanged, session.Metadata().Reason)
	})

	t.Run("step environment changed", func(t *testing.T) {
		changed := request
		changed.Step.Env = []string{"TARGET=${outputs.artifact}"}
		session, err := build.Prepare(ctx, store, changed)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, dagrun.BuildReasonRecipeChanged, session.Metadata().Reason)
	})

	t.Run("effective shell changed", func(t *testing.T) {
		changed := request
		changed.Shell = []string{"bash", "-e"}
		session, err := build.Prepare(ctx, store, changed)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, dagrun.BuildReasonRecipeChanged, session.Metadata().Reason)
	})

	t.Run("reuse disabled", func(t *testing.T) {
		disabled := request
		disabled.NoReuse = true
		session, err := build.Prepare(ctx, store, disabled)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, dagrun.BuildReasonReuseDisabled, session.Metadata().Reason)
	})

	t.Run("secret consuming step", func(t *testing.T) {
		secret := request
		secret.HasSecrets = true
		session, err := build.Prepare(ctx, store, secret)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close("")) })
		require.NoError(t, session.Evaluate(ctx))
		assert.Equal(t, dagrun.BuildDecisionAlways, session.Metadata().Decision)
		assert.Equal(t, dagrun.BuildReasonIneligible, session.Metadata().Reason)
		assert.Empty(t, session.Metadata().Fingerprint)
	})
}

func TestIneligibleCommitPreservesReusableManifest(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workingDir := t.TempDir()
	inputPath := filepath.Join(workingDir, "input.txt")
	outputPath := filepath.Join(workingDir, "output.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))
	store := materialization.New(filepath.Join(t.TempDir(), "materializations"))
	request := prepareRequest(workingDir, inputPath, outputPath)

	first, err := build.Prepare(ctx, store, request)
	require.NoError(t, err)
	require.NoError(t, first.Evaluate(ctx))
	_, staging, err := first.NewAttempt(0)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(staging, []byte("first"), 0o600))
	require.NoError(t, first.Commit(ctx, staging))
	require.NoError(t, first.Close(staging))

	ineligibleRequest := request
	ineligibleRequest.DAGRunID = "run-2"
	ineligibleRequest.AttemptID = "attempt-2"
	ineligibleRequest.HasSecrets = true
	ineligible, err := build.Prepare(ctx, store, ineligibleRequest)
	require.NoError(t, err)
	require.NoError(t, ineligible.Evaluate(ctx))
	require.Equal(t, dagrun.BuildDecisionAlways, ineligible.Metadata().Decision)
	_, staging, err = ineligible.NewAttempt(0)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(staging, []byte("second"), 0o600))
	require.NoError(t, ineligible.Commit(ctx, staging))
	require.NoError(t, ineligible.Close(staging))

	request.DAGRunID = "run-3"
	request.AttemptID = "attempt-3"
	third, err := build.Prepare(ctx, store, request)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, third.Close("")) })
	require.NoError(t, third.Evaluate(ctx))
	require.Equal(t, dagrun.BuildDecisionExecute, third.Metadata().Decision)
	require.Equal(t, dagrun.BuildReasonOutputChanged, third.Metadata().Reason)
}

func TestPrepareDryRunDoesNotAcquirePathLocks(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	inputPath := filepath.Join(workingDir, "input.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))
	store := &previewStore{}
	request := prepareRequest(workingDir, inputPath, filepath.Join(workingDir, "output.txt"))
	request.Dry = true

	session, err := build.Prepare(context.Background(), store, request)
	require.NoError(t, err)
	require.NoError(t, session.Evaluate(context.Background()))
	assert.False(t, store.acquireCalled)
	assert.Equal(t, dagrun.BuildDecisionExecute, session.Metadata().Decision)
	assert.Equal(t, dagrun.BuildReasonManifestMissing, session.Metadata().Reason)
}

func TestCommitRejectsUnavailableStore(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	inputPath := filepath.Join(workingDir, "input.txt")
	outputPath := filepath.Join(workingDir, "output.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))
	session, err := build.Prepare(context.Background(), nil, prepareRequest(workingDir, inputPath, outputPath))
	require.ErrorContains(t, err, "store is unavailable")
	require.NotNil(t, session)

	err = session.Commit(context.Background(), filepath.Join(workingDir, ".output.tmp"))
	require.ErrorContains(t, err, "store is unavailable")
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
		assert.Equal(t, build.ComparisonKey(upper), build.ComparisonKey(lower))
		return
	}
	require.ErrorIs(t, lowerErr, os.ErrNotExist)
	assert.NotEqual(t, build.ComparisonKey(upper), build.ComparisonKey(lower))
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
		build.ComparisonKey(filepath.Join(realDir, "artifact.txt")),
		build.ComparisonKey(filepath.Join(aliasDir, "artifact.txt")),
	)
	assert.Equal(t,
		build.IdentityKey(filepath.Join(realDir, "artifact.txt")),
		build.IdentityKey(filepath.Join(aliasDir, "artifact.txt")),
	)
}

func TestIdentityKeyPreservesAuthoredCase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	assert.NotEqual(t,
		build.IdentityKey(filepath.Join(dir, "Artifact.txt")),
		build.IdentityKey(filepath.Join(dir, "artifact.txt")),
	)
}

func TestNewAttemptBoundsStagingFilename(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	inputPath := filepath.Join(workingDir, "input.txt")
	outputPath := filepath.Join(workingDir, strings.Repeat("a", 240)+".txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("input"), 0o600))
	store := materialization.New(filepath.Join(t.TempDir(), "materializations"))
	session, err := build.Prepare(context.Background(), store, prepareRequest(workingDir, inputPath, outputPath))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close("")) })

	_, staging, err := session.NewAttempt(0)
	require.NoError(t, err)
	require.LessOrEqual(t, len(filepath.Base(staging)), 255)
	require.NoError(t, os.WriteFile(staging, []byte("output"), 0o600))
}

func TestResolvePathRejectsExistingOutputDirectory(t *testing.T) {
	t.Parallel()

	outputPath := filepath.Join(t.TempDir(), "output")
	require.NoError(t, os.Mkdir(outputPath, 0o750))

	_, err := build.ResolvePath(outputPath, "", true)
	require.ErrorContains(t, err, "must be a regular file")
}

type previewStore struct {
	acquireCalled bool
}

type notifyingStore struct {
	build.MaterializationStore
	acquireStarted chan struct{}
}

type prepareResult struct {
	session *build.Session
	err     error
}

func (s notifyingStore) AcquirePaths(
	ctx context.Context,
	requests []build.PathLockRequest,
) (build.MaterializationLock, error) {
	close(s.acquireStarted)
	return s.MaterializationStore.AcquirePaths(ctx, requests)
}

func (s *previewStore) Get(context.Context, string) (*build.Materialization, error) {
	return nil, build.ErrMaterializationNotFound
}

func (s *previewStore) AcquirePaths(context.Context, []build.PathLockRequest) (build.MaterializationLock, error) {
	s.acquireCalled = true
	return nil, nil
}

func (*previewStore) Commit(context.Context, build.MaterializationLock, build.MaterializationCommit) error {
	return nil
}

func prepareRequest(workingDir, inputPath, outputPath string) build.PrepareRequest {
	return build.PrepareRequest{
		DAG: &ir.DAG{
			Name:       "build-test",
			Type:       ir.TypeBuild,
			WorkingDir: workingDir,
		},
		Step: ir.Step{
			ID:       "build",
			Name:     "build",
			Commands: []ir.CommandEntry{{Command: "build"}},
			Inputs:   []ir.StepInputDeclaration{{Name: "source", Path: inputPath}},
			Outputs:  []ir.StepOutputDeclaration{{Name: "artifact", Path: outputPath}},
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
