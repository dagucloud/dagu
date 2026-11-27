// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

const (
	// maxRunSteps is the most steps a run carries.
	maxRunSteps = eventstore.MaxSnapshotDependencySteps
	// maxStepsBytes bounds a run's encoded steps, escapes included, so a
	// finished run always fits a report alone.
	maxStepsBytes = 32 << 10
	// maxActiveStepsBytes bounds the encoded steps of a report's runs in
	// progress; the runs past it carry none in that report.
	maxActiveStepsBytes = 512 << 10
	// activeStepsEvery is how long the steps read for a run in progress
	// serve the reports that follow, which can go out every few seconds.
	activeStepsEvery = time.Minute
)

// reportedSteps are a run's steps as a report carries them: its first
// regular steps in run order and how many it has in all, and its handlers
// by kind.
type reportedSteps struct {
	Steps      []reportedStep     `json:"steps,omitempty"`
	Handlers   map[string]stepRun `json:"handlers,omitempty"`
	StepsTotal int                `json:"steps_total,omitempty"`
}

// reportedStep is a regular step of a run, as a report carries it.
type reportedStep struct {
	Name string `json:"name"`
	stepRun
	// Depends are the positions, among the run's reported steps, of the
	// steps this one waits for.
	Depends []int `json:"depends,omitempty"`
}

// stepRun is how a step ran, as a report carries it.
type stepRun struct {
	Status string `json:"status"`
	// Start is when the step started, in whole seconds from its run's
	// start; nil when either has no time.
	Start *int64 `json:"start,omitempty"`
	// Took is how many whole seconds the step ran; nil until it finished.
	Took    *int64 `json:"took,omitempty"`
	Retries int    `json:"retries,omitempty"`
}

// runSteps returns the steps of status as a report carries them: the first
// maxRunSteps named regular steps in run order that fit in maxStepsBytes
// encoded with the run's handlers, how many named regular steps there are
// in all, and the handlers. Each step names the steps it waits for by their
// positions among those returned.
func runSteps(status *ir.DAGRunStatus) reportedSteps {
	var nodes []*ir.Node
	positions := map[string]int{}
	total := 0
	for _, node := range status.Nodes {
		if node.Step.Name == "" {
			continue
		}
		if total < maxRunSteps {
			positions[node.Step.Name] = len(nodes)
			nodes = append(nodes, node)
		}
		total++
	}
	runStart, timed := snapshotSecond(status.StartedAt)
	reported := reportedSteps{StepsTotal: total, Steps: make([]reportedStep, 0, len(nodes))}
	for kind, node := range runHandlers(status) {
		if reported.Handlers == nil {
			reported.Handlers = map[string]stepRun{}
		}
		reported.Handlers[kind.String()] = newStepRun(node, runStart, timed)
	}
	for _, node := range nodes {
		reported.Steps = append(reported.Steps, reportedStep{
			Name:    truncateBytes(node.Step.Name, maxStepNameBytes),
			stepRun: newStepRun(node, runStart, timed),
			Depends: dependPositions(&node.Step, positions),
		})
	}
	if len(nodes) == 0 || encodedLen(reported) <= maxStepsBytes {
		return reported
	}

	// A larger prefix only adds steps and edges, so its encoded size cannot shrink.
	limit := sort.Search(len(reported.Steps), func(i int) bool {
		return encodedLen(reported.prefix(i+1)) > maxStepsBytes
	})
	return reported.prefix(limit)
}

// prefix keeps the first n steps and the edges between them.
func (reported reportedSteps) prefix(n int) reportedSteps {
	reported.Steps = slices.Clone(reported.Steps[:n])
	for i := range reported.Steps {
		depends := slices.Clone(reported.Steps[i].Depends)
		reported.Steps[i].Depends = slices.DeleteFunc(depends, func(p int) bool { return p >= n })
	}
	return reported
}

// runHandlers are the handlers status declares, by kind.
func runHandlers(status *ir.DAGRunStatus) map[ir.HandlerType]*ir.Node {
	handlers := map[ir.HandlerType]*ir.Node{}
	for kind, node := range map[ir.HandlerType]*ir.Node{
		ir.HandlerOnInit:    status.OnInit,
		ir.HandlerOnWait:    status.OnWait,
		ir.HandlerOnSuccess: status.OnSuccess,
		ir.HandlerOnFailure: status.OnFailure,
		ir.HandlerOnAbort:   status.OnAbort,
		ir.HandlerOnExit:    status.OnExit,
	} {
		if node != nil {
			handlers[kind] = node
		}
	}
	return handlers
}

// newStepRun is how node ran, its times counted from runStart when timed.
func newStepRun(node *ir.Node, runStart time.Time, timed bool) stepRun {
	run := stepRun{Status: node.Status.String(), Retries: node.RetryCount}
	started, ok := snapshotSecond(node.StartedAt)
	if !timed || !ok {
		return run
	}
	run.Start = wholeSeconds(started.Sub(runStart))
	if finished, ok := snapshotSecond(node.FinishedAt); ok {
		run.Took = wholeSeconds(max(finished.Sub(started), 0))
	}
	return run
}

// wholeSeconds is d in whole seconds.
func wholeSeconds(d time.Duration) *int64 {
	s := int64(d / time.Second)
	return &s
}

// dependPositions are the positions of the steps step waits for, explicit
// and inferred alike, given the position of each step by name.
func dependPositions(step *ir.Step, positions map[string]int) []int {
	var out []int
	var seen [maxRunSteps]bool
	for _, name := range step.AllDepends() {
		if p, ok := positions[name]; ok && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// activeEntry is a run in progress as a report carries it: the run, and
// its steps when they were read.
type activeEntry struct {
	activeRun
	reportedSteps
}

// readSteps are the steps last read for a run in progress, and their
// encoded size.
type readSteps struct {
	at    time.Time
	steps reportedSteps
	size  int
}

// stepReader reads the steps of the runs in progress, reusing a read for
// activeStepsEvery.
type stepReader struct {
	runs RunFinder
	// read holds the last read of each run listed, by the run as listed, so
	// a run whose status changed is read again.
	read map[activeRun]readSteps
}

// entries returns runs with the steps of each, within maxActiveStepsBytes
// in the order given. A run whose steps cannot be read, or whose latest
// attempt is another, carries none, as every run does without a RunFinder.
// Reads of runs no longer listed are forgotten.
func (s *stepReader) entries(ctx context.Context, runs []activeRun, now time.Time) []activeEntry {
	entries := make([]activeEntry, len(runs))
	listed := make(map[activeRun]readSteps, len(runs))
	budget := maxActiveStepsBytes
	for i, run := range runs {
		entries[i] = activeEntry{activeRun: run}
		if s.runs == nil {
			continue
		}
		read, ok := s.read[run]
		if !ok || now.Sub(read.at) >= activeStepsEvery {
			read = s.readRun(ctx, run, now)
		}
		listed[run] = read
		if read.size > budget || (len(read.steps.Steps) == 0 && len(read.steps.Handlers) == 0) {
			continue
		}
		budget -= read.size
		entries[i].reportedSteps = read.steps
	}
	s.read = listed
	return entries
}

// readRun reads the steps of run's attempt at now: none when its latest
// attempt is another or cannot be read.
func (s *stepReader) readRun(ctx context.Context, run activeRun, now time.Time) readSteps {
	read := readSteps{at: now}
	attempt, err := s.runs.FindAttempt(ctx, ir.NewDAGRunRef(run.Workflow, run.DAGRunID))
	if err != nil {
		logger.Debug(ctx, "Failed to find a run in progress for its steps in the server report", tag.DAG(run.Workflow), tag.RunID(run.DAGRunID), tag.Error(err))
		return read
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil || status == nil {
		logger.Debug(ctx, "Failed to read a run in progress for its steps in the server report", tag.DAG(run.Workflow), tag.RunID(run.DAGRunID), tag.Error(err))
		return read
	}
	if status.AttemptID != run.AttemptID {
		return read
	}
	read.steps = runSteps(status)
	read.size = encodedLen(read.steps)
	return read
}
