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
		name          string
		input         CreateInput
		wantWorkspace string
		wantErr       error
	}{
		{
			name: "EmptyWorkspaceNormalizesToGlobal",
			input: CreateInput{
				Workspace:    "",
				Ref:          "prod/db-password",
				ProviderType: ProviderDaguManaged,
				CreatedBy:    "alice",
			},
			wantWorkspace: GlobalWorkspace,
		},
		{
			name: "ValidNamedWorkspace",
			input: CreateInput{
				Workspace:    "payments",
				Ref:          "prod/db-password",
				ProviderType: ProviderDaguManaged,
				CreatedBy:    "alice",
			},
			wantWorkspace: "payments",
		},
		{
			name: "ValidGlobalWorkspace",
			input: CreateInput{
				Workspace:    GlobalWorkspace,
				Ref:          "prod/db-password",
				ProviderType: ProviderDaguManaged,
				CreatedBy:    "alice",
			},
			wantWorkspace: GlobalWorkspace,
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
			name: "RejectGlobalWorkspaceLiteral",
			input: CreateInput{
				Workspace:    "global",
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
			assert.Equal(t, tt.wantWorkspace, got.Workspace)
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
