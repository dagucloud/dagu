// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceLifecycleAndDefinitionMutation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := NewFileStores(t.TempDir())
	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	service := NewService(
		stores.Definitions,
		stores.Runtimes,
		stores.Locker,
		NewValidator(nil),
		WithClock(func() time.Time { return now }),
		WithIDGenerator(func() (string, error) { return testControllerID, nil }),
	)

	created, err := service.Create(ctx, validCreateYAML())
	require.NoError(t, err)
	assert.Equal(t, testControllerID, created.Definition.ID)
	assert.Contains(t, created.RawYAML, "id: "+testControllerID)
	assert.False(t, created.ResourceUpdatedAt.IsZero())

	items, err := service.List(ctx)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, core.NotStarted, items[0].Status)
	assert.Equal(t, DefaultMaxTurns, items[0].MaxTurns)

	startPrompt := "  preserve this prompt verbatim  "
	runtime, err := service.Start(ctx, testControllerID, startPrompt)
	require.NoError(t, err)
	assert.Equal(t, core.Running, runtime.Status)
	assert.Equal(t, DefaultStateName, runtime.CurrentState)
	require.Len(t, runtime.Context, 1)
	assert.Equal(t, startPrompt, runtime.Context[0].Content)

	updatedYAML := []byte(strings.Replace(string(validPersistedYAML(testControllerID)), "name: Incident flow", "name: Updated flow", 1))
	_, err = service.Update(ctx, testControllerID, updatedYAML)
	assert.ErrorIs(t, err, ErrActiveController)
	assert.ErrorIs(t, service.Delete(ctx, testControllerID), ErrActiveController)

	stored, err := stores.Runtimes.Get(ctx, testControllerID)
	require.NoError(t, err)
	question := "Which region?"
	stored.Status = core.Waiting
	stored.WaitingQuestion = &question
	stored.DAGRunRefs = []DAGRunRef{{State: "default", DAG: "classify", DAGRunID: "run-1"}}
	stored.UpdatedAt = now.Add(time.Minute)
	require.NoError(t, stores.Runtimes.Put(ctx, stored))
	items, err = service.List(ctx)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotNil(t, items[0].LatestDAGRun)
	assert.Equal(t, "run-1", items[0].LatestDAGRun.DAGRunID)

	now = now.Add(2 * time.Minute)
	runtime, err = service.Prompt(ctx, testControllerID, "us-east")
	require.NoError(t, err)
	assert.Equal(t, core.Running, runtime.Status)
	assert.Nil(t, runtime.WaitingQuestion)
	require.Len(t, runtime.Context, 2)
	assert.Equal(t, "us-east", runtime.Context[1].Content)

	now = now.Add(time.Minute)
	runtime, err = service.Stop(ctx, testControllerID)
	require.NoError(t, err)
	assert.Equal(t, core.Aborted, runtime.Status)
	assert.Nil(t, runtime.FinishedAt)

	unchanged, err := service.Stop(ctx, testControllerID)
	require.NoError(t, err)
	assert.Equal(t, runtime.UpdatedAt, unchanged.UpdatedAt)

	stored, err = stores.Runtimes.Get(ctx, testControllerID)
	require.NoError(t, err)
	finishedAt := now.Add(time.Minute)
	stored.FinishedAt = &finishedAt
	stored.UpdatedAt = finishedAt
	require.NoError(t, stores.Runtimes.Put(ctx, stored))
	items, err = service.List(ctx)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotNil(t, items[0].FinishedAt)
	assert.Equal(t, finishedAt, *items[0].FinishedAt)

	now = now.Add(2 * time.Minute)
	updated, err := service.Update(ctx, testControllerID, updatedYAML)
	require.NoError(t, err)
	assert.Equal(t, "Updated flow", updated.Definition.Name)

	runtime, err = service.Start(ctx, testControllerID, "new execution")
	require.NoError(t, err)
	assert.Zero(t, runtime.TurnCount)
	assert.Empty(t, runtime.DAGRunRefs)
	require.Len(t, runtime.Context, 1)
	assert.Equal(t, "new execution", runtime.Context[0].Content)
}

func TestServicePromptAndStartEnforceLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := NewFileStores(t.TempDir())
	service := NewService(
		stores.Definitions,
		stores.Runtimes,
		stores.Locker,
		NewValidator(nil),
		WithIDGenerator(func() (string, error) { return testControllerID, nil }),
	)
	require.NoError(t, stores.Definitions.Create(ctx, testControllerID, validPersistedYAML(testControllerID)))

	_, err := service.Prompt(ctx, testControllerID, "extra")
	assert.ErrorIs(t, err, ErrInvalidLifecycle)
	_, err = service.Stop(ctx, testControllerID)
	assert.ErrorIs(t, err, ErrInvalidLifecycle)

	_, err = service.Start(ctx, testControllerID, "start")
	require.NoError(t, err)
	_, err = service.Start(ctx, testControllerID, "again")
	assert.ErrorIs(t, err, ErrInvalidLifecycle)
	_, err = service.Prompt(ctx, testControllerID, "interrupt")
	assert.ErrorIs(t, err, ErrInvalidLifecycle)
}

func TestServiceUpdateRejectsIdentityChanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := NewFileStores(t.TempDir())
	service := NewService(stores.Definitions, stores.Runtimes, stores.Locker, NewValidator(nil))
	require.NoError(t, stores.Definitions.Create(ctx, testControllerID, validPersistedYAML(testControllerID)))

	otherID := "ctrl_bbbbbbbbbbbbbbbb"
	_, err := service.Update(ctx, testControllerID, validPersistedYAML(otherID))
	assert.ErrorIs(t, err, ErrInvalidDefinition)

	changedWorkspace := strings.Replace(string(validPersistedYAML(testControllerID)), "workspace=ops", "workspace=security", 1)
	_, err = service.Update(ctx, testControllerID, []byte(changedWorkspace))
	assert.ErrorIs(t, err, ErrInvalidLifecycle)
}

func TestServiceDeleteCleansOrphanRuntimeAndReportsMissingResource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := t.TempDir()
	stores := NewFileStores(dataDir)
	service := NewService(stores.Definitions, stores.Runtimes, stores.Locker, NewValidator(nil))
	now := time.Now().UTC()
	runtime := validRunningRuntime(testControllerID, now)
	runtime.Status = core.Succeeded
	runtime.FinishedAt = &now
	require.NoError(t, stores.Runtimes.Put(ctx, runtime))

	require.NoError(t, service.Delete(ctx, testControllerID))
	_, err := stores.Runtimes.Get(ctx, testControllerID)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.ErrorIs(t, service.Delete(ctx, testControllerID), ErrNotFound)
}

type failOnceDefinitionDeleteStore struct {
	DefinitionStore
	err error
}

func (s *failOnceDefinitionDeleteStore) Delete(ctx context.Context, id string) error {
	if s.err != nil {
		err := s.err
		s.err = nil
		return err
	}
	return s.DefinitionStore.Delete(ctx, id)
}

func TestServiceDeleteCanRetryAfterPartialFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := NewFileStores(t.TempDir())
	require.NoError(t, stores.Definitions.Create(ctx, testControllerID, validPersistedYAML(testControllerID)))
	now := time.Now().UTC()
	runtime := validRunningRuntime(testControllerID, now)
	runtime.Status = core.Succeeded
	runtime.FinishedAt = &now
	require.NoError(t, stores.Runtimes.Put(ctx, runtime))

	deleteErr := errors.New("definition delete unavailable")
	definitions := &failOnceDefinitionDeleteStore{DefinitionStore: stores.Definitions, err: deleteErr}
	service := NewService(definitions, stores.Runtimes, stores.Locker, NewValidator(nil))

	err := service.Delete(ctx, testControllerID)
	assert.ErrorIs(t, err, deleteErr)
	_, err = stores.Definitions.Get(ctx, testControllerID)
	require.NoError(t, err)
	_, err = stores.Runtimes.Get(ctx, testControllerID)
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, service.Delete(ctx, testControllerID))
	_, err = stores.Definitions.Get(ctx, testControllerID)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestServiceStartRevalidatesDAGReferences(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := NewFileStores(t.TempDir())
	yaml := strings.Replace(string(validPersistedYAML(testControllerID)), "dags: []", "dags:\n  - inspect-alert", 1)
	yaml = strings.Replace(yaml, "    transitions:\n", "    dags:\n      - inspect-alert\n    transitions:\n", 1)
	require.NoError(t, stores.Definitions.Create(ctx, testControllerID, []byte(yaml)))

	missing := errors.New("deleted")
	validator := NewValidator(DAGResolverFunc(func(context.Context, string) (DAGReference, error) {
		return DAGReference{}, missing
	}))
	service := NewService(stores.Definitions, stores.Runtimes, stores.Locker, validator)
	_, err := service.Start(ctx, testControllerID, "start")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidDefinition)
	_, runtimeErr := stores.Runtimes.Get(ctx, testControllerID)
	assert.ErrorIs(t, runtimeErr, ErrNotFound)
}

func TestServicePromptFailsControllerWhenRuntimeSnapshotWouldOverflow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := NewFileStores(t.TempDir())
	require.NoError(t, stores.Definitions.Create(ctx, testControllerID, validPersistedYAML(testControllerID)))
	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	runtime := validRunningRuntime(testControllerID, now)
	question := "Need more input"
	runtime.Status = core.Waiting
	runtime.WaitingQuestion = &question
	runtime.Context[0].Content = ""
	emptySnapshot, err := json.Marshal(runtime)
	require.NoError(t, err)
	contentBytes := MaxRuntimeBytes - runtimeTerminalReserve - len(emptySnapshot) - 128
	require.Positive(t, contentBytes)
	runtime.Context[0].Content = strings.Repeat("x", contentBytes)
	require.NoError(t, stores.Runtimes.Put(ctx, runtime))

	service := NewService(
		stores.Definitions,
		stores.Runtimes,
		stores.Locker,
		NewValidator(nil),
		WithClock(func() time.Time { return now.Add(time.Minute) }),
	)
	view, err := service.Prompt(ctx, testControllerID, strings.Repeat("p", 1024))
	require.NoError(t, err)
	assert.Equal(t, core.Failed, view.Status)
	require.NotNil(t, view.LastError)
	assert.Equal(t, "runtime_snapshot_limit", *view.LastError)
	require.Len(t, view.Context, 1)

	stored, err := stores.Runtimes.Get(ctx, testControllerID)
	require.NoError(t, err)
	assert.Equal(t, core.Failed, stored.Status)
	require.NotNil(t, stored.FinishedAt)
	require.Len(t, stored.Context, 1)
}
