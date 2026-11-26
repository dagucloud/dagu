// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The default profile that workspace ops names applies to a DAG labelled
// with ops only while projects are off.
func TestDAGProfileResolverWorkspaceDefault(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	settingsStore, err := store.NewDAGSettingsStore(backend.Collection("dag-settings"))
	require.NoError(t, err)
	profileStore, err := store.NewProfileStore(backend.Collection("profiles"))
	require.NoError(t, err)
	prod, err := profile.New(profile.CreateInput{Name: "prod"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, profileStore.Create(ctx, prod))
	ref, err := profile.WorkspaceInheritedRef("ops")
	require.NoError(t, err)
	defaults, err := profile.NewInherited(ref, profile.InheritedCreateInput{}, time.Now())
	require.NoError(t, err)
	defaults.DefaultProfile = "prod"
	require.NoError(t, profileStore.Create(ctx, defaults))

	for projectsOn, want := range map[bool]string{false: "prod", true: ""} {
		resolved, err := scheduler.NewDAGProfileResolver(settingsStore, profileStore, projectsOn).ResolveProfile(ctx, "example", "ops")
		require.NoError(t, err)
		assert.Equal(t, want, resolved, "projects on: %t", projectsOn)
	}
}
