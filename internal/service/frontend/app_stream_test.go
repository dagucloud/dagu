// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/dagucloud/dagu/v2/internal/service/frontend/sse"
)

// With projects off or on, the server's stream wakes on the queue items,
// scheduler state, and suspend flags that the default project's stores
// write, wherever its paths place them.
func TestAppStreamFollowsDefaultProjectStores(t *testing.T) {
	t.Parallel()
	for _, projectsOn := range []bool{false, true} {
		t.Run(map[bool]string{false: "Off", true: "On"}[projectsOn], func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			root := t.TempDir()
			cfg := &config.Config{ProjectsEnabled: projectsOn}
			cfg.Core.SkipExamples = true
			cfg.Paths = config.PathsConfig{
				DAGsDir:         filepath.Join(root, "dags"),
				ProjectsDir:     filepath.Join(root, "projects"),
				DataDir:         filepath.Join(root, "data"),
				QueueDir:        filepath.Join(root, "queue"),
				SuspendFlagsDir: filepath.Join(root, "suspend"),
			}
			stores, err := persisfile.NewProjectStores(ctx, cfg, persisfile.NewServerStores(cfg),
				project.Project{Name: project.DefaultName}, persisfile.ProjectStoreOptions{})
			require.NoError(t, err)
			// The watchers start on existing directories only.
			require.NoError(t, stores.QueueStore.Enqueue(ctx, "etl", queue.QueuePriorityLow, ir.NewDAGRunRef("etl", "run-0")))
			require.NoError(t, stores.SchedulerStateStore.Save(ctx, &schedulerstate.State{}))

			stream, err := newAppStream(cfg)
			require.NoError(t, err)
			t.Cleanup(stream.Shutdown)
			events, unsubscribe := stream.Subscribe(ctx)
			defer unsubscribe()

			require.NoError(t, stores.QueueStore.Enqueue(ctx, "etl", queue.QueuePriorityLow, ir.NewDAGRunRef("etl", "run-1")))
			awaitAppEvent(t, events, sse.AppEventTypeQueue)
			require.NoError(t, stores.SchedulerStateStore.Save(ctx, &schedulerstate.State{
				DAGs: map[string]schedulerstate.DAGWatermark{"etl": {LastScheduledTime: time.Now()}},
			}))
			awaitAppEvent(t, events, sse.AppEventTypeScheduler)
			require.NoError(t, stores.DAGRepository.SetSuspended(ctx, "etl", true))
			awaitAppEvent(t, events, sse.AppEventTypeDAGChanged)
		})
	}
}

func awaitAppEvent(t *testing.T, events <-chan sse.AppEvent, want sse.AppEventType) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		select {
		case event := <-events:
			if event.Type == want {
				return
			}
		case <-ctx.Done():
			t.Fatalf("no %s event", want)
		}
	}
}
