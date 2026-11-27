// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
)

// A project that is not registered is not found, while a registered one
// whose stores the cache has not opened yet reports another error, so a
// webhook request for it can be retried.
func TestWebhookProjectsGet(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg := &config.Config{ProjectsEnabled: true}
	cfg.Paths.DataDir = filepath.Join(root, "data")
	cfg.Paths.ProjectsDir = filepath.Join(root, "projects")
	registry := projectregistry.New(cfg.Paths.DataDir)
	_, err := registry.Create(ctx, project.CreateInput{Name: "weather"})
	require.NoError(t, err)
	server := persisfile.NewServerStores(cfg)
	// The cache never runs, so it opens no project.
	cache := persisfile.NewProjectStoreCache(registry, nil, nil, server.ProjectReleases)
	projects := NewWebhookProjects(cfg, server, registry, cache)

	_, err = projects.Get(ctx, "nowhere")
	assert.ErrorIs(t, err, project.ErrNotFound)

	_, err = projects.Get(ctx, "Weather")
	require.Error(t, err)
	assert.NotErrorIs(t, err, project.ErrNotFound)
}
