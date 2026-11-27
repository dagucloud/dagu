// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The steps of a report's runs in progress stay within maxActiveStepsBytes:
// the runs listed first carry theirs, and the runs past the budget carry
// none.
func TestRunsInProgressShareTheirStepsBudget(t *testing.T) {
	t.Parallel()

	statuses := map[string]*ir.DAGRunStatus{}
	var active []activeRun
	for i := range 30 {
		id := fmt.Sprint("run-", i)
		status := run("etl", id, ir.Running)
		for j := range 250 {
			name := fmt.Sprintf("%03d%s", j, strings.Repeat("\u0001", maxStepNameBytes))
			status.Nodes = append(status.Nodes, &ir.Node{Step: ir.Step{Name: name}, Status: ir.NodeNotStarted})
		}
		statuses[id] = status
		active = append(active, newActiveRun(status))
	}
	reader := &stepReader{runs: &fakeRuns{statuses: statuses}}

	entries := reader.entries(t.Context(), active, testClockStart)

	require.Len(t, entries, len(active))
	size, carried := 0, 0
	for _, entry := range entries {
		if len(entry.Steps) == 0 {
			continue
		}
		data, err := json.Marshal(entry.Steps)
		require.NoError(t, err)
		size += len(data)
		carried++
	}
	assert.LessOrEqual(t, size, maxActiveStepsBytes)
	assert.NotEmpty(t, entries[0].Steps, "the runs listed first carry their steps")
	assert.Empty(t, entries[len(entries)-1].Steps, "the runs past the budget carry none")
	assert.Less(t, carried, len(entries))
}

// Each step names the steps it waits for by their positions among the
// reported steps, explicit and inferred dependencies alike. A step without
// a name takes no position, a handler is no step, and a step past those
// reported is left out of the dependencies that name it.
func TestStepsNameTheirDependenciesByPosition(t *testing.T) {
	t.Parallel()

	status := run("etl", "run-1", ir.Succeeded)
	status.Nodes = []*ir.Node{
		{Step: ir.Step{}},
		{Step: ir.Step{Name: "extract"}},
		{Step: ir.Step{Name: "transform", Depends: []string{"extract", "extract"}}},
		{Step: ir.Step{
			Name:            "load",
			Depends:         []string{"transform"},
			InferredDepends: []ir.InferredDependency{{Step: "extract", Field: "rows"}},
		}},
	}
	status.OnSuccess = &ir.Node{Step: ir.Step{Name: "onSuccess"}}

	reported := runSteps(status)

	assert.Equal(t, 3, reported.StepsTotal)
	depends := map[string][]int{}
	for _, step := range reported.Steps {
		depends[step.Name] = step.Depends
	}
	assert.Equal(t, map[string][]int{"extract": nil, "transform": {0}, "load": {1, 0}}, depends)

	many := run("etl", "run-2", ir.Succeeded)
	for i := range maxRunSteps + 50 {
		many.Nodes = append(many.Nodes, &ir.Node{Step: ir.Step{Name: fmt.Sprint("step-", i)}})
	}
	many.Nodes[0].Step.Depends = []string{"step-1", fmt.Sprint("step-", maxRunSteps)}

	steps := runSteps(many).Steps

	require.Len(t, steps, maxRunSteps)
	assert.Equal(t, []int{1}, steps[0].Depends, "a step past those reported is left out")
}

func TestStepsBudgetOnlyReportedDependencies(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		count        int
		padding      int
		firstOmitted int
	}{
		{name: "step count", count: 2 * maxRunSteps, firstOmitted: maxRunSteps},
		{name: "encoded size", count: 100, padding: maxStepNameBytes - len("step-000"), firstOmitted: 70},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := run("etl", "run-1", ir.Succeeded)
			var omitted []string
			for i := range tc.count {
				name := fmt.Sprintf("step-%03d%s", i, strings.Repeat("x", tc.padding))
				status.Nodes = append(status.Nodes, &ir.Node{Step: ir.Step{Name: name}, Status: ir.NodeSucceeded})
				if i >= tc.firstOmitted {
					omitted = append(omitted, name)
				}
			}
			withoutEdges := runSteps(status)
			require.LessOrEqual(t, len(withoutEdges.Steps), tc.firstOmitted)
			for _, node := range status.Nodes[:50] {
				node.Step.Depends = omitted
			}
			status.Nodes[0].Step.Depends = append([]string{status.Nodes[1].Step.Name}, omitted...)

			reported := runSteps(status)
			require.Equal(t, len(withoutEdges.Steps), len(reported.Steps))
			assert.Equal(t, []int{1}, reported.Steps[0].Depends)
			assert.LessOrEqual(t, encodedLen(reported), maxStepsBytes)
		})
	}
}

// A run's handlers are reported apart from its steps, by kind.
func TestHandlersAreReportedByKind(t *testing.T) {
	t.Parallel()

	status := run("etl", "run-1", ir.Succeeded)
	status.Nodes = []*ir.Node{{Step: ir.Step{Name: "extract"}}}
	status.OnInit = &ir.Node{Step: ir.Step{Name: "init"}}
	status.OnWait = &ir.Node{Step: ir.Step{Name: "wait"}}
	status.OnSuccess = &ir.Node{Step: ir.Step{Name: "success"}}
	status.OnFailure = &ir.Node{Step: ir.Step{Name: "failure"}}
	status.OnAbort = &ir.Node{Step: ir.Step{Name: "abort"}}
	status.OnExit = &ir.Node{Step: ir.Step{Name: "exit"}}

	reported := runSteps(status)

	require.Len(t, reported.Steps, 1)
	assert.Equal(t, "extract", reported.Steps[0].Name)
	assert.Equal(t, 1, reported.StepsTotal, "handlers are not counted among the steps")
	assert.ElementsMatch(t, []string{"onInit", "onWait", "onSuccess", "onFailure", "onAbort", "onExit"},
		slices.Collect(maps.Keys(reported.Handlers)))
}

// A step carries its times in whole seconds from its run's start, as the
// run's start is reported, and its retries only when it was retried. A
// step that finished before it started took no time, and a run without a
// start carries no step times.
func TestStepsCarryTheirTimesInSecondsFromTheRunsStart(t *testing.T) {
	t.Parallel()

	status := run("etl", "run-1", ir.Succeeded)
	status.StartedAt = "2026-10-10T09:00:00.700Z"
	status.Nodes = []*ir.Node{
		{Step: ir.Step{Name: "extract"}, Status: ir.NodeSucceeded,
			StartedAt: "2026-10-10T09:00:00.900Z", FinishedAt: "2026-10-10T09:01:14.200Z", RetryCount: 1},
		{Step: ir.Step{Name: "load"}, Status: ir.NodeSucceeded,
			StartedAt: "2026-10-10T09:01:14Z", FinishedAt: "2026-10-10T09:01:10Z"},
		{Step: ir.Step{Name: "notify"}, Status: ir.NodeNotStarted, StartedAt: "-"},
	}
	status.OnExit = &ir.Node{Step: ir.Step{Name: "onExit"}, Status: ir.NodeRunning, StartedAt: "2026-10-10T09:01:15Z"}

	data, err := json.Marshal(runSteps(status))

	require.NoError(t, err)
	assert.JSONEq(t, `{
		"steps": [
			{"name": "extract", "status": "succeeded", "start": 0, "took": 74, "retries": 1},
			{"name": "load", "status": "succeeded", "start": 74, "took": 0},
			{"name": "notify", "status": "not_started"}
		],
		"handlers": {"onExit": {"status": "running", "start": 75}},
		"steps_total": 3
	}`, string(data))

	status.StartedAt = ""
	for _, step := range runSteps(status).Steps {
		assert.Nil(t, step.Start, step.Name)
		assert.Nil(t, step.Took, step.Name)
	}
}

// A handler can share a regular step's name, but only regular steps are
// waited for, so a dependency on that name points at the regular step.
func TestADependencyNamesTheStepNotTheHandler(t *testing.T) {
	t.Parallel()

	status := run("etl", "run-1", ir.Succeeded)
	status.Nodes = []*ir.Node{
		{Step: ir.Step{Name: "onSuccess"}},
		{Step: ir.Step{Name: "load", Depends: []string{"onSuccess"}}},
	}
	status.OnSuccess = &ir.Node{Step: ir.Step{Name: "onSuccess"}}

	steps := runSteps(status).Steps

	require.Len(t, steps, 2)
	assert.Equal(t, []int{0}, steps[1].Depends)
}
