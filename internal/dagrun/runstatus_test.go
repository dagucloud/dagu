// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitialStatusSnapshotsDAGRetryMetadata(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name:     "retry-dag",
		Queue:    "shared-queue",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &ir.DAGRetryPolicy{
			Limit:       3,
			Interval:    2 * time.Minute,
			Backoff:     2.0,
			MaxInterval: 10 * time.Minute,
		},
	}

	status := dagrun.InitialStatus(dag)

	assert.Equal(t, 3, status.AutoRetryLimit)
	assert.Equal(t, 2*time.Minute, status.AutoRetryInterval)
	assert.Equal(t, 2.0, status.AutoRetryBackoff)
	assert.Equal(t, 10*time.Minute, status.AutoRetryMaxInterval)
	assert.Equal(t, "shared-queue", status.ProcGroup)
	assert.Equal(t, "retry-dag", status.SuspendFlagName)
}

func TestInitialStatusSnapshotsDisabledDAGRetryPolicy(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name:     "retry-disabled-dag",
		Queue:    "shared-queue",
		Location: "/tmp/retry-disabled-dag.yaml",
		RetryPolicy: &ir.DAGRetryPolicy{
			Limit:       0,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: time.Hour,
		},
	}

	status := dagrun.InitialStatus(dag)

	assert.Equal(t, 0, status.AutoRetryLimit)
	assert.Equal(t, time.Minute, status.AutoRetryInterval)
	assert.Equal(t, 0.0, status.AutoRetryBackoff)
	assert.Equal(t, time.Hour, status.AutoRetryMaxInterval)
	assert.Equal(t, "shared-queue", status.ProcGroup)
	assert.Equal(t, "retry-disabled-dag", status.SuspendFlagName)
}

func TestPendingStepRetriesFromStatus(t *testing.T) {
	t.Parallel()

	t.Run("PrefersPersistedField", func(t *testing.T) {
		status := &dagrun.DAGRunStatus{
			PendingStepRetries: []ir.PendingStepRetry{
				{StepName: "persisted", Interval: 5 * time.Second},
			},
			Nodes: []*dagrun.Node{
				{
					Step: ir.Step{
						Name: "derived",
						RetryPolicy: ir.RetryPolicy{
							Interval: 2 * time.Second,
						},
					},
					Status:     ir.NodeRetrying,
					RetryCount: 1,
				},
			},
		}

		retries := dagrun.PendingStepRetriesFromStatus(status)
		assert.Equal(t, []ir.PendingStepRetry{
			{StepName: "persisted", Interval: 5 * time.Second},
		}, retries)
	})

	t.Run("FallsBackToNodesForLegacyStatuses", func(t *testing.T) {
		status := &dagrun.DAGRunStatus{
			Nodes: []*dagrun.Node{
				{
					Step: ir.Step{
						Name: "legacy",
						RetryPolicy: ir.RetryPolicy{
							Interval: 2 * time.Second,
						},
					},
					Status:     ir.NodeRetrying,
					RetryCount: 1,
				},
			},
		}

		retries := dagrun.PendingStepRetriesFromStatus(status)
		assert.Equal(t, []ir.PendingStepRetry{
			{StepName: "legacy", Interval: 2 * time.Second},
		}, retries)
	})

	t.Run("FallsBackToRegularAndHandlerNodesForLegacyStatuses", func(t *testing.T) {
		status := &dagrun.DAGRunStatus{
			Nodes: []*dagrun.Node{
				{
					Step: ir.Step{
						Name: "regular",
						RetryPolicy: ir.RetryPolicy{
							Interval: time.Second,
						},
					},
					Status:     ir.NodeRetrying,
					RetryCount: 1,
				},
			},
			OnFailure: &dagrun.Node{
				Step: ir.Step{
					Name: "onFailure",
					RetryPolicy: ir.RetryPolicy{
						Interval: 3 * time.Second,
					},
				},
				Status:     ir.NodeRetrying,
				RetryCount: 1,
			},
		}

		retries := dagrun.PendingStepRetriesFromStatus(status)
		assert.Equal(t, []ir.PendingStepRetry{
			{StepName: "regular", Interval: time.Second},
			{StepName: "onFailure", Interval: 3 * time.Second},
		}, retries)
	})

	t.Run("FallsBackToHandlerIdentityWhenHandlerStepNameMissing", func(t *testing.T) {
		status := &dagrun.DAGRunStatus{
			OnFailure: &dagrun.Node{
				Step: ir.Step{
					RetryPolicy: ir.RetryPolicy{
						Interval: 3 * time.Second,
					},
				},
				Status:     ir.NodeRetrying,
				RetryCount: 1,
			},
		}

		retries := dagrun.PendingStepRetriesFromStatus(status)
		assert.Equal(t, []ir.PendingStepRetry{
			{StepName: "onFailure", Interval: 3 * time.Second},
		}, retries)
	})

	t.Run("ExplicitEmptySliceSurvivesJSONRoundTrip", func(t *testing.T) {
		status := &dagrun.DAGRunStatus{
			PendingStepRetries: []ir.PendingStepRetry{},
			Nodes: []*dagrun.Node{
				{
					Step: ir.Step{
						Name: "legacy",
						RetryPolicy: ir.RetryPolicy{
							Interval: 2 * time.Second,
						},
					},
					Status:     ir.NodeRetrying,
					RetryCount: 1,
				},
			},
		}

		data, err := json.Marshal(status)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"pendingStepRetries":[]`)

		var decoded dagrun.DAGRunStatus
		require.NoError(t, json.Unmarshal(data, &decoded))
		require.NotNil(t, decoded.PendingStepRetries)
		assert.Empty(t, dagrun.PendingStepRetriesFromStatus(&decoded))
	})
}

func TestNodePreconditionResultsJSONRoundTrip(t *testing.T) {
	t.Parallel()

	node := dagrun.NewNodeFromStep(ir.Step{
		Name: "conditioned",
		Preconditions: []*ir.Condition{
			{Condition: "ready", Expected: "true"},
		},
	})
	node.PreconditionResults[0].Error = "condition was not met"

	data, err := json.Marshal(node)
	require.NoError(t, err)

	var payload struct {
		Step struct {
			Preconditions []dagrun.ConditionResult `json:"preconditions"`
		} `json:"step"`
	}
	require.NoError(t, json.Unmarshal(data, &payload))
	require.Equal(t, []dagrun.ConditionResult{{
		Condition: "ready",
		Expected:  "true",
		Error:     "condition was not met",
	}}, payload.Step.Preconditions)

	var decoded dagrun.Node
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, node.Step, decoded.Step)
	require.Equal(t, node.PreconditionResults, decoded.PreconditionResults)
}

func TestNewDAGRunCondition(t *testing.T) {
	t.Parallel()

	checkedAt := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)

	condition := dagrun.NewDAGRunCondition(
		"Runnable",
		"False",
		"MaxConcurrencyReached",
		"The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
		checkedAt,
	)

	assert.Equal(t, dagrun.DAGRunCondition{
		Type:      "Runnable",
		Status:    "False",
		Reason:    "MaxConcurrencyReached",
		Message:   "The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
		CheckedAt: "2026-05-19T01:02:03Z",
	}, condition)

	data, err := json.Marshal(condition)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"type": "Runnable",
		"status": "False",
		"reason": "MaxConcurrencyReached",
		"message": "The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
		"checkedAt": "2026-05-19T01:02:03Z"
	}`, string(data))
}

func TestMergeDAGRunConditionsUpsertsByTypeAndOrdersConditions(t *testing.T) {
	t.Parallel()

	checkedAt := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)
	older := checkedAt.Add(-time.Minute)
	newer := checkedAt.Add(time.Minute)

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
		older,
	)
	concurrencyReadyNewer := dagrun.NewDAGRunCondition(
		"ConcurrencyReady",
		"True",
		"ConcurrencyAvailable",
		"The queue active-run concurrency limit has capacity.",
		newer,
	)
	workerReady := dagrun.NewDAGRunCondition(
		"WorkerReady",
		"Unknown",
		"WorkerStateUnknown",
		"Worker availability is still being checked.",
		checkedAt,
	)

	conditions := dagrun.MergeDAGRunConditions(nil, concurrencyReadyOlder, workerReady)
	conditions = dagrun.MergeDAGRunConditions(conditions, runnable)
	conditions = dagrun.MergeDAGRunConditions(conditions, concurrencyReadyNewer)
	conditions = dagrun.MergeDAGRunConditions(
		conditions,
		dagrun.NewDAGRunCondition(
			"ConcurrencyReady",
			"False",
			"StaleConcurrencyObservation",
			"This older observation must not replace the current condition.",
			older,
		),
	)

	assert.Equal(t, []dagrun.DAGRunCondition{
		runnable,
		concurrencyReadyNewer,
		workerReady,
	}, conditions)
}

func TestNormalizeDAGRunConditions(t *testing.T) {
	t.Parallel()

	checkedAt := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)
	conditions := []dagrun.DAGRunCondition{
		dagrun.NewDAGRunCondition(
			"Runnable",
			"False",
			"MaxConcurrencyReached",
			"The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
			checkedAt,
		),
		dagrun.NewDAGRunCondition(
			"ConcurrencyReady",
			"False",
			"MaxConcurrencyReached",
			"The queue active-run concurrency limit has been reached.",
			checkedAt.Add(time.Second),
		),
	}

	queued := &dagrun.DAGRunStatus{
		Status: ir.Queued,
		Conditions: append(conditions, dagrun.NewDAGRunCondition(
			"Runnable",
			"Unknown",
			"StaleRunnableObservation",
			"This older duplicate must be removed during normalization.",
			checkedAt.Add(-time.Second),
		)),
	}
	dagrun.NormalizeDAGRunConditions(queued)
	assert.Equal(t, conditions, queued.Conditions)

	running := &dagrun.DAGRunStatus{
		Status:     ir.Running,
		Conditions: conditions,
	}
	dagrun.NormalizeDAGRunConditions(running)
	assert.Nil(t, running.Conditions)

	dagrun.NormalizeDAGRunConditions(nil)
}

func TestDAGRunStatusUnmarshalJSONDeprecatedTags(t *testing.T) {
	t.Parallel()

	var status dagrun.DAGRunStatus
	err := json.Unmarshal([]byte(`{"name":"legacy","tags":["env=prod","team=platform"]}`), &status)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"env=prod", "team=platform"}, status.Labels)

	var explicitLabels dagrun.DAGRunStatus
	err = json.Unmarshal([]byte(`{"name":"canonical","labels":[],"tags":["env=legacy"]}`), &explicitLabels)
	require.NoError(t, err)
	assert.Empty(t, explicitLabels.Labels)
}
