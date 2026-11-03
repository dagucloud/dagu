// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/queue"
)

// Limits on the queues one report lists; queues_total counts them all.
const (
	maxQueues = 256
	// maxQueueNameLen is the longest queue name Dagu Console takes; a queue
	// with a longer name is left out.
	maxQueueNameLen = 128
	// queuesEvery is how long one read of the queues serves reports, so
	// reports sent early do not read them again.
	queuesEvery = time.Minute
	// maxQueueReasons bounds the queued runs one read opens to say why
	// they wait, and reasonAfter is how long the next run in line waits,
	// with room in its queue, before a read asks.
	maxQueueReasons = 50
	reasonAfter     = time.Minute
)

// Queues is what reports read of the server's queues. A nil Store leaves
// queues out of reports; a nil Procs, Leases, or Runs leaves out what it
// tells. A report whose read of the queues or of the runs in progress
// fails leaves the queues out, rather than send counts that are short.
type Queues struct {
	// Store sums up the queues that hold queued runs, and reads a queue's
	// next run in line.
	Store QueueStore
	// Procs lists the runs in progress on the server's own processes, by
	// queue, and Leases those on distributed workers.
	Procs  ProcLister
	Leases LeaseLister
	// Runs opens a queued run's attempt, to read why it cannot start.
	Runs RunFinder
	// Capacity is how many runs of the queue may run at once; a queue runs
	// at least one, as the scheduler does.
	Capacity func(queue string) int
}

// QueueStore sums up the queues that hold queued runs and reads a queued
// run by its item ID.
type QueueStore interface {
	queue.Summarizer
	GetByItemID(ctx context.Context, name, itemID string) (queue.QueuedItemData, error)
}

// ProcLister lists the runs in progress on the server's own processes, by
// queue.
type ProcLister interface {
	ListAllAlive(ctx context.Context) (map[string][]ir.DAGRunRef, error)
}

// LeaseLister lists the leases of runs on distributed workers.
type LeaseLister interface {
	ListAll(ctx context.Context) ([]dispatch.DAGRunLease, error)
}

// RunFinder finds a run's latest attempt.
type RunFinder interface {
	FindAttempt(ctx context.Context, ref ir.DAGRunRef) (dagrun.Attempt, error)
}

// queueHealth is a queue in a report's health: how many runs wait and run,
// how many may run at once, when the next run in line was queued, and,
// when the server knows, why it cannot start.
type queueHealth struct {
	Name          string `json:"name"`
	Queued        int    `json:"queued"`
	Running       int    `json:"running"`
	MaxActive     int    `json:"max_active"`
	NextQueuedAt  string `json:"next_queued_at,omitempty"`
	BlockedReason string `json:"blocked_reason,omitempty"`

	// next is the next run in line, by its item ID and when it was queued.
	next   string
	nextAt time.Time
}

// queueRead is one read of the queues, when it was made, and what it asked
// of why queues' next runs wait.
type queueRead struct {
	queues []queueHealth
	total  int
	at     time.Time
	checks reasonChecks
}

// reasonChecks are, by queue, why its next run waited when a read last
// asked, when it asked, and that run's item ID.
type reasonChecks map[string]reasonCheck

type reasonCheck struct {
	reason string
	at     time.Time
	next   string
}

// read lists the queues that hold queued or running runs as of now, up to
// maxQueues, in the order queueRank gives, and counts them all. checks are
// what earlier reads asked of why queues' next runs wait. It returns false
// when the queues cannot be read.
func (q Queues) read(ctx context.Context, now time.Time, checks reasonChecks) (queueRead, bool) {
	summaries, err := q.Store.Summaries(ctx)
	if err != nil {
		logger.Debug(ctx, "Failed to read the queues for the server report", tag.Error(err))
		return queueRead{}, false
	}
	running, ok := q.running(ctx, now)
	if !ok {
		return queueRead{}, false
	}
	byName := make(map[string]*queueHealth, len(summaries)+len(running))
	add := func(name string) *queueHealth {
		h, ok := byName[name]
		if !ok {
			h = &queueHealth{Name: name, MaxActive: q.capacity(name)}
			byName[name] = h
		}
		return h
	}
	for _, s := range summaries {
		h := add(s.Name)
		h.Queued, h.next, h.nextAt = s.Queued, s.Next, s.NextQueuedAt
		if !s.NextQueuedAt.IsZero() {
			h.NextQueuedAt = formatTime(s.NextQueuedAt)
		}
	}
	for name, n := range running {
		add(name).Running = n
	}

	queues := make([]queueHealth, 0, len(byName))
	for name, h := range byName {
		if name != "" && len(name) <= maxQueueNameLen {
			queues = append(queues, *h)
		}
	}
	checks = q.explain(ctx, queues, now, checks)
	slices.SortFunc(queues, func(a, b queueHealth) int {
		return cmp.Or(
			cmp.Compare(queueRank(a), queueRank(b)),
			compareWaiting(a.nextAt, b.nextAt),
			cmp.Compare(a.Name, b.Name),
		)
	})
	total := len(queues)
	return queueRead{queues: queues[:min(total, maxQueues)], total: total, at: now, checks: checks}, true
}

// running counts the runs in progress on the server's processes and on
// distributed workers, by queue, each run once. It returns false when
// either cannot be read.
func (q Queues) running(ctx context.Context, now time.Time) (map[string]int, bool) {
	counts := make(map[string]int)
	counted := make(map[ir.DAGRunRef]struct{})
	if q.Procs != nil {
		alive, err := q.Procs.ListAllAlive(ctx)
		if err != nil {
			logger.Debug(ctx, "Failed to read the runs in progress for the server report", tag.Error(err))
			return nil, false
		}
		for name, runs := range alive {
			counts[name] += len(runs)
			for _, run := range runs {
				counted[run] = struct{}{}
			}
		}
	}
	if q.Leases != nil {
		leases, err := q.Leases.ListAll(ctx)
		if err != nil {
			logger.Debug(ctx, "Failed to read the distributed runs for the server report", tag.Error(err))
			return nil, false
		}
		// Leases are kept per attempt, so a run can hold more than one.
		for _, lease := range leases {
			if _, seen := counted[lease.DAGRun]; seen || !lease.IsFresh(now, dagrun.DefaultStaleLeaseThreshold) {
				continue
			}
			counted[lease.DAGRun] = struct{}{}
			counts[cmp.Or(lease.QueueName, lease.DAGRun.Name)]++
		}
	}
	return counts, true
}

// explain says why the next run cannot start, of each queue with room
// whose next run waited reasonAfter. It asks of at most maxQueueReasons of
// them, those an earlier read asked of longest ago first, then the longest
// waits, then by name, so with more of them each is asked in turn; the
// others keep the answer an earlier read got of the same next run. A queue
// whose next run changed counts as not asked. It returns what it and
// earlier reads asked of the queues still waiting.
func (q Queues) explain(ctx context.Context, queues []queueHealth, now time.Time, prev reasonChecks) reasonChecks {
	checks := make(reasonChecks)
	if q.Runs == nil {
		return checks
	}
	var waiting []*queueHealth
	for i := range queues {
		h := &queues[i]
		if h.next != "" && h.Running < h.MaxActive && now.Sub(h.nextAt) >= reasonAfter {
			waiting = append(waiting, h)
		}
	}
	asked := func(h *queueHealth) (reasonCheck, bool) {
		c, ok := prev[h.Name]
		return c, ok && c.next == h.next
	}
	askedAt := func(h *queueHealth) time.Time {
		if c, ok := asked(h); ok {
			return c.at
		}
		return time.Time{}
	}
	slices.SortFunc(waiting, func(a, b *queueHealth) int {
		return cmp.Or(askedAt(a).Compare(askedAt(b)), a.nextAt.Compare(b.nextAt), cmp.Compare(a.Name, b.Name))
	})
	for i, h := range waiting {
		if i < maxQueueReasons {
			h.BlockedReason = q.blockedReason(ctx, h)
			checks[h.Name] = reasonCheck{reason: h.BlockedReason, at: now, next: h.next}
		} else if c, ok := asked(h); ok {
			h.BlockedReason = c.reason
			checks[h.Name] = c
		}
	}
	return checks
}

// blockedReason is why the queue's next run cannot start for want of a
// worker, or "" when its status says nothing of it or cannot be read.
func (q Queues) blockedReason(ctx context.Context, h *queueHealth) string {
	reason, err := q.readBlockedReason(ctx, h)
	if err != nil {
		logger.Debug(ctx, "Failed to read why a queue's next run waits for the server report", tag.Queue(h.Name), tag.Error(err))
	}
	return reason
}

// readBlockedReason reads the queue's next run as summed up, so the answer
// is about the run its check names.
func (q Queues) readBlockedReason(ctx context.Context, h *queueHealth) (string, error) {
	item, err := q.Store.GetByItemID(ctx, h.Name, h.next)
	if err != nil {
		return "", err
	}
	ref, err := item.Data()
	if err != nil || ref == nil {
		return "", err
	}
	attempt, err := q.Runs.FindAttempt(ctx, *ref)
	if err != nil {
		return "", err
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil || status == nil {
		return "", err
	}
	return workerBlockReason(status.Conditions), nil
}

// workerBlockReason is the reason a run's conditions give for why it cannot
// start for want of a worker, or "".
func workerBlockReason(conditions []ir.DAGRunCondition) string {
	for _, c := range conditions {
		if c.Type != ir.DAGRunConditionRunnable || c.Status == ir.DAGRunConditionStatusTrue {
			continue
		}
		switch c.Reason {
		case ir.DAGRunReasonNoMatchingWorker, ir.DAGRunReasonNoAvailableWorker, ir.DAGRunReasonWorkerDispatchUnavailable:
			return c.Reason
		}
	}
	return ""
}

func (q Queues) capacity(name string) int {
	if q.Capacity == nil {
		return 1
	}
	return max(q.Capacity(name), 1)
}

// queueRank orders the queues a report lists: those whose next run waits
// for a worker first, then those with room whose next run has not started,
// then those whose runs fill them, then those with nothing queued. A full
// queue's next run waits for its own runs, so it is busy, not stuck.
func queueRank(h queueHealth) int {
	switch {
	case h.BlockedReason != "":
		return 0
	case h.Queued > 0 && h.Running < h.MaxActive:
		return 1
	case h.Queued > 0:
		return 2
	}
	return 3
}

// compareWaiting orders the earlier queued first, and one with nothing
// queued last.
func compareWaiting(a, b time.Time) int {
	switch {
	case a.IsZero() && b.IsZero():
		return 0
	case a.IsZero():
		return 1
	case b.IsZero():
		return -1
	}
	return a.Compare(b)
}
