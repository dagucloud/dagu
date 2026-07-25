// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package controller implements the decision layer of a controller DAG: the
// catalog of actions offered to the LLM, the goal state it works against, and
// the planner that turns a conversation into the next action.
package controller

import (
	"encoding/json"
	"fmt"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// TaskState tracks one goal across the lifetime of a controller run.
type TaskState struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Done        bool   `json:"done,omitempty"`
	// Reason is the justification the controller gave when completing the task.
	Reason string `json:"reason,omitempty"`
}

// PendingAction records the tool call whose observation has not been reported
// back to the LLM yet. It is set while a chosen step runs so that a run which
// suspends mid-action can report the outcome after it resumes.
type PendingAction struct {
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Step       string `json:"step"`
}

// State is the controller's durable memory. It survives suspension because it is
// persisted on the controller node and carried into the resumed run.
type State struct {
	Tasks []TaskState `json:"tasks"`
	// StepRuns counts how many times each step has been started.
	StepRuns map[string]int `json:"stepRuns,omitempty"`
	// Turns counts LLM decisions made so far.
	Turns int `json:"turns,omitempty"`
	// Pending is set while an action is in flight.
	Pending *PendingAction `json:"pending,omitempty"`
	// Nudges counts consecutive turns where the LLM declined to act while tasks
	// were still open.
	Nudges int `json:"nudges,omitempty"`

	// messages is the conversation. It is persisted separately, as the node's
	// chat transcript, so the UI can render it with the other LLM steps.
	messages []exec.LLMMessage
}

// NewState builds the initial state for a controller DAG.
func NewState(dag *core.DAG) *State {
	tasks := make([]TaskState, 0, len(dag.Tasks))
	for _, task := range dag.Tasks {
		tasks = append(tasks, TaskState{Name: task.Name, Description: task.Description})
	}
	return &State{Tasks: tasks, StepRuns: map[string]int{}}
}

// LoadState restores state persisted by an earlier attempt of the same run and
// reconciles it with the DAG, so that editing the task list between attempts
// neither drops progress nor resurrects removed tasks.
func LoadState(raw json.RawMessage, messages []exec.LLMMessage, dag *core.DAG) (*State, error) {
	fresh := NewState(dag)
	if len(raw) == 0 {
		fresh.messages = messages
		return fresh, nil
	}

	var stored State
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("failed to restore controller state: %w", err)
	}

	progress := make(map[string]TaskState, len(stored.Tasks))
	for _, task := range stored.Tasks {
		progress[task.Name] = task
	}
	for i, task := range fresh.Tasks {
		if prev, ok := progress[task.Name]; ok {
			fresh.Tasks[i].Done = prev.Done
			fresh.Tasks[i].Reason = prev.Reason
		}
	}

	fresh.Turns = stored.Turns
	fresh.Nudges = stored.Nudges
	fresh.Pending = stored.Pending
	if stored.StepRuns != nil {
		fresh.StepRuns = stored.StepRuns
	}
	fresh.messages = messages
	return fresh, nil
}

// TasksFromState decodes the task progress persisted on a controller node.
// Unreadable or absent state yields no tasks rather than an error, so a display
// surface never fails on it.
func TasksFromState(raw json.RawMessage) []TaskState {
	if len(raw) == 0 {
		return nil
	}
	var stored State
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil
	}
	return stored.Tasks
}

// Marshal serializes the state for persistence on the controller node.
func (s *State) Marshal() (json.RawMessage, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("failed to persist controller state: %w", err)
	}
	return raw, nil
}

// Messages returns the conversation so far.
func (s *State) Messages() []exec.LLMMessage {
	return s.messages
}

// Append adds a message to the conversation.
func (s *State) Append(msgs ...exec.LLMMessage) {
	s.messages = append(s.messages, msgs...)
}

// AllDone reports whether every task has been completed.
func (s *State) AllDone() bool {
	for _, task := range s.Tasks {
		if !task.Done {
			return false
		}
	}
	return true
}

// OpenTaskNames lists the tasks still awaiting completion.
func (s *State) OpenTaskNames() []string {
	var open []string
	for _, task := range s.Tasks {
		if !task.Done {
			open = append(open, task.Name)
		}
	}
	return open
}

// CompleteTask marks a task done. Completing an unknown task, or one that is
// already done, is reported back to the controller as a tool error rather than
// failing the run.
func (s *State) CompleteTask(name, reason string) error {
	for i, task := range s.Tasks {
		if task.Name != name {
			continue
		}
		if task.Done {
			return fmt.Errorf("task %q is already complete", name)
		}
		s.Tasks[i].Done = true
		s.Tasks[i].Reason = reason
		return nil
	}
	return fmt.Errorf("unknown task %q; declared tasks are %v", name, s.taskNames())
}

func (s *State) taskNames() []string {
	names := make([]string, 0, len(s.Tasks))
	for _, task := range s.Tasks {
		names = append(names, task.Name)
	}
	return names
}

// StepRunCount reports how many times a step has been started in this run.
func (s *State) StepRunCount(step string) int {
	return s.StepRuns[step]
}

// RecordStepRun counts a step start and returns the new total.
func (s *State) RecordStepRun(step string) int {
	if s.StepRuns == nil {
		s.StepRuns = map[string]int{}
	}
	s.StepRuns[step]++
	return s.StepRuns[step]
}
