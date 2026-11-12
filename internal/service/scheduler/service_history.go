// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/samples"
)

// The scheduler records one sample for each service run that ends
// (specs/097-service-run-history.md). A store error never stops a DAG: the
// scheduler logs it and starts the next run as usual.

const (
	// historyGapMargin is added to restart_wait_sec to give the time after
	// which a run that starts later records a gap event.
	historyGapMargin = 60 * time.Second

	// historyUptimeMargin is added to restart_wait_sec to give the longest gap
	// between two runs that counts as active time. The restart is by design
	// there: the pause and the queue delay are not a stop. Runs of a service
	// with restart_wait_sec of 1 start up to 5 s apart, so 10 s covers a
	// queue delay twice as long as the longest delay measured.
	historyUptimeMargin = 10 * time.Second

	// historyOpenRetry is the delay before the scheduler opens again a store
	// that failed. The runs that ended meanwhile are imported at that time.
	historyOpenRetry = 30 * time.Second

	// historySeenRuns is how many ended runs of a DAG the history remembers, to
	// not record a run twice. It is more than restartRecentRuns.
	historySeenRuns = 64

	// historyMessageBytes is the longest message of a sample.
	historyMessageBytes = 256

	// historyMaxEventBytes bounds the events file of a history.
	historyMaxEventBytes = 4 << 20
)

// historyImportLimits are the counts of retained runs that an import reads in
// turn, until a run is not newer than the newest sample.
var historyImportLimits = []int{100, 1000, 10000}

// ServiceHistoryConfig configures a ServiceHistory.
type ServiceHistoryConfig struct {
	// Dir returns the directory of the run history of a DAG. It reports false
	// when the store keeps no run history.
	Dir func(dagName string) (string, bool)
	// RecentRuns returns the retained runs of a DAG, newest first.
	RecentRuns func(ctx context.Context, dagName string, limit int) ([]ir.DAGRunStatus, error)
	// Open opens the store of a directory for writing.
	Open func(ctx context.Context, dir string, o samples.Options) (samples.Recorder, error)
	// NewestEnd returns the end of the newest sample of a store.
	NewestEnd func(dir string) (time.Time, bool, error)
	// Clock returns the current time.
	Clock Clock
}

// ServiceHistory records the run samples of the service DAGs. One goroutine
// at a time can use it. A nil ServiceHistory records nothing.
type ServiceHistory struct {
	cfg  ServiceHistoryConfig
	dags map[string]*dagHistory
}

// runKey identifies one attempt of a run.
type runKey struct {
	runID     string
	attemptID string
}

// dagHistory is the state of the history of one DAG.
type dagHistory struct {
	// rec is nil while the store is closed or failed.
	rec samples.Recorder

	// seen holds the last historySeenRuns recorded or imported runs. ring gives
	// the order in which they leave the set.
	seen map[runKey]struct{}
	ring []runKey
	next int

	// prevKnown says whether the history knows if the last recorded run
	// succeeded. prevOK is the answer. The history asks for a gap event only
	// after a run that succeeded, or when it does not know: the scanner makes a
	// run that does not succeed wait longer.
	prevKnown, prevOK bool

	retryAt time.Time
	lastLog time.Time
	lastErr error
}

// NewServiceHistory creates a ServiceHistory.
func NewServiceHistory(cfg ServiceHistoryConfig) *ServiceHistory {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &ServiceHistory{cfg: cfg, dags: make(map[string]*dagHistory)}
}

// Observe records each run that ended and that the history did not record
// yet, the oldest end first. A retry of a run is one more run: it has its own
// attempt ID. The first call for a DAG opens its store and imports the
// retained runs. A store error is logged and returned; the next call tries
// again, after historyOpenRetry for a store that did not open.
func (h *ServiceHistory) Observe(ctx context.Context, dag *ir.DAG, runs ...ir.DAGRunStatus) error {
	if h == nil || dag == nil {
		return nil
	}
	dir, ok := h.cfg.Dir(dag.Name)
	if !ok {
		return nil
	}
	d := h.dags[dag.Name]
	if d == nil {
		d = &dagHistory{seen: make(map[runKey]struct{}, historySeenRuns)}
		h.dags[dag.Name] = d
	}
	if d.rec == nil {
		now := h.cfg.Clock()
		if now.Before(d.retryAt) {
			return d.lastErr
		}
		if err := h.openAndImport(ctx, dag, dir, d); err != nil {
			return h.fail(ctx, dag.Name, d, err, now)
		}
	}
	for _, run := range h.pending(d, runs) {
		if err := h.record(ctx, dag, d, &run); err != nil {
			return h.fail(ctx, dag.Name, d, err, h.cfg.Clock())
		}
	}
	return nil
}

// Forget closes the stores of the DAGs that are not in keep.
func (h *ServiceHistory) Forget(keep map[string]struct{}) {
	if h == nil {
		return
	}
	for name, d := range h.dags {
		if _, ok := keep[name]; !ok {
			h.closeDAG(context.Background(), name, d)
			delete(h.dags, name)
		}
	}
}

// Close closes all the stores.
func (h *ServiceHistory) Close(ctx context.Context) {
	if h == nil {
		return
	}
	for name, d := range h.dags {
		h.closeDAG(context.WithoutCancel(ctx), name, d)
	}
	clear(h.dags)
}

func (h *ServiceHistory) closeDAG(ctx context.Context, name string, d *dagHistory) {
	if d.rec == nil {
		return
	}
	if err := d.rec.Close(ctx); err != nil {
		logger.Warn(ctx, "Failed to close the run history", tag.DAG(name), tag.Error(err))
	}
	d.rec = nil
}

// fail closes the store of a DAG, so that the next call opens it again and
// imports the runs that it missed. It logs the error at most once in
// restartErrorLogInterval.
func (h *ServiceHistory) fail(ctx context.Context, name string, d *dagHistory, err error, now time.Time) error {
	h.closeDAG(ctx, name, d)
	d.retryAt, d.lastErr = now.Add(historyOpenRetry), err
	if ctx.Err() != nil || (!d.lastLog.IsZero() && now.Sub(d.lastLog) < restartErrorLogInterval) {
		return err
	}
	d.lastLog = now
	logger.Warn(ctx, "Run history is not recorded", tag.DAG(name), tag.Error(err))
	return err
}

// openAndImport opens the store and records the retained runs that ended after
// its newest sample.
func (h *ServiceHistory) openAndImport(ctx context.Context, dag *ir.DAG, dir string, d *dagHistory) error {
	rec, err := h.cfg.Open(ctx, dir, samples.Options{
		NoLifecycleEvents: true,
		WriteEach:         true,
		Uptime:            true,
		MaxEventBytes:     historyMaxEventBytes,
	})
	if err != nil {
		return err
	}
	d.rec = rec
	clear(d.seen)
	d.ring, d.next = d.ring[:0], 0
	d.prevKnown = false
	newest, hasNewest, err := h.cfg.NewestEnd(dir)
	if err != nil {
		return err
	}

	runs, err := h.retainedRuns(ctx, dag.Name, newest, hasNewest)
	if err != nil {
		return err
	}
	// The runs that the store already holds are marked as seen, oldest first,
	// so that the newest ones stay in the set.
	var imported []ir.DAGRunStatus
	var stored *ir.DAGRunStatus // the run of the store that ended last
	for i := len(runs) - 1; i >= 0; i-- {
		if !runEnded(runs[i].Status.String()) {
			continue
		}
		d.mark(runKeyOf(&runs[i]))
		end := h.runEnd(&runs[i])
		if !hasNewest || end.After(newest) {
			imported = append(imported, runs[i])
		} else if stored == nil || !end.Before(h.runEnd(stored)) {
			stored = &runs[i]
		}
	}
	if stored != nil {
		d.prevKnown, d.prevOK = true, stored.Status.IsSuccess()
	}
	for _, run := range sortedByEnd(h, imported) {
		if err := h.record(ctx, dag, d, &run); err != nil {
			return err
		}
	}
	if len(imported) > 0 {
		logger.Info(ctx, "Imported retained runs into the run history",
			tag.DAG(dag.Name), tag.Count(len(imported)))
	}
	return nil
}

// retainedRuns reads the retained runs, newest first, with a growing limit
// until a run ended at or before newest, or until no more runs exist. An empty
// store reads the largest count at once.
func (h *ServiceHistory) retainedRuns(ctx context.Context, name string, newest time.Time, hasNewest bool) ([]ir.DAGRunStatus, error) {
	limits := historyImportLimits
	if !hasNewest {
		// An empty store takes all the retained runs.
		limits = limits[len(limits)-1:]
	}
	var runs []ir.DAGRunStatus
	for _, limit := range limits {
		var err error
		runs, err = h.cfg.RecentRuns(ctx, name, limit)
		if err != nil {
			return nil, err
		}
		if len(runs) < limit {
			break
		}
		covered := slices.ContainsFunc(runs, func(run ir.DAGRunStatus) bool {
			return runEnded(run.Status.String()) && !h.runEnd(&run).After(newest)
		})
		if covered {
			break
		}
	}
	return runs, nil
}

// pending returns the runs that ended and that are not recorded, the oldest
// end first.
func (h *ServiceHistory) pending(d *dagHistory, runs []ir.DAGRunStatus) []ir.DAGRunStatus {
	var out []ir.DAGRunStatus
	for i := range runs {
		if !runEnded(runs[i].Status.String()) {
			continue
		}
		key := runKeyOf(&runs[i])
		if _, ok := d.seen[key]; ok {
			continue
		}
		if slices.ContainsFunc(out, func(run ir.DAGRunStatus) bool { return runKeyOf(&run) == key }) {
			continue
		}
		out = append(out, runs[i])
	}
	return sortedByEnd(h, out)
}

func sortedByEnd(h *ServiceHistory, runs []ir.DAGRunStatus) []ir.DAGRunStatus {
	slices.SortStableFunc(runs, func(a, b ir.DAGRunStatus) int {
		return h.runEnd(&a).Compare(h.runEnd(&b))
	})
	return runs
}

func (h *ServiceHistory) record(ctx context.Context, dag *ir.DAG, d *dagHistory, run *ir.DAGRunStatus) error {
	s := h.sampleOf(dag, run)
	if d.prevKnown && !d.prevOK {
		s.GapAfter = 0
	}
	if err := d.rec.Record(ctx, s); err != nil {
		return err
	}
	d.prevKnown, d.prevOK = true, run.Status.IsSuccess()
	d.mark(runKeyOf(run))
	return nil
}

func runKeyOf(run *ir.DAGRunStatus) runKey {
	return runKey{runID: run.DAGRunID, attemptID: run.AttemptID}
}

// mark remembers a run. The oldest run leaves the set after historySeenRuns
// runs.
func (d *dagHistory) mark(key runKey) {
	if _, ok := d.seen[key]; ok {
		return
	}
	if len(d.ring) < historySeenRuns {
		d.ring = append(d.ring, key)
	} else {
		delete(d.seen, d.ring[d.next])
		d.ring[d.next] = key
		d.next = (d.next + 1) % historySeenRuns
	}
	d.seen[key] = struct{}{}
}

// runEnd returns the end of a run. A run with no end time takes the time of
// its creation, its start, or else now.
func (h *ServiceHistory) runEnd(run *ir.DAGRunStatus) time.Time {
	if t := parseHistoryTime(run.FinishedAt); !t.IsZero() {
		return t
	}
	if run.CreatedAt > 0 {
		return time.UnixMilli(run.CreatedAt).UTC()
	}
	if t := parseHistoryTime(run.StartedAt); !t.IsZero() {
		return t
	}
	return h.cfg.Clock().UTC().Truncate(time.Millisecond)
}

// parseHistoryTime reads a time of a run status, with millisecond precision. An
// empty or invalid text gives the zero time.
func parseHistoryTime(text string) time.Time {
	t, err := stringutil.ParseTime(text)
	if err != nil || t.IsZero() {
		return time.Time{}
	}
	return t.UTC().Truncate(time.Millisecond)
}

// sampleOf maps an ended run to its sample.
func (h *ServiceHistory) sampleOf(dag *ir.DAG, run *ir.DAGRunStatus) samples.Sample {
	end := h.runEnd(run)
	s := samples.Sample{
		Time:     end,
		RunID:    run.DAGRunID,
		GapAfter: dag.RestartWait + historyGapMargin,
		// A restart that is on time is active time, also after a run that failed.
		ActiveGap: dag.RestartWait + historyUptimeMargin,
	}
	if started := parseHistoryTime(run.StartedAt); !started.IsZero() && !started.After(end) {
		s.Time, s.Duration = started, end.Sub(started)
	}
	if run.Status == ir.Failed {
		s.ExitCode = 1
		s.Message = cutHistoryMessage(runErrorText(run))
	}
	return s
}

// runErrorText returns the error of a run, else the error of its first failed
// step.
func runErrorText(run *ir.DAGRunStatus) string {
	if text := strings.TrimSpace(run.Error); text != "" {
		return text
	}
	for _, node := range run.NodesInRunOrder() {
		if node.Status == ir.NodeFailed {
			if text := strings.TrimSpace(node.Error); text != "" {
				return text
			}
		}
	}
	return ""
}

// cutHistoryMessage cuts a message to historyMessageBytes, at a character
// boundary.
func cutHistoryMessage(msg string) string {
	if len(msg) <= historyMessageBytes {
		return msg
	}
	return strings.ToValidUTF8(msg[:historyMessageBytes], "")
}
