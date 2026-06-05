// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitialStatusSnapshotsDAGRetryMetadata(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
		Name:     "retry-dag",
		Queue:    "shared-queue",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &core.DAGRetryPolicy{
			Limit:       3,
			Interval:    2 * time.Minute,
			Backoff:     2.0,
			MaxInterval: 10 * time.Minute,
		},
	}

	status := exec.InitialStatus(dag)

	assert.Equal(t, 3, status.AutoRetryLimit)
	assert.Equal(t, 2*time.Minute, status.AutoRetryInterval)
	assert.Equal(t, 2.0, status.AutoRetryBackoff)
	assert.Equal(t, 10*time.Minute, status.AutoRetryMaxInterval)
	assert.Equal(t, "shared-queue", status.ProcGroup)
	assert.Equal(t, "retry-dag", status.SuspendFlagName)
}

func TestInitialStatusSnapshotsDisabledDAGRetryPolicy(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
		Name:     "retry-disabled-dag",
		Queue:    "shared-queue",
		Location: "/tmp/retry-disabled-dag.yaml",
		RetryPolicy: &core.DAGRetryPolicy{
			Limit:       0,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: time.Hour,
		},
	}

	status := exec.InitialStatus(dag)

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
		status := &exec.DAGRunStatus{
			PendingStepRetries: []exec.PendingStepRetry{
				{StepName: "persisted", Interval: 5 * time.Second},
			},
			Nodes: []*exec.Node{
				{
					Step: core.Step{
						Name: "derived",
						RetryPolicy: core.RetryPolicy{
							Interval: 2 * time.Second,
						},
					},
					Status:     core.NodeRetrying,
					RetryCount: 1,
				},
			},
		}

		retries := exec.PendingStepRetriesFromStatus(status)
		assert.Equal(t, []exec.PendingStepRetry{
			{StepName: "persisted", Interval: 5 * time.Second},
		}, retries)
	})

	t.Run("FallsBackToNodesForLegacyStatuses", func(t *testing.T) {
		status := &exec.DAGRunStatus{
			Nodes: []*exec.Node{
				{
					Step: core.Step{
						Name: "legacy",
						RetryPolicy: core.RetryPolicy{
							Interval: 2 * time.Second,
						},
					},
					Status:     core.NodeRetrying,
					RetryCount: 1,
				},
			},
		}

		retries := exec.PendingStepRetriesFromStatus(status)
		assert.Equal(t, []exec.PendingStepRetry{
			{StepName: "legacy", Interval: 2 * time.Second},
		}, retries)
	})

	t.Run("FallsBackToRegularAndHandlerNodesForLegacyStatuses", func(t *testing.T) {
		status := &exec.DAGRunStatus{
			Nodes: []*exec.Node{
				{
					Step: core.Step{
						Name: "regular",
						RetryPolicy: core.RetryPolicy{
							Interval: time.Second,
						},
					},
					Status:     core.NodeRetrying,
					RetryCount: 1,
				},
			},
			OnFailure: &exec.Node{
				Step: core.Step{
					Name: "onFailure",
					RetryPolicy: core.RetryPolicy{
						Interval: 3 * time.Second,
					},
				},
				Status:     core.NodeRetrying,
				RetryCount: 1,
			},
		}

		retries := exec.PendingStepRetriesFromStatus(status)
		assert.Equal(t, []exec.PendingStepRetry{
			{StepName: "regular", Interval: time.Second},
			{StepName: "onFailure", Interval: 3 * time.Second},
		}, retries)
	})

	t.Run("FallsBackToHandlerIdentityWhenHandlerStepNameMissing", func(t *testing.T) {
		status := &exec.DAGRunStatus{
			OnFailure: &exec.Node{
				Step: core.Step{
					RetryPolicy: core.RetryPolicy{
						Interval: 3 * time.Second,
					},
				},
				Status:     core.NodeRetrying,
				RetryCount: 1,
			},
		}

		retries := exec.PendingStepRetriesFromStatus(status)
		assert.Equal(t, []exec.PendingStepRetry{
			{StepName: "onFailure", Interval: 3 * time.Second},
		}, retries)
	})

	t.Run("ExplicitEmptySliceSurvivesJSONRoundTrip", func(t *testing.T) {
		status := &exec.DAGRunStatus{
			PendingStepRetries: []exec.PendingStepRetry{},
			Nodes: []*exec.Node{
				{
					Step: core.Step{
						Name: "legacy",
						RetryPolicy: core.RetryPolicy{
							Interval: 2 * time.Second,
						},
					},
					Status:     core.NodeRetrying,
					RetryCount: 1,
				},
			},
		}

		data, err := json.Marshal(status)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"pendingStepRetries":[]`)

		var decoded exec.DAGRunStatus
		require.NoError(t, json.Unmarshal(data, &decoded))
		require.NotNil(t, decoded.PendingStepRetries)
		assert.Empty(t, exec.PendingStepRetriesFromStatus(&decoded))
	})
}

func TestDAGRunStatusUnmarshalJSONDeprecatedTags(t *testing.T) {
	t.Parallel()

	var status exec.DAGRunStatus
	err := json.Unmarshal([]byte(`{"name":"legacy","tags":["env=prod","team=platform"]}`), &status)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"env=prod", "team=platform"}, status.Labels)

	var explicitLabels exec.DAGRunStatus
	err = json.Unmarshal([]byte(`{"name":"canonical","labels":[],"tags":["env=legacy"]}`), &explicitLabels)
	require.NoError(t, err)
	assert.Empty(t, explicitLabels.Labels)
}
