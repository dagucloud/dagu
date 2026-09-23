// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package humantask

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const pushBackFeedbackForm = `{
  "type":"object",
  "properties":{
    "feedback":{"type":"string"},
    "priority":{"type":"integer","default":2}
  },
  "required":["feedback"],
  "additionalProperties":false
}`

// newPushBackFixture builds setup -> implement -> test -> Review -> publish,
// plus notify beside implement, where Review rewinds to implement.
func newPushBackFixture(t *testing.T) *serviceFixture {
	t.Helper()
	fixture := newServiceFixture(t, nil)
	review := fixture.status.Nodes[0]
	review.Step.Depends = []string{"test"}
	review.Step.HumanTask.PushBack = &ir.HumanTaskPushBackConfig{
		RewindTo: "implement",
		Form:     json.RawMessage(pushBackFeedbackForm),
	}
	implement := stepNode("implement", ir.NodeSucceeded, "setup")
	implement.Stdout = "/logs/implement.out"
	fixture.status.Nodes = []*ir.Node{
		stepNode("setup", ir.NodeSucceeded),
		implement,
		stepNode("test", ir.NodeSucceeded, "implement"),
		review,
		stepNode("publish", ir.NodeNotStarted, "Review"),
		stepNode("notify", ir.NodeSucceeded, "setup"),
	}
	return fixture
}

func (f *serviceFixture) pushBackReview(t *testing.T, values map[string]any, expectedIteration *int) (PushBackResult, error) {
	t.Helper()
	return f.service.PushBack(t.Context(), PushBackRequest{
		DAGName:           f.dag.Name,
		DAGRunID:          f.status.DAGRunID,
		StepID:            "review",
		Input:             Input{Values: values},
		ExpectedIteration: expectedIteration,
		By:                "alice",
		ByID:              "user-1",
	})
}

func (f *serviceFixture) node(t *testing.T, name string) *ir.Node {
	t.Helper()
	for _, node := range f.status.Nodes {
		if node.Step.Name == name {
			return node
		}
	}
	require.FailNow(t, "node not found", name)
	return nil
}

// reopenReview marks the reset steps as done again with Review open, as the
// resumed run leaves them at the next checkpoint.
func (f *serviceFixture) reopenReview(t *testing.T) {
	t.Helper()
	f.status.Status = ir.Waiting
	for _, name := range []string{"implement", "test"} {
		f.node(t, name).Status = ir.NodeSucceeded
	}
	f.node(t, "Review").Status = ir.NodeWaiting
}

func TestPushBackResetsRewoundStepsAndQueuesResume(t *testing.T) {
	fixture := newPushBackFixture(t)

	result, err := fixture.pushBackReview(t, map[string]any{"feedback": "add tests"}, new(0))
	require.NoError(t, err)
	assert.Equal(t, PushBackResult{
		DAGName: "deploy", DAGRunID: "run-1", StepID: "review", RewindTo: "implement",
		Iteration: 1, ResumeRequested: true, Queued: true,
	}, result)
	assert.Equal(t, ir.Queued, fixture.status.Status)
	assert.Equal(t, []ir.DAGRunRef{fixture.status.DAGRun()}, fixture.queue.enqueued)

	for _, name := range []string{"setup", "notify"} {
		node := fixture.node(t, name)
		assert.Equal(t, ir.NodeSucceeded, node.Status, name)
		assert.Zero(t, node.ApprovalIteration, name)
	}
	feedback := map[string]string{"feedback": "add tests", "priority": "2"}
	wantHistory := []ir.PushBackEntry{{
		Iteration: 1, By: "alice", ByID: "user-1", At: "2026-07-21T01:02:03Z", Inputs: feedback,
		Step: "Review", HumanTask: true,
	}}
	for _, name := range []string{"implement", "test", "Review", "publish"} {
		node := fixture.node(t, name)
		assert.Equal(t, ir.NodeNotStarted, node.Status, name)
		assert.Equal(t, 1, node.ApprovalIteration, name)
		assert.Equal(t, feedback, node.PushBackInputs, name)
		assert.Equal(t, wantHistory, node.PushBackHistory, name)
	}
	assert.Equal(t, "/logs/implement.out", fixture.node(t, "implement").PushBackPreviousStdout)
}

func TestPushBackAccumulatesHistoryAcrossIterations(t *testing.T) {
	fixture := newPushBackFixture(t)
	_, err := fixture.pushBackReview(t, map[string]any{"feedback": "add tests"}, nil)
	require.NoError(t, err)
	fixture.reopenReview(t)

	result, err := fixture.pushBackReview(t, map[string]any{"feedback": "rename it", "priority": json.Number("1")}, new(1))
	require.NoError(t, err)
	assert.Equal(t, 2, result.Iteration)

	implement := fixture.node(t, "implement")
	assert.Equal(t, 2, implement.ApprovalIteration)
	assert.Equal(t, map[string]string{"feedback": "rename it", "priority": "1"}, implement.PushBackInputs)
	require.Len(t, implement.PushBackHistory, 2)
	assert.Equal(t, map[string]string{"feedback": "add tests", "priority": "2"}, implement.PushBackHistory[0].Inputs)
	assert.Equal(t, 2, implement.PushBackHistory[1].Iteration)
	assert.Len(t, fixture.queue.enqueued, 2)
}

func TestPushBackRejectsStaleExpectedIteration(t *testing.T) {
	fixture := newPushBackFixture(t)
	before := mustStatusJSON(t, fixture.status)

	_, err := fixture.pushBackReview(t, map[string]any{"feedback": "add tests"}, new(1))
	require.Error(t, err)
	assert.Equal(t, ErrorConflict, KindOf(err))
	assert.ErrorContains(t, err, `human task step "review" is at push-back iteration 0, not 1`)
	assert.JSONEq(t, before, mustStatusJSON(t, fixture.status))
	assert.Empty(t, fixture.queue.enqueued)
}

// A push-back from another task can reset this task. Its iteration must still
// move forward, so a page kept open from its earlier review stays stale.
func TestPushBackKeepsStalePagesStaleAcrossTasks(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	task := func(id, depends, rewindTo string, iteration int) *ir.Node {
		return &ir.Node{
			Step: ir.Step{
				ID: id, Name: id, Depends: []string{depends},
				HumanTask: &ir.HumanTaskConfig{Prompt: id, PushBack: &ir.HumanTaskPushBackConfig{RewindTo: rewindTo}},
			},
			Status:            ir.NodeWaiting,
			ApprovalIteration: iteration,
		}
	}
	docs := stepNode("docs", ir.NodeSucceeded, "implement")
	docs.ApprovalIteration = 2
	fixture.status.Nodes = []*ir.Node{
		stepNode("implement", ir.NodeSucceeded),
		docs,
		task("code_review", "implement", "implement", 0),
		task("docs_review", "docs", "docs", 2),
	}

	result, err := fixture.service.PushBack(t.Context(), PushBackRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "code_review", ExpectedIteration: new(0),
	})
	require.NoError(t, err)
	assert.Equal(t, 3, result.Iteration)
	assert.Equal(t, 3, fixture.node(t, "docs_review").ApprovalIteration)

	fixture.status.Status = ir.Waiting
	for _, name := range []string{"implement", "docs"} {
		fixture.node(t, name).Status = ir.NodeSucceeded
	}
	fixture.node(t, "docs_review").Status = ir.NodeWaiting
	_, err = fixture.service.PushBack(t.Context(), PushBackRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "docs_review", ExpectedIteration: new(1),
	})
	require.Error(t, err)
	assert.Equal(t, ErrorConflict, KindOf(err))
	assert.ErrorContains(t, err, "at push-back iteration 3, not 1")
}

// A push-back can move a task's iteration by more than one when a reset step
// was already ahead. Retrying it must still accept the iteration it started
// from, and only that one.
func TestPushBackRepeatAcceptsStartingIterationAfterJump(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	docs := stepNode("docs", ir.NodeSucceeded, "implement")
	docs.ApprovalIteration = 2
	fixture.status.Nodes = []*ir.Node{
		stepNode("implement", ir.NodeSucceeded),
		docs,
		{
			Step: ir.Step{
				ID: "code_review", Name: "code_review", Depends: []string{"docs"},
				HumanTask: &ir.HumanTaskConfig{Prompt: "Review", PushBack: &ir.HumanTaskPushBackConfig{RewindTo: "implement"}},
			},
			Status: ir.NodeWaiting,
		},
	}
	fixture.queue.enqueueErrors = []error{errors.New("queue unavailable")}
	request := PushBackRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "code_review", ExpectedIteration: new(0),
	}

	result, err := fixture.service.PushBack(t.Context(), request)
	var queueFailure *PushBackQueueError
	require.ErrorAs(t, err, &queueFailure)
	assert.Equal(t, 3, result.Iteration)

	request.ExpectedIteration = new(2)
	_, err = fixture.service.PushBack(t.Context(), request)
	require.Error(t, err)
	assert.ErrorContains(t, err, "already pushed back at iteration 3")

	request.ExpectedIteration = new(0)
	result, err = fixture.service.PushBack(t.Context(), request)
	require.NoError(t, err)
	assert.True(t, result.AlreadyPushedBack)
	assert.True(t, result.Queued)
	assert.Equal(t, 3, result.Iteration)
}

func TestPushBackValidatesRequest(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*testing.T, *serviceFixture)
		values  map[string]any
		coerce  bool
		expect  *int
		kind    ErrorKind
		message string
	}{
		{
			name:    "missing required feedback",
			values:  map[string]any{},
			kind:    ErrorInvalid,
			message: `invalid feedback for human task step "review"`,
		},
		{
			name:    "undeclared feedback",
			values:  map[string]any{"feedback": "add tests", "PATH": "/tmp"},
			kind:    ErrorInvalid,
			message: "PATH",
		},
		{
			name:    "string coercion follows the form",
			values:  map[string]any{"feedback": "add tests", "priority": "high"},
			coerce:  true,
			kind:    ErrorInvalid,
			message: "priority",
		},
		{
			name: "task without push_back",
			mutate: func(t *testing.T, f *serviceFixture) {
				f.node(t, "Review").Step.HumanTask.PushBack = nil
			},
			values:  map[string]any{"feedback": "add tests"},
			kind:    ErrorInvalid,
			message: `human task step "review" does not declare with.push_back`,
		},
		{
			name: "input without feedback form",
			mutate: func(t *testing.T, f *serviceFixture) {
				f.node(t, "Review").Step.HumanTask.PushBack.Form = nil
			},
			values:  map[string]any{"feedback": "add tests"},
			kind:    ErrorInvalid,
			message: "does not accept input",
		},
		{
			name: "feedback above max output size",
			mutate: func(_ *testing.T, f *serviceFixture) {
				f.dag.MaxOutputSize = 16
			},
			values:  map[string]any{"feedback": strings.Repeat("x", 32)},
			kind:    ErrorInvalid,
			message: "maximum size",
		},
		{
			name:    "feedback above push-back input limit",
			values:  map[string]any{"feedback": strings.Repeat("x", dagrun.MaxPushBackInputsSize)},
			kind:    ErrorInvalid,
			message: "maximum size of 16384 bytes",
		},
		{
			name:    "negative expected iteration",
			values:  map[string]any{"feedback": "add tests"},
			expect:  new(-1),
			kind:    ErrorInvalid,
			message: "must not be negative",
		},
		{
			name: "completed task",
			mutate: func(t *testing.T, f *serviceFixture) {
				review := f.node(t, "Review")
				review.Status = ir.NodeSucceeded
				review.HumanTaskInput = json.RawMessage(`{}`)
			},
			values:  map[string]any{"feedback": "add tests"},
			kind:    ErrorConflict,
			message: `human task step "review" was already completed`,
		},
		{
			name: "run not waiting",
			mutate: func(_ *testing.T, f *serviceFixture) {
				f.status.Status = ir.Running
			},
			values:  map[string]any{"feedback": "add tests"},
			kind:    ErrorConflict,
			message: "is not waiting",
		},
		{
			name: "task not open",
			mutate: func(t *testing.T, f *serviceFixture) {
				f.node(t, "Review").Status = ir.NodeNotStarted
			},
			values:  map[string]any{"feedback": "add tests"},
			kind:    ErrorConflict,
			message: `human task step "review" is not waiting`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPushBackFixture(t)
			if tt.mutate != nil {
				tt.mutate(t, fixture)
			}
			before := mustStatusJSON(t, fixture.status)

			_, err := fixture.service.PushBack(t.Context(), PushBackRequest{
				DAGName:           fixture.dag.Name,
				DAGRunID:          fixture.status.DAGRunID,
				StepID:            "review",
				Input:             Input{Values: tt.values, CoerceStrings: tt.coerce},
				ExpectedIteration: tt.expect,
			})
			require.Error(t, err)
			assert.Equal(t, tt.kind, KindOf(err))
			assert.ErrorContains(t, err, tt.message)
			assert.JSONEq(t, before, mustStatusJSON(t, fixture.status))
			assert.Empty(t, fixture.queue.enqueued)
		})
	}
}

// A completion stored while the push-back is being applied wins, and the
// push-back changes nothing.
func TestPushBackLosesToConcurrentCompletion(t *testing.T) {
	fixture := newPushBackFixture(t)
	fixture.backend.beforeCompareAndSwap = func() {
		review := fixture.node(t, "Review")
		review.Status = ir.NodeSucceeded
		review.HumanTaskInput = json.RawMessage(`{}`)
	}

	_, err := fixture.pushBackReview(t, map[string]any{"feedback": "add tests"}, nil)
	require.Error(t, err)
	assert.Equal(t, ErrorConflict, KindOf(err))
	assert.ErrorContains(t, err, "already completed")
	assert.Equal(t, ir.NodeSucceeded, fixture.node(t, "implement").Status)
	assert.Empty(t, fixture.queue.enqueued)
}

func TestPushBackResumeGate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *serviceFixture)
		queued bool
	}{
		{
			name:   "nothing else waits",
			queued: true,
		},
		{
			name: "independent task waits",
			mutate: func(_ *testing.T, f *serviceFixture) {
				f.status.Nodes = append(f.status.Nodes, waitingHumanTaskNode("Other"))
			},
			queued: true,
		},
		{
			name: "failed step elsewhere while another task waits",
			mutate: func(_ *testing.T, f *serviceFixture) {
				f.status.Nodes = append(f.status.Nodes,
					waitingHumanTaskNode("Other"),
					stepNode("lint", ir.NodeFailed, "setup"),
				)
			},
		},
		{
			name: "rewind target with build inputs while another task waits",
			mutate: func(t *testing.T, f *serviceFixture) {
				f.status.Nodes = append(f.status.Nodes, waitingHumanTaskNode("Other"))
				f.node(t, "implement").Step.Inputs = []ir.StepInputDeclaration{{Name: "src", Path: "src"}}
			},
		},
		{
			name: "failed step elsewhere and nothing else waits",
			mutate: func(_ *testing.T, f *serviceFixture) {
				f.status.Nodes = append(f.status.Nodes, stepNode("lint", ir.NodeFailed, "setup"))
			},
			queued: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPushBackFixture(t)
			if tt.mutate != nil {
				tt.mutate(t, fixture)
			}

			result, err := fixture.pushBackReview(t, map[string]any{"feedback": "add tests"}, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.queued, result.ResumeRequested)
			assert.Equal(t, tt.queued, result.Queued)
			assert.Equal(t, ir.NodeNotStarted, fixture.node(t, "implement").Status)
			if tt.queued {
				assert.Equal(t, ir.Queued, fixture.status.Status)
				assert.Len(t, fixture.queue.enqueued, 1)
				return
			}
			assert.Equal(t, ir.Waiting, fixture.status.Status)
			assert.Empty(t, fixture.queue.enqueued)
		})
	}
}

// A push-back is stored before the resume is queued. When queueing fails the
// run is recoverable: resume is pending, and an identical repeat or a resume
// request queues it.
func TestPushBackStaysStoredWhenEnqueueFails(t *testing.T) {
	fixture := newPushBackFixture(t)
	queueErr := errors.New("queue unavailable")
	fixture.queue.enqueueErrors = []error{queueErr}

	result, err := fixture.pushBackReview(t, map[string]any{"feedback": "add tests"}, new(0))
	require.Error(t, err)
	var queueFailure *PushBackQueueError
	require.ErrorAs(t, err, &queueFailure)
	assert.ErrorIs(t, err, queueErr)
	assert.ErrorContains(t, err, `human task "review" was pushed back, but the DAG-run could not be queued for resume`)
	assert.Equal(t, result, queueFailure.Result)
	assert.True(t, result.ResumeRequested)
	assert.False(t, result.Queued)
	assert.Equal(t, ir.Waiting, fixture.status.Status)
	assert.Equal(t, ir.NodeNotStarted, fixture.node(t, "implement").Status)
	assert.True(t, ResumePending(fixture.status))
	assert.True(t, PushBackPending(fixture.status))

	resumed, err := fixture.service.Resume(t.Context(), fixture.dag.Name, fixture.status.DAGRunID)
	require.NoError(t, err)
	assert.True(t, resumed.Queued)
	assert.Equal(t, ir.Queued, fixture.status.Status)
	assert.Equal(t, 1, fixture.node(t, "Review").ApprovalIteration)
}

func TestPushBackIdenticalRepeatRetriesQueue(t *testing.T) {
	fixture := newPushBackFixture(t)
	fixture.queue.enqueueErrors = []error{errors.New("queue unavailable")}
	feedback := map[string]any{"feedback": "add tests"}
	_, err := fixture.pushBackReview(t, feedback, new(0))
	require.Error(t, err)

	result, err := fixture.pushBackReview(t, feedback, new(0))
	require.NoError(t, err)
	assert.Equal(t, PushBackResult{
		DAGName: "deploy", DAGRunID: "run-1", StepID: "review", RewindTo: "implement",
		Iteration: 1, AlreadyPushedBack: true, ResumeRequested: true, Queued: true,
	}, result)
	assert.Equal(t, ir.Queued, fixture.status.Status)
	assert.Len(t, fixture.node(t, "implement").PushBackHistory, 1)

	// Once the run has left waiting, a repeat never queues another resume.
	result, err = fixture.pushBackReview(t, feedback, nil)
	require.NoError(t, err)
	assert.True(t, result.AlreadyPushedBack)
	assert.False(t, result.ResumeRequested)
	assert.False(t, result.Queued)
	assert.Len(t, fixture.queue.enqueued, 1)
}

func TestPushBackRepeatRejectsDifferentRequest(t *testing.T) {
	tests := []struct {
		name     string
		feedback map[string]any
		expected *int
		message  string
	}{
		{
			name:     "different feedback",
			feedback: map[string]any{"feedback": "rename it"},
			message:  `human task step "review" was already pushed back with different feedback`,
		},
		{
			name:     "different expected iteration",
			feedback: map[string]any{"feedback": "add tests"},
			expected: new(1),
			message:  `human task step "review" was already pushed back at iteration 1 and has not opened again`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPushBackFixture(t)
			fixture.queue.enqueueErrors = []error{errors.New("queue unavailable")}
			_, err := fixture.pushBackReview(t, map[string]any{"feedback": "add tests"}, nil)
			require.Error(t, err)
			before := mustStatusJSON(t, fixture.status)

			_, err = fixture.pushBackReview(t, tt.feedback, tt.expected)
			require.Error(t, err)
			assert.Equal(t, ErrorConflict, KindOf(err))
			assert.ErrorContains(t, err, tt.message)
			assert.JSONEq(t, before, mustStatusJSON(t, fixture.status))
			assert.Empty(t, fixture.queue.enqueued)
		})
	}
}

// Another process can move the run on between the push-back and its queue
// request. A new attempt or queued run carries the push-back; a run that
// stopped does not, and saying it was queued would be wrong.
func TestPushBackReportsRunThatMovedOn(t *testing.T) {
	tests := []struct {
		name    string
		moveOn  func(*serviceFixture)
		message string
	}{
		{
			name: "another request queued the run",
			moveOn: func(f *serviceFixture) {
				f.status.Status = ir.Queued
			},
		},
		{
			name: "a resumed attempt started",
			moveOn: func(f *serviceFixture) {
				f.status.Status = ir.Running
				f.status.AttemptID = "attempt-2"
				f.status.AttemptKey = "key-2"
			},
		},
		{
			name: "the run was stopped",
			moveOn: func(f *serviceFixture) {
				f.status.Status = ir.Aborted
			},
			message: `human task "review" was pushed back, but the DAG-run left waiting (status: aborted)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPushBackFixture(t)
			calls := 0
			fixture.backend.beforeCompareAndSwap = func() {
				calls++
				if calls == 2 {
					tt.moveOn(fixture)
				}
			}

			result, err := fixture.pushBackReview(t, map[string]any{"feedback": "add tests"}, nil)
			assert.True(t, result.ResumeRequested)
			assert.False(t, result.Queued)
			assert.Empty(t, fixture.queue.enqueued)
			assert.Equal(t, 1, fixture.node(t, "Review").ApprovalIteration)
			if tt.message == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, ErrorConflict, KindOf(err))
			assert.ErrorContains(t, err, tt.message)
		})
	}
}

func TestPushBackDiscardsDownstreamCompletion(t *testing.T) {
	fixture := newPushBackFixture(t)
	outputs := `{"note":"ok"}`
	docs := &ir.Node{
		Step: ir.Step{
			ID: "docs", Name: "docs", Depends: []string{"implement"},
			HumanTask: &ir.HumanTaskConfig{Prompt: "Review docs"},
		},
		Status:           ir.NodeSucceeded,
		HumanTaskInput:   json.RawMessage(`{"note":"ok"}`),
		StepOutputsValue: &outputs,
	}
	fixture.status.Nodes = append(fixture.status.Nodes, docs)

	_, err := fixture.pushBackReview(t, map[string]any{"feedback": "add tests"}, nil)
	require.NoError(t, err)

	docs = fixture.node(t, "docs")
	assert.Equal(t, ir.NodeNotStarted, docs.Status)
	assert.Empty(t, docs.HumanTaskInput)
	assert.Nil(t, docs.StepOutputsValue)
	assert.Equal(t, 1, docs.ApprovalIteration)
}

func mustStatusJSON(t *testing.T, status *ir.DAGRunStatus) string {
	t.Helper()
	data, err := json.Marshal(status)
	require.NoError(t, err)
	return string(data)
}
