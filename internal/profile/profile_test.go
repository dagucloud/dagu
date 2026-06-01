// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package profile_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/profile"
)

func TestNewValidatesProfile(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 1, 1, 2, 3, 0, time.UTC)

	p, err := profile.New(profile.CreateInput{
		Name:        "local",
		Description: "Local runtime values",
		Protected:   true,
		CreatedBy:   "alice",
	}, now)
	require.NoError(t, err)

	assert.NotEmpty(t, p.ID)
	assert.Equal(t, "local", p.Name)
	assert.Equal(t, "Local runtime values", p.Description)
	assert.Equal(t, profile.StatusActive, p.Status)
	assert.True(t, p.Protected)
	assert.Equal(t, "alice", p.CreatedBy)
	assert.Equal(t, now, p.CreatedAt)
	assert.Equal(t, now, p.UpdatedAt)
}

func TestValidateName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want bool
	}{
		{name: "local", want: true},
		{name: "staging-us_1", want: true},
		{name: "prod.blue", want: true},
		{name: "", want: false},
		{name: "Prod", want: false},
		{name: "-prod", want: false},
		{name: "prod/slash", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := profile.ValidateName(tt.name)
			if tt.want {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, profile.ErrInvalidName)
			}
		})
	}
}

func TestProfileSetEntriesValidatesRuntimeEnvironmentKeys(t *testing.T) {
	t.Parallel()

	p, err := profile.New(profile.CreateInput{Name: "local"}, time.Now())
	require.NoError(t, err)

	require.NoError(t, p.SetVariable("LOG_LEVEL", "debug", "alice", time.Now()))
	require.NoError(t, p.SetSecret("CLICKHOUSE_DSN_PY", "secret-id-1", "alice", time.Now()))

	assert.Equal(t, "debug", p.Entries[0].Value)
	assert.Equal(t, profile.EntryKindVariable, p.Entries[0].Kind)
	assert.Equal(t, "secret-id-1", p.Entries[1].SecretID)
	assert.Equal(t, profile.EntryKindSecret, p.Entries[1].Kind)

	assert.ErrorIs(t, p.SetVariable("DAGU_INTERNAL", "x", "alice", time.Now()), profile.ErrReservedKey)
	assert.ErrorIs(t, p.SetVariable("bad-key", "x", "alice", time.Now()), profile.ErrInvalidKey)
	assert.ErrorIs(t, p.SetSecret("LOG_LEVEL", "secret-id-2", "alice", time.Now()), profile.ErrDuplicateKey)
}
