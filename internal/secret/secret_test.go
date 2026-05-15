// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secret

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSecret_ValidatesIdentity(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 14, 1, 2, 3, 0, time.UTC)

	tests := []struct {
		name    string
		input   CreateInput
		wantErr error
	}{
		{
			name: "ValidDefaultWorkspace",
			input: CreateInput{
				Workspace:    "",
				Ref:          "prod/db-password",
				DisplayName:  "Database password",
				ProviderType: ProviderDaguManaged,
				CreatedBy:    "alice",
			},
		},
		{
			name: "ValidNamedWorkspace",
			input: CreateInput{
				Workspace:    "payments",
				Ref:          "prod/db-password",
				DisplayName:  "Database password",
				ProviderType: ProviderDaguManaged,
				CreatedBy:    "alice",
			},
		},
		{
			name: "RejectDefaultWorkspaceLiteral",
			input: CreateInput{
				Workspace:    "default",
				Ref:          "prod/db-password",
				ProviderType: ProviderDaguManaged,
				CreatedBy:    "alice",
			},
			wantErr: ErrInvalidWorkspace,
		},
		{
			name: "RejectInvalidRef",
			input: CreateInput{
				Workspace:    "payments",
				Ref:          "../db-password",
				ProviderType: ProviderDaguManaged,
				CreatedBy:    "alice",
			},
			wantErr: ErrInvalidRef,
		},
		{
			name: "RejectMissingProviderType",
			input: CreateInput{
				Workspace: "payments",
				Ref:       "prod/db-password",
				CreatedBy: "alice",
			},
			wantErr: ErrInvalidProviderType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := New(tt.input, now)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, got.ID)
			assert.Equal(t, tt.input.Workspace, got.Workspace)
			assert.Equal(t, tt.input.Ref, got.Ref)
			assert.Equal(t, StatusActive, got.Status)
			assert.Equal(t, now, got.CreatedAt)
			assert.Equal(t, now, got.UpdatedAt)
		})
	}
}

func TestProviderRefFingerprint(t *testing.T) {
	t.Parallel()

	got, err := ProviderRefFingerprint("hmac-key", ProviderVault, "conn-1", "secret/data/prod/db")
	require.NoError(t, err)
	assert.NotEmpty(t, got)

	same, err := ProviderRefFingerprint("hmac-key", ProviderVault, "conn-1", "secret/data/prod/db")
	require.NoError(t, err)
	assert.Equal(t, got, same)

	different, err := ProviderRefFingerprint("other-key", ProviderVault, "conn-1", "secret/data/prod/db")
	require.NoError(t, err)
	assert.NotEqual(t, got, different)
	assert.NotContains(t, got, "secret/data/prod/db")
}
