// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package controller implements the decision layer of a controller DAG: the
// catalog of actions offered to the LLM, the goal state it works against, and
// the planner that turns a conversation into the next action.
package controller

import (
	"encoding/json"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// TaskStatus is where a goal stands. A run ends once no task is open.
type TaskStatus string

const (
	// TaskOpen is a goal still to be settled. Tasks start here.
	TaskOpen TaskStatus = "open"
	// TaskCompleted is a goal that was achieved.
	TaskCompleted TaskStatus = "completed"
	// TaskSkipped is a goal the controller judged unnecessary. It does not fail
	// the run: nothing went wrong, there was simply nothing to do.
	TaskSkipped TaskStatus = "skipped"
	// TaskFailed is a goal that cannot be achieved. It fails the run, because
	// the goal was neither met nor waived.
	TaskFailed TaskStatus = "failed"
)

// ValidTaskStatus reports whether a value names a task status the controller may
// set. "open" is included: settling a task can be undone.
func ValidTaskStatus(value string) bool {
	switch TaskStatus(value) {
	case TaskOpen, TaskCompleted, TaskSkipped, TaskFailed:
		return true
	default:
		return false
	}
}

// TaskState tracks one goal across the lifetime of a controller run.
type TaskState struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Status      TaskStatus `json:"status,omitempty"`
	// Reason is the justification the controller gave for the current status.
	Reason string `json:"reason,omitempty"`

	// Done is the status this field used to carry. It is read when restoring a
	// run that was suspended by an earlier version, and never written.
	Done bool `json:"done,omitempty"`
}

// settled reports whether the task no longer needs the controller's attention.
func (t TaskState) settled() bool {
	return t.Status != TaskOpen && t.Status != ""
}

// PendingAction records the tool call whose observation has not been reported
// back to the LLM yet. It is set while a chosen step runs so that a run which
// suspends mid-action can report the outcome after it resumes.
type PendingAction struct {
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Step       string `json:"step"`
	// Question is the text put to a person, set when the pending action is a
	// question the controller asked.
	Question string `json:"question,omitempty"`
}

// Event kinds recorded on the controller's decision timeline.
const (
	// EventAction is one run of a declared step.
	EventAction = "action"
	// EventTaskStatus records the controller settling, or reopening, a task.
	// The new status is carried on the event.
	EventTaskStatus = "task_status"
	// EventAskUser is a question the controller put to a person.
	EventAskUser = "ask_user"
	// EventRejected is a tool call the controller could not carry out.
	EventRejected = "rejected"
	// EventStalled is a turn where the model declined to act.
	EventStalled = "stalled"
)

// Event is one entry on the controller's decision timeline. The timeline is what
// makes a controller run legible: it records what ran, in what order, and when
// each task was satisfied, none of which a dependency graph can express.
type Event struct {
	Turn int    `json:"turn"`
	Kind string `json:"kind"`
	// Name is the step or task the event concerns.
	Name string `json:"name,omitempty"`
	// Status is the resulting node status, for action events.
	Status string `json:"status,omitempty"`
	// Attempt counts this run of the step, starting at 1.
	Attempt int `json:"attempt,omitempty"`
	// Reason carries the controller's justification, or why a call was rejected.
	Reason     string `json:"reason,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
	// ChildDAGRunID identifies the child run this action produced, so the
	// timeline can link straight to it. Empty for steps that run no child DAG.
	ChildDAGRunID string `json:"childDagRunId,omitempty"`
	// ChildDAGName is the child DAG that ran.
	ChildDAGName string `json:"childDagName,omitempty"`
}

// State is the controller's durable memory. It survives suspension because it is
// persisted on the controller node and carried into the resumed run.
type State struct {
	Tasks []TaskState `json:"tasks"`
	// Events is the decision timeline, in the order decisions were made.
	Events []Event `json:"events,omitempty"`
	// StepRuns counts how many times each step has been started.
	StepRuns map[string]int `json:"stepRuns,omitempty"`
	// Turns counts LLM decisions made so far.
	Turns int `json:"turns,omitempty"`
	// Pending is set while an action is in flight.
	Pending *PendingAction `json:"pending,omitempty"`
	// Nudges counts consecutive turns where the LLM declined to act while tasks
	// were still open.
	Nudges int `json:"nudges,omitempty"`
	// Answers records what a person replied to each question, so the controller
	// is held to an answer it already has.
	Answers map[string]string `json:"answers,omitempty"`
	// ObservationAging records that old tool results must remain compacted for
	// the rest of this run.
	ObservationAging bool `json:"observationAging,omitempty"`

	// messages is the conversation. It is persisted separately, as the node's
	// chat transcript, so the UI can render it with the other LLM steps.
	messages []dagrun.LLMMessage
}

// NewState builds the initial state for a controller DAG.
func NewState(dag *ir.DAG) *State {
	tasks := make([]TaskState, 0, len(dag.Tasks))
	for _, task := range dag.Tasks {
		tasks = append(tasks, TaskState{
			Name:        task.Name,
			Description: task.Description,
			Status:      TaskOpen,
		})
	}
	return &State{Tasks: tasks, StepRuns: map[string]int{}}
}

// LoadState restores state persisted by an earlier attempt of the same run and
// reconciles it with the DAG, so that editing the task list between attempts
// neither drops progress nor resurrects removed tasks.
func LoadState(raw json.RawMessage, messages []dagrun.LLMMessage, dag *ir.DAG) (*State, error) {
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
		prev, ok := progress[task.Name]
		if !ok {
			continue
		}
		fresh.Tasks[i].Status = prev.Status
		fresh.Tasks[i].Reason = prev.Reason
		if fresh.Tasks[i].Status == "" {
			// Restored from a run suspended before statuses existed.
			fresh.Tasks[i].Status = TaskOpen
			if prev.Done {
				fresh.Tasks[i].Status = TaskCompleted
			}
		}
	}

	fresh.Events = stored.Events
	fresh.Answers = stored.Answers
	fresh.ObservationAging = stored.ObservationAging
	fresh.Turns = stored.Turns
	fresh.Nudges = stored.Nudges
	fresh.Pending = stored.Pending
	if stored.StepRuns != nil {
		fresh.StepRuns = stored.StepRuns
	}
	fresh.messages = messages
	return fresh, nil
}

// RecordAnswer stores a reply so the same question is not put to a person twice.
func (s *State) RecordAnswer(question, answer string) {
	if question == "" {
		return
	}
	if s.Answers == nil {
		s.Answers = map[string]string{}
	}
	s.Answers[question] = answer
}

// PriorAnswer returns what a person already said to this exact question.
func (s *State) PriorAnswer(question string) (string, bool) {
	answer, ok := s.Answers[question]
	return answer, ok
}

// QuestionCount reports how many questions have been answered so far.
func (s *State) QuestionCount() int {
	return len(s.Answers)
}

// RecordEvent appends an entry to the decision timeline, stamping it with the
// turn it belongs to.
func (s *State) RecordEvent(e Event) {
	e.Turn = s.Turns
	s.Events = append(s.Events, e)
}

// FinalizeEvent updates the most recent event for a step with the outcome it
// reached. An action that suspended was recorded as waiting, and only the run
// that resumes knows how it ended.
func (s *State) FinalizeEvent(step, status, finishedAt, reason string) {
	for i := len(s.Events) - 1; i >= 0; i-- {
		if s.Events[i].Name != step {
			continue
		}
		s.Events[i].Status = status
		s.Events[i].FinishedAt = finishedAt
		if reason != "" {
			s.Events[i].Reason = reason
		}
		return
	}
}

// EventsFromState decodes the decision timeline persisted on a controller node.
// Unreadable or absent state yields no events rather than an error.
func EventsFromState(raw json.RawMessage) []Event {
	if len(raw) == 0 {
		return nil
	}
	var stored State
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil
	}
	return stored.Events
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
func (s *State) Messages() []dagrun.LLMMessage {
	return s.messages
}

// Append adds a message to the conversation.
func (s *State) Append(msgs ...dagrun.LLMMessage) {
	s.messages = append(s.messages, msgs...)
}

// Settled reports whether no task is still open, which is what ends the run.
func (s *State) Settled() bool {
	for _, task := range s.Tasks {
		if !task.settled() {
			return false
		}
	}
	return true
}

// OpenTaskNames lists the tasks the controller has yet to settle.
func (s *State) OpenTaskNames() []string {
	var open []string
	for _, task := range s.Tasks {
		if !task.settled() {
			open = append(open, task.Name)
		}
	}
	return open
}

// FailedTasks lists the tasks the controller declared unachievable, with the
// reason it gave. A run with any of these has failed.
func (s *State) FailedTasks() []TaskState {
	var failed []TaskState
	for _, task := range s.Tasks {
		if task.Status == TaskFailed {
			failed = append(failed, task)
		}
	}
	return failed
}

// SetTaskStatus records where a task now stands. Naming an unknown task, or
// restating the status a task already has, is reported back to the controller as
// a tool error rather than failing the run.
func (s *State) SetTaskStatus(name string, status TaskStatus, reason string) error {
	for i, task := range s.Tasks {
		if task.Name != name {
			continue
		}
		if task.Status == status {
			return fmt.Errorf("task %q is already %s", name, status)
		}
		s.Tasks[i].Status = status
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
