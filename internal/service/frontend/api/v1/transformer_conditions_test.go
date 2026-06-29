// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"encoding/json"
	"testing"

	frontendapi "github.com/dagucloud/dagu/internal/service/frontend/api/v1"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToDAGRunSummaryIncludesConditions(t *testing.T) {
	status := exec.DAGRunStatus{
		Name:     "queued-dag",
		DAGRunID: "run-1",
		Status:   core.Queued,
		Conditions: []exec.DAGRunCondition{
			{
				Type:      "Queued",
				Status:    "True",
				Reason:    "QueueCapacity",
				Message:   "DAG-run is waiting for a worker.",
				CheckedAt: "2026-05-19T01:02:03Z",
			},
		},
	}

	summary := frontendapi.ToDAGRunSummaryForTest(status)

	payload := marshalResponse(t, summary)
	conditions := requireConditions(t, payload)
	assert.Equal(t, "Queued", conditions[0]["type"])
	assert.Equal(t, "True", conditions[0]["status"])
	assert.Equal(t, "QueueCapacity", conditions[0]["reason"])
	assert.Equal(t, "DAG-run is waiting for a worker.", conditions[0]["message"])
	assert.Equal(t, "2026-05-19T01:02:03Z", conditions[0]["checkedAt"])
}

func TestToDAGRunDetailsIncludesConditions(t *testing.T) {
	status := exec.DAGRunStatus{
		Name:     "queued-dag",
		DAGRunID: "run-1",
		Status:   core.Queued,
		Conditions: []exec.DAGRunCondition{
			{
				Type:      "Queued",
				Status:    "Unknown",
				Reason:    "WorkerHeartbeatMissing",
				Message:   "Worker state is still being checked.",
				CheckedAt: "2026-05-19T01:02:03Z",
			},
		},
	}

	details := frontendapi.ToDAGRunDetails(status)

	payload := marshalResponse(t, details)
	conditions := requireConditions(t, payload)
	assert.Equal(t, "Queued", conditions[0]["type"])
	assert.Equal(t, "Unknown", conditions[0]["status"])
	assert.Equal(t, "WorkerHeartbeatMissing", conditions[0]["reason"])
	assert.Equal(t, "Worker state is still being checked.", conditions[0]["message"])
	assert.Equal(t, "2026-05-19T01:02:03Z", conditions[0]["checkedAt"])
}

func TestToDAGRunSummarySkipsConditionsWithInvalidCheckedAt(t *testing.T) {
	status := exec.DAGRunStatus{
		Name:     "queued-dag",
		DAGRunID: "run-1",
		Status:   core.Queued,
		Conditions: []exec.DAGRunCondition{
			{
				Type:      "Queued",
				Status:    "True",
				Reason:    "QueueCapacity",
				Message:   "DAG-run is waiting for a worker.",
				CheckedAt: "not-a-time",
			},
		},
	}

	summary := frontendapi.ToDAGRunSummaryForTest(status)

	payload := marshalResponse(t, summary)
	_, ok := payload["conditions"]
	assert.False(t, ok, "invalid checkedAt must not be exposed as a zero timestamp")
}

func TestToDAGRunSummarySkipsConditionsWhenStatusIsNotQueued(t *testing.T) {
	status := exec.DAGRunStatus{
		Name:     "running-dag",
		DAGRunID: "run-1",
		Status:   core.Running,
		Conditions: []exec.DAGRunCondition{
			{
				Type:      "Queued",
				Status:    "True",
				Reason:    "QueueCapacity",
				Message:   "DAG-run is waiting for a worker.",
				CheckedAt: "2026-05-19T01:02:03Z",
			},
		},
	}

	summary := frontendapi.ToDAGRunSummaryForTest(status)

	payload := marshalResponse(t, summary)
	_, ok := payload["conditions"]
	assert.False(t, ok, "runtime conditions must not be exposed for non-queued runs")
}

func marshalResponse(t *testing.T, value any) map[string]any {
	t.Helper()

	data, err := json.Marshal(value)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))
	return payload
}

func requireConditions(t *testing.T, payload map[string]any) []map[string]string {
	t.Helper()

	rawConditions, ok := payload["conditions"]
	require.True(t, ok, "conditions field is missing from response payload")

	conditionValues, ok := rawConditions.([]any)
	require.True(t, ok, "conditions field has unexpected type %T", rawConditions)
	require.Len(t, conditionValues, 1)

	condition, ok := conditionValues[0].(map[string]any)
	require.True(t, ok, "condition has unexpected type %T", conditionValues[0])

	result := map[string]string{}
	for key, value := range condition {
		text, ok := value.(string)
		require.True(t, ok, "condition field %q has unexpected type %T", key, value)
		result[key] = text
	}
	return []map[string]string{result}
}
