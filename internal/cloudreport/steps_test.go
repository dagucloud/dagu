// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"encoding/json"
	"fmt"
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
// a name takes no position, and a step past those reported is left out of
// the dependencies that name it.
func TestStepsNameTheirDependenciesByPosition(t *testing.T) {
	t.Parallel()

	status := run("etl", "run-1", ir.Succeeded)
	status.Nodes = []*ir.Node{
		{Step: ir.Step{}},
		{Step: ir.Step{Name: "extract"}},
		{Step: ir.Step{Name: "transform", Depends: []string{"extract"}}},
		{Step: ir.Step{
			Name:            "load",
			Depends:         []string{"transform"},
			InferredDepends: []ir.InferredDependency{{Step: "extract", Field: "rows"}},
		}},
	}
	status.OnSuccess = &ir.Node{Step: ir.Step{Name: "onSuccess"}}

	steps, total := runSteps(status)

	assert.Equal(t, 4, total)
	depends := map[string][]int{}
	for _, step := range steps {
		depends[step.Name] = step.Depends
	}
	assert.Equal(t, map[string][]int{"extract": nil, "transform": {0}, "load": {1, 0}, "onSuccess": nil}, depends)

	many := run("etl", "run-2", ir.Succeeded)
	for i := range maxRunSteps + 50 {
		many.Nodes = append(many.Nodes, &ir.Node{Step: ir.Step{Name: fmt.Sprint("step-", i)}})
	}
	many.Nodes[0].Step.Depends = []string{"step-1", fmt.Sprint("step-", maxRunSteps)}

	steps, _ = runSteps(many)

	require.Len(t, steps, maxRunSteps)
	assert.Equal(t, []int{1}, steps[0].Depends, "a step past those reported is left out")
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

	steps, _ := runSteps(status)

	require.Len(t, steps, 3)
	assert.Equal(t, []int{0}, steps[1].Depends)
}
