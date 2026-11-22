// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"context"
	"encoding/json"
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
}

// runSteps returns the named steps of status in run order, handlers
// included: the first maxRunSteps that fit in maxStepsBytes encoded, and
// how many there are in all.
func runSteps(status *ir.DAGRunStatus) ([]reportedStep, int) {
	var steps []reportedStep
	total, size, full := 0, len("[]"), false
	for _, node := range status.NodesInRunOrder() {
		if node.Step.Name == "" {
			continue
		}
		total++
		if full {
			continue
		}
		step := reportedStep{
			Name:       truncateBytes(node.Step.Name, maxStepNameBytes),
			Status:     node.Status.String(),
			StartedAt:  snapshotTime(node.StartedAt),
			FinishedAt: snapshotTime(node.FinishedAt),
			Retries:    node.RetryCount,
		}
		n := encodedStepSize(step)
		if len(steps) == maxRunSteps || size+n > maxStepsBytes {
			full = true
			continue
		}
		steps = append(steps, step)
		size += n
	}
	return steps, total
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

// readSteps are the steps last read for a run attempt in progress.
type readSteps struct {
	at    time.Time
	steps []reportedStep
	total int
}

// stepReader reads the steps of the runs in progress, reusing a read for
// activeStepsEvery.
type stepReader struct {
	runs RunFinder
	read map[activeKey]readSteps
}

// activeKey names a run attempt in progress.
type activeKey struct{ workflow, runID, attemptID string }

// entries returns runs with the steps of each, within maxActiveStepsBytes
// in the order given. A run whose steps cannot be read, or whose latest
// attempt is another, carries none. Reads of runs no longer listed are
// forgotten.
func (s *stepReader) entries(ctx context.Context, runs []activeRun, now time.Time) []activeEntry {
	entries := make([]activeEntry, len(runs))
	if s == nil || s.runs == nil {
		for i, run := range runs {
			entries[i] = activeEntry{activeRun: run}
		}
		return entries
	}
	listed := make(map[activeKey]readSteps, len(runs))
	budget := maxActiveStepsBytes
	for i, run := range runs {
		entries[i] = activeEntry{activeRun: run}
		key := activeKey{run.Workflow, run.DAGRunID, run.AttemptID}
		read, ok := s.read[key]
		if !ok || now.Sub(read.at) >= activeStepsEvery {
			read = readSteps{at: now}
			read.steps, read.total = s.readRun(ctx, run)
		}
		listed[key] = read
		size := len("[]")
		for _, step := range read.steps {
			size += encodedStepSize(step)
		}
		if len(read.steps) == 0 || size > budget {
			continue
		}
		budget -= size
		entries[i].Steps, entries[i].StepsTotal = read.steps, read.total
	}
	s.read = listed
	return entries
}

// readRun reads the steps of run's attempt, or none when its latest attempt
// is another or cannot be read.
func (s *stepReader) readRun(ctx context.Context, run activeRun) ([]reportedStep, int) {
	attempt, err := s.runs.FindAttempt(ctx, ir.NewDAGRunRef(run.Workflow, run.DAGRunID))
	if err != nil {
		logger.Debug(ctx, "Failed to find a run in progress for its steps in the server report", tag.DAG(run.Workflow), tag.RunID(run.DAGRunID), tag.Error(err))
		return nil, 0
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil || status == nil {
		logger.Debug(ctx, "Failed to read a run in progress for its steps in the server report", tag.DAG(run.Workflow), tag.RunID(run.DAGRunID), tag.Error(err))
		return nil, 0
	}
	if status.AttemptID != run.AttemptID {
		return nil, 0
	}
	return runSteps(status)
}
