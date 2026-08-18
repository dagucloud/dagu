// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/agentloop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestAgentDisplay returns a display writing to the returned buffer
// instead of stderr.
func newTestAgentDisplay(dag *ir.DAG) (*AgentDAGProgressDisplay, *bytes.Buffer) {
	display := NewAgentDAGProgressDisplay(dag)
	var buf bytes.Buffer
	display.progressWriter = progressWriter{out: &buf}
	return display, &buf
}

func TestCreateProgressReporter_AgentDAG(t *testing.T) {
	agentDAG := &ir.DAG{Name: "triage", Type: ir.TypeAgent}
	reporter := createProgressReporter(agentDAG, "run-1", nil)
	assert.IsType(t, &AgentDAGProgressDisplay{}, reporter)

	graphDAG := &ir.DAG{Name: "etl"}
	reporter = createProgressReporter(graphDAG, "run-2", nil)
	assert.IsType(t, &SimpleProgressDisplay{}, reporter)
}

func TestAgentDAGProgressDisplay_UpdateNode(t *testing.T) {
	display, out := newTestAgentDisplay(&ir.DAG{Name: "triage", Type: ir.TypeAgent})

	state := agentloop.State{
		Tasks: []agentloop.TaskState{{Name: "triage", Status: agentloop.TaskOpen}},
		Events: []agentloop.Event{
			{Turn: 1, Kind: agentloop.EventAction, Name: "disk", Status: "succeeded"},
			{Turn: 2, Kind: agentloop.EventAction, Name: "load", Status: "succeeded"},
		},
		Turns: 2,
	}
	raw, err := json.Marshal(state)
	require.NoError(t, err)

	display.UpdateNode(&ir.Node{
		Step:       ir.Step{Name: ir.AgentStepName},
		Status:     ir.NodeRunning,
		AgentState: raw,
	})

	assert.Equal(t, 2, display.printedEvents)
	assert.Equal(t, 2, display.state.Turns)
	assert.Contains(t, out.String(), "turn 1  disk ✓")
	assert.Contains(t, out.String(), "turn 2  load ✓")

	// A later update prints only events that were not shown yet.
	out.Reset()
	state.Events = append(state.Events, agentloop.Event{
		Turn: 3, Kind: agentloop.EventTaskStatus, Name: "triage",
		Status: string(agentloop.TaskCompleted), Reason: "all good",
	})
	raw, err = json.Marshal(state)
	require.NoError(t, err)
	display.UpdateNode(&ir.Node{
		Step:       ir.Step{Name: ir.AgentStepName},
		Status:     ir.NodeRunning,
		AgentState: raw,
	})
	assert.Equal(t, 3, display.printedEvents)
	assert.NotContains(t, out.String(), "disk")
	assert.Contains(t, out.String(), "task triage completed: all good")
}

// agentStateNode wraps an agent state into the node update the
// display receives.
func agentStateNode(t *testing.T, state agentloop.State) *ir.Node {
	t.Helper()
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	return &ir.Node{
		Step:       ir.Step{Name: ir.AgentStepName},
		Status:     ir.NodeRunning,
		AgentState: raw,
	}
}

func TestAgentDAGProgressDisplay_HoldsBackUnsettledAction(t *testing.T) {
	display, out := newTestAgentDisplay(&ir.DAG{Name: "triage", Type: ir.TypeAgent})

	// A resumed run first reports the inherited timeline with the suspended
	// action still waiting.
	state := agentloop.State{
		Events: []agentloop.Event{
			{Turn: 1, Kind: agentloop.EventAction, Name: "disk", Status: "succeeded"},
			{Turn: 2, Kind: agentloop.EventAction, Name: "review", Status: "waiting"},
		},
		Turns: 2,
	}
	display.UpdateNode(agentStateNode(t, state))
	assert.Contains(t, out.String(), "disk ✓")
	assert.NotContains(t, out.String(), "review")
	assert.Equal(t, 1, display.printedEvents)

	// The action is then finalized in place: same timeline length, new status.
	state.Events[1].Status = "succeeded"
	display.UpdateNode(agentStateNode(t, state))
	assert.Contains(t, out.String(), "review ✓")
	assert.Equal(t, 2, display.printedEvents)
}

func TestAgentDAGProgressDisplay_PrintFinalFlushesUnsettledAction(t *testing.T) {
	display, out := newTestAgentDisplay(&ir.DAG{Name: "triage", Type: ir.TypeAgent})

	state := agentloop.State{
		Events: []agentloop.Event{
			{Turn: 1, Kind: agentloop.EventAction, Name: "review", Status: "waiting"},
		},
		Turns: 1,
	}
	display.UpdateNode(agentStateNode(t, state))
	assert.NotContains(t, out.String(), "review")

	// Suspension ends the process with the action still waiting; the final
	// flush shows it as it stands.
	display.UpdateStatus(&ir.DAGRunStatus{Status: ir.Waiting})
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

func TestAgentDAGProgressDisplay_UpdateNode_TracksRunningActions(t *testing.T) {
	display, out := newTestAgentDisplay(&ir.DAG{Name: "triage", Type: ir.TypeAgent})

	display.UpdateNode(&ir.Node{Step: ir.Step{Name: "disk"}, Status: ir.NodeRunning})
	display.UpdateNode(&ir.Node{Step: ir.Step{Name: "load"}, Status: ir.NodeRunning})
	assert.Equal(t, map[string]struct{}{"disk": {}, "load": {}}, display.runningActions)
	display.UpdateNode(&ir.Node{Step: ir.Step{Name: "disk"}, Status: ir.NodeRetrying})
	display.UpdateNode(&ir.Node{Step: ir.Step{Name: "load"}, Status: ir.NodeWaiting})
	assert.Equal(t, map[string]struct{}{"disk": {}, "load": {}}, display.runningActions)

	display.UpdateNode(&ir.Node{Step: ir.Step{Name: "disk"}, Status: ir.NodeSucceeded})
	assert.Equal(t, map[string]struct{}{"load": {}}, display.runningActions)

	// A finished node that is not in flight leaves the active set alone.
	display.UpdateNode(&ir.Node{Step: ir.Step{Name: "disk"}, Status: ir.NodeSucceeded})
	assert.Equal(t, map[string]struct{}{"load": {}}, display.runningActions)

	display.UpdateNode(&ir.Node{Step: ir.Step{Name: "disk"}, Status: ir.NodeRunning})
	display.renderLiveLine()
	assert.Contains(t, out.String(), "running disk, load")
}

func TestAgentDAGProgressDisplay_UpdateNode_IgnoresBadState(t *testing.T) {
	display, out := newTestAgentDisplay(&ir.DAG{Name: "triage", Type: ir.TypeAgent})

	display.UpdateNode(&ir.Node{
		Step:       ir.Step{Name: ir.AgentStepName},
		Status:     ir.NodeRunning,
		AgentState: json.RawMessage("{not json"),
	})
	assert.Equal(t, 0, display.printedEvents)

	display.UpdateNode(&ir.Node{
		Step:   ir.Step{Name: ir.AgentStepName},
		Status: ir.NodeRunning,
	})
	assert.Equal(t, 0, display.printedEvents)
	assert.Equal(t, "", out.String())
}

func TestAgentDAGProgressDisplay_FormatEvent(t *testing.T) {
	display, _ := newTestAgentDisplay(&ir.DAG{Name: "triage", Type: ir.TypeAgent})

	tests := []struct {
		name  string
		event agentloop.Event
		want  []string
	}{
		{
			name: "action succeeded with duration",
			event: agentloop.Event{
				Turn: 1, Kind: agentloop.EventAction, Name: "disk", Status: "succeeded",
				StartedAt: "2026-08-02T16:31:21+09:00", FinishedAt: "2026-08-02T16:31:23+09:00",
			},
			want: []string{"turn 1", "disk", "✓", "2.0s"},
		},
		{
			name: "action failed with reason",
			event: agentloop.Event{
				Turn: 2, Kind: agentloop.EventAction, Name: "probe", Status: "failed",
				Reason: "exit status 6",
			},
			want: []string{"turn 2", "probe", "✗", "exit status 6"},
		},
		{
			name: "repeated action shows attempt",
			event: agentloop.Event{
				Turn: 3, Kind: agentloop.EventAction, Name: "probe", Status: "succeeded", Attempt: 2,
			},
			want: []string{"probe", "✓", "(attempt 2)"},
		},
		{
			name: "task completed with reason",
			event: agentloop.Event{
				Turn: 4, Kind: agentloop.EventTaskStatus, Name: "triage",
				Status: string(agentloop.TaskCompleted), Reason: "machine healthy",
			},
			want: []string{"task triage completed", "machine healthy"},
		},
		{
			name: "task reopened",
			event: agentloop.Event{
				Turn: 5, Kind: agentloop.EventTaskStatus, Name: "triage",
				Status: string(agentloop.TaskOpen), Reason: "later work invalidated it",
			},
			want: []string{"task triage reopened"},
		},
		{
			name: "task failed uses failure mark",
			event: agentloop.Event{
				Turn: 6, Kind: agentloop.EventTaskStatus, Name: "triage",
				Status: string(agentloop.TaskFailed), Reason: "cannot reach host",
			},
			want: []string{"✗", "task triage failed", "cannot reach host"},
		},
		{
			name: "question",
			event: agentloop.Event{
				Turn: 7, Kind: agentloop.EventAskUser, Name: ir.AskUserStepName,
				Reason: "Which region?",
			},
			want: []string{"asked", "Which region?"},
		},
		{
			name: "rejected call",
			event: agentloop.Event{
				Turn: 8, Kind: agentloop.EventRejected, Name: "unknown_tool", Reason: "no such tool",
			},
			want: []string{"rejected unknown_tool", "no such tool"},
		},
		{
			name:  "stalled turn",
			event: agentloop.Event{Turn: 9, Kind: agentloop.EventStalled, Reason: "no action chosen"},
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
	assert.Equal(t, "", eventDuration(agentloop.Event{StartedAt: "2026-08-02T16:31:21+09:00"}))
	assert.Equal(t, "", eventDuration(agentloop.Event{
		StartedAt: "not a time", FinishedAt: "2026-08-02T16:31:23+09:00",
	}))
	assert.NotEqual(t, "", eventDuration(agentloop.Event{
		StartedAt: "2026-08-02T16:31:21+09:00", FinishedAt: "2026-08-02T16:31:23+09:00",
	}))
}

func TestAgentDAGProgressDisplay_OpenTasksText(t *testing.T) {
	// Before any agent state arrives, every declared task is open.
	display, _ := newTestAgentDisplay(&ir.DAG{
		Name: "triage", Type: ir.TypeAgent,
		Tasks: []ir.AgentTask{{Name: "a"}, {Name: "b"}},
	})
	assert.Equal(t, "2 tasks open", display.openTasksText())

	display.state = agentloop.State{Tasks: []agentloop.TaskState{
		{Name: "a", Status: agentloop.TaskOpen},
		{Name: "b", Status: agentloop.TaskCompleted},
	}}
	assert.Equal(t, "1 task open", display.openTasksText())

	display.state.Tasks[1].Status = agentloop.TaskOpen
	assert.Equal(t, "2 tasks open", display.openTasksText())
}
