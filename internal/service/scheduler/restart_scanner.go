// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

const (
	// restartScanInterval is how often service DAGs are checked. It bounds the
	// delay a restart can add to restart_wait_sec.
	restartScanInterval = time.Second

	// restartErrorLogInterval limits how often a failing DAG is logged, since
	// the same error would otherwise repeat at every scan.
	restartErrorLogInterval = 30 * time.Second
)

// RestartScannerConfig configures a RestartScanner.
type RestartScannerConfig struct {
	// Entries returns the loaded DAG definitions.
	Entries func() []DAGEntry
	// IsSuspended reports whether a DAG, or the whole scheduler, is suspended.
	IsSuspended IsSuspendedFunc
	// IsRunning reports whether a DAG has a live process.
	IsRunning IsRunningFunc
	// IsQueued reports whether a DAG has queued items.
	IsQueued IsQueuedFunc
	// GetLatestStatus returns the status of the latest run of a DAG.
	GetLatestStatus GetLatestStatusFunc
	// RunExists reports whether a run with the ID already has a record.
	RunExists RunExistsFunc
	// Enqueue queues a run. It is nil when queues are disabled.
	Enqueue EnqueueFunc
	// Clock returns the current time.
	Clock Clock
}

// RestartScanner keeps one run of every DAG that sets restart_policy alive: it
// queues the first run of such a DAG, then a new run restart_wait_sec after the
// previous one ends. See specs/080-restart-policy.md.
//
// The scanner keeps no state of its own besides log throttling. Whether a run is
// due follows from the DAG's latest run, so a scheduler restart or failover
// resumes where the previous scheduler stopped.
type RestartScanner struct {
	cfg RestartScannerConfig

	warnedNoQueue map[string]struct{}
	lastErrorLog  map[string]time.Time
}

// NewRestartScanner creates a RestartScanner.
func NewRestartScanner(cfg RestartScannerConfig) *RestartScanner {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.IsSuspended == nil {
		cfg.IsSuspended = func(context.Context, string) (bool, error) { return false, nil }
	}
	return &RestartScanner{
		cfg:           cfg,
		warnedNoQueue: make(map[string]struct{}),
		lastErrorLog:  make(map[string]time.Time),
	}
}

// Start scans until ctx is done.
func (s *RestartScanner) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.scan(ctx)

	ticker := time.NewTicker(restartScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scan(ctx)
		}
	}
}

func (s *RestartScanner) scan(ctx context.Context) {
	now := s.cfg.Clock()
	for _, entry := range s.cfg.Entries() {
		if ctx.Err() != nil {
			return
		}
		if entry.DAG == nil || !entry.DAG.RestartPolicy.Enabled() {
			continue
		}
		if err := s.process(ctx, entry, now); err != nil {
			s.logError(ctx, entry.DAG.Name, now, err)
		}
	}
}

func (s *RestartScanner) process(ctx context.Context, entry DAGEntry, now time.Time) error {
	dag := entry.DAG

	suspended, err := isSuspendedDAG(ctx, s.cfg.IsSuspended, nil, dag, entry.DefinitionID)
	if err != nil {
		return err
	}
	if suspended {
		return nil
	}

	if s.cfg.Enqueue == nil {
		if _, warned := s.warnedNoQueue[dag.Name]; !warned {
			s.warnedNoQueue[dag.Name] = struct{}{}
			logger.Warn(ctx, "DAG sets restart_policy but queues are disabled; it will not be restarted",
				tag.DAG(dag.Name),
			)
		}
		return nil
	}

	// A live process or a queued item keeps the DAG busy. On error the DAG is
	// assumed busy, so a failing store cannot lead to two runs.
	if running, err := s.cfg.IsRunning(ctx, dag); err != nil || running {
		return err
	}
	if queued, err := s.cfg.IsQueued(ctx, dag); err != nil || queued {
		return err
	}

	status, err := s.cfg.GetLatestStatus(ctx, dag)
	if err != nil {
		return err
	}
	decision := decideRestart(dag.RestartPolicy, dag.RestartWait, &status, now)
	if !decision.start {
		return nil
	}

	runID := GenerateRestartRunID(dag.Name, status.DAGRunID, decision.at)
	exists, err := s.cfg.RunExists(ctx, dag, runID)
	if err != nil || exists {
		return err
	}

	logger.Info(ctx, "Restarting DAG",
		tag.DAG(dag.Name),
		tag.RunID(runID),
		slog.String("reason", decision.reason),
		slog.String("previous_run_id", status.DAGRunID),
		slog.String("previous_status", status.Status.String()),
	)
	return s.cfg.Enqueue(ctx, entry, runID, ir.TriggerTypeRestart, now)
}

func (s *RestartScanner) logError(ctx context.Context, dagName string, now time.Time, err error) {
	if last, ok := s.lastErrorLog[dagName]; ok && now.Sub(last) < restartErrorLogInterval {
		return
	}
	s.lastErrorLog[dagName] = now
	logger.Error(ctx, "Restart scanner failed to process DAG",
		tag.DAG(dagName),
		tag.Error(err),
	)
}

// restartDecision tells whether a service DAG is due for a new run.
type restartDecision struct {
	start  bool
	reason string
	// at is the time the new run's ID is derived from: the end of the previous
	// run, or now for the first run.
	at time.Time
}

// decideRestart applies the restart policy to the latest run of a DAG.
func decideRestart(
	policy ir.RestartPolicy,
	wait time.Duration,
	latest *ir.DAGRunStatus,
	now time.Time,
) restartDecision {
	switch {
	case latest.DAGRunID == "":
		return restartDecision{start: true, reason: "first_run", at: now}
	case latest.Status == ir.NotStarted || latest.Status.IsActive():
		return restartDecision{reason: "run_active"}
	case policy == ir.RestartPolicyUnlessStopped && latest.Status == ir.Aborted:
		return restartDecision{reason: "stopped"}
	}

	endedAt, ok := retryReferenceTime(latest)
	if !ok {
		return restartDecision{start: true, reason: "previous_run_end_unknown", at: now}
	}
	if now.Before(endedAt.Add(wait)) {
		return restartDecision{reason: "waiting"}
	}
	return restartDecision{start: true, reason: "previous_run_ended", at: endedAt}
}
