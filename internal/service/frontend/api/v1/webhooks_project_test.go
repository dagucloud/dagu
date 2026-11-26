// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	authservice "github.com/dagucloud/dagu/v2/internal/service/auth"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	apiimpl "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	frontendfile "github.com/dagucloud/dagu/v2/internal/service/frontend/file"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const projectHookSpec = "steps:\n  - name: s\n    command: echo hi\n"

// projectWebhookServer is a server with projects on, whose
// webhook requests reach every registered project.
type projectWebhookServer struct {
	test.Server
	registry *projectregistry.Registry
	cache    *persisfile.ProjectStoreCache
	stores   *persisfile.ServerStores
	late     *lateWebhookProjects
}

// lateWebhookProjects passes calls to the projects set once the server's
// configuration is known.
type lateWebhookProjects struct {
	inner atomic.Pointer[apiimpl.WebhookProjects]
}

func (l *lateWebhookProjects) Get(ctx context.Context, name string) (*apiimpl.WebhookProject, error) {
	return (*l.inner.Load()).Get(ctx, name)
}

func (l *lateWebhookProjects) Migrated(ctx context.Context, dagName string) (*apiimpl.WebhookProject, error) {
	return (*l.inner.Load()).Migrated(ctx, dagName)
}

func (l *lateWebhookProjects) Library(ctx context.Context, path string) (bool, error) {
	return (*l.inner.Load()).Library(ctx, path)
}

func setupProjectWebhookServer(t *testing.T) *projectWebhookServer {
	t.Helper()
	late := &lateWebhookProjects{}
	server := test.SetupServer(t,
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.ProjectsEnabled = true
			cfg.Server.Auth.Mode = config.AuthModeBuiltin
			cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-key"
			cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
		}),
		test.WithServerOptions(
			frontend.WithLicenseManager(defaultTestLicenseManager()),
			frontend.WithAPIOption(apiimpl.WithWebhookProjects(late)),
		),
	)
	cfg := server.Config
	stores := persisfile.NewServerStores(cfg)
	defaults, err := persisfile.NewProjectStores(server.Context, cfg, stores, project.Project{Name: project.DefaultName}, persisfile.ProjectStoreOptions{})
	require.NoError(t, err)
	registry := projectregistry.New(cfg.Paths.DataDir)
	cache := persisfile.NewProjectStoreCache(registry, defaults,
		func(ctx context.Context, p project.Project) (*persisfile.ProjectStores, error) {
			return persisfile.NewProjectStores(ctx, cfg, stores, p, persisfile.ProjectStoreOptions{})
		},
		stores.ProjectReleases,
	)
	go cache.Run(server.Context)
	projects := frontendfile.NewWebhookProjects(cfg, stores, registry, cache)
	late.inner.Store(&projects)

	server.Client().Post("/api/v1/auth/setup", api.SetupRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)
	return &projectWebhookServer{Server: server, registry: registry, cache: cache, stores: stores, late: late}
}

// addProject registers a project, waits until its stores are open, and
// returns it.
func (s *projectWebhookServer) addProject(t *testing.T, in project.CreateInput) project.Project {
	t.Helper()
	p, err := s.registry.Create(context.Background(), in)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, ok := s.cache.Get(p.ID)
		return ok
	}, 10*time.Second, 20*time.Millisecond)
	return p
}

// writeDAG writes a DAG file of the project called name.
func (s *projectWebhookServer) writeDAG(t *testing.T, projectName, dagName string) {
	t.Helper()
	dir := s.Config.Paths.DAGsDir
	if projectName != project.DefaultName {
		dir = filepath.Join(s.Config.Paths.ProjectsDir, projectName)
	}
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, dagName+".yaml"), []byte(projectHookSpec), 0o600))
}

// createWebhook gives the DAG of the project called name a webhook and
// returns its token.
func (s *projectWebhookServer) createWebhook(t *testing.T, projectName, dagName string) string {
	t.Helper()
	settings, err := persisfile.NewProjectSettings(s.Config, s.stores, projectName)
	require.NoError(t, err)
	webhooks, err := settings.WebhookStore(nil)
	require.NoError(t, err)
	result, err := authservice.New(nil, authservice.Config{}, authservice.WithWebhookStore(webhooks)).
		CreateWebhook(context.Background(), dagName, "admin")
	require.NoError(t, err)
	return result.FullToken
}

// trigger posts to a webhook URL and returns the response.
func (s *projectWebhookServer) trigger(t *testing.T, path, token string, status int) api.WebhookResponse {
	t.Helper()
	resp := s.Client().Post("/api/v1/webhooks/"+path, api.WebhookRequest{}).
		WithBearerToken(token).ExpectStatus(status).Send(t)
	var result api.WebhookResponse
	if status == http.StatusOK {
		resp.Unmarshal(t, &result)
	}
	return result
}

// requireRunIn requires that the run was queued in the named project and
// in no other of names.
func (s *projectWebhookServer) requireRunIn(t *testing.T, owner string, run api.WebhookResponse, names ...string) {
	t.Helper()
	ref := ir.NewDAGRunRef(run.DagName, run.DagRunId)
	for _, name := range names {
		stores, err := persisfile.NewProjectStores(context.Background(), s.Config, s.stores,
			project.Project{Name: name}, persisfile.ProjectStoreOptions{})
		require.NoError(t, err)
		_, err = stores.DAGRunRepository.FindAttempt(context.Background(), ref)
		if name == owner {
			assert.NoError(t, err, "run in project %s", name)
		} else {
			assert.Error(t, err, "run in project %s", name)
		}
	}
}

// A per-project URL authenticates with that project's webhook, matching the
// project's name in any case, and queues the run in that project. A project
// that is not registered is not found.
func TestProjectWebhookAuthenticatesWithProjectWebhook(t *testing.T) {
	webhookParallel(t)
	server := setupProjectWebhookServer(t)
	server.addProject(t, project.CreateInput{Name: "weather"})
	server.writeDAG(t, "weather", "hook")
	token := server.createWebhook(t, "weather", "hook")

	for _, path := range []string{"weather/hook", "Weather/hook"} {
		run := server.trigger(t, path, token, http.StatusOK)
		server.requireRunIn(t, "weather", run, "weather", project.DefaultName)
	}
	stores, err := persisfile.NewProjectStores(context.Background(), server.Config, server.stores,
		project.Project{Name: "weather"}, persisfile.ProjectStoreOptions{})
	require.NoError(t, err)
	queued, err := stores.QueueStore.Len(context.Background(), "hook")
	require.NoError(t, err)
	assert.Equal(t, 2, queued)

	server.trigger(t, "weather/hook", "dagu_wh_wrong", http.StatusUnauthorized)
	// The URL without a project reaches the default project, which has no
	// webhook for the DAG.
	server.trigger(t, "hook", token, http.StatusUnauthorized)
	server.trigger(t, "nowhere/hook", token, http.StatusNotFound)
}

// The default project's webhooks answer both its per-project URL and the
// URL without a project.
func TestProjectWebhookDefaultProject(t *testing.T) {
	webhookParallel(t)
	server := setupProjectWebhookServer(t)
	server.writeDAG(t, project.DefaultName, "plain")
	token := server.createWebhook(t, project.DefaultName, "plain")

	run := server.trigger(t, "default/plain", token, http.StatusOK)
	server.requireRunIn(t, project.DefaultName, run, project.DefaultName)
	server.trigger(t, "plain", token, http.StatusOK)
}

// unopenedProjects has every project registered and none open.
type unopenedProjects struct{}

func (unopenedProjects) Get(_ context.Context, name string) (*apiimpl.WebhookProject, error) {
	return nil, errors.New("the stores of project " + name + " are not open")
}

func (unopenedProjects) Migrated(context.Context, string) (*apiimpl.WebhookProject, error) {
	return nil, nil
}

func (unopenedProjects) Library(context.Context, string) (bool, error) {
	return false, nil
}

// A registered project whose stores are not open yet answers 503, which
// webhook senders retry, rather than 404, which they do not.
func TestProjectWebhookNotOpenUnavailable(t *testing.T) {
	webhookParallel(t)
	server := setupProjectWebhookServer(t)
	var projects apiimpl.WebhookProjects = unopenedProjects{}
	server.late.inner.Store(&projects)

	server.trigger(t, "weather/hook", "dagu_wh_token", http.StatusServiceUnavailable)
}

// While projects are off, a per-project URL is not found.
func TestProjectWebhookOffNotFound(t *testing.T) {
	webhookParallel(t)
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)
	createTestDAG(t, server, token, "plain")
	resp := server.Client().Post("/api/v1/dags/plain/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)
	var created api.WebhookCreateResponse
	resp.Unmarshal(t, &created)

	server.Client().Post("/api/v1/webhooks/default/plain", api.WebhookRequest{}).
		WithBearerToken(created.Token).ExpectStatus(http.StatusNotFound).Send(t)
	server.Client().Post("/api/v1/webhooks/plain", api.WebhookRequest{}).
		WithBearerToken(created.Token).ExpectStatus(http.StatusOK).Send(t)
}
