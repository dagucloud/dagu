// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

const (
	// maxActiveRuns is how many runs in progress a report lists.
	maxActiveRuns = 200
	// activeLookback is how far back a rebuild looks for runs in progress.
	activeLookback = 30 * 24 * time.Hour
	// rebuildRetryInterval is how long a failed rebuild waits to try again.
	rebuildRetryInterval = time.Minute
)

// activeStatuses are the statuses of runs in progress.
var activeStatuses = []ir.Status{ir.Queued, ir.Running, ir.Waiting}

// RunStatuses lists the statuses of top-level DAG runs.
type RunStatuses interface {
	ListStatuses(ctx context.Context, options persis.DAGRunListOptions) ([]*ir.DAGRunStatus, error)
}

// reportTime is an RFC 3339 time in a report; it encodes as null when the
// time is unknown.
type reportTime string

func (t reportTime) MarshalJSON() ([]byte, error) {
	if t == "" {
		return []byte("null"), nil
	}
	return json.Marshal(string(t))
}

// activeRun is a top-level DAG-run attempt in progress: queued, running, or
// waiting.
type activeRun struct {
	Workflow    string     `json:"workflow"`
	DAGRunID    string     `json:"dag_run_id"`
	AttemptID   string     `json:"attempt_id"`
	Status      string     `json:"status"`
	TriggerType string     `json:"trigger_type"`
	QueuedAt    reportTime `json:"queued_at"`
	StartedAt   reportTime `json:"started_at"`
}

func newActiveRun(status *ir.DAGRunStatus) activeRun {
	return activeRun{
		Workflow:    status.Name,
		DAGRunID:    status.DAGRunID,
		AttemptID:   status.AttemptID,
		Status:      status.Status.String(),
		TriggerType: triggerType(status.TriggerType),
		QueuedAt:    reportTime(snapshotTime(status.QueuedAt)),
		StartedAt:   reportTime(snapshotTime(status.StartedAt)),
	}
}

// key identifies the DAG run. A run has one attempt in progress at a time.
func (r activeRun) key() string {
	return r.Workflow + "\x00" + r.DAGRunID
}

// since is when the run started, or was queued if it has not started.
func (r activeRun) since() reportTime {
	if r.StartedAt != "" {
		return r.StartedAt
	}
	return r.QueuedAt
}

// triggerType names how a run was started, or returns "" when it is unknown.
func triggerType(t ir.TriggerType) string {
	if t == ir.TriggerTypeUnknown {
		return ""
	}
	return t.String()
}

// reportable reports whether status belongs to a top-level run whose names
// and IDs Dagu Console keeps.
func reportable(status *ir.DAGRunStatus) bool {
	return status.Parent.Zero() && len(status.Name) <= maxNameBytes &&
		len(status.DAGRunID) <= maxIDBytes && len(status.AttemptID) <= maxIDBytes
}

// runChange is how an event changes the runs in progress.
type runChange struct {
	run activeRun
	// ended reports that the attempt in run ended.
	ended bool
}

// activeSet holds the runs in progress by run key. A nil set is unknown. It
// encodes as a list, newest first.
type activeSet map[string]activeRun

// activeSetOf collects the reportable runs in progress among statuses.
func activeSetOf(statuses []*ir.DAGRunStatus) activeSet {
	set := make(activeSet, len(statuses))
	for _, status := range statuses {
		if status.Status.IsActive() && reportable(status) {
			set.apply(runChange{run: newActiveRun(status)})
		}
	}
	return set
}

// apply records c. An attempt that ended leaves the set unless a later
// attempt of its run replaced it.
func (s activeSet) apply(c runChange) {
	key := c.run.key()
	if !c.ended {
		s[key] = c.run
		return
	}
	if current, ok := s[key]; ok && current.AttemptID == c.run.AttemptID {
		delete(s, key)
	}
}

// with returns a copy of s with changes applied. An unknown set stays
// unknown.
func (s activeSet) with(changes []runChange) activeSet {
	next := maps.Clone(s)
	if next == nil {
		return nil
	}
	for _, c := range changes {
		next.apply(c)
	}
	return next
}

// list returns the runs that started or were queued most recently first.
func (s activeSet) list() []activeRun {
	runs := make([]activeRun, 0, len(s))
	for _, run := range s {
		runs = append(runs, run)
	}
	slices.SortFunc(runs, func(a, b activeRun) int {
		return cmp.Or(
			cmp.Compare(b.since(), a.since()),
			cmp.Compare(a.Workflow, b.Workflow),
			cmp.Compare(a.DAGRunID, b.DAGRunID),
		)
	})
	return runs
}

func (s activeSet) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.list())
}

func (s *activeSet) UnmarshalJSON(data []byte) error {
	var runs []activeRun
	if err := json.Unmarshal(data, &runs); err != nil {
		return err
	}
	if runs == nil {
		*s = nil
		return nil
	}
	set := make(activeSet, len(runs))
	for _, run := range runs {
		set[run.key()] = run
	}
	*s = set
	return nil
}

// activeReport is what a report says about the runs in progress.
type activeReport struct {
	// known reports that the runs in progress are known; a report leaves
	// them out otherwise.
	known bool
	// runs are the first maxActiveRuns of them, and total counts them all.
	runs  []activeRun
	total int
}

func newActiveReport(set activeSet) activeReport {
	if set == nil {
		return activeReport{}
	}
	runs := set.list()
	return activeReport{known: true, runs: runs[:min(len(runs), maxActiveRuns)], total: len(runs)}
}

func (r activeReport) equal(other activeReport) bool {
	return r.known == other.known && r.total == other.total && slices.Equal(r.runs, other.runs)
}

// rebuild lists the runs in progress in the background.
type rebuild struct {
	cancel context.CancelFunc
	// done closes once runs and err are set.
	done chan struct{}
	runs activeSet
	err  error
	// pending holds the changes that acknowledged reports applied since the
	// listing began, in order, to apply on top of what it found.
	pending []runChange
}
