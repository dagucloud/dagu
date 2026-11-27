// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/project"
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
		contexts[name] = &Context{Context: context.Background(), Config: cfg, serverStores: server, settings: settings, projectStores: &file.ProjectStores{ProjectSettings: settings}}
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

// Only runs that can go to workers match worker nodes: the default
// project's, but not a library DAG's, nor another project's. Those match
// their inventory nodes alone.
func TestRuntimeStoresWorkerNodesInDefaultProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cfg := runtimeStoresConfig(t)
	cfg.Paths.AltDAGsDir = filepath.Join(filepath.Dir(cfg.Paths.DAGsDir), "library")
	libraryDAG := filepath.Join(cfg.Paths.AltDAGsDir, "etl.yaml")
	require.NoError(t, os.MkdirAll(cfg.Paths.AltDAGsDir, 0o750))
	require.NoError(t, os.WriteFile(libraryDAG, []byte("steps:\n  - run: echo etl\n"), 0o600))
	server := file.NewServerStores(cfg)
	workers, err := server.WorkerNodeStore()
	require.NoError(t, err)
	require.NoError(t, workers.Record(ctx, inventory.WorkerNode{Name: "gw-1", Host: "gw.tokyo", Labels: ir.NewLabels([]string{"web"}), Online: true}))

	defaults, err := file.NewProjectStores(ctx, cfg, server, project.Project{Name: project.DefaultName}, file.ProjectStoreOptions{})
	require.NoError(t, err)
	library, err := defaults.RunStores(ctx, libraryDAG)
	require.NoError(t, err)
	require.True(t, library.InLibrary())
	weather, err := file.NewProjectSettings(cfg, server, "weather")
	require.NoError(t, err)

	for _, tc := range []struct {
		name    string
		project string
		stores  *file.ProjectStores
		want    []string
	}{
		{name: "default", project: project.DefaultName, stores: defaults, want: []string{"gw-1", "web01"}},
		{name: "library", project: project.DefaultName, stores: library, want: []string{"web01"}},
		{name: "other project", project: "weather", stores: &file.ProjectStores{ProjectSettings: weather}, want: []string{"web01"}},
	} {
		c := &Context{Context: ctx, Config: cfg, serverStores: server, settings: tc.stores.ProjectSettings,
			projectStores: tc.stores, project: project.Project{Name: tc.project}}
		stores := c.runtimeStores()
		// A library DAG runs with its project's settings, so it reads the
		// default project's inventory.
		if tc.name != "library" {
			_, err := stores.Inventory.Create(ctx, inventory.Inventory{Name: "fleet", Nodes: []inventory.Node{
				{Name: "web01", Host: "10.0.1.11", Labels: []string{"web"}},
			}})
			require.NoError(t, err, tc.name)
		}

		matched, err := stores.Nodes.Select(ctx, "web")
		require.NoError(t, err)
		named, err := stores.Nodes.Named(ctx, []string{"gw-1", "web01"})
		require.NoError(t, err)
		for _, nodes := range [][]inventory.ResolvedNode{matched, named} {
			names := make([]string, len(nodes))
			for i, node := range nodes {
				names[i] = node.Name
			}
			assert.Equal(t, tc.want, names, tc.name)
		}
	}
}
