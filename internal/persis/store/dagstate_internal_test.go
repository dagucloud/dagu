// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

func TestDAGStateRecordIDEncodesFilesystemSensitiveParts(t *testing.T) {
	ref := dagrun.StateRef{
		Scope:     dagrun.StateScopeDAG,
		Namespace: "Daily:Agent",
		Key:       "Cursor/CON:<latest>",
	}

	id, err := dagStateRecordID(ref)
	require.NoError(t, err)
	require.NotContains(t, id, ref.Namespace)
	require.NotContains(t, id, ref.Key)
	require.NotContains(t, filepath.Base(id), ":")
	require.NotContains(t, filepath.Base(id), "<")
	require.NotContains(t, filepath.Base(id), ">")

	roundTrip, err := dagStateRefFromRecordID(id)
	require.NoError(t, err)
	require.Equal(t, ref, roundTrip)
}

func TestDAGStateRefFromRecordIDRejectsUnversionedIDs(t *testing.T) {
	_, err := dagStateRefFromRecordID("dag/daily-agent/cursor")
	require.ErrorIs(t, err, dagrun.ErrInvalidStateRef)
}

func TestDAGStateRecordIDAvoidsHierarchicalKeyPathCollisions(t *testing.T) {
	plain, err := dagStateRecordID(dagrun.StateRef{Scope: dagrun.StateScopeDAG, Namespace: "daily-agent", Key: "cursor"})
	require.NoError(t, err)

	nested, err := dagStateRecordID(dagrun.StateRef{Scope: dagrun.StateScopeDAG, Namespace: "daily-agent", Key: "cursor/feed"})
	require.NoError(t, err)

	require.NotEqual(t, plain+"/feed", nested)
	require.Len(t, strings.Split(nested, "/"), 4)
}

func TestDAGStateRecordIDPrefixPreservesStateKeyPrefix(t *testing.T) {
	prefix, err := dagStateRecordIDPrefix(dagrun.StateListOptions{
		Scope:     dagrun.StateScopeDAG,
		Namespace: "daily-agent",
		KeyPrefix: "cursors/",
	})
	require.NoError(t, err)

	id, err := dagStateRecordID(dagrun.StateRef{Scope: dagrun.StateScopeDAG, Namespace: "daily-agent", Key: "cursors/feed"})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(id, prefix))

	other, err := dagStateRecordID(dagrun.StateRef{Scope: dagrun.StateScopeDAG, Namespace: "daily-agent", Key: "tokens/feed"})
	require.NoError(t, err)
	require.False(t, strings.HasPrefix(other, prefix))
}

func TestDAGStateStoreListUsesRecordIDsLimitBeforeDecode(t *testing.T) {
	ctx := context.Background()
	firstID, err := dagStateRecordID(stateRefForStoreTest("cursors/a"))
	require.NoError(t, err)
	secondID, err := dagStateRecordID(stateRefForStoreTest("cursors/b"))
	require.NoError(t, err)
	thirdID, err := dagStateRecordID(stateRefForStoreTest("cursors/c"))
	require.NoError(t, err)
	col := newCountingRecordIDCollection(t, []string{firstID, secondID, thirdID})
	s := NewDAGStateStore(col)

	list, err := s.List(ctx, dagrun.StateListOptions{
		Scope:     dagrun.StateScopeDAG,
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

func stateRefForStoreTest(key string) dagrun.StateRef {
	return dagrun.StateRef{Scope: dagrun.StateScopeDAG, Namespace: "daily-agent", Key: key}
}

func normalizeStateValueForStoreTest(t *testing.T, value string) json.RawMessage {
	t.Helper()
	msg, err := dagrun.NormalizeStateValue([]byte(value))
	require.NoError(t, err)
	return msg
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
		ref, err := dagStateRefFromRecordID(id)
		require.NoError(t, err)
		entry := &dagrun.StateEntry{
			StateRef: ref,
			Value:    normalizeStateValueForStoreTest(t, `1`),
			Version:  1,
		}
		data, err := persis.Encode(entry)
		require.NoError(t, err)
		records[id] = &persis.Record{ID: id, Data: data}
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
