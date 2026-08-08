// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package transform_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/transform"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusSerialization(t *testing.T) {
	startedAt, finishedAt := time.Now(), time.Now().Add(time.Second*1)
	dag := &ir.DAG{
		HandlerOn: ir.HandlerOn{},
		Steps: []ir.Step{
			{
				Name: "1", Description: "",
				Dir: "dir", Commands: []ir.CommandEntry{{Command: "echo", Args: []string{"1"}}},
				Depends: []string{}, ContinueOn: ir.ContinueOn{},
				RetryPolicy: ir.RetryPolicy{}, MailOnError: false,
				RepeatPolicy: ir.RepeatPolicy{}, Preconditions: []*ir.Condition{},
			},
		},
		MailOn:    &ir.MailOn{},
		ErrorMail: &ir.MailConfig{},
		InfoMail:  &ir.MailConfig{},
		SMTP:      &ir.SMTPConfig{},
	}
	dagRunID := uuid.Must(uuid.NewV7()).String()
	statusToPersist := dagrun.NewStatusBuilder(dag).Create(dagRunID, ir.Succeeded, 0, startedAt, dagrun.WithFinishedAt(finishedAt))

	rawJSON, err := json.Marshal(statusToPersist)
	require.NoError(t, err)

	statusObject, err := dagrun.StatusFromJSON(string(rawJSON))
	require.NoError(t, err)

	require.Equal(t, statusToPersist.Name, statusObject.Name)
	require.Equal(t, 1, len(statusObject.Nodes))
	require.Equal(t, dag.Steps[0].Name, statusObject.Nodes[0].Step.Name)
}

func TestStatusBuilder(t *testing.T) {
	dag := &ir.DAG{
		Name: "test-dag",
		HandlerOn: ir.HandlerOn{
			Exit:    &ir.Step{Name: "exit-handler"},
			Success: &ir.Step{Name: "success-handler"},
			Failure: &ir.Step{Name: "failure-handler"},
			Abort:   &ir.Step{Name: "abort-handler"},
		},
		Steps: []ir.Step{
			{Name: "step1", Commands: []ir.CommandEntry{{Command: "echo", Args: []string{"hello"}}}},
			{Name: "step2", Commands: []ir.CommandEntry{{Command: "echo", Args: []string{"world"}}}},
		},
		Params: []string{"param1", "param2"},
		Preconditions: []*ir.Condition{
			{Condition: "test -f file.txt", Expected: "true"},
		},
	}

	builder := dagrun.NewStatusBuilder(dag)
	dagRunID := "test-run-123"
	s := ir.Running
	pid := 12345
	startedAt := time.Now()

	// Test basic creation
	result := builder.Create(dagRunID, s, pid, startedAt)

	assert.Equal(t, dag.Name, result.Name)
	assert.Equal(t, dagRunID, result.DAGRunID)
	assert.Equal(t, s, result.Status)
	assert.Equal(t, dagrun.PID(pid), result.PID)
	assert.NotEmpty(t, result.StartedAt)
	assert.Equal(t, 2, len(result.Nodes))
	assert.NotNil(t, result.OnExit)
	assert.NotNil(t, result.OnSuccess)
	assert.NotNil(t, result.OnFailure)
	assert.NotNil(t, result.OnAbort)
	assert.Equal(t, "param1 param2", result.Params)
	assert.Equal(t, dag.Params, result.ParamsList)
	assert.Equal(t, dagrun.NewConditionResults(dag.Preconditions), result.Preconditions)
}

func TestStatusBuilderWithOptions(t *testing.T) {
	dag := &ir.DAG{
		Name: "test-dag",
		Steps: []ir.Step{
			{Name: "step1"},
		},
	}

	builder := dagrun.NewStatusBuilder(dag)
	dagRunID := "test-run-456"
	s := ir.Succeeded
	pid := 54321
	startedAt := time.Now()
	finishedAt := startedAt.Add(5 * time.Minute)

	// Create nodes for options
	nodes := []runtime.NodeData{
		{
			Step: ir.Step{Name: "step1"},
			State: runtime.NodeState{
				Status:     ir.NodeSucceeded,
				StartedAt:  startedAt,
				FinishedAt: finishedAt,
			},
		},
	}

	exitNode := runtime.NewNode(ir.Step{Name: "exit-step"}, runtime.NodeState{})
	successNode := runtime.NewNode(ir.Step{Name: "success-step"}, runtime.NodeState{})
	failureNode := runtime.NewNode(ir.Step{Name: "failure-step"}, runtime.NodeState{})
	abortNode := runtime.NewNode(ir.Step{Name: "abort-step"}, runtime.NodeState{})

	rootRef := dagrun.NewDAGRunRef("root-dag", "root-run-123")
	parentRef := dagrun.NewDAGRunRef("parent-dag", "parent-run-456")

	// Test with all options
	result := builder.Create(
		dagRunID,
		s,
		pid,
		startedAt,
		dagrun.WithFinishedAt(finishedAt),
		transform.WithNodes(nodes),
		transform.WithOnExitNode(exitNode),
		transform.WithOnSuccessNode(successNode),
		transform.WithOnFailureNode(failureNode),
		transform.WithOnAbortNode(abortNode),
		dagrun.WithLogFilePath("/tmp/log.txt"),
		dagrun.WithWorkingDir("/tmp/work"),
		dagrun.WithPreconditions([]*ir.Condition{{Condition: "test", Expected: "true"}}),
		dagrun.WithHierarchyRefs(rootRef, parentRef),
		dagrun.WithAttemptID("attempt-789"),
		dagrun.WithQueuedAt("2024-01-01 12:00:00"),
		dagrun.WithCreatedAt(1234567890),
		dagrun.WithWorkerID("worker-abc"),
		dagrun.WithPIDStartedAt(9876543210),
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
	assert.Equal(t, int64(9876543210), result.PIDStartedAt)
}

func TestStatusBuilderWithConditions(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Name: "queued-dag"}
	checkedAt := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)
	runnable := dagrun.NewDAGRunCondition(
		"Runnable",
		"False",
		"MaxConcurrencyReached",
		"The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
		checkedAt,
	)
	concurrencyReadyOlder := dagrun.NewDAGRunCondition(
		"ConcurrencyReady",
		"False",
		"MaxConcurrencyReached",
		"The queue active-run concurrency limit has been reached.",
		checkedAt.Add(-time.Minute),
	)
	concurrencyReadyNewer := dagrun.NewDAGRunCondition(
		"ConcurrencyReady",
		"True",
		"ConcurrencyAvailable",
		"The queue active-run concurrency limit has capacity.",
		checkedAt.Add(time.Minute),
	)

	result := dagrun.NewStatusBuilder(dag).Create(
		"queued-run",
		ir.Queued,
		0,
		time.Time{},
		dagrun.WithConditions([]dagrun.DAGRunCondition{
			concurrencyReadyOlder,
			runnable,
			concurrencyReadyNewer,
		}),
	)

	assert.Equal(t, []dagrun.DAGRunCondition{
		runnable,
		concurrencyReadyNewer,
	}, result.Conditions)
}

func TestStatusBuilderWithConditionsClearsConditionsForNonQueuedStatus(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Name: "running-dag"}
	checkedAt := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)

	result := dagrun.NewStatusBuilder(dag).Create(
		"running-run",
		ir.Running,
		0,
		time.Time{},
		dagrun.WithConditions([]dagrun.DAGRunCondition{
			dagrun.NewDAGRunCondition(
				"Runnable",
				"False",
				"MaxConcurrencyReached",
				"The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
				checkedAt,
			),
		}),
	)

	assert.Empty(t, result.Conditions)
}

func TestStatusBuilderPopulatesPendingStepRetriesFromNodes(t *testing.T) {
	dag := &ir.DAG{
		Name: "retrying-dag",
		Steps: []ir.Step{
			{Name: "step1"},
		},
	}

	builder := dagrun.NewStatusBuilder(dag)
	result := builder.Create(
		"retry-run",
		ir.Queued,
		123,
		time.Now(),
		transform.WithNodes([]runtime.NodeData{
			{
				Step: ir.Step{
					Name: "step1",
					RetryPolicy: ir.RetryPolicy{
						Interval: 2 * time.Second,
					},
				},
				State: runtime.NodeState{
					Status:     ir.NodeRetrying,
					RetryCount: 1,
				},
			},
		}),
	)

	assert.Equal(t, []ir.PendingStepRetry{
		{StepName: "step1", Interval: 2 * time.Second},
	}, result.PendingStepRetries)
}

func TestStatusBuilderPendingStepRetriesOptionOverridesAutoDerivation(t *testing.T) {
	dag := &ir.DAG{
		Name: "retrying-dag",
		Steps: []ir.Step{
			{Name: "step1"},
		},
	}

	builder := dagrun.NewStatusBuilder(dag)
	result := builder.Create(
		"retry-run",
		ir.Queued,
		123,
		time.Now(),
		transform.WithNodes([]runtime.NodeData{
			{
				Step: ir.Step{
					Name: "step1",
					RetryPolicy: ir.RetryPolicy{
						Interval: 2 * time.Second,
					},
				},
				State: runtime.NodeState{
					Status:     ir.NodeRetrying,
					RetryCount: 1,
				},
			},
		}),
		dagrun.WithPendingStepRetries([]ir.PendingStepRetry{}),
	)

	assert.NotNil(t, result.PendingStepRetries)
	assert.Empty(t, result.PendingStepRetries)
}

func TestStatusBuilderPopulatesPendingStepRetriesFromHandlerNodes(t *testing.T) {
	dag := &ir.DAG{
		Name: "retrying-dag",
		HandlerOn: ir.HandlerOn{
			Failure: &ir.Step{Name: "onFailure"},
		},
	}

	failureHandler := runtime.NewNode(
		ir.Step{
			Name: "onFailure",
			RetryPolicy: ir.RetryPolicy{
				Interval: 3 * time.Second,
			},
		},
		runtime.NodeState{
			Status:     ir.NodeRetrying,
			RetryCount: 1,
		},
	)

	builder := dagrun.NewStatusBuilder(dag)
	result := builder.Create(
		"retry-run",
		ir.Queued,
		123,
		time.Now(),
		transform.WithOnFailureNode(failureHandler),
	)

	assert.Equal(t, []ir.PendingStepRetry{
		{StepName: "onFailure", Interval: 3 * time.Second},
	}, result.PendingStepRetries)
}

func TestInitialStatus(t *testing.T) {
	dag := &ir.DAG{
		Name: "initial-test",
		HandlerOn: ir.HandlerOn{
			Exit:    &ir.Step{Name: "exit"},
			Success: &ir.Step{Name: "success"},
			Failure: &ir.Step{Name: "failure"},
			Abort:   &ir.Step{Name: "abort"},
		},
		Steps: []ir.Step{
			{Name: "step1"},
			{Name: "step2"},
		},
		Params: []string{"arg1", "arg2"},
		Preconditions: []*ir.Condition{
			{Condition: "test condition"},
		},
	}

	st := dagrun.InitialStatus(dag)

	assert.Equal(t, dag.Name, st.Name)
	assert.Equal(t, ir.NotStarted, st.Status)
	assert.Equal(t, dagrun.PID(0), st.PID)
	assert.Equal(t, 2, len(st.Nodes))
	assert.NotNil(t, st.OnExit)
	assert.NotNil(t, st.OnSuccess)
	assert.NotNil(t, st.OnFailure)
	assert.NotNil(t, st.OnAbort)
	assert.Equal(t, "arg1 arg2", st.Params)
	assert.Equal(t, dag.Params, st.ParamsList)
	assert.Equal(t, dagrun.NewConditionResults(dag.Preconditions), st.Preconditions)
	assert.NotZero(t, st.CreatedAt)
	assert.Equal(t, "", st.StartedAt)
	assert.Equal(t, "", st.FinishedAt)
}

func TestStatusFromJSONError(t *testing.T) {
	// Test with invalid JSON
	_, err := dagrun.StatusFromJSON("invalid json")
	assert.Error(t, err)

	// Test with empty string
	_, err = dagrun.StatusFromJSON("")
	assert.Error(t, err)
}

func TestDAGRunStatus_DAGRun(t *testing.T) {
	dagRunStatus := &dagrun.DAGRunStatus{
		Name:     "test-dag",
		DAGRunID: "run-123",
	}

	dagRun := dagRunStatus.DAGRun()
	assert.Equal(t, "test-dag", dagRun.Name)
	assert.Equal(t, "run-123", dagRun.ID)
}

func TestDAGRunStatus_Errors(t *testing.T) {
	dagRunStatus := &dagrun.DAGRunStatus{
		Nodes: []*dagrun.Node{
			{Step: ir.Step{Name: "step1"}, Error: "error1"},
			{Step: ir.Step{Name: "step2"}, Error: ""},
			{Step: ir.Step{Name: "step3"}, Error: "error3"},
		},
		OnExit:    &dagrun.Node{Step: ir.Step{Name: "exit"}, Error: "exit error"},
		OnSuccess: &dagrun.Node{Step: ir.Step{Name: "success"}, Error: ""},
		OnFailure: &dagrun.Node{Step: ir.Step{Name: "failure"}, Error: "failure error"},
		OnAbort:   &dagrun.Node{Step: ir.Step{Name: "cancel"}, Error: "cancel error"},
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
	dagRunStatus := &dagrun.DAGRunStatus{
		Nodes: []*dagrun.Node{
			{Step: ir.Step{Name: "step1"}},
			{Step: ir.Step{Name: "step2"}},
		},
		OnExit:    &dagrun.Node{Step: ir.Step{Name: "exit"}},
		OnSuccess: &dagrun.Node{Step: ir.Step{Name: "success"}},
		OnFailure: &dagrun.Node{Step: ir.Step{Name: "failure"}},
		OnAbort:   &dagrun.Node{Step: ir.Step{Name: "cancel"}},
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
		pid      dagrun.PID
		expected string
	}{
		{
			name:     "PositivePID",
			pid:      dagrun.PID(12345),
			expected: "12345",
		},
		{
			name:     "ZeroPID",
			pid:      dagrun.PID(0),
			expected: "",
		},
		{
			name:     "NegativePID",
			pid:      dagrun.PID(-1),
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
	steps := []ir.Step{
		{
			Name:        "step1",
			Commands:    []ir.CommandEntry{{Command: "echo", Args: []string{"hello"}}},
			Description: "First step",
		},
		{
			Name:        "step2",
			Commands:    []ir.CommandEntry{{Command: "echo", Args: []string{"world"}}},
			Description: "Second step",
		},
	}

	nodes := dagrun.NewNodesFromSteps(steps)

	assert.Equal(t, 2, len(nodes))
	assert.Equal(t, "step1", nodes[0].Step.Name)
	assert.Equal(t, "step2", nodes[1].Step.Name)
	assert.Equal(t, ir.NodeNotStarted, nodes[0].Status)
	assert.Equal(t, ir.NodeNotStarted, nodes[1].Status)
}

func TestWithCreatedAtDefaultTime(t *testing.T) {
	dag := &ir.DAG{Name: "test"}
	dagRunStatus := dagrun.InitialStatus(dag)

	// Test WithCreatedAt with 0 - should use current time
	beforeTime := time.Now().UnixMilli()
	dagrun.WithCreatedAt(0)(&dagRunStatus)
	afterTime := time.Now().UnixMilli()

	assert.GreaterOrEqual(t, dagRunStatus.CreatedAt, beforeTime)
	assert.LessOrEqual(t, dagRunStatus.CreatedAt, afterTime)
}
