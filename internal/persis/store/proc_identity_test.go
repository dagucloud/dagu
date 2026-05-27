// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core/exec"
)

func TestProcEntryIdentityRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		id    exec.ProcEntryID
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

			entry := exec.ProcEntry{Identity: tc.id}
			assert.Equal(t, tc.kind, procEntryIdentityKind(entry))

			value, ok := procEntryIdentityValue(entry, tc.kind)
			require.True(t, ok)
			assert.Equal(t, tc.value, value)

			_, ok = procEntryIdentityValue(entry, "other")
			assert.False(t, ok)
		})
	}
}

func TestProcEntryIdentityRejectsMalformedTokens(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   exec.ProcEntryID
	}{
		{name: "zero", id: exec.ProcEntryID{}},
		{name: "missing separator", id: exec.NewProcEntryID("plain-file.proc")},
		{name: "empty kind", id: exec.NewProcEntryID(":cmVjb3Jk")},
		{name: "empty value", id: exec.NewProcEntryID("collection:")},
		{name: "bad encoding", id: exec.NewProcEntryID("collection:not base64")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entry := exec.ProcEntry{Identity: tc.id}
			assert.Empty(t, procEntryIdentityKind(entry))

			value, ok := procEntryIdentityValue(entry, procEntryIdentityCollection)
			assert.False(t, ok)
			assert.Empty(t, value)
		})
	}
}
