// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/secret"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// With projects on, the runs of two projects that hold a secret, profile
// defaults, and a profile of the same names each resolve their own
// project's values, whatever workspace a run names.
func TestRuntimeStoresResolveInProject(t *testing.T) {
	t.Parallel()

	cfg := runtimeStoresConfig(t)
	server := file.NewServerStores(cfg)
	projects := []string{"alpha", "beta"}
	contexts := make(map[string]*Context, len(projects))
	for _, name := range projects {
		settings, err := file.NewProjectSettings(cfg, server, name)
		require.NoError(t, err)
		contexts[name] = &Context{Context: context.Background(), Config: cfg, settings: settings, projectStores: &file.ProjectStores{ProjectSettings: settings}}
		seedRuntimeSettings(t, contexts[name].runtimeStores(), name)
	}

	for _, name := range projects {
		ctx := contexts[name]
		stores := ctx.runtimeStores()
		value, err := stores.SecretResolver.ResolveReference(ctx, secretref.Ref{Name: "TOKEN", Ref: "api/token"})
		require.NoError(t, err)
		assert.Equal(t, name+"-token", value)

		resolved, err := stores.ProfileResolver.ResolveRuntime(ctx, profile.RuntimeRequest{ProfileName: "prod", Workspace: "ops"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"TIER": name}, resolved.Defaults.Variables)
		assert.Equal(t, map[string]string{"PROFILE_TOKEN": name + "-profile"}, resolved.Selected.Secrets)
	}
}

// runtimeStoresConfig returns a config with projects on and its paths in a
// temporary directory.
func runtimeStoresConfig(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{ProjectsEnabled: true}
	cfg.Paths.DAGsDir = filepath.Join(root, "dags")
	cfg.Paths.ProjectsDir = filepath.Join(root, "projects")
	cfg.Paths.DataDir = filepath.Join(root, "data")
	return cfg
}

// seedRuntimeSettings gives a project the secret api/token, the profile
// default TIER, and the profile prod with the secret PROFILE_TOKEN, each
// with a value that names the project.
func seedRuntimeSettings(t *testing.T, stores executionStores, name string) {
	t.Helper()
	ctx := context.Background()
	require.NotNil(t, stores.SecretStore)
	require.NotNil(t, stores.ProfileStore)

	sec, err := secret.New(secret.CreateInput{Ref: "api/token", ProviderType: secret.ProviderDaguManaged}, time.Now())
	require.NoError(t, err)
	require.NoError(t, stores.SecretStore.Create(ctx, sec, &secret.WriteValueInput{Value: name + "-token"}))

	defaults, err := profile.NewInherited(profile.GlobalInheritedRef(), profile.InheritedCreateInput{}, time.Now())
	require.NoError(t, err)
	require.NoError(t, defaults.SetVariable("TIER", name, "test", time.Now()))
	require.NoError(t, stores.ProfileStore.Create(ctx, defaults))

	prod, err := profile.New(profile.CreateInput{Name: "prod"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, stores.ProfileStore.Create(ctx, prod))
	_, err = profile.NewManager(stores.ProfileStore, stores.SecretStore).SetSecret(ctx, prod, "PROFILE_TOKEN", name+"-profile", "test")
	require.NoError(t, err)
}
