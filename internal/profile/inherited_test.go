// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package profile_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInheritedRefNamesAndSecretRefs(t *testing.T) {
	globalRef := profile.GlobalInheritedRef()
	assert.Equal(t, "_global", globalRef.PublicName())
	assert.Equal(t, "_global", globalRef.StorageName())
	assert.Equal(t, "runtime-profile-defaults/global/key-4150495f544f4b454e", globalRef.SecretRef("API_TOKEN"))

	workspaceRef, err := profile.WorkspaceInheritedRef("payments")
	require.NoError(t, err)
	assert.Equal(t, "_workspaces/payments", workspaceRef.PublicName())
	assert.Equal(t, "_workspace.7061796d656e7473", workspaceRef.StorageName())
	assert.Equal(t, "runtime-profile-defaults/workspaces/7061796d656e7473/key-4150495f544f4b454e", workspaceRef.SecretRef("API_TOKEN"))
}

func TestNewInheritedProfile(t *testing.T) {
	ref := profile.GlobalInheritedRef()
	item, err := profile.NewInherited(ref, profile.InheritedCreateInput{
		Description: "Shared runtime profile defaults",
		CreatedBy:   "alice",
	}, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	assert.Equal(t, "_global", item.Name)
	assert.Equal(t, "Shared runtime profile defaults", item.Description)
	assert.Equal(t, profile.StatusActive, item.Status)
	assert.True(t, item.Protected)
}

func TestIsInheritedStorageNameRequiresValidWorkspaceName(t *testing.T) {
	workspaceRef, err := profile.WorkspaceInheritedRef("payments")
	require.NoError(t, err)

	assert.True(t, profile.IsInheritedStorageName("_global"))
	assert.True(t, profile.IsInheritedStorageName(workspaceRef.StorageName()))
	assert.False(t, profile.IsInheritedStorageName("_workspace.2f"))
	assert.False(t, profile.IsInheritedStorageName("_workspace.616c6c"))
	assert.False(t, profile.IsInheritedStorageName("_workspace.676c6f62616c"))
}

func TestMergeResolvedUsesHigherLayerKindAndValue(t *testing.T) {
	global := &profile.Resolved{
		Name: "global",
		Variables: map[string]string{
			"SHARED":      "global",
			"GLOBAL_ONLY": "global",
		},
		Secrets: map[string]string{
			"ROTATED": "global-secret",
		},
		Entries: []profile.ResolvedEntry{
			{Key: "SHARED", Kind: profile.EntryKindVariable},
			{Key: "GLOBAL_ONLY", Kind: profile.EntryKindVariable},
			{Key: "ROTATED", Kind: profile.EntryKindSecret},
		},
	}
	selected := &profile.Resolved{
		Name: "prod",
		Variables: map[string]string{
			"SHARED":  "selected",
			"ROTATED": "selected-variable",
		},
		Secrets: map[string]string{},
		Entries: []profile.ResolvedEntry{
			{Key: "SHARED", Kind: profile.EntryKindVariable},
			{Key: "ROTATED", Kind: profile.EntryKindVariable},
		},
	}

	merged := profile.MergeResolved("effective", global, selected)

	assert.Equal(t, "selected", merged.Variables["SHARED"])
	assert.Equal(t, "global", merged.Variables["GLOBAL_ONLY"])
	assert.Equal(t, "selected-variable", merged.Variables["ROTATED"])
	assert.NotContains(t, merged.Secrets, "ROTATED")
	require.Len(t, merged.Entries, 3)
	assert.Equal(t, profile.EntryKindVariable, merged.Entries[2].Kind)
}
