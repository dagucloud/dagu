// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package transform_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusSerialization(t *testing.T) {
	startedAt, finishedAt := time.Now(), time.Now().Add(time.Second*1)
	dag := &core.DAG{
		HandlerOn: core.HandlerOn{},
		Steps: []core.Step{
			{
				Name: "1", Description: "",
				Dir: "dir", Commands: []core.CommandEntry{{Command: "echo", Args: []string{"1"}}},
				Depends: []string{}, ContinueOn: core.ContinueOn{},
				RetryPolicy: core.RetryPolicy{}, MailOnError: false,
				RepeatPolicy: core.RepeatPolicy{}, Preconditions: []*core.Condition{},
			},
		},
		MailOn:    &core.MailOn{},
		ErrorMail: &core.MailConfig{},
		InfoMail:  &core.MailConfig{},
		SMTP:      &core.SMTPConfig{},
	}
	dagRunID := uuid.Must(uuid.NewV7()).String()
	statusToPersist := transform.NewStatusBuilder(dag).Create(dagRunID, core.Succeeded, 0, startedAt, transform.WithFinishedAt(finishedAt))

	rawJSON, err := json.Marshal(statusToPersist)
	require.NoError(t, err)

	statusObject, err := exec.StatusFromJSON(string(rawJSON))
	require.NoError(t, err)

	require.Equal(t, statusToPersist.Name, statusObject.Name)
	require.Equal(t, 1, len(statusObject.Nodes))
	require.Equal(t, dag.Steps[0].Name, statusObject.Nodes[0].Step.Name)
}

func TestStatusBuilder(t *testing.T) {
	dag := &core.DAG{
		Name: "test-dag",
		HandlerOn: core.HandlerOn{
			Exit:    &core.Step{Name: "exit-handler"},
			Success: &core.Step{Name: "success-handler"},
			Failure: &core.Step{Name: "failure-handler"},
			Abort:   &core.Step{Name: "abort-handler"},
		},
		Steps: []core.Step{
			{Name: "step1", Commands: []core.CommandEntry{{Command: "echo", Args: []string{"hello"}}}},
			{Name: "step2", Commands: []core.CommandEntry{{Command: "echo", Args: []string{"world"}}}},
		},
		Params: []string{"param1", "param2"},
		Preconditions: []*core.Condition{
			{Condition: "test -f file.txt", Expected: "true"},
		},
	}

	builder := transform.NewStatusBuilder(dag)
	dagRunID := "test-run-123"
	s := core.Running
	pid := 12345
	startedAt := time.Now()

	// Test basic creation
	result := builder.Create(dagRunID, s, pid, startedAt)

	assert.Equal(t, dag.Name, result.Name)
	assert.Equal(t, dagRunID, result.DAGRunID)
	assert.Equal(t, s, result.Status)
	assert.Equal(t, exec.PID(pid), result.PID)
	assert.NotEmpty(t, result.StartedAt)
	assert.Equal(t, 2, len(result.Nodes))
	assert.NotNil(t, result.OnExit)
	assert.NotNil(t, result.OnSuccess)
	assert.NotNil(t, result.OnFailure)
	assert.NotNil(t, result.OnAbort)
	assert.Equal(t, "param1 param2", result.Params)
	assert.Equal(t, dag.Params, result.ParamsList)
	assert.Equal(t, dag.Preconditions, result.Preconditions)
}

func TestStatusBuilderWithOptions(t *testing.T) {
	dag := &core.DAG{
		Name: "test-dag",
		Steps: []core.Step{
			{Name: "step1"},
		},
	}

	builder := transform.NewStatusBuilder(dag)
	dagRunID := "test-run-456"
	s := core.Succeeded
	pid := 54321
	startedAt := time.Now()
	finishedAt := startedAt.Add(5 * time.Minute)

	// Create nodes for options
	nodes := []runtime.NodeData{
		{
			Step: core.Step{Name: "step1"},
			State: runtime.NodeState{
				Status:     core.NodeSucceeded,
				StartedAt:  startedAt,
				FinishedAt: finishedAt,
			},
		},
	}

	exitNode := runtime.NewNode(core.Step{Name: "exit-step"}, runtime.NodeState{})
	successNode := runtime.NewNode(core.Step{Name: "success-step"}, runtime.NodeState{})
	failureNode := runtime.NewNode(core.Step{Name: "failure-step"}, runtime.NodeState{})
	abortNode := runtime.NewNode(core.Step{Name: "abort-step"}, runtime.NodeState{})

	rootRef := exec.NewDAGRunRef("root-dag", "root-run-123")
	parentRef := exec.NewDAGRunRef("parent-dag", "parent-run-456")

	// Test with all options
	result := builder.Create(
		dagRunID,
		s,
		pid,
		startedAt,
		transform.WithFinishedAt(finishedAt),
		transform.WithNodes(nodes),
		transform.WithOnExitNode(exitNode),
		transform.WithOnSuccessNode(successNode),
		transform.WithOnFailureNode(failureNode),
		transform.WithOnAbortNode(abortNode),
		transform.WithLogFilePath("/tmp/log.txt"),
		transform.WithWorkingDir("/tmp/work"),
		transform.WithPreconditions([]*core.Condition{{Condition: "test", Expected: "true"}}),
		transform.WithHierarchyRefs(rootRef, parentRef),
		transform.WithAttemptID("attempt-789"),
		transform.WithQueuedAt("2024-01-01 12:00:00"),
		transform.WithCreatedAt(1234567890),
		transform.WithWorkerID("worker-abc"),
	)

	assert.Equal(t, stringutil.FormatTime(finishedAt), result.FinishedAt)
	assert.Equal(t, 1, len(result.Nodes))
	assert.Equal(t, "exit-step", result.OnExit.Step.Name)
	assert.Equal(t, "success-step", result.OnSuccess.Step.Name)
	assert.Equal(t, "failure-step", result.OnFailure.Step.Name)
	assert.Equal(t, "abort-step", result.OnAbort.Step.Name)
	assert.Equal(t, "/tmp/log.txt", result.Log)
	assert.Equal(t, "/tmp/work", result.WorkingDir)
	assert.Equal(t, 1, len(result.Preconditions))
	assert.Equal(t, rootRef, result.Root)
	assert.Equal(t, parentRef, result.Parent)
	assert.Equal(t, "attempt-789", result.AttemptID)
	assert.Equal(t, "2024-01-01 12:00:00", result.QueuedAt)
	assert.Equal(t, int64(1234567890), result.CreatedAt)
	assert.Equal(t, "worker-abc", result.WorkerID)
}

func TestStatusBuilderPopulatesPendingStepRetriesFromNodes(t *testing.T) {
	dag := &core.DAG{
		Name: "retrying-dag",
		Steps: []core.Step{
			{Name: "step1"},
		},
	}

	builder := transform.NewStatusBuilder(dag)
	result := builder.Create(
		"retry-run",
		core.Queued,
		123,
		time.Now(),
		transform.WithNodes([]runtime.NodeData{
			{
				Step: core.Step{
					Name: "step1",
					RetryPolicy: core.RetryPolicy{
						Interval: 2 * time.Second,
					},
				},
				State: runtime.NodeState{
					Status:     core.NodeRetrying,
					RetryCount: 1,
				},
			},
		}),
	)

	assert.Equal(t, []exec.PendingStepRetry{
		{StepName: "step1", Interval: 2 * time.Second},
	}, result.PendingStepRetries)
}

func TestStatusBuilderPendingStepRetriesOptionOverridesAutoDerivation(t *testing.T) {
	dag := &core.DAG{
		Name: "retrying-dag",
		Steps: []core.Step{
			{Name: "step1"},
		},
	}

	builder := transform.NewStatusBuilder(dag)
	result := builder.Create(
		"retry-run",
		core.Queued,
		123,
		time.Now(),
		transform.WithNodes([]runtime.NodeData{
			{
				Step: core.Step{
					Name: "step1",
					RetryPolicy: core.RetryPolicy{
						Interval: 2 * time.Second,
					},
				},
				State: runtime.NodeState{
					Status:     core.NodeRetrying,
					RetryCount: 1,
				},
			},
		}),
		transform.WithPendingStepRetries([]exec.PendingStepRetry{}),
	)

	assert.NotNil(t, result.PendingStepRetries)
	assert.Empty(t, result.PendingStepRetries)
}

func TestStatusBuilderPopulatesPendingStepRetriesFromHandlerNodes(t *testing.T) {
	dag := &core.DAG{
		Name: "retrying-dag",
		HandlerOn: core.HandlerOn{
			Failure: &core.Step{Name: "onFailure"},
		},
	}

	failureHandler := runtime.NewNode(
		core.Step{
			Name: "onFailure",
			RetryPolicy: core.RetryPolicy{
				Interval: 3 * time.Second,
			},
		},
		runtime.NodeState{
			Status:     core.NodeRetrying,
			RetryCount: 1,
		},
	)

	builder := transform.NewStatusBuilder(dag)
	result := builder.Create(
		"retry-run",
		core.Queued,
		123,
		time.Now(),
		transform.WithOnFailureNode(failureHandler),
	)

	assert.Equal(t, []exec.PendingStepRetry{
		{StepName: "onFailure", Interval: 3 * time.Second},
	}, result.PendingStepRetries)
}

func TestInitialStatus(t *testing.T) {
	dag := &core.DAG{
		Name: "initial-test",
		HandlerOn: core.HandlerOn{
			Exit:    &core.Step{Name: "exit"},
			Success: &core.Step{Name: "success"},
			Failure: &core.Step{Name: "failure"},
			Abort:   &core.Step{Name: "abort"},
		},
		Steps: []core.Step{
			{Name: "step1"},
			{Name: "step2"},
		},
		Params: []string{"arg1", "arg2"},
		Preconditions: []*core.Condition{
			{Condition: "test condition"},
		},
	}

	st := exec.InitialStatus(dag)

	assert.Equal(t, dag.Name, st.Name)
	assert.Equal(t, core.NotStarted, st.Status)
	assert.Equal(t, exec.PID(0), st.PID)
	assert.Equal(t, 2, len(st.Nodes))
	assert.NotNil(t, st.OnExit)
	assert.NotNil(t, st.OnSuccess)
	assert.NotNil(t, st.OnFailure)
	assert.NotNil(t, st.OnAbort)
	assert.Equal(t, "arg1 arg2", st.Params)
	assert.Equal(t, dag.Params, st.ParamsList)
	assert.Equal(t, dag.Preconditions, st.Preconditions)
	assert.NotZero(t, st.CreatedAt)
	assert.Equal(t, "", st.StartedAt)
	assert.Equal(t, "", st.FinishedAt)
}

func TestStatusFromJSONError(t *testing.T) {
	// Test with invalid JSON
	_, err := exec.StatusFromJSON("invalid json")
	assert.Error(t, err)

	// Test with empty string
	_, err = exec.StatusFromJSON("")
	assert.Error(t, err)
}

func TestDAGRunStatus_DAGRun(t *testing.T) {
	dagRunStatus := &exec.DAGRunStatus{
		Name:     "test-dag",
		DAGRunID: "run-123",
	}

	dagRun := dagRunStatus.DAGRun()
	assert.Equal(t, "test-dag", dagRun.Name)
	assert.Equal(t, "run-123", dagRun.ID)
}

func TestDAGRunStatus_Errors(t *testing.T) {
	dagRunStatus := &exec.DAGRunStatus{
		Nodes: []*exec.Node{
			{Step: core.Step{Name: "step1"}, Error: "error1"},
			{Step: core.Step{Name: "step2"}, Error: ""},
			{Step: core.Step{Name: "step3"}, Error: "error3"},
		},
		OnExit:    &exec.Node{Step: core.Step{Name: "exit"}, Error: "exit error"},
		OnSuccess: &exec.Node{Step: core.Step{Name: "success"}, Error: ""},
		OnFailure: &exec.Node{Step: core.Step{Name: "failure"}, Error: "failure error"},
		OnAbort:   &exec.Node{Step: core.Step{Name: "cancel"}, Error: "cancel error"},
	}

	errors := dagRunStatus.Errors()
	assert.Equal(t, 5, len(errors))
	assert.Contains(t, errors[0].Error(), "node step1: error1")
	assert.Contains(t, errors[1].Error(), "node step3: error3")
	assert.Contains(t, errors[2].Error(), "onExit: exit error")
	assert.Contains(t, errors[3].Error(), "onFailure: failure error")
	assert.Contains(t, errors[4].Error(), "onAbort: cancel error")
}

func TestDAGRunStatus_NodeByName(t *testing.T) {
	dagRunStatus := &exec.DAGRunStatus{
		Nodes: []*exec.Node{
			{Step: core.Step{Name: "step1"}},
			{Step: core.Step{Name: "step2"}},
		},
		OnExit:    &exec.Node{Step: core.Step{Name: "exit"}},
		OnSuccess: &exec.Node{Step: core.Step{Name: "success"}},
		OnFailure: &exec.Node{Step: core.Step{Name: "failure"}},
		OnAbort:   &exec.Node{Step: core.Step{Name: "cancel"}},
	}

	// Test finding regular nodes
	node, err := dagRunStatus.NodeByName("step1")
	assert.NoError(t, err)
	assert.Equal(t, "step1", node.Step.Name)

	// Test finding handler nodes
	node, err = dagRunStatus.NodeByName("exit")
	assert.NoError(t, err)
	assert.Equal(t, "exit", node.Step.Name)

	node, err = dagRunStatus.NodeByName("success")
	assert.NoError(t, err)
	assert.Equal(t, "success", node.Step.Name)

	// Test node not found
	_, err = dagRunStatus.NodeByName("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node nonexistent not found")
}

func TestPID_String(t *testing.T) {
	tests := []struct {
		name     string
		pid      exec.PID
		expected string
	}{
		{
			name:     "PositivePID",
			pid:      exec.PID(12345),
			expected: "12345",
		},
		{
			name:     "ZeroPID",
			pid:      exec.PID(0),
			expected: "",
		},
		{
			name:     "NegativePID",
			pid:      exec.PID(-1),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.pid.String())
		})
	}
}

func TestNewNodesFromSteps(t *testing.T) {
	steps := []core.Step{
		{
			Name:        "step1",
			Commands:    []core.CommandEntry{{Command: "echo", Args: []string{"hello"}}},
			Description: "First step",
		},
		{
			Name:        "step2",
			Commands:    []core.CommandEntry{{Command: "echo", Args: []string{"world"}}},
			Description: "Second step",
		},
	}

	nodes := exec.NewNodesFromSteps(steps)

	assert.Equal(t, 2, len(nodes))
	assert.Equal(t, "step1", nodes[0].Step.Name)
	assert.Equal(t, "step2", nodes[1].Step.Name)
	assert.Equal(t, core.NodeNotStarted, nodes[0].Status)
	assert.Equal(t, core.NodeNotStarted, nodes[1].Status)
}

func TestWithCreatedAtDefaultTime(t *testing.T) {
	dag := &core.DAG{Name: "test"}
	dagRunStatus := exec.InitialStatus(dag)

	// Test WithCreatedAt with 0 - should use current time
	beforeTime := time.Now().UnixMilli()
	transform.WithCreatedAt(0)(&dagRunStatus)
	afterTime := time.Now().UnixMilli()

	assert.GreaterOrEqual(t, dagRunStatus.CreatedAt, beforeTime)
	assert.LessOrEqual(t, dagRunStatus.CreatedAt, afterTime)
}
