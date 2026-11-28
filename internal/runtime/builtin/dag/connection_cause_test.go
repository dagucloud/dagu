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
// when each failed child run has such a cause, so the parent run's failure
// message is skipped as the children's are.
func TestParallelConnectionCause(t *testing.T) {
	t.Parallel()

	runs := []executor.RunParams{{RunID: "run-a"}, {RunID: "run-b"}, {RunID: "run-c"}}
	broken := func(names ...string) *ir.RunStatus {
		return &ir.RunStatus{Status: ir.Failed, ConnectionCause: names}
	}
	succeeded := &ir.RunStatus{Status: ir.Succeeded}
	tests := []struct {
		name    string
		results map[string]*ir.RunStatus
		want    []string
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exec := &parallelExecutor{runParamsList: runs, results: tt.results}
			assert.Equal(t, tt.want, exec.ConnectionCause())
		})
	}
}
