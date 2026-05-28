// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/dagstate"
	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func newDAGStateStore(t *testing.T) dagstate.Store {
	t.Helper()
	return store.NewDAGStateStore(testutil.NewMemoryBackend().Collection("dag_state"))
}

func stateRef(key string) dagstate.Ref {
	return dagstate.Ref{Scope: dagstate.ScopeDAG, Namespace: "daily-agent", Key: key}
}

func rawJSON(t *testing.T, value string) json.RawMessage {
	t.Helper()
	msg, err := dagstate.NormalizeValue([]byte(value))
	require.NoError(t, err)
	return msg
}

func TestDAGStateStorePutGetAndVersion(t *testing.T) {
	ctx := context.Background()
	s := newDAGStateStore(t)

	entry, err := s.Put(ctx, stateRef("cursor"), rawJSON(t, `{"last_id":123}`), dagstate.PutOptions{
		UpdatedBy: &dagstate.UpdateSource{
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
	updated, err := s.Put(ctx, stateRef("cursor"), rawJSON(t, `{"last_id":456}`), dagstate.PutOptions{
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

	_, err := s.Put(ctx, stateRef("cursor"), rawJSON(t, `1`), dagstate.PutOptions{})
	require.NoError(t, err)

	staleVersion := int64(99)
	_, err = s.Put(ctx, stateRef("cursor"), rawJSON(t, `2`), dagstate.PutOptions{
		ExpectedVersion: &staleVersion,
	})
	require.ErrorIs(t, err, dagstate.ErrConflict)
}

func TestDAGStateStoreCreateOnlyRejectsExistingKey(t *testing.T) {
	ctx := context.Background()
	s := newDAGStateStore(t)

	_, err := s.Put(ctx, stateRef("cursor"), rawJSON(t, `"first"`), dagstate.PutOptions{
		CreateOnly: true,
	})
	require.NoError(t, err)

	_, err = s.Put(ctx, stateRef("cursor"), rawJSON(t, `"second"`), dagstate.PutOptions{
		CreateOnly: true,
	})
	require.ErrorIs(t, err, dagstate.ErrConflict)
}

func TestDAGStateStoreDeleteAndList(t *testing.T) {
	ctx := context.Background()
	s := newDAGStateStore(t)

	_, err := s.Put(ctx, stateRef("cursors/api"), rawJSON(t, `"api"`), dagstate.PutOptions{})
	require.NoError(t, err)
	_, err = s.Put(ctx, stateRef("cursors/db"), rawJSON(t, `"db"`), dagstate.PutOptions{})
	require.NoError(t, err)
	_, err = s.Put(ctx, stateRef("tokens/api"), rawJSON(t, `"token"`), dagstate.PutOptions{})
	require.NoError(t, err)

	list, err := s.List(ctx, dagstate.ListOptions{
		Scope:     dagstate.ScopeDAG,
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
	require.ErrorIs(t, err, dagstate.ErrNotFound)
}

func TestDAGStateStoreListUsesRecordIDsLimitBeforeDecode(t *testing.T) {
	ctx := context.Background()
	firstID, err := stateRef("cursors/a").RecordID()
	require.NoError(t, err)
	secondID, err := stateRef("cursors/b").RecordID()
	require.NoError(t, err)
	thirdID, err := stateRef("cursors/c").RecordID()
	require.NoError(t, err)
	col := newCountingRecordIDCollection(t, []string{
		firstID,
		secondID,
		thirdID,
	})
	s := store.NewDAGStateStore(col)

	list, err := s.List(ctx, dagstate.ListOptions{
		Scope:     dagstate.ScopeDAG,
		Namespace: "daily-agent",
		KeyPrefix: "cursors/",
		Limit:     1,
	})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "cursors/a", list[0].Key)
	assert.Equal(t, 1, col.getCalls)
	assert.Equal(t, 0, col.listCalls)
}

func TestDAGStateStoreValidation(t *testing.T) {
	ctx := context.Background()
	s := newDAGStateStore(t)

	_, err := s.Put(ctx, dagstate.Ref{Scope: dagstate.ScopeDAG, Namespace: "daily-agent", Key: "../bad"}, rawJSON(t, `1`), dagstate.PutOptions{})
	require.ErrorIs(t, err, dagstate.ErrInvalidRef)

	_, err = s.Put(ctx, stateRef("bad-json"), json.RawMessage(`{`), dagstate.PutOptions{})
	require.ErrorIs(t, err, dagstate.ErrInvalidValue)
}

func TestDAGStateStoreFileBackendSerializesConcurrentUpdates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	stores := []dagstate.Store{
		store.NewDAGStateStore(file.NewCollection(dir)),
		store.NewDAGStateStore(file.NewCollection(dir)),
	}
	ref := stateRef("counter")
	_, err := stores[0].Put(ctx, ref, rawJSON(t, `0`), dagstate.PutOptions{})
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
				_, err = s.Put(ctx, ref, rawJSON(t, fmt.Sprintf(`%d`, current+1)), dagstate.PutOptions{
					ExpectedVersion: &expected,
				})
				if errors.Is(err, dagstate.ErrConflict) {
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

func TestNormalizeValueCompactsJSON(t *testing.T) {
	value, err := dagstate.NormalizeValue([]byte(`{ "b": 2, "a": 1 }`))
	require.NoError(t, err)
	assert.Equal(t, `{"a":1,"b":2}`, string(value))

	_, err = dagstate.NormalizeValue([]byte(`{`))
	require.True(t, errors.Is(err, dagstate.ErrInvalidValue))
}

type countingRecordIDCollection struct {
	ids       []string
	records   map[string]*persis.Record
	getCalls  int
	listCalls int
}

func newCountingRecordIDCollection(t *testing.T, ids []string) *countingRecordIDCollection {
	t.Helper()

	records := make(map[string]*persis.Record, len(ids))
	for _, id := range ids {
		ref, err := dagstate.RefFromRecordID(id)
		require.NoError(t, err)
		entry := &dagstate.Entry{
			Ref:     ref,
			Value:   rawJSON(t, `1`),
			Version: 1,
		}
		data, enc, err := persis.Encode(entry)
		require.NoError(t, err)
		records[id] = &persis.Record{ID: id, Data: data, Encoding: enc}
	}
	return &countingRecordIDCollection{ids: append([]string(nil), ids...), records: records}
}

func (c *countingRecordIDCollection) RecordIDs(_ context.Context, prefix string) ([]string, error) {
	var out []string
	for _, id := range c.ids {
		if strings.HasPrefix(id, prefix) {
			out = append(out, id)
		}
	}
	return out, nil
}

func (c *countingRecordIDCollection) Get(_ context.Context, id string) (*persis.Record, error) {
	c.getCalls++
	rec, ok := c.records[id]
	if !ok {
		return nil, persis.ErrNotFound
	}
	cp := *rec
	cp.Data = append([]byte(nil), rec.Data...)
	return &cp, nil
}

func (c *countingRecordIDCollection) Put(context.Context, *persis.Record) error {
	return nil
}

func (c *countingRecordIDCollection) Create(context.Context, *persis.Record) error {
	return nil
}

func (c *countingRecordIDCollection) Delete(context.Context, string) error {
	return nil
}

func (c *countingRecordIDCollection) CompareAndDelete(context.Context, *persis.Record) error {
	return nil
}

func (c *countingRecordIDCollection) List(context.Context, persis.ListQuery) (*persis.Page, error) {
	c.listCalls++
	return &persis.Page{}, nil
}

func (c *countingRecordIDCollection) CompareAndSwap(context.Context, string, []byte, []byte) error {
	return nil
}

func (c *countingRecordIDCollection) Claim(context.Context, persis.ListQuery) (*persis.Record, error) {
	return nil, persis.ErrNotFound
}
