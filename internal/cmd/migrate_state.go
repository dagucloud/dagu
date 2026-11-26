// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"cmp"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"slices"
	"strconv"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/migration"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	"github.com/dagucloud/dagu/v2/internal/service/healthcheck"
)

// migrateState moves v2 state into the state directories of projects before
// the command opens a store, or waits for the process that does. It does
// nothing for a worker, which keeps no state of its own, and for a command
// that reads no state. While projects are off, it only warns when the state
// was migrated, since the process then does not see it. Meanwhile a server,
// scheduler, or coordinator answers its health endpoint with the progress,
// and every other request with 503.
func migrateState(ctx context.Context, cmdName string, scope commandScope, cfg *config.Config) error {
	if scope == commandScopeStatic || cmdName == "worker" {
		return nil
	}
	if !cfg.ProjectsEnabled {
		warnMigrated(ctx, cfg)
		return nil
	}
	migrator := migration.New(cfg, file.NewServiceRegistry(cfg), newV2RunStopper(cfg).stop)
	done, err := migrator.Done()
	if err != nil || done {
		return err
	}

	endpoints, err := migrationGateEndpoints(ctx, cmdName, cfg)
	if err != nil {
		return err
	}
	if len(endpoints) > 0 {
		gate, err := healthcheck.StartMigrationGate(ctx, endpoints, func() any { return migrator.Progress() })
		if err != nil {
			return err
		}
		defer func() {
			if err := gate.Stop(ctx); err != nil {
				logger.Warn(ctx, "Failed to stop the migration health gate", tag.Error(err))
			}
		}()
	}
	return migrator.Run(ctx)
}

// warnMigrated warns when the state in paths.data_dir was migrated to
// projects, which a process with projects off neither reads nor writes.
func warnMigrated(ctx context.Context, cfg *config.Config) {
	if cfg.Paths.DataDir == "" {
		return
	}
	if done, err := migration.New(cfg, nil, nil).Done(); err == nil && done {
		logger.Warn(ctx, "The state in paths.data_dir was migrated to projects; with projects off, "+
			"this process does not see it and writes to the directories that migration emptied",
			tag.Dir(cfg.Paths.DataDir))
	}
}

// migrationGateEndpoints returns where the command serves its health
// endpoint.
func migrationGateEndpoints(ctx context.Context, cmdName string, cfg *config.Config) ([]healthcheck.GateEndpoint, error) {
	switch cmdName {
	case "server", "start-all":
		healthPath, err := frontend.HealthPath(ctx, cfg)
		if err != nil {
			return nil, err
		}
		endpoint := healthcheck.GateEndpoint{
			Addr:       net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)),
			HealthPath: healthPath,
		}
		if tlsCfg := cfg.Server.TLS; tlsCfg != nil {
			cert, err := tls.LoadX509KeyPair(tlsCfg.CertFile, tlsCfg.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("load TLS certificate: %w", err)
			}
			endpoint.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		}
		return []healthcheck.GateEndpoint{endpoint}, nil
	case "scheduler":
		return healthPortEndpoints(cfg.Scheduler.Port), nil
	case "coordinator":
		return healthPortEndpoints(cfg.Coordinator.HealthPort), nil
	default:
		return nil, nil
	}
}

// v2RunStopper cancels the runs kept in v2's directories, as dagu stop does:
// each run whose process is live, and each run on a worker, which the
// coordinator stops when the worker next connects.
type v2RunStopper struct {
	runs      *persis.DAGRunRepository
	procs     *persis.ProcRepository
	remote    dispatch.ActiveDistributedRunStore
	manager   runtime.Manager
	requested map[string]struct{}
	// failures counts, by run, the stop requests that failed.
	failures map[string]int
}

// maxStopAttempts bounds how often a run's stop is requested again after a
// request fails, about once a second.
const maxStopAttempts = 30

// newV2RunStopper opens the run stores in v2's directories. It does not
// access the filesystem.
func newV2RunStopper(cfg *config.Config) *v2RunStopper {
	runs := file.NewDAGRunRepository(cfg)
	procs := file.NewProcRepository(cfg)
	return &v2RunStopper{
		runs:      runs,
		procs:     procs,
		remote:    file.NewServerStores(cfg).ActiveDistributedRunStore,
		manager:   runtime.NewManager(runs, procs, cfg),
		requested: make(map[string]struct{}),
		failures:  make(map[string]int),
	}
}

// stop requests the stop of each run whose stop was not requested yet, and
// returns the runs whose process is still live. A run on a worker has no
// local process. A failed request is made again on a later call, up to
// maxStopAttempts times.
func (s *v2RunStopper) stop(ctx context.Context) ([]string, error) {
	entries, err := s.procs.ListAllEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list live run processes: %w", err)
	}
	var live, stop []ir.DAGRunRef
	for _, entry := range entries {
		if entry.Fresh {
			root := cmp.Or(entry.Meta.Root(), entry.Meta.DAGRun())
			live = append(live, root)
			stop = append(stop, root)
		}
	}
	remote, err := s.remote.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list runs on workers: %w", err)
	}
	for _, run := range remote {
		if run.Status == ir.Running {
			stop = append(stop, cmp.Or(run.Root, run.DAGRun))
		}
	}
	for _, ref := range stop {
		key := ref.String()
		if _, done := s.requested[key]; done {
			continue
		}
		err := s.stopRun(ctx, ref)
		if err == nil {
			s.requested[key] = struct{}{}
			continue
		}
		s.failures[key]++
		runCtx := logger.WithValues(ctx, tag.DAG(ref.Name), tag.RunID(ref.ID))
		if s.failures[key] < maxStopAttempts {
			logger.Warn(runCtx, "Failed to cancel a live run before migrating state to projects; retrying", tag.Error(err))
			continue
		}
		// Migration still waits for the run; only the requests end.
		s.requested[key] = struct{}{}
		// With projects on, dagu stop waits for this migration.
		logger.Error(runCtx, "Gave up cancelling a live run before migrating state to projects; "+
			"kill the run's process, or run dagu stop with projects off. Migration goes on once the "+
			"process exits or its heartbeat goes stale",
			tag.Error(err))
	}

	names := make([]string, 0, len(live))
	for _, ref := range live {
		names = append(names, ref.String())
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// stopRun requests the stop of the root run ref. The request also marks
// the stop as an operator's, so a DAG with a restart_policy does not start
// again once a scheduler runs.
func (s *v2RunStopper) stopRun(ctx context.Context, ref ir.DAGRunRef) error {
	attempt, err := s.runs.FindAttempt(ctx, ref)
	if err != nil {
		return err
	}
	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return err
	}
	if err := s.manager.Stop(ctx, dag, ref.ID); err != nil {
		return err
	}
	logger.Info(logger.WithValues(ctx, tag.DAG(ref.Name), tag.RunID(ref.ID)),
		"Cancelled a live run before migrating state to projects")
	return nil
}

// healthPortEndpoints returns the endpoint of a health server on port, which
// 0 disables.
func healthPortEndpoints(port int) []healthcheck.GateEndpoint {
	if port == 0 {
		return nil
	}
	return []healthcheck.GateEndpoint{{Addr: fmt.Sprintf(":%d", port), HealthPath: healthcheck.Path}}
}
