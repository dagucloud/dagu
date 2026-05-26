// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/require"
)

func TestScheduler_StandbyHealthServerStartsBeforeLockAndStopsCleanly(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	fixture := newHASchedulerFixture(t)
	ctx := context.Background()

	sc1 := newHASchedulerForTest(t, fixture, TestHooks{})
	sc1.SetClock(clock)
	errCh1 := startSchedulerForTest(t, sc1, ctx)
	defer func() {
		sc1.Stop(ctx)
		waitSchedulerStopForTest(t, errCh1, "leader scheduler")
	}()

	waitStarted := make(chan struct{}, 1)
	sc2 := newHASchedulerForTest(t, fixture, TestHooks{
		OnLockWait: func() {
			select {
			case waitStarted <- struct{}{}:
			default:
			}
		},
	})
	sc2.SetClock(clock)
	errCh2 := startWaitingSchedulerForTest(t, sc2, ctx, waitStarted)

	require.False(t, sc2.IsRunning(), "standby scheduler should still be waiting on the lock")

	standbyURL := requireHealthServerURL(t, sc2.healthServer)
	requireHealthySchedulerHealth(t, standbyURL)

	sc2.Stop(ctx)
	waitSchedulerStopForTest(t, errCh2, "standby scheduler")
	requireHealthServerStopped(t, standbyURL)
}

type haSchedulerFixture struct {
	cfg         *config.Config
	dagRunStore exec.DAGRunStore
	queueStore  exec.QueueStore
	procStore   exec.ProcStore
	dagRunMgr   runtime.Manager
}

func newHASchedulerFixture(t *testing.T) *haSchedulerFixture {
	t.Helper()

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Core: config.Core{
			Location: time.UTC,
		},
		Paths: config.PathsConfig{
			DataDir:            filepath.Join(tmpDir, "data"),
			DAGsDir:            filepath.Join(tmpDir, "dags"),
			DAGRunsDir:         filepath.Join(tmpDir, "data", "dag-runs"),
			ArtifactDir:        filepath.Join(tmpDir, "data", "artifacts"),
			QueueDir:           filepath.Join(tmpDir, "data", "queue"),
			ProcDir:            filepath.Join(tmpDir, "data", "proc"),
			ServiceRegistryDir: filepath.Join(tmpDir, "data", "service-registry"),
			LogDir:             filepath.Join(tmpDir, "logs"),
		},
		Proc: config.Proc{
			HeartbeatInterval:     5 * time.Second,
			HeartbeatSyncInterval: 10 * time.Second,
			StaleThreshold:        90 * time.Second,
		},
		Scheduler: config.Scheduler{
			Port:               0,
			LockStaleThreshold: 30 * time.Second,
			LockRetryInterval:  50 * time.Millisecond,
		},
		DefaultExecMode: config.ExecutionModeLocal,
	}

	dagRunStore := dagrun.New(
		cfg.Paths.DAGRunsDir,
		dagrun.WithArtifactDir(cfg.Paths.ArtifactDir),
	)
	queueStore := store.NewQueueStore(file.NewCollection(cfg.Paths.QueueDir))
	procStore := newSchedulerTestProcStore(cfg.Paths.ProcDir, cfg)

	return &haSchedulerFixture{
		cfg:         cfg,
		dagRunStore: dagRunStore,
		queueStore:  queueStore,
		procStore:   procStore,
		dagRunMgr:   runtime.NewManager(dagRunStore, procStore, cfg),
	}
}

func newHASchedulerForTest(t *testing.T, fixture *haSchedulerFixture, hooks TestHooks) *Scheduler {
	t.Helper()

	sc, err := NewWithHooksForTest(
		fixture.cfg,
		&staticEntryReader{},
		fixture.dagRunMgr,
		fixture.dagRunStore,
		fixture.queueStore,
		fixture.procStore,
		nil,
		nil,
		nil,
		hooks,
	)
	require.NoError(t, err)

	sc.healthServer = newHealthServerWithAddr("127.0.0.1:0")
	return sc
}

func startSchedulerForTest(t *testing.T, sc *Scheduler, ctx context.Context) chan error {
	t.Helper()

	errCh := make(chan error, 1)
	go func() {
		errCh <- sc.Start(ctx)
	}()

	var startErr error
	var exited bool
	require.Eventually(t, func() bool {
		if sc.IsRunning() {
			return true
		}
		select {
		case err := <-errCh:
			startErr = err
			exited = true
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond, "scheduler did not start in time")

	if exited {
		require.NoError(t, startErr)
		t.Fatal("scheduler exited before reporting running")
	}

	return errCh
}

func startWaitingSchedulerForTest(t *testing.T, sc *Scheduler, ctx context.Context, waitStarted <-chan struct{}) chan error {
	t.Helper()

	errCh := make(chan error, 1)
	go func() {
		errCh <- sc.Start(ctx)
	}()

	select {
	case <-waitStarted:
		return errCh
	case err := <-errCh:
		require.NoError(t, err)
		t.Fatal("scheduler exited before waiting on the lock")
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not begin waiting on the lock")
	}

	return errCh
}

func waitSchedulerStopForTest(t *testing.T, errCh <-chan error, name string) {
	t.Helper()

	select {
	case err := <-errCh:
		require.NoError(t, err, "%s returned an unexpected error", name)
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not stop in time", name)
	}
}

func requireHealthServerURL(t *testing.T, hs *HealthServer) string {
	t.Helper()

	var url string
	require.Eventually(t, func() bool {
		url = hs.URL()
		return url != ""
	}, 5*time.Second, 10*time.Millisecond, "health server did not bind an address")
	return url
}

func requireHealthySchedulerHealth(t *testing.T, baseURL string) {
	t.Helper()

	client := &http.Client{Timeout: time.Second}

	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL + "/health")
		if err != nil {
			return false
		}
		defer func() {
			_ = resp.Body.Close()
		}()
		if resp.StatusCode != http.StatusOK {
			return false
		}

		var healthResp HealthResponse
		if err := json.NewDecoder(resp.Body).Decode(&healthResp); err != nil {
			return false
		}

		return healthResp.Status == "healthy"
	}, 5*time.Second, 10*time.Millisecond, "health endpoint did not become healthy")
}

func requireHealthServerStopped(t *testing.T, baseURL string) {
	t.Helper()

	client := &http.Client{Timeout: 200 * time.Millisecond}

	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL + "/health")
		if err == nil {
			_ = resp.Body.Close()
			return false
		}
		return true
	}, 5*time.Second, 10*time.Millisecond, "health server still responded after stop")
}

type staticEntryReader struct{}

func (*staticEntryReader) Init(context.Context) error {
	return nil
}

func (*staticEntryReader) Start(context.Context) {}

func (*staticEntryReader) Stop() {}

func (*staticEntryReader) DAGs() []*core.DAG {
	return nil
}

func (*staticEntryReader) DAGStore() exec.DAGStore {
	return nil
}
