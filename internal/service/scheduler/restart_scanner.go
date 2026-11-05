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

// Only the scheduler that holds the scheduler lock runs the scanner. A DAG gets
// no new run while it has a live process, a queued item or an active run, or
// while its latest run cannot be read. The ID of a restart follows from the run
// it replaces, so two schedulers that decide the same restart around a failover
// create one run.
//
// A run that an operator stopped carries a stop request on its attempt,
// whatever status it ends with: a stopped distributed run whose worker is gone
// ends as failed. Dequeuing a restart leaves the same request
// (queue.AbortQueuedDAGRun). A run whose process died has none, so the DAG
// restarts once the zombie detector or the coordinator marks the run failed. A
// process killed while it writes its status can leave the run lock of the DAG
// behind, which delays the restart until the lock is stale.
//
// Each run in a row that does not succeed within restartBackoffRunTime of its
// start doubles the wait before the next run, up to restartBackoffMaxWait.

const (
	// restartScanInterval is how often service DAGs are checked. It bounds the
	// delay a restart can add to restart_wait_sec.
	restartScanInterval = time.Second

	// restartErrorLogInterval limits how often a failing DAG is logged, since
	// the same error would otherwise repeat at every scan.
	restartErrorLogInterval = 30 * time.Second

	// restartBackoffRunTime is how long a run that does not succeed must last
	// for the next run to follow it after restart_wait_sec. Each run in a row
	// that fails sooner doubles the wait.
	restartBackoffRunTime = 10 * time.Second

	// restartBackoffMaxWait bounds the doubled wait. A restart_wait_sec above
	// it is not doubled.
	restartBackoffMaxWait = 5 * time.Minute
)

// StopRequestedFunc reports whether an operator asked to stop a run of a DAG.
type StopRequestedFunc func(ctx context.Context, dag *ir.DAG, runID string) (bool, error)

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
	// GetLatestStatus returns the status of the latest run of a DAG, the
	// initial status for a DAG without a run, and an error when the latest run
	// cannot be read.
	GetLatestStatus GetLatestStatusFunc
	// IsStopRequested reports whether an operator stopped a run.
	IsStopRequested StopRequestedFunc
	// RunExists reports whether a run with the ID already has a record.
	RunExists RunExistsFunc
	// Enqueue queues a run. It is nil when queues are disabled.
	Enqueue EnqueueFunc
	// Clock returns the current time.
	Clock Clock
}

// RestartScanner keeps one run of every DAG that sets restart_policy alive: it
// queues the first run of such a DAG, then a new run restart_wait_sec after the
// previous one ends. With unless-stopped, no run follows a run that an operator
// stopped.
//
// Whether a run is due follows from the DAG's latest run, so a scheduler restart
// or failover resumes where the previous scheduler stopped. The scanner only
// remembers what saves logs and store reads, and the runs in a row that failed
// soon after they started; a scheduler that takes over counts them anew.
type RestartScanner struct {
	cfg RestartScannerConfig

	warnedNoQueue map[string]struct{}
	lastErrorLog  map[string]time.Time
	// stoppedRuns maps a DAG name to the attempt of its latest run, once an
	// operator is known to have stopped that attempt.
	stoppedRuns map[string]string
	backoff     map[string]restartBackoff
}

// restartBackoff counts the runs of a DAG in a row that failed soon after they
// started.
type restartBackoff struct {
	// runID is the latest run counted, so each run is counted once.
	runID    string
	failures int
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
		stoppedRuns:   make(map[string]string),
		backoff:       make(map[string]restartBackoff),
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
	services := make(map[string]struct{})
	for _, entry := range s.cfg.Entries() {
		if ctx.Err() != nil {
			return
		}
		if entry.DAG == nil || !entry.DAG.RestartPolicy.Enabled() {
			continue
		}
		services[entry.DAG.Name] = struct{}{}
		if err := s.process(ctx, entry, now); err != nil {
			s.logError(ctx, entry.DAG.Name, now, err)
		}
	}
	s.forgetOthers(services)
}

func (s *RestartScanner) process(ctx context.Context, entry DAGEntry, now time.Time) error {
	dag := entry.DAG

	suspended, err := isSuspendedDAG(ctx, s.cfg.IsSuspended, nil, dag, entry.DefinitionID)
	if err != nil {
		return err
	}
	if suspended {
		logNoRestart(ctx, dag, "suspended")
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
		if running {
			logNoRestart(ctx, dag, "run_alive")
		}
		return err
	}
	if queued, err := s.cfg.IsQueued(ctx, dag); err != nil || queued {
		if queued {
			logNoRestart(ctx, dag, "run_queued")
		}
		return err
	}

	status, err := s.cfg.GetLatestStatus(ctx, dag)
	if err != nil {
		return err
	}
	decision := decideRestart(s.restartWait(ctx, dag, &status), &status, now)
	if decision.start && dag.RestartPolicy == ir.RestartPolicyUnlessStopped && status.DAGRunID != "" {
		stopped, err := s.stoppedByOperator(ctx, dag, &status)
		if err != nil {
			return err
		}
		if stopped {
			decision = restartDecision{reason: "stopped"}
		}
	}
	if !decision.start {
		logNoRestart(ctx, dag, decision.reason)
		return nil
	}

	runID := GenerateRestartRunID(dag.Name, status.DAGRunID, decision.previousEnd)
	exists, err := s.cfg.RunExists(ctx, dag, runID)
	if err != nil {
		return err
	}
	if exists {
		logNoRestart(ctx, dag, "run_exists", tag.RunID(runID))
		return nil
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

// stoppedByOperator reports whether an operator stopped the run, whatever
// status it ended with: a stopped distributed run whose worker is gone ends as
// failed. Other runs, such as a run whose DAG-level precondition was not met or
// a restart dropped from the queue while the DAG was suspended, do not stop the
// DAG.
func (s *RestartScanner) stoppedByOperator(ctx context.Context, dag *ir.DAG, status *ir.DAGRunStatus) (bool, error) {
	if status.AttemptID != "" && s.stoppedRuns[dag.Name] == status.AttemptID {
		return true, nil
	}
	if s.cfg.IsStopRequested == nil {
		return status.Status == ir.Aborted, nil
	}
	stopped, err := s.cfg.IsStopRequested(ctx, dag, status.DAGRunID)
	if err != nil || !stopped {
		return false, err
	}
	if status.AttemptID != "" {
		s.stoppedRuns[dag.Name] = status.AttemptID
	}
	return true, nil
}

// restartWait is how long the DAG waits after its latest run ends:
// restart_wait_sec, doubled for each run in a row that failed soon after it
// started, so a DAG that keeps crashing does not restart every few seconds.
func (s *RestartScanner) restartWait(ctx context.Context, dag *ir.DAG, latest *ir.DAGRunStatus) time.Duration {
	if latest.DAGRunID == "" || latest.Status == ir.NotStarted || latest.Status.IsActive() {
		return dag.RestartWait
	}
	b := s.backoff[dag.Name]
	if b.runID == latest.DAGRunID {
		return backoffWait(dag.RestartWait, b.failures)
	}

	b.runID = latest.DAGRunID
	if failedSoonAfterStart(latest) {
		b.failures++
	} else {
		b.failures = 0
	}
	s.backoff[dag.Name] = b
	wait := backoffWait(dag.RestartWait, b.failures)
	if b.failures > 1 {
		logger.Warn(ctx, "DAG keeps failing soon after it starts; its next run waits longer",
			tag.DAG(dag.Name),
			tag.RunID(latest.DAGRunID),
			slog.Int("failures", b.failures),
			slog.Duration("wait", wait),
		)
	}
	return wait
}

// backoffWait doubles wait for each failure after the first, up to
// restartBackoffMaxWait, or up to wait when it is longer.
func backoffWait(wait time.Duration, failures int) time.Duration {
	limit := max(wait, restartBackoffMaxWait)
	for i := 1; i < failures && wait < limit; i++ {
		wait *= 2
	}
	return min(wait, limit)
}

// failedSoonAfterStart reports whether a run that ended did not succeed within
// restartBackoffRunTime of its start. A run without a start time never ran.
func failedSoonAfterStart(status *ir.DAGRunStatus) bool {
	if status.Status.IsSuccess() {
		return false
	}
	startedAt, ok := parseRFC3339(status.StartedAt)
	if !ok {
		return true
	}
	finishedAt, ok := parseRFC3339(status.FinishedAt)
	return !ok || finishedAt.Sub(startedAt) < restartBackoffRunTime
}

// forgetOthers drops what the scanner remembers about DAGs it no longer keeps
// alive.
func (s *RestartScanner) forgetOthers(services map[string]struct{}) {
	for name := range s.warnedNoQueue {
		if _, ok := services[name]; !ok {
			delete(s.warnedNoQueue, name)
		}
	}
	for name := range s.lastErrorLog {
		if _, ok := services[name]; !ok {
			delete(s.lastErrorLog, name)
		}
	}
	for name := range s.stoppedRuns {
		if _, ok := services[name]; !ok {
			delete(s.stoppedRuns, name)
		}
	}
	for name := range s.backoff {
		if _, ok := services[name]; !ok {
			delete(s.backoff, name)
		}
	}
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

func logNoRestart(ctx context.Context, dag *ir.DAG, reason string, attrs ...slog.Attr) {
	attrs = append([]slog.Attr{tag.DAG(dag.Name), slog.String("reason", reason)}, attrs...)
	logger.Debug(ctx, "Not restarting DAG", attrs...)
}

// restartDecision tells whether a service DAG is due for a new run.
type restartDecision struct {
	start  bool
	reason string
	// previousEnd is when the run the new run follows ended. It is zero for the
	// first run of a DAG and when the end of the previous run is unknown.
	previousEnd time.Time
}

// decideRestart tells whether the latest run of a DAG is followed by a new run.
// The restart policy is applied by the caller, since unless-stopped needs to
// know who aborted a run.
func decideRestart(wait time.Duration, latest *ir.DAGRunStatus, now time.Time) restartDecision {
	switch {
	case latest.DAGRunID == "":
		return restartDecision{start: true, reason: "first_run"}
	case latest.Status == ir.NotStarted || latest.Status.IsActive():
		return restartDecision{reason: "run_active"}
	}

	endedAt, ok := retryReferenceTime(latest)
	if !ok {
		return restartDecision{start: true, reason: "previous_run_end_unknown"}
	}
	// The end is stored to the second, so the run may have ended up to a second
	// after it.
	if now.Before(endedAt.Add(time.Second + wait)) {
		return restartDecision{reason: "waiting"}
	}
	return restartDecision{start: true, reason: "previous_run_ended", previousEnd: endedAt}
}
