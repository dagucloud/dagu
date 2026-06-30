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
				Type:      "Runnable",
				Status:    "False",
				Reason:    "MaxConcurrencyReached",
				Message:   "The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
				CheckedAt: "2026-05-19T01:02:03Z",
			},
			{
				Type:      "ConcurrencyReady",
				Status:    "False",
				Reason:    "MaxConcurrencyReached",
				Message:   "The queue active-run concurrency limit has been reached.",
				CheckedAt: "2026-05-19T01:02:04Z",
			},
		},
	}

	summary := frontendapi.ToDAGRunSummaryForTest(status)

	payload := marshalResponse(t, summary)
	conditions := requireConditions(t, payload, 2)
	assert.Equal(t, "Runnable", conditions[0]["type"])
	assert.Equal(t, "False", conditions[0]["status"])
	assert.Equal(t, "MaxConcurrencyReached", conditions[0]["reason"])
	assert.Equal(t, "The DAG-run cannot start because the queue active-run concurrency limit has been reached.", conditions[0]["message"])
	assert.Equal(t, "2026-05-19T01:02:03Z", conditions[0]["checkedAt"])
	assert.Equal(t, "ConcurrencyReady", conditions[1]["type"])
	assert.Equal(t, "False", conditions[1]["status"])
	assert.Equal(t, "MaxConcurrencyReached", conditions[1]["reason"])
	assert.Equal(t, "The queue active-run concurrency limit has been reached.", conditions[1]["message"])
	assert.Equal(t, "2026-05-19T01:02:04Z", conditions[1]["checkedAt"])
}

func TestToDAGRunDetailsIncludesConditions(t *testing.T) {
	status := exec.DAGRunStatus{
		Name:     "queued-dag",
		DAGRunID: "run-1",
		Status:   core.Queued,
		Conditions: []exec.DAGRunCondition{
			{
				Type:      "WorkerReady",
				Status:    "Unknown",
				Reason:    "WorkerHeartbeatMissing",
				Message:   "Worker state is still being checked.",
				CheckedAt: "2026-05-19T01:02:03Z",
			},
		},
	}

	details := frontendapi.ToDAGRunDetails(status)

	payload := marshalResponse(t, details)
	conditions := requireConditions(t, payload, 1)
	assert.Equal(t, "WorkerReady", conditions[0]["type"])
	assert.Equal(t, "Unknown", conditions[0]["status"])
	assert.Equal(t, "WorkerHeartbeatMissing", conditions[0]["reason"])
	assert.Equal(t, "Worker state is still being checked.", conditions[0]["message"])
	assert.Equal(t, "2026-05-19T01:02:03Z", conditions[0]["checkedAt"])
}

func TestToDAGRunSummarySkipsOnlyConditionsWithInvalidCheckedAt(t *testing.T) {
	status := exec.DAGRunStatus{
		Name:     "queued-dag",
		DAGRunID: "run-1",
		Status:   core.Queued,
		Conditions: []exec.DAGRunCondition{
			{
				Type:      "Runnable",
				Status:    "False",
				Reason:    "MaxConcurrencyReached",
				Message:   "The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
				CheckedAt: "2026-05-19T01:02:03Z",
			},
			{
				Type:      "ConcurrencyReady",
				Status:    "False",
				Reason:    "MaxConcurrencyReached",
				Message:   "The queue active-run concurrency limit has been reached.",
				CheckedAt: "not-a-time",
			},
		},
	}

	summary := frontendapi.ToDAGRunSummaryForTest(status)

	payload := marshalResponse(t, summary)
	conditions := requireConditions(t, payload, 1)
	assert.Equal(t, "Runnable", conditions[0]["type"])
	assert.Equal(t, "2026-05-19T01:02:03Z", conditions[0]["checkedAt"])
}

func TestToDAGRunSummarySkipsConditionsWhenStatusIsNotQueued(t *testing.T) {
	status := exec.DAGRunStatus{
		Name:     "running-dag",
		DAGRunID: "run-1",
		Status:   core.Running,
		Conditions: []exec.DAGRunCondition{
			{
				Type:      "Runnable",
				Status:    "False",
				Reason:    "MaxConcurrencyReached",
				Message:   "The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
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

func requireConditions(t *testing.T, payload map[string]any, expectedLen int) []map[string]string {
	t.Helper()

	rawConditions, ok := payload["conditions"]
	require.True(t, ok, "conditions field is missing from response payload")

	conditionValues, ok := rawConditions.([]any)
	require.True(t, ok, "conditions field has unexpected type %T", rawConditions)
	require.Len(t, conditionValues, expectedLen)

	result := make([]map[string]string, 0, len(conditionValues))
	for i, conditionValue := range conditionValues {
		condition, ok := conditionValue.(map[string]any)
		require.True(t, ok, "condition %d has unexpected type %T", i, conditionValue)

		fields := map[string]string{}
		for key, value := range condition {
			text, ok := value.(string)
			require.True(t, ok, "condition %d field %q has unexpected type %T", i, key, value)
			fields[key] = text
		}
		result = append(result, fields)
	}
	return result
}
