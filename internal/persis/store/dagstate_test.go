// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

func newDAGStateStore(t *testing.T) dagrun.StateStore {
	t.Helper()
	return store.NewDAGStateStore(testutil.NewMemoryBackend().Collection("dag_state"))
}

func stateRef(key string) dagrun.StateRef {
	return dagrun.StateRef{Scope: dagrun.StateScopeDAG, Namespace: "daily-agent", Key: key}
}

func rawJSON(t *testing.T, value string) json.RawMessage {
	t.Helper()
	msg, err := dagrun.NormalizeStateValue([]byte(value))
	require.NoError(t, err)
	return msg
}

func TestDAGStateStorePutGetAndVersion(t *testing.T) {
	ctx := context.Background()
	s := newDAGStateStore(t)

	entry, err := s.Put(ctx, stateRef("cursor"), rawJSON(t, `{"last_id":123}`), dagrun.StatePutOptions{
		UpdatedBy: &dagrun.StateUpdateSource{
			DAGName:  "daily-agent",
			DAGRunID: "run-1",
			StepName: "save-cursor",
		},
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), entry.Version)
	require.NotEmpty(t, entry.Hash)
	require.JSONEq(t, `{"last_id":123}`, string(entry.Value))
	require.NotNil(t, entry.UpdatedBy)

	got, err := s.Get(ctx, stateRef("cursor"))
	require.NoError(t, err)
	require.Equal(t, entry.Version, got.Version)
	require.Equal(t, entry.Hash, got.Hash)
	require.JSONEq(t, `{"last_id":123}`, string(got.Value))

	expectedVersion := got.Version
	updated, err := s.Put(ctx, stateRef("cursor"), rawJSON(t, `{"last_id":456}`), dagrun.StatePutOptions{
		ExpectedVersion: &expectedVersion,
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), updated.Version)
	require.NotEqual(t, got.Hash, updated.Hash)
	require.JSONEq(t, `{"last_id":456}`, string(updated.Value))
}

func TestDAGStateStorePutRejectsStaleExpectedVersion(t *testing.T) {
	ctx := context.Background()
	s := newDAGStateStore(t)

	_, err := s.Put(ctx, stateRef("cursor"), rawJSON(t, `1`), dagrun.StatePutOptions{})
	require.NoError(t, err)

	staleVersion := int64(99)
	_, err = s.Put(ctx, stateRef("cursor"), rawJSON(t, `2`), dagrun.StatePutOptions{
		ExpectedVersion: &staleVersion,
	})
	require.ErrorIs(t, err, dagrun.ErrStateConflict)
}

func TestDAGStateStoreCreateOnlyRejectsExistingKey(t *testing.T) {
	ctx := context.Background()
	s := newDAGStateStore(t)

	_, err := s.Put(ctx, stateRef("cursor"), rawJSON(t, `"first"`), dagrun.StatePutOptions{
		CreateOnly: true,
	})
	require.NoError(t, err)

	_, err = s.Put(ctx, stateRef("cursor"), rawJSON(t, `"second"`), dagrun.StatePutOptions{
		CreateOnly: true,
	})
	require.ErrorIs(t, err, dagrun.ErrStateConflict)
}

func TestDAGStateStoreDeleteAndList(t *testing.T) {
	ctx := context.Background()
	s := newDAGStateStore(t)

	_, err := s.Put(ctx, stateRef("cursors/api"), rawJSON(t, `"api"`), dagrun.StatePutOptions{})
	require.NoError(t, err)
	_, err = s.Put(ctx, stateRef("cursors/db"), rawJSON(t, `"db"`), dagrun.StatePutOptions{})
	require.NoError(t, err)
	_, err = s.Put(ctx, stateRef("tokens/api"), rawJSON(t, `"token"`), dagrun.StatePutOptions{})
	require.NoError(t, err)

	list, err := s.List(ctx, dagrun.StateListOptions{
		Scope:     dagrun.StateScopeDAG,
		Namespace: "daily-agent",
		KeyPrefix: "cursors/",
	})
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "cursors/api", list[0].Key)
	assert.Equal(t, "cursors/db", list[1].Key)

	deleted, err := s.Delete(ctx, stateRef("cursors/api"))
	require.NoError(t, err)
	require.True(t, deleted)

	deleted, err = s.Delete(ctx, stateRef("cursors/api"))
	require.NoError(t, err)
	require.False(t, deleted)

	_, err = s.Get(ctx, stateRef("cursors/api"))
	require.ErrorIs(t, err, dagrun.ErrStateNotFound)
}

func TestDAGStateStoreValidation(t *testing.T) {
	ctx := context.Background()
	s := newDAGStateStore(t)

	_, err := s.Put(ctx, dagrun.StateRef{Scope: dagrun.StateScopeDAG, Namespace: "daily-agent", Key: "../bad"}, rawJSON(t, `1`), dagrun.StatePutOptions{})
	require.ErrorIs(t, err, dagrun.ErrInvalidStateRef)

	_, err = s.Put(ctx, stateRef("bad-json"), json.RawMessage(`{`), dagrun.StatePutOptions{})
	require.ErrorIs(t, err, dagrun.ErrInvalidStateValue)
}

func TestDAGStateStoreFileBackendSerializesConcurrentUpdates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	stores := []dagrun.StateStore{
		store.NewDAGStateStore(file.NewCollection(dir)),
		store.NewDAGStateStore(file.NewCollection(dir)),
	}
	ref := stateRef("counter")
	_, err := stores[0].Put(ctx, ref, rawJSON(t, `0`), dagrun.StatePutOptions{})
	require.NoError(t, err)

	const updates = 20
	var wg sync.WaitGroup
	errCh := make(chan error, updates)
	for i := range updates {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := stores[i%len(stores)]
			for {
				entry, err := s.Get(ctx, ref)
				if err != nil {
					errCh <- err
					return
				}
				var current int
				if err := json.Unmarshal(entry.Value, &current); err != nil {
					errCh <- err
					return
				}
				expected := entry.Version
				_, err = s.Put(ctx, ref, rawJSON(t, fmt.Sprintf(`%d`, current+1)), dagrun.StatePutOptions{
					ExpectedVersion: &expected,
				})
				if errors.Is(err, dagrun.ErrStateConflict) {
					continue
				}
				if err != nil {
					errCh <- err
				}
				return
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	got, err := stores[0].Get(ctx, ref)
	require.NoError(t, err)
	assert.JSONEq(t, fmt.Sprintf(`%d`, updates), string(got.Value))
}
