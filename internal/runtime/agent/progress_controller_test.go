// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/controller"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestControllerDisplay returns a display writing to the returned buffer
// instead of stderr.
func newTestControllerDisplay(dag *ir.DAG) (*ControllerProgressDisplay, *bytes.Buffer) {
	display := NewControllerProgressDisplay(dag)
	var buf bytes.Buffer
	display.progressWriter = progressWriter{out: &buf}
	return display, &buf
}

func TestCreateProgressReporter_ControllerDAG(t *testing.T) {
	controllerDAG := &ir.DAG{Name: "triage", Type: ir.TypeController}
	reporter := createProgressReporter(controllerDAG, "run-1", nil)
	assert.IsType(t, &ControllerProgressDisplay{}, reporter)

	graphDAG := &ir.DAG{Name: "etl"}
	reporter = createProgressReporter(graphDAG, "run-2", nil)
	assert.IsType(t, &SimpleProgressDisplay{}, reporter)
}

func TestControllerProgressDisplay_UpdateNode(t *testing.T) {
	display, out := newTestControllerDisplay(&ir.DAG{Name: "triage", Type: ir.TypeController})

	state := controller.State{
		Tasks: []controller.TaskState{{Name: "triage", Status: controller.TaskOpen}},
		Events: []controller.Event{
			{Turn: 1, Kind: controller.EventAction, Name: "disk", Status: "succeeded"},
			{Turn: 2, Kind: controller.EventAction, Name: "load", Status: "succeeded"},
		},
		Turns: 2,
	}
	raw, err := json.Marshal(state)
	require.NoError(t, err)

	display.UpdateNode(&exec.Node{
		Step:            ir.Step{Name: ir.ControllerStepName},
		Status:          ir.NodeRunning,
		ControllerState: raw,
	})

	assert.Equal(t, 2, display.printedEvents)
	assert.Equal(t, 2, display.state.Turns)
	assert.Contains(t, out.String(), "turn 1  disk ✓")
	assert.Contains(t, out.String(), "turn 2  load ✓")

	// A later update prints only events that were not shown yet.
	out.Reset()
	state.Events = append(state.Events, controller.Event{
		Turn: 3, Kind: controller.EventTaskStatus, Name: "triage",
		Status: string(controller.TaskCompleted), Reason: "all good",
	})
	raw, err = json.Marshal(state)
	require.NoError(t, err)
	display.UpdateNode(&exec.Node{
		Step:            ir.Step{Name: ir.ControllerStepName},
		Status:          ir.NodeRunning,
		ControllerState: raw,
	})
	assert.Equal(t, 3, display.printedEvents)
	assert.NotContains(t, out.String(), "disk")
	assert.Contains(t, out.String(), "task triage completed: all good")
}

// controllerStateNode wraps a controller state into the node update the
// display receives.
func controllerStateNode(t *testing.T, state controller.State) *exec.Node {
	t.Helper()
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	return &exec.Node{
		Step:            ir.Step{Name: ir.ControllerStepName},
		Status:          ir.NodeRunning,
		ControllerState: raw,
	}
}

func TestControllerProgressDisplay_HoldsBackUnsettledAction(t *testing.T) {
	display, out := newTestControllerDisplay(&ir.DAG{Name: "triage", Type: ir.TypeController})

	// A resumed run first reports the inherited timeline with the suspended
	// action still waiting.
	state := controller.State{
		Events: []controller.Event{
			{Turn: 1, Kind: controller.EventAction, Name: "disk", Status: "succeeded"},
			{Turn: 2, Kind: controller.EventAction, Name: "review", Status: "waiting"},
		},
		Turns: 2,
	}
	display.UpdateNode(controllerStateNode(t, state))
	assert.Contains(t, out.String(), "disk ✓")
	assert.NotContains(t, out.String(), "review")
	assert.Equal(t, 1, display.printedEvents)

	// The action is then finalized in place: same timeline length, new status.
	state.Events[1].Status = "succeeded"
	display.UpdateNode(controllerStateNode(t, state))
	assert.Contains(t, out.String(), "review ✓")
	assert.Equal(t, 2, display.printedEvents)
}

func TestControllerProgressDisplay_PrintFinalFlushesUnsettledAction(t *testing.T) {
	display, out := newTestControllerDisplay(&ir.DAG{Name: "triage", Type: ir.TypeController})

	state := controller.State{
		Events: []controller.Event{
			{Turn: 1, Kind: controller.EventAction, Name: "review", Status: "waiting"},
		},
		Turns: 1,
	}
	display.UpdateNode(controllerStateNode(t, state))
	assert.NotContains(t, out.String(), "review")

	// Suspension ends the process with the action still waiting; the final
	// flush shows it as it stands.
	display.UpdateStatus(&exec.DAGRunStatus{Status: ir.Waiting})
	display.printFinal()
	assert.Contains(t, out.String(), "review ⏸")
	assert.Contains(t, out.String(), "⏸ ")
}

func TestActionSettled(t *testing.T) {
	assert.False(t, actionSettled(""))
	assert.False(t, actionSettled("running"))
	assert.False(t, actionSettled("waiting"))
	assert.False(t, actionSettled("retrying"))
	assert.True(t, actionSettled("succeeded"))
	assert.True(t, actionSettled("failed"))
	assert.True(t, actionSettled("aborted"))
	assert.True(t, actionSettled("rejected"))
}

func TestControllerProgressDisplay_UpdateNode_TracksRunningAction(t *testing.T) {
	display, _ := newTestControllerDisplay(&ir.DAG{Name: "triage", Type: ir.TypeController})

	display.UpdateNode(&exec.Node{Step: ir.Step{Name: "disk"}, Status: ir.NodeRunning})
	assert.Equal(t, "disk", display.runningAction)

	display.UpdateNode(&exec.Node{Step: ir.Step{Name: "disk"}, Status: ir.NodeSucceeded})
	assert.Equal(t, "", display.runningAction)

	// A finished node that is not the one in flight leaves the marker alone.
	display.UpdateNode(&exec.Node{Step: ir.Step{Name: "load"}, Status: ir.NodeRunning})
	display.UpdateNode(&exec.Node{Step: ir.Step{Name: "disk"}, Status: ir.NodeSucceeded})
	assert.Equal(t, "load", display.runningAction)
}

func TestControllerProgressDisplay_UpdateNode_IgnoresBadState(t *testing.T) {
	display, out := newTestControllerDisplay(&ir.DAG{Name: "triage", Type: ir.TypeController})

	display.UpdateNode(&exec.Node{
		Step:            ir.Step{Name: ir.ControllerStepName},
		Status:          ir.NodeRunning,
		ControllerState: json.RawMessage("{not json"),
	})
	assert.Equal(t, 0, display.printedEvents)

	display.UpdateNode(&exec.Node{
		Step:   ir.Step{Name: ir.ControllerStepName},
		Status: ir.NodeRunning,
	})
	assert.Equal(t, 0, display.printedEvents)
	assert.Equal(t, "", out.String())
}

func TestControllerProgressDisplay_FormatEvent(t *testing.T) {
	display, _ := newTestControllerDisplay(&ir.DAG{Name: "triage", Type: ir.TypeController})

	tests := []struct {
		name  string
		event controller.Event
		want  []string
	}{
		{
			name: "action succeeded with duration",
			event: controller.Event{
				Turn: 1, Kind: controller.EventAction, Name: "disk", Status: "succeeded",
				StartedAt: "2026-08-02T16:31:21+09:00", FinishedAt: "2026-08-02T16:31:23+09:00",
			},
			want: []string{"turn 1", "disk", "✓", "2.0s"},
		},
		{
			name: "action failed with reason",
			event: controller.Event{
				Turn: 2, Kind: controller.EventAction, Name: "probe", Status: "failed",
				Reason: "exit status 6",
			},
			want: []string{"turn 2", "probe", "✗", "exit status 6"},
		},
		{
			name: "repeated action shows attempt",
			event: controller.Event{
				Turn: 3, Kind: controller.EventAction, Name: "probe", Status: "succeeded", Attempt: 2,
			},
			want: []string{"probe", "✓", "(attempt 2)"},
		},
		{
			name: "task completed with reason",
			event: controller.Event{
				Turn: 4, Kind: controller.EventTaskStatus, Name: "triage",
				Status: string(controller.TaskCompleted), Reason: "machine healthy",
			},
			want: []string{"task triage completed", "machine healthy"},
		},
		{
			name: "task reopened",
			event: controller.Event{
				Turn: 5, Kind: controller.EventTaskStatus, Name: "triage",
				Status: string(controller.TaskOpen), Reason: "later work invalidated it",
			},
			want: []string{"task triage reopened"},
		},
		{
			name: "task failed uses failure mark",
			event: controller.Event{
				Turn: 6, Kind: controller.EventTaskStatus, Name: "triage",
				Status: string(controller.TaskFailed), Reason: "cannot reach host",
			},
			want: []string{"✗", "task triage failed", "cannot reach host"},
		},
		{
			name: "question",
			event: controller.Event{
				Turn: 7, Kind: controller.EventAskUser, Name: ir.AskUserStepName,
				Reason: "Which region?",
			},
			want: []string{"asked", "Which region?"},
		},
		{
			name: "rejected call",
			event: controller.Event{
				Turn: 8, Kind: controller.EventRejected, Name: "unknown_tool", Reason: "no such tool",
			},
			want: []string{"rejected unknown_tool", "no such tool"},
		},
		{
			name:  "stalled turn",
			event: controller.Event{Turn: 9, Kind: controller.EventStalled, Reason: "no action chosen"},
			want:  []string{"stalled", "no action chosen"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line := display.formatEvent(tc.event)
			for _, fragment := range tc.want {
				assert.Contains(t, line, fragment)
			}
		})
	}
}

func TestCompactReason(t *testing.T) {
	assert.Equal(t, "one line", compactReason("one\n line\t"))

	long := strings.Repeat("あ", maxReasonWidth+10)
	got := compactReason(long)
	assert.Equal(t, maxReasonWidth+1, len([]rune(got)))
	assert.True(t, strings.HasSuffix(got, "…"))
}

func TestEventDuration(t *testing.T) {
	assert.Equal(t, "", eventDuration(controller.Event{StartedAt: "2026-08-02T16:31:21+09:00"}))
	assert.Equal(t, "", eventDuration(controller.Event{
		StartedAt: "not a time", FinishedAt: "2026-08-02T16:31:23+09:00",
	}))
	assert.NotEqual(t, "", eventDuration(controller.Event{
		StartedAt: "2026-08-02T16:31:21+09:00", FinishedAt: "2026-08-02T16:31:23+09:00",
	}))
}

func TestControllerProgressDisplay_OpenTasksText(t *testing.T) {
	// Before any controller state arrives, every declared task is open.
	display, _ := newTestControllerDisplay(&ir.DAG{
		Name: "triage", Type: ir.TypeController,
		Tasks: []ir.ControllerTask{{Name: "a"}, {Name: "b"}},
	})
	assert.Equal(t, "2 tasks open", display.openTasksText())

	display.state = controller.State{Tasks: []controller.TaskState{
		{Name: "a", Status: controller.TaskOpen},
		{Name: "b", Status: controller.TaskCompleted},
	}}
	assert.Equal(t, "1 task open", display.openTasksText())

	display.state.Tasks[1].Status = controller.TaskOpen
	assert.Equal(t, "2 tasks open", display.openTasksText())
}
