// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Only the scheduler that holds the scheduler lock runs the scanner. A DAG gets
// no new run while it has a live process, a run on a worker, a queued item or
// an active run, or while its latest run cannot be read. The ID of a restart
// follows from the run it replaces, so two schedulers that decide the same
// restart around a failover create one run.
//
// A run that an operator stopped carries a stop request on its attempt,
// whatever status it ends with: a stopped distributed run whose worker is gone
// ends as failed. Dequeuing a restart leaves the same request
// (queue.AbortQueuedDAGRun). A run whose process died has none, so the DAG
// restarts once the zombie detector or the coordinator marks the run failed. A
// process killed while it writes its status can leave the run lock of the DAG
// behind, which delays the restart until the lock is stale.
//
// Two runs of a DAG can still overlap: a manual start can race with a restart,
// and runs can predate the policy. Either can end first, so a new run follows the
// run that ended last. A stop of one run then leaves the DAG stopped only if no
// other run ended after it.
//
// A run that has not started within restartNotStartedTimeout, and that no
// process, worker, queue item or pending dispatch will still start, is marked
// failed, and the DAG restarts after it. A crash while a run is queued can
// leave such a run behind.
//
// Between checks, DAG-run events tell which DAGs to check: a DAG is checked at
// the scan after its run ends, when its wait ends, and every
// restartReconcileInterval, since an event can be lost. Without events, every
// scan checks every DAG that does not wait.
//
// Each run in a row that does not succeed within restartBackoffRunTime of its
// start doubles the wait before the next run, up to restartBackoffMaxWait.
//
// Runs go through queues: while queues are disabled, a service DAG gets no run
// and every scan returns an error for it, logged at most every
// restartErrorLogInterval.

const (
	// restartScanInterval is how often the scanner reads events and checks the
	// DAGs due for a check. It bounds the delay a restart can add to
	// restart_wait_sec.
	restartScanInterval = time.Second

	// restartReconcileInterval is how often a DAG whose run is active, or that
	// stays stopped, is checked when no event says its run ended.
	restartReconcileInterval = 30 * time.Second

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

	// restartNotStartedTimeout is how long a run may stay queued or not started
	// before the scanner checks whether anything will still start it.
	restartNotStartedTimeout = 5 * time.Minute

	// restartRecentRuns is how many recent runs of a DAG are searched for the run
	// that ended last.
	restartRecentRuns = 10
)

// errRestartQueuesDisabled says a service DAG gets no run: its runs go through
// queues.
var errRestartQueuesDisabled = errors.New("restart_policy starts runs through queues, and queues are disabled")

// stuckRestartRunError is the error of a run that the scanner fails because
// nothing will start it.
const stuckRestartRunError = "the run did not start, and nothing was left to start it"

// StopRequestedFunc reports whether an operator asked to stop a run of a DAG.
type StopRequestedFunc func(ctx context.Context, dag *ir.DAG, runID string) (bool, error)

// dagRunEventReader reads the DAG-run events recorded after a cursor.
type dagRunEventReader interface {
	DAGRunHeadCursor(ctx context.Context) (eventstore.DAGRunCursor, error)
	ReadDAGRunEvents(ctx context.Context, cursor eventstore.DAGRunCursor) ([]*eventstore.Event, eventstore.DAGRunCursor, error)
}

// RestartScannerConfig configures a RestartScanner.
type RestartScannerConfig struct {
	// Entries returns the loaded DAG definitions.
	Entries func() []DAGEntry
	// IsSuspended reports whether a DAG, or the whole scheduler, is suspended.
	IsSuspended IsSuspendedFunc
	// IsRunning reports whether a DAG has a live process.
	IsRunning IsRunningFunc
	// HasDistributedRun reports whether a DAG has a live run on a distributed
	// worker, which leaves no local process. It is nil without distributed
	// execution.
	HasDistributedRun IsRunningFunc
	// IsQueued reports whether a DAG has queued items.
	IsQueued IsQueuedFunc
	// IsDispatchPending reports whether a run waits in the coordinator for a
	// worker, which leaves no queue item, process or lease. It is nil without
	// distributed execution.
	IsDispatchPending func(ctx context.Context, status *ir.DAGRunStatus) (bool, error)
	// FailStuckRun marks a run that never started as failed at the given time,
	// unless its latest attempt or status changed since it was read. Without
	// it, a new run follows a stuck run directly.
	FailStuckRun func(ctx context.Context, dag *ir.DAG, status *ir.DAGRunStatus, at time.Time) error
	// GetLatestStatus returns the status of the latest run of a DAG, the
	// initial status for a DAG without a run, and an error when the latest run
	// cannot be read.
	GetLatestStatus GetLatestStatusFunc
	// RecentRuns returns the recent runs of a DAG, newest first.
	RecentRuns func(ctx context.Context, dag *ir.DAG) ([]ir.DAGRunStatus, error)
	// IsStopRequested reports whether an operator stopped a run.
	IsStopRequested StopRequestedFunc
	// Enqueue queues a run, unless a run with the ID already exists. It is nil
	// when queues are disabled.
	Enqueue EnqueueFunc
	// Clock returns the current time.
	Clock Clock
}

// RestartScanner keeps one run of every DAG that sets restart_policy alive: it
// queues the first run of such a DAG, then a new run restart_wait_sec after the
// previous one ends. With unless-stopped, no run follows a run that an operator
// stopped.
//
// Whether a run is due follows from the DAG's stored runs, so a scheduler restart
// or failover resumes where the previous scheduler stopped. The scanner only
// remembers what saves logs and store reads, and the runs in a row that failed
// soon after they started; a scheduler that takes over counts them anew.
type RestartScanner struct {
	cfg RestartScannerConfig
	// events is nil when the event store is disabled.
	events dagRunEventReader
	// cursor is where events are read from, once tailing is set.
	cursor  eventstore.DAGRunCursor
	tailing bool

	lastErrorLog      map[string]time.Time
	lastEventErrorLog time.Time
	// stoppedRuns maps a DAG name to the attempt of its latest run, once an
	// operator is known to have stopped that attempt.
	stoppedRuns map[string]string
	backoff     map[string]restartBackoff
	// nextCheck maps a DAG name to when it is checked again, unless an event
	// says its run ended first.
	nextCheck map[string]restartCheck
}

// restartCheck is when a DAG is checked again.
type restartCheck struct {
	at time.Time
	// restartWait is the restart_wait_sec the time follows from; an edited
	// DAG is checked at once.
	restartWait time.Duration
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
		cfg:          cfg,
		lastErrorLog: make(map[string]time.Time),
		stoppedRuns:  make(map[string]string),
		backoff:      make(map[string]restartBackoff),
		nextCheck:    make(map[string]restartCheck),
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
	s.readEvents(ctx, now)
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

// readEvents makes the DAGs whose run ended due for a check. The first read
// only takes the head of the event store; the same scan checks every DAG that
// does not wait, which covers what came before.
func (s *RestartScanner) readEvents(ctx context.Context, now time.Time) {
	if s.events == nil {
		return
	}
	if !s.tailing {
		cursor, err := s.events.DAGRunHeadCursor(ctx)
		if err != nil {
			s.logEventError(ctx, now, err)
			return
		}
		s.cursor, s.tailing = cursor, true
		return
	}
	events, cursor, err := s.events.ReadDAGRunEvents(ctx, s.cursor)
	if err != nil {
		s.logEventError(ctx, now, err)
		return
	}
	s.cursor = cursor
	for _, event := range events {
		if runEnded(event.Status) {
			delete(s.nextCheck, event.DAGName)
		}
	}
}

// runEnded reports whether a DAG-run event status is one a run ends with.
// Rewriting a status emits an event of another type, so the status is used.
func runEnded(status string) bool {
	switch status {
	case ir.Succeeded.String(), ir.PartiallySucceeded.String(), ir.Failed.String(),
		ir.Aborted.String(), ir.Rejected.String():
		return true
	}
	return false
}

func (s *RestartScanner) process(ctx context.Context, entry DAGEntry, now time.Time) error {
	dag := entry.DAG

	// Nothing makes a run due before the check: a run started meanwhile only
	// keeps the DAG busy, and an event wakes the DAG once that run ends.
	if check, ok := s.nextCheck[dag.Name]; ok && check.restartWait == dag.RestartWait && now.Before(check.at) {
		return nil
	}
	delete(s.nextCheck, dag.Name)

	suspended, err := isSuspendedDAG(ctx, s.cfg.IsSuspended, nil, dag, entry.DefinitionID)
	if err != nil {
		return err
	}
	if suspended {
		logNoRestart(ctx, dag, "suspended")
		return nil
	}

	if s.cfg.Enqueue == nil {
		return errRestartQueuesDisabled
	}

	status, err := s.statusToFollow(ctx, dag)
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
		if !decision.due.IsZero() {
			s.checkAt(dag, decision.due)
		} else {
			s.checkLater(dag, now)
		}
		logNoRestart(ctx, dag, decision.reason)
		return nil
	}

	// A live process, a live run on a worker or a queued item of another run
	// keeps the DAG busy: an older run can outlive a newer one. These cost more
	// than the latest run, so they are read only before a restart. On error the
	// DAG is assumed busy, so a failing store cannot lead to two runs.
	if running, err := s.cfg.IsRunning(ctx, dag); err != nil || running {
		if running {
			s.checkLater(dag, now)
			logNoRestart(ctx, dag, "run_alive")
		}
		return err
	}
	if s.cfg.HasDistributedRun != nil {
		if running, err := s.cfg.HasDistributedRun(ctx, dag); err != nil || running {
			if running {
				s.checkLater(dag, now)
				logNoRestart(ctx, dag, "run_alive_on_worker")
			}
			return err
		}
	}
	if queued, err := s.cfg.IsQueued(ctx, dag); err != nil || queued {
		if queued {
			s.checkLater(dag, now)
			logNoRestart(ctx, dag, "run_queued")
		}
		return err
	}
	if decision.stuck {
		if handled, err := s.failStuckRun(ctx, dag, &status, now); handled || err != nil {
			return err
		}
	}

	runID := GenerateRestartRunID(dag.Name, status.DAGRunID, decision.previousEnd)
	logger.Info(ctx, "Restarting DAG",
		tag.DAG(dag.Name),
		tag.RunID(runID),
		slog.String("reason", decision.reason),
		slog.String("previous_run_id", status.DAGRunID),
		slog.String("previous_status", status.Status.String()),
	)
	if err := s.cfg.Enqueue(ctx, entry, runID, ir.TriggerTypeRestart, now); err != nil {
		return err
	}
	s.checkLater(dag, now)
	return nil
}

// statusToFollow returns the status of the run that a new run would follow: the
// latest run or, once it has ended, the run that ended last. The end is stored to
// the second, and a tie goes to the older run: a stop of the newer one in the same
// second does not hide a run that nobody stopped.
func (s *RestartScanner) statusToFollow(ctx context.Context, dag *ir.DAG) (ir.DAGRunStatus, error) {
	latest, err := s.cfg.GetLatestStatus(ctx, dag)
	if err != nil || s.cfg.RecentRuns == nil || !runEnded(latest.Status.String()) {
		return latest, err
	}
	lastEnd, ok := retryReferenceTime(&latest)
	if !ok {
		return latest, nil
	}
	recent, err := s.cfg.RecentRuns(ctx, dag)
	if err != nil {
		return ir.DAGRunStatus{}, err
	}
	for i := range recent {
		if !runEnded(recent[i].Status.String()) {
			continue
		}
		if end, ok := retryReferenceTime(&recent[i]); ok && !end.Before(lastEnd) {
			latest, lastEnd = recent[i], end
		}
	}
	return latest, nil
}

// failStuckRun fails a latest run that never started, unless a coordinator
// still holds it for a worker. It reports whether the scan of the DAG ends
// there: the DAG restarts after the failed run like after any ended run.
func (s *RestartScanner) failStuckRun(ctx context.Context, dag *ir.DAG, status *ir.DAGRunStatus, now time.Time) (bool, error) {
	if s.cfg.IsDispatchPending != nil {
		if pending, err := s.cfg.IsDispatchPending(ctx, status); err != nil || pending {
			if pending {
				s.checkLater(dag, now)
				logNoRestart(ctx, dag, "run_dispatch_pending")
			}
			return true, err
		}
	}
	if s.cfg.FailStuckRun == nil {
		return false, nil
	}
	logger.Warn(ctx, "Failing a run that never started",
		tag.DAG(dag.Name),
		tag.RunID(status.DAGRunID),
		slog.String("status", status.Status.String()),
	)
	if err := s.cfg.FailStuckRun(ctx, dag, status, now); err != nil {
		return true, err
	}
	s.checkAt(dag, now)
	return true, nil
}

// checkAt sets when the DAG is checked again.
func (s *RestartScanner) checkAt(dag *ir.DAG, at time.Time) {
	s.nextCheck[dag.Name] = restartCheck{at: at, restartWait: dag.RestartWait}
}

// checkLater leaves a DAG that waits for a run to end until an event says it
// ended, or until restartReconcileInterval passes. Without events, the next
// scan checks it.
func (s *RestartScanner) checkLater(dag *ir.DAG, now time.Time) {
	if s.tailing {
		s.checkAt(dag, now.Add(restartReconcileInterval))
	}
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
	for name := range s.nextCheck {
		if _, ok := services[name]; !ok {
			delete(s.nextCheck, name)
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

// logEventError logs a failed event read at most every restartErrorLogInterval.
func (s *RestartScanner) logEventError(ctx context.Context, now time.Time, err error) {
	if !s.lastEventErrorLog.IsZero() && now.Sub(s.lastEventErrorLog) < restartErrorLogInterval {
		return
	}
	s.lastEventErrorLog = now
	logger.Warn(ctx, "Restart scanner failed to read DAG-run events", tag.Error(err))
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
	// due is when the next run becomes due, for a DAG that waits.
	due time.Time
	// stuck says the latest run never started within restartNotStartedTimeout.
	stuck bool
}

// decideRestart tells whether the latest run of a DAG is followed by a new run.
// The restart policy is applied by the caller, since unless-stopped needs to
// know who aborted a run.
func decideRestart(wait time.Duration, latest *ir.DAGRunStatus, now time.Time) restartDecision {
	switch {
	case latest.DAGRunID == "":
		return restartDecision{start: true, reason: "first_run"}
	case latest.Status == ir.NotStarted || latest.Status == ir.Queued:
		createdAt, ok := retryReferenceTime(latest)
		if !ok || !now.Before(createdAt.Add(restartNotStartedTimeout)) {
			return restartDecision{start: true, reason: "run_never_started", stuck: true}
		}
		// A queued run normally starts within seconds, and its end event wakes
		// the DAG; a not started one waits for a worker.
		if latest.Status == ir.Queued {
			return restartDecision{reason: "run_active"}
		}
		return restartDecision{reason: "run_not_started", due: createdAt.Add(restartNotStartedTimeout)}
	case latest.Status.IsActive():
		return restartDecision{reason: "run_active"}
	}

	endedAt, ok := retryReferenceTime(latest)
	if !ok {
		return restartDecision{start: true, reason: "previous_run_end_unknown"}
	}
	// The end is stored to the second, so the run may have ended up to a second
	// after it.
	if due := endedAt.Add(time.Second + wait); now.Before(due) {
		return restartDecision{reason: "waiting", due: due}
	}
	return restartDecision{start: true, reason: "previous_run_ended", previousEnd: endedAt}
}
