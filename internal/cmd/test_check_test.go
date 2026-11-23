// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/stretchr/testify/assert"
)

// A text output matches a string exactly and any other value by the JSON
// rule against its text; a type: json output always matches by the JSON
// rule, which ignores unlisted keys and compares arrays and numbers by
// value.
func TestCheckOutputs(t *testing.T) {
	t.Parallel()

	published := `{"files":"3","stats":"{\"files\":3,\"lines\":40,\"tags\":[\"a\",\"b\"]}","ratio":1.50,"note":"not json"}`
	status := &ir.DAGRunStatus{Status: ir.Succeeded, Nodes: []*ir.Node{{
		Step:             ir.Step{Name: "count"},
		Status:           ir.NodeSucceeded,
		DoneCount:        1,
		StepOutputsValue: &published,
	}}}
	tc := &testCase{ended: []*ir.DAGRunStatus{status}}
	stats := `{"files":3,"lines":40,"tags":["a","b"]}`

	for _, tt := range []struct {
		name   string
		output dagtest.OutputExpect
		// failure is the report line, or empty when the output matches.
		failure string
	}{
		{"TextExact", dagtest.OutputExpect{Name: "files", Value: "3"}, ""},
		{"TextDiffers", dagtest.OutputExpect{Name: "files", Value: "03"}, `step count output files: expected "03", got "3"`},
		{"TextAsJSON", dagtest.OutputExpect{Name: "files", Value: 3}, ""},
		{"Subset", dagtest.OutputExpect{Name: "stats", Value: map[string]any{"files": 3}, JSON: true}, ""},
		{"SubsetDiffers", dagtest.OutputExpect{Name: "stats", Value: map[string]any{"lines": 41}, JSON: true},
			`step count output stats: expected {"lines":41}, got ` + stats},
		{"ArrayExact", dagtest.OutputExpect{Name: "stats", Value: map[string]any{"tags": []any{"a"}}, JSON: true},
			`step count output stats: expected {"tags":["a"]}, got ` + stats},
		{"NumberByValue", dagtest.OutputExpect{Name: "ratio", Value: 1.5}, ""},
		{"TextNotJSON", dagtest.OutputExpect{Name: "note", Value: map[string]any{"a": 1}},
			`step count output note: expected {"a":1}, got "not json"`},
		{"NotPublished", dagtest.OutputExpect{Name: "missing", Value: "x"}, `step count output missing: expected "x", got nothing`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			failures := tc.check(dagtest.Expect{Steps: []dagtest.StepExpect{
				{Key: "count", Step: "count", Outputs: []dagtest.OutputExpect{tt.output}},
			}}, status)
			if tt.failure == "" {
				assert.Empty(t, failures)
				return
			}
			assert.Equal(t, []string{tt.failure}, failureLines(failures))
		})
	}
}

// Notifications pair without regard to order. An item without step or to
// may take any notification of its kind, so a stricter item still finds
// its own.
func TestCheckNotifications(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Steps: []ir.Step{{Name: "load data", ID: "load"}}}
	failure := agent.Notification{Kind: agent.NotificationMailOnFailure, To: []string{"a@example.com", "b@example.com"}}
	stepError := agent.Notification{Kind: agent.NotificationMailOnError, Step: "load data", To: []string{"a@example.com"}}
	sentLine := "mail_on_error step load to a@example.com; mail_on.failure to a@example.com, b@example.com"

	for _, tt := range []struct {
		name     string
		sent     []agent.Notification
		expected []dagtest.Notification
		// failure is the report line, or empty when the list holds.
		failure string
	}{
		{"AnyOrder", []agent.Notification{stepError, failure}, []dagtest.Notification{
			{Kind: "mail_on.failure", To: []string{"b@example.com", "a@example.com"}},
			{Kind: "mail_on_error", Step: "load data"},
		}, ""},
		{"LooseItemFirst", []agent.Notification{
			{Kind: agent.NotificationMailOnFailure, To: []string{"a@example.com"}},
			{Kind: agent.NotificationMailOnFailure, To: []string{"b@example.com"}},
		}, []dagtest.Notification{
			{Kind: "mail_on.failure"},
			{Kind: "mail_on.failure", To: []string{"a@example.com"}},
		}, ""},
		{"None", []agent.Notification{stepError, failure}, []dagtest.Notification{},
			"notifications: expected none, got " + sentLine},
		{"OtherRecipients", []agent.Notification{stepError, failure}, []dagtest.Notification{
			{Kind: "mail_on_error", Step: "load data"},
			{Kind: "mail_on.failure", To: []string{"a@example.com"}},
		}, "notifications: expected mail_on_error step load; mail_on.failure to a@example.com, got " + sentLine},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := &testCase{dag: dag, notifications: tt.sent}
			failures := tc.check(dagtest.Expect{Notifications: tt.expected}, &ir.DAGRunStatus{Status: ir.Failed})
			if tt.failure == "" {
				assert.Empty(t, failures)
				return
			}
			assert.Equal(t, []string{tt.failure}, failureLines(failures))
		})
	}
}
