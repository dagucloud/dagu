// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcEntryIdentityRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		id    proc.ProcEntryID
		kind  string
		value string
	}{
		{
			name:  "collection",
			id:    collectionProcEntryID("queue-a/proc-dag/run-1"),
			kind:  procEntryIdentityCollection,
			value: "queue-a/proc-dag/run-1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			value, ok := tc.id.StoreValue(tc.kind)
			require.True(t, ok)
			assert.Equal(t, tc.value, value)

			_, ok = tc.id.StoreValue("other")
			assert.False(t, ok)
		})
	}
}

func TestProcEntryIdentityRejectsMalformedTokens(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   proc.ProcEntryID
	}{
		{name: "zero", id: proc.ProcEntryID{}},
		{name: "missing separator", id: proc.NewProcEntryID("plain-file.proc")},
		{name: "empty kind", id: proc.NewProcEntryID(":cmVjb3Jk")},
		{name: "empty value", id: proc.NewProcEntryID("collection:")},
		{name: "bad encoding", id: proc.NewProcEntryID("collection:not base64")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			value, ok := tc.id.StoreValue(procEntryIdentityCollection)
			assert.False(t, ok)
			assert.Empty(t, value)
		})
	}
}
