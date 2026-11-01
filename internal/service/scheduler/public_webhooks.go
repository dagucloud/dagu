// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cloudrelay"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/publicwebhook"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// newPublicWebhooks creates the service that holds the server's public
// webhook URLs, or nil when deps leave the feature out.
func newPublicWebhooks(cfg *config.Config, deps Dependencies, s *Scheduler) *publicwebhook.Service {
	if deps.Webhooks == nil || deps.PublicWebhookStore == nil || deps.CloudCredentials == nil {
		return nil
	}
	return publicwebhook.New(publicwebhook.Config{
		Credentials: deps.CloudCredentials,
		Webhooks:    deps.Webhooks,
		Store:       deps.PublicWebhookStore,
		Runs: &webhookRuns{
			config:                 cfg,
			dagRepository:          s.dagRepository,
			dagRunRepository:       s.dagRunRepository,
			queueStore:             s.queueStore,
			dagExecutor:            s.dagExecutor,
			workspaceBaseConfigDir: workspace.BaseConfigDir(cfg.Paths.DAGsDir),
		},
		Hello:          cloudrelay.Hello{Name: serverName(cfg), Version: config.Version},
		MaxPayloadSize: cfg.Webhooks.MaxPayloadSize,
	})
}

// serverName names the server to the relay as the license does.
func serverName(cfg *config.Config) string {
	if cfg.License.ServerName != "" {
		return cfg.License.ServerName
	}
	hostname, _ := os.Hostname()
	return hostname
}

// webhookRuns queues the runs public webhook deliveries ask for.
type webhookRuns struct {
	config                 *config.Config
	dagRepository          *persis.DAGRepository
	dagRunRepository       *persis.DAGRunRepository
	queueStore             queuedomain.QueueStore
	dagExecutor            *DAGExecutor
	workspaceBaseConfigDir string
}

func (r *webhookRuns) DAG(ctx context.Context, dagName string) (*ir.DAG, error) {
	return r.dagRepository.GetDetails(ctx, dagName, persis.DAGLoadOptions{})
}

func (r *webhookRuns) Queued(ctx context.Context, dag *ir.DAG) (int, error) {
	return r.queueStore.Len(ctx, dag.ProcGroup())
}

func (r *webhookRuns) Enqueue(ctx context.Context, dagName string, dag *ir.DAG, runID, params string) error {
	profileName, err := r.dagExecutor.defaultProfileName(ctx, dagName, dag)
	switch {
	case errors.Is(err, profile.ErrInvalidName), errors.Is(err, profile.ErrNotFound),
		errors.Is(err, profile.ErrDisabled), errors.Is(err, errInvalidWorkspaceLabel):
		return fmt.Errorf("%w: %w", publicwebhook.ErrInvalidRun, err)
	case err != nil:
		return fmt.Errorf("failed to resolve DAG profile: %w", err)
	}
	return EnqueueWebhookRun(
		ctx,
		r.dagRunRepository,
		r.queueStore,
		r.config.Paths.LogDir,
		r.config.Paths.ArtifactDir,
		r.config.Paths.BaseConfig,
		r.workspaceBaseConfigDir,
		dagName,
		dag,
		runID,
		params,
		profileName,
		time.Now(),
	)
}
