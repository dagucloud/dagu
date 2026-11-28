// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

// A parallel step takes the connections that failed its child runs only
// when each failed child run has such a cause in the parent run's
// workspace, so the parent run's failure message is skipped exactly where
// the connections' messages go.
func TestParallelConnectionCause(t *testing.T) {
	t.Parallel()

	runs := []executor.RunParams{{RunID: "run-a"}, {RunID: "run-b"}, {RunID: "run-c"}}
	broken := func(names ...string) *ir.RunStatus {
		return &ir.RunStatus{Status: ir.Failed, ConnectionCause: names, CauseWorkspace: "ops"}
	}
	succeeded := &ir.RunStatus{Status: ir.Succeeded}
	tests := []struct {
		name    string
		results map[string]*ir.RunStatus
		// noResult names the runs whose latest attempt returned no result.
		noResult map[string]struct{}
		want     []string
	}{
		{
			name:    "EveryFailedRunBroken",
			results: map[string]*ir.RunStatus{"run-a": broken("work-google"), "run-b": succeeded, "run-c": broken("work-google", "drive")},
			want:    []string{"work-google", "drive"},
		},
		{
			name:    "OtherFailedRun",
			results: map[string]*ir.RunStatus{"run-a": broken("work-google"), "run-b": succeeded, "run-c": {Status: ir.Failed}},
		},
		{
			// A child run that returned no result failed to start.
			name:    "MissingResult",
			results: map[string]*ir.RunStatus{"run-a": broken("work-google"), "run-b": succeeded},
		},
		{
			// A retry that failed before its child run started supersedes the
			// earlier result.
			name:     "LatestAttemptWithoutResult",
			results:  map[string]*ir.RunStatus{"run-a": broken("work-google"), "run-b": succeeded, "run-c": broken("work-google")},
			noResult: map[string]struct{}{"run-c": {}},
		},
		{
			// The child's connection messages go through another workspace's
			// routes, which the parent's channels may not share.
			name: "OtherWorkspace",
			results: map[string]*ir.RunStatus{
				"run-a": broken("work-google"), "run-b": succeeded,
				"run-c": {Status: ir.Failed, ConnectionCause: []string{"work-google"}, CauseWorkspace: "sales"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exec := &parallelExecutor{
				runParamsList: runs,
				results:       tt.results,
				noResult:      tt.noResult,
				workspace:     causeWorkspace{name: "ops", valid: true},
			}
			assert.Equal(t, tt.want, exec.ConnectionCause())
		})
	}
}

// A call step takes its child run's cause only when the child runs in the
// parent run's workspace. A child in another workspace sends its connection
// messages through that workspace's routes, so a channel only in the
// parent's routes would otherwise get neither message.
func TestCallConnectionCause(t *testing.T) {
	t.Parallel()

	ops := causeWorkspace{name: "ops", valid: true}
	tests := []struct {
		name      string
		workspace causeWorkspace
		result    *ir.RunStatus
		want      []string
	}{
		{
			name:      "SameWorkspace",
			workspace: ops,
			result:    &ir.RunStatus{Status: ir.Failed, ConnectionCause: []string{"work-google"}, CauseWorkspace: "ops"},
			want:      []string{"work-google"},
		},
		{
			name:      "OtherWorkspace",
			workspace: ops,
			result:    &ir.RunStatus{Status: ir.Failed, ConnectionCause: []string{"work-google"}, CauseWorkspace: "sales"},
		},
		{
			// A parent and a child without a workspace label both use the
			// global routes.
			name:      "BothWithoutWorkspace",
			workspace: causeWorkspace{valid: true},
			result:    &ir.RunStatus{Status: ir.Failed, ConnectionCause: []string{"shared-google"}},
			want:      []string{"shared-google"},
		},
		{
			name:      "ParentLabelsInvalid",
			workspace: causeWorkspace{},
			result:    &ir.RunStatus{Status: ir.Failed, ConnectionCause: []string{"shared-google"}},
		},
		{name: "NoResult", workspace: ops},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exec := &dagExecutor{workspace: tt.workspace, result: tt.result}
			assert.Equal(t, tt.want, exec.ConnectionCause())
		})
	}
}
