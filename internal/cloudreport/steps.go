// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

const (
	// maxRunSteps is the most steps a run carries.
	maxRunSteps = 200
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

// reportedStep is a step of a run, as a report carries it.
type reportedStep struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	Retries    int    `json:"retries"`
	// Depends are the positions, among the run's reported steps, of the
	// steps this one waits for.
	Depends []int `json:"depends,omitempty"`
	// Handler is the kind of handler the step is, such as onExit; empty for
	// a regular step.
	Handler string `json:"handler,omitempty"`
}

// runSteps returns the named steps of status in run order, handlers
// included: the first maxRunSteps that fit in maxStepsBytes encoded, and
// how many there are in all. Each step names the steps it waits for by
// their positions among those returned, and each handler its kind.
func runSteps(status *ir.DAGRunStatus) ([]reportedStep, int) {
	handlers := handlerKinds(status)
	var nodes []*ir.Node
	positions := map[string]int{}
	for _, node := range status.NodesInRunOrder() {
		if node.Step.Name == "" {
			continue
		}
		// Only regular steps are waited for, and a handler can share one's name.
		if _, ok := handlers[node]; !ok {
			positions[node.Step.Name] = len(nodes)
		}
		nodes = append(nodes, node)
	}
	var steps []reportedStep
	size := len("[]")
	for _, node := range nodes {
		step := reportedStep{
			Name:       truncateBytes(node.Step.Name, maxStepNameBytes),
			Status:     node.Status.String(),
			StartedAt:  snapshotTime(node.StartedAt),
			FinishedAt: snapshotTime(node.FinishedAt),
			Retries:    node.RetryCount,
			Depends:    dependPositions(&node.Step, positions),
			Handler:    handlers[node].String(),
		}
		n := encodedStepSize(step)
		if len(steps) == maxRunSteps || size+n > maxStepsBytes {
			break
		}
		steps = append(steps, step)
		size += n
	}
	// A step can wait for one past those returned, which no position names.
	for i := range steps {
		steps[i].Depends = slices.DeleteFunc(steps[i].Depends, func(p int) bool { return p >= len(steps) })
	}
	return steps, len(nodes)
}

// handlerKinds are the kinds of the handlers status declares, by node.
func handlerKinds(status *ir.DAGRunStatus) map[*ir.Node]ir.HandlerType {
	kinds := map[*ir.Node]ir.HandlerType{}
	for kind, node := range map[ir.HandlerType]*ir.Node{
		ir.HandlerOnInit:    status.OnInit,
		ir.HandlerOnWait:    status.OnWait,
		ir.HandlerOnSuccess: status.OnSuccess,
		ir.HandlerOnFailure: status.OnFailure,
		ir.HandlerOnAbort:   status.OnAbort,
		ir.HandlerOnExit:    status.OnExit,
	} {
		if node != nil {
			kinds[node] = kind
		}
	}
	return kinds
}

// dependPositions are the positions of the steps step waits for, explicit
// and inferred alike, given the position of each step by name.
func dependPositions(step *ir.Step, positions map[string]int) []int {
	var out []int
	for _, name := range step.AllDepends() {
		if p, ok := positions[name]; ok {
			out = append(out, p)
		}
	}
	return out
}

// encodedStepSize is how many bytes step adds to a run's encoded steps.
func encodedStepSize(step reportedStep) int {
	data, _ := json.Marshal(step)
	return len(data) + len(",")
}

// activeEntry is a run in progress as a report carries it: the run, and
// its steps when they were read.
type activeEntry struct {
	activeRun
	Steps      []reportedStep `json:"steps,omitempty"`
	StepsTotal int            `json:"steps_total,omitempty"`
}

// readSteps are the steps last read for a run in progress, and their
// encoded size.
type readSteps struct {
	at    time.Time
	steps []reportedStep
	total int
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
		if len(read.steps) == 0 || read.size > budget {
			continue
		}
		budget -= read.size
		entries[i].Steps, entries[i].StepsTotal = read.steps, read.total
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
	read.steps, read.total = runSteps(status)
	data, _ := json.Marshal(read.steps)
	read.size = len(data)
	return read
}
