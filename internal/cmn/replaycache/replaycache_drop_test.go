// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package replaycache

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type opRecord struct {
	Op int `json:"op"`
}

// Drop forgets the records of a step that match, leaves the others, and
// treats a step without records as nothing to do.
func TestDrop(t *testing.T) {
	t.Parallel()

	store := New(t.TempDir())
	records := Open[opRecord](store.Path("invoices", "post"))
	records.Stage("first", opRecord{Op: 0})
	records.Stage("second", opRecord{Op: 1})
	require.NoError(t, records.Commit(t.Context()))

	isSecond := func(entry json.RawMessage) bool {
		var r opRecord
		return json.Unmarshal(entry, &r) == nil && r.Op == 1
	}
	removed, err := store.Drop(t.Context(), "invoices", "post", isSecond)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	_, kept := Open[opRecord](store.Path("invoices", "post")).Lookup("first")
	assert.True(t, kept)
	_, gone := Open[opRecord](store.Path("invoices", "post")).Lookup("second")
	assert.False(t, gone)

	removed, err = store.Drop(t.Context(), "invoices", "missing", isSecond)
	require.NoError(t, err)
	assert.Zero(t, removed)
}
