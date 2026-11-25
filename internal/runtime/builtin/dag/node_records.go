// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/noderuns"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// The records of a step with nodes are observability only: a failed write
// is logged and never fails the step.

// nodeRecordInterval bounds how often the records of a running step with
// nodes are rewritten.
const nodeRecordInterval = time.Second

// recordNodeRuns records the runs of a step with nodes before any node
// starts, keeps the records current while the step runs, and returns the
// function that records them once more when the step ends. It records nothing
// for a parallel step, or for a step that has no log file.
func (e *parallelExecutor) recordNodeRuns(ctx context.Context) func() {
	if e.nodes == nil {
		return func() {}
	}
	dir := nodeRunsDir(ctx, e.step.Name)
	if dir == "" {
		return func() {}
	}
	e.lock.Lock()
	e.nodes.changed = true
	e.lock.Unlock()
	e.writeNodeRuns(ctx, dir, false)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(nodeRecordInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				e.writeNodeRuns(ctx, dir, false)
			case <-stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return func() {
		close(stop)
		<-done
		e.writeNodeRuns(ctx, dir, true)
	}
}

// nodeRunsDir returns the directory of a step's records, beside its log
// files, or "" when the step has none.
func nodeRunsDir(ctx context.Context, stepName string) string {
	scope := runtime.GetEnv(ctx).Scope
	if scope == nil {
		return ""
	}
	stdout, _ := scope.Get(runenv.EnvKeyDAGRunStepStdoutFile)
	return noderuns.Dir(stdout, stepName)
}

// writeNodeRuns writes the records and their counts when they changed since
// the last write, and always once the step has ended. While the step runs, it
// reports counts that changed to the runner.
func (e *parallelExecutor) writeNodeRuns(ctx context.Context, dir string, ended bool) {
	e.lock.Lock()
	if !e.nodes.changed && !ended {
		e.lock.Unlock()
		return
	}
	e.nodes.changed = false
	runs := e.nodeRuns(ended)
	e.lock.Unlock()

	counts, err := noderuns.Write(dir, runs)
	if err != nil {
		logger.Warn(ctx, "Failed to write node run records", tag.Dir(dir), tag.Error(err))
		// The next write tries again, and reports the counts once they are
		// stored.
		e.lock.Lock()
		e.nodes.changed = true
		e.lock.Unlock()
		return
	}
	// The status write at the end of the step follows the last records.
	if !ended {
		e.reportNodeCounts(counts)
	}
}

// SetProgressCallback implements executor.ProgressCallbackAware.
func (e *nodesExecutor) SetProgressCallback(callback func()) {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.nodes.progress = callback
}

// reportNodeCounts asks the runner to write the run status when the counts
// changed since the last report. Readers of the run refresh on a status
// write, and a child's own write can reach them before these records do, so
// a write after the records keeps them from missing the change.
func (e *parallelExecutor) reportNodeCounts(counts ir.NodeRunCounts) {
	e.lock.Lock()
	progress := e.nodes.progress
	changed := counts != e.nodes.reported
	e.nodes.reported = counts
	e.lock.Unlock()
	if changed && progress != nil {
		progress()
	}
}

// nodeRuns returns the records of the matched nodes in name order. Callers
// hold e.lock.
func (e *parallelExecutor) nodeRuns(ended bool) []ir.NodeRun {
	runs := make([]ir.NodeRun, 0, len(e.nodes.matched))
	for _, node := range e.nodes.matched {
		run := ir.NodeRun{Node: node.Node, Status: e.nodeRunStatus(node, ended)}
		if node.Run.RunID != "" {
			id := node.Run.RunID
			run.DAGName, run.DAGRunID = node.Run.DAGName, id
			run.StartedAt = stringutil.FormatTime(e.nodes.started[id])
			if run.Status != ir.NodeRunRunning && run.Status != ir.NodeRunNotStarted {
				run.FinishedAt = stringutil.FormatTime(e.nodes.finished[id])
			}
			if result, ok := e.results[id]; ok && run.Status != ir.NodeRunRunning {
				run.Steps, run.FailedStep = result.Steps, result.FailedStep
				// A child run that a retry reused returns at once, so the
				// child's own times tell when it ran.
				if result.StartedAt != "" {
					run.StartedAt, run.FinishedAt = result.StartedAt, result.FinishedAt
				}
			}
		}
		runs = append(runs, run)
	}
	return runs
}
