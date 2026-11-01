// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storedLine encodes a status the way a status file stores it.
func storedLine(t testing.TB, status ir.DAGRunStatus) string {
	t.Helper()
	data, err := json.Marshal(ir.StoredStatus(status))
	require.NoError(t, err)
	return string(data)
}

func TestStoredStatusKeepsARepeatedOutputOnce(t *testing.T) {
	t.Parallel()

	large := `{"diff":"` + strings.Repeat("+added line\n", 20000) + `end of diff"}`
	node := &ir.Node{
		Step:             ir.Step{Name: "fetch"},
		Status:           ir.NodeSucceeded,
		OutputValue:      new(large),
		OutputsValue:     new(large),
		StepOutputsValue: new(large),
	}
	status := ir.DAGRunStatus{Name: "review", DAGRunID: "run-1", Nodes: []*ir.Node{node}, OnExit: node}

	line := storedLine(t, status)
	// Once for the step and once for its exit handler.
	assert.Equal(t, 2, strings.Count(line, "end of diff"))

	read, err := ir.StatusFromJSON(line)
	require.NoError(t, err)
	for _, got := range []*ir.Node{read.Nodes[0], read.OnExit} {
		require.NotNil(t, got.OutputValue)
		require.NotNil(t, got.OutputsValue)
		require.NotNil(t, got.StepOutputsValue)
		assert.Equal(t, large, *got.OutputValue)
		assert.Equal(t, large, *got.OutputsValue)
		assert.Equal(t, large, *got.StepOutputsValue)
	}
	// The status passed in keeps every field.
	assert.Equal(t, large, *node.OutputsValue)
	assert.Equal(t, large, *node.StepOutputsValue)
}

func TestStoredStatusKeepsOutputsThatDiffer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		output, outputs, step *string
	}{
		{name: "all differ", output: new("plain"), outputs: new(`{"a":1}`), step: new(`{"b":2}`)},
		{name: "step outputs repeat outputs", output: new("plain"), outputs: new(`{"a":1}`), step: new(`{"a":1}`)},
		{name: "only outputs set", outputs: new(`{"a":1}`)},
		{name: "empty values repeat", output: new(""), outputs: new(""), step: new("")},
		{name: "none set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status := ir.DAGRunStatus{Nodes: []*ir.Node{{
				Step: ir.Step{Name: "step"}, OutputValue: tt.output, OutputsValue: tt.outputs, StepOutputsValue: tt.step,
			}}}
			read, err := ir.StatusFromJSON(storedLine(t, status))
			require.NoError(t, err)
			got := read.Nodes[0]
			assert.Equal(t, tt.output, got.OutputValue)
			assert.Equal(t, tt.outputs, got.OutputsValue)
			assert.Equal(t, tt.step, got.StepOutputsValue)
		})
	}
}

func TestStatusLinesWrittenBeforeStoringOutputsOnceStillRead(t *testing.T) {
	t.Parallel()

	line := `{"name":"review","dagRunId":"run-1","nodes":[{"step":{"name":"fetch"},"status":4,` +
		`"outputValue":"{\"diff\":\"x\"}","outputsValue":"{\"diff\":\"x\"}","stepOutputsValue":"{\"diff\":\"x\"}"}]}`
	read, err := ir.StatusFromJSON(line)
	require.NoError(t, err)
	got := read.Nodes[0]
	for _, value := range []*string{got.OutputValue, got.OutputsValue, got.StepOutputsValue} {
		require.NotNil(t, value)
		assert.Equal(t, `{"diff":"x"}`, *value)
	}
}

func TestANodeOutsideAStatusFileKeepsEveryOutputField(t *testing.T) {
	t.Parallel()

	node := ir.Node{Step: ir.Step{Name: "fetch"}, OutputValue: new("v"), OutputsValue: new("v"), StepOutputsValue: new("v")}
	data, err := json.Marshal(node)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &fields))
	for _, name := range []string{"outputValue", "outputsValue", "stepOutputsValue"} {
		assert.JSONEq(t, `"v"`, string(fields[name]), name)
	}
	assert.NotContains(t, fields, "outputsValueFrom")
	assert.NotContains(t, fields, "stepOutputsValueFrom")
}

// BenchmarkStoredStatusSize reports the bytes a status line takes for a step
// with a large output, stored with and without repeating it.
func BenchmarkStoredStatusSize(b *testing.B) {
	large := `{"diff":"` + strings.Repeat("+added line\n", 60000) + `"}`
	status := ir.DAGRunStatus{Nodes: []*ir.Node{{
		Step: ir.Step{Name: "fetch"}, OutputValue: new(large), OutputsValue: new(large), StepOutputsValue: new(large),
	}}}
	b.Run("repeated", func(b *testing.B) {
		var size int
		for b.Loop() {
			data, _ := json.Marshal(status)
			size = len(data)
		}
		b.ReportMetric(float64(size), "bytes/line")
	})
	b.Run("stored", func(b *testing.B) {
		var size int
		for b.Loop() {
			data, _ := json.Marshal(ir.StoredStatus(status))
			size = len(data)
		}
		b.ReportMetric(float64(size), "bytes/line")
	})
}
