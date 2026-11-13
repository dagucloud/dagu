// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// recentRunsForEstimate is how many of a DAG's latest successful runs the
// estimate of a plan is taken from.
const recentRunsForEstimate = 20

// Outlook is how a plan would run on the DAG's queue, judged from the DAG's
// recent runs.
type Outlook struct {
	// MaxConcurrency is how many runs at once the backfill asks for.
	MaxConcurrency int
	// Concurrency is how many runs of the backfill run at once: the lower
	// of MaxConcurrency and the queue's limit.
	Concurrency int
	// QueueName is the DAG's queue, and QueueLimit how many of its runs
	// run at once.
	QueueName  string
	QueueLimit int
	// RunDuration is the median duration of the DAG's recent successful
	// runs, or zero when it has none.
	RunDuration time.Duration
}

// LoadOutlook reads the outlook of a backfill of dag that runs up to
// maxConcurrency runs at once on a queue that runs queueLimit.
func LoadOutlook(ctx context.Context, reader HistoryReader, dag *ir.DAG, maxConcurrency, queueLimit int) (Outlook, error) {
	statuses, err := reader.ListStatuses(ctx, persis.DAGRunListOptions{
		ExactName: dag.Name,
		Statuses:  []ir.Status{ir.Succeeded},
		Limit:     recentRunsForEstimate,
	})
	if err != nil {
		return Outlook{}, err
	}
	maxConcurrency, queueLimit = max(maxConcurrency, 1), max(queueLimit, 1)
	return Outlook{
		MaxConcurrency: maxConcurrency,
		Concurrency:    min(maxConcurrency, queueLimit),
		QueueName:      dag.ProcGroup(),
		QueueLimit:     queueLimit,
		RunDuration:    medianDuration(statuses),
	}, nil
}

// Estimate returns how long runs runs of the backfill take, or zero when
// the DAG has no recent successful run.
func (o Outlook) Estimate(runs int) time.Duration {
	if o.RunDuration <= 0 || runs <= 0 {
		return 0
	}
	concurrency := max(o.Concurrency, 1)
	rounds := (runs + concurrency - 1) / concurrency
	return time.Duration(rounds) * o.RunDuration
}

// FillsQueue reports whether the backfill can take every place in the
// queue, so a live run may wait for an executing backfill run.
func (o Outlook) FillsQueue() bool {
	return o.QueueLimit > 0 && o.Concurrency >= o.QueueLimit
}

// roughDuration formats a duration to the minute, at least one, as "5m" or
// "2h20m".
func roughDuration(d time.Duration) string {
	d = max(d.Round(time.Minute), time.Minute)
	return strings.TrimSuffix(d.String(), "0s")
}

func medianDuration(statuses []*ir.DAGRunStatus) time.Duration {
	var durations []time.Duration
	for _, status := range statuses {
		started, err := stringutil.ParseTime(status.StartedAt)
		if err != nil || started.IsZero() {
			continue
		}
		finished, err := stringutil.ParseTime(status.FinishedAt)
		if err != nil || finished.Before(started) {
			continue
		}
		durations = append(durations, finished.Sub(started))
	}
	if len(durations) == 0 {
		return 0
	}
	slices.Sort(durations)
	mid := len(durations) / 2
	if len(durations)%2 == 0 {
		return (durations[mid-1] + durations[mid]) / 2
	}
	return durations[mid]
}
