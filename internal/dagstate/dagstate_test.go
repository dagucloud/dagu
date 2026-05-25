// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagstate

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeValueRejectsNormalizedValueOverLimit(t *testing.T) {
	raw := []byte(`"` + strings.Repeat("<", DefaultMaxValueBytes/6+1) + `"`)
	assert.Less(t, len(raw), DefaultMaxValueBytes)

	_, err := NormalizeValue(raw)
	require.ErrorIs(t, err, ErrValueTooLarge)
}

func TestNormalizeValuePreservesNumericPrecision(t *testing.T) {
	value, err := NormalizeValue([]byte(`{"id":9007199254740993,"decimal":1.2300}`))
	require.NoError(t, err)
	assert.Equal(t, `{"decimal":1.2300,"id":9007199254740993}`, string(value))
}

func TestRefRecordIDEncodesFilesystemSensitiveParts(t *testing.T) {
	ref := Ref{
		Scope:     ScopeDAG,
		Namespace: "Daily:Agent",
		Key:       "Cursor/CON:<latest>",
	}

	id, err := ref.RecordID()
	require.NoError(t, err)
	require.NotContains(t, id, ref.Namespace)
	require.NotContains(t, id, ref.Key)
	require.NotContains(t, filepath.Base(id), ":")
	require.NotContains(t, filepath.Base(id), "<")
	require.NotContains(t, filepath.Base(id), ">")

	roundTrip, err := RefFromRecordID(id)
	require.NoError(t, err)
	require.Equal(t, ref, roundTrip)
}

func TestRefRecordIDAvoidsHierarchicalKeyPathCollisions(t *testing.T) {
	plain, err := (Ref{Scope: ScopeDAG, Namespace: "daily-agent", Key: "cursor"}).RecordID()
	require.NoError(t, err)

	nested, err := (Ref{Scope: ScopeDAG, Namespace: "daily-agent", Key: "cursor/feed"}).RecordID()
	require.NoError(t, err)

	require.NotEqual(t, plain+"/feed", nested)
	require.Len(t, strings.Split(nested, "/"), 4)
}

func TestListOptionsRecordIDPrefixPreservesStateKeyPrefix(t *testing.T) {
	prefix, err := (ListOptions{
		Scope:     ScopeDAG,
		Namespace: "daily-agent",
		KeyPrefix: "cursors/",
	}).RecordIDPrefix()
	require.NoError(t, err)

	id, err := (Ref{Scope: ScopeDAG, Namespace: "daily-agent", Key: "cursors/feed"}).RecordID()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(id, prefix))

	other, err := (Ref{Scope: ScopeDAG, Namespace: "daily-agent", Key: "tokens/feed"}).RecordID()
	require.NoError(t, err)
	require.False(t, strings.HasPrefix(other, prefix))
}
