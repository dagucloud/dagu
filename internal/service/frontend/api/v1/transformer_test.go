// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	openapi "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeArtifactFile(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "artifact.txt"), []byte("artifact"), 0o600)
	require.NoError(t, err)
	return dir
}

func TestToDAGRunSummaryIncludesScheduleTime(t *testing.T) {
	status := exec.DAGRunStatus{
		Name:           "test-dag",
		DAGRunID:       "run-1",
		AutoRetryCount: 2,
		AutoRetryLimit: 5,
		ArchiveDir:     writeArtifactFile(t),
		Status:         core.Queued,
		ScheduleTime:   "2026-03-13T00:00:00Z",
	}

	summary := toDAGRunSummary(status)
	require.NotNil(t, summary.ScheduleTime)
	assert.Equal(t, status.ScheduleTime, *summary.ScheduleTime)
	assert.Equal(t, status.AutoRetryCount, summary.AutoRetryCount)
	require.NotNil(t, summary.AutoRetryLimit)
	assert.Equal(t, status.AutoRetryLimit, *summary.AutoRetryLimit)
	assert.True(t, summary.ArtifactsAvailable)
}

func TestToDAGRunDetailsIncludesScheduleTime(t *testing.T) {
	status := exec.DAGRunStatus{
		Name:           "test-dag",
		DAGRunID:       "run-1",
		AutoRetryCount: 3,
		AutoRetryLimit: 5,
		ArchiveDir:     writeArtifactFile(t),
		Status:         core.Queued,
		QueuedAt:       "2026-03-13T00:01:00Z",
		ScheduleTime:   "2026-03-13T00:00:00Z",
	}

	details := ToDAGRunDetails(status)
	require.NotNil(t, details.ScheduleTime)
	assert.Equal(t, status.ScheduleTime, *details.ScheduleTime)
	require.NotNil(t, details.QueuedAt)
	assert.Equal(t, status.QueuedAt, *details.QueuedAt)
	assert.Equal(t, status.AutoRetryCount, details.AutoRetryCount)
	require.NotNil(t, details.AutoRetryLimit)
	assert.Equal(t, status.AutoRetryLimit, *details.AutoRetryLimit)
	assert.True(t, details.ArtifactsAvailable)
}

func TestToDAGRunSummaryOmitsAutoRetryLimitWhenUnconfigured(t *testing.T) {
	status := exec.DAGRunStatus{
		Name:           "test-dag",
		DAGRunID:       "run-1",
		AutoRetryCount: 0,
		AutoRetryLimit: 0,
		Status:         core.Failed,
	}

	summary := toDAGRunSummary(status)
	assert.Nil(t, summary.AutoRetryLimit)
	assert.False(t, summary.ArtifactsAvailable)
}

func TestToDAGRunDetailsOmitsAutoRetryLimitWhenUnconfigured(t *testing.T) {
	status := exec.DAGRunStatus{
		Name:           "test-dag",
		DAGRunID:       "run-1",
		AutoRetryCount: 0,
		AutoRetryLimit: 0,
		Status:         core.Failed,
	}

	details := ToDAGRunDetails(status)
	assert.Nil(t, details.AutoRetryLimit)
	assert.False(t, details.ArtifactsAvailable)
}

func TestToDAGDetailsIncludesParamDefDescriptions(t *testing.T) {
	details := toDAGDetails(&core.DAG{
		Name: "described-params",
		ParamDefs: []core.ParamDef{
			{
				Name:        "notes",
				Type:        core.ParamDefTypeString,
				Description: "Free-form operator notes",
			},
		},
	})

	require.NotNil(t, details)
	require.NotNil(t, details.ParamDefs)
	require.Len(t, *details.ParamDefs, 1)
	require.NotNil(t, (*details.ParamDefs)[0].Description)
	assert.Equal(t, "Free-form operator notes", *(*details.ParamDefs)[0].Description)
}

func TestToDAGDetailsIncludesArtifactsDir(t *testing.T) {
	details := toDAGDetails(&core.DAG{
		Name: "artifacts-dir",
		Artifacts: &core.ArtifactsConfig{
			Enabled: true,
			Dir:     "/var/lib/dagu/artifacts",
		},
	})

	require.NotNil(t, details)
	require.NotNil(t, details.Artifacts)
	assert.True(t, details.Artifacts.Enabled)
	require.NotNil(t, details.Artifacts.Dir)
	assert.Equal(t, "/var/lib/dagu/artifacts", *details.Artifacts.Dir)
}

func TestToNodeIncludesNormalizedPushBackHistory(t *testing.T) {
	node := &exec.Node{
		Step: core.Step{
			Name: "review",
			Approval: &core.ApprovalConfig{
				Input: []string{"FEEDBACK"},
			},
		},
		Status:            core.NodeWaiting,
		StartedAt:         "2026-04-26T06:00:00Z",
		FinishedAt:        "2026-04-26T06:01:00Z",
		Stdout:            "stdout.log",
		Stderr:            "stderr.log",
		ApprovalIteration: 1,
		PushBackInputs:    map[string]string{"FEEDBACK": "revise the summary", "IGNORED": "x"},
	}

	result := toNode(node)

	require.NotNil(t, result.PushBackHistory)
	require.Len(t, *result.PushBackHistory, 1)
	entry := (*result.PushBackHistory)[0]
	assert.Equal(t, 1, entry.Iteration)
	require.NotNil(t, entry.Inputs)
	assert.Equal(t, "revise the summary", (*entry.Inputs)["FEEDBACK"])
	_, ok := (*entry.Inputs)["IGNORED"]
	assert.False(t, ok)
}

func TestToDAGIncludesTypedSchedules(t *testing.T) {
	cronSchedule, err := core.NewCronSchedule("*/5 * * * *")
	require.NoError(t, err)

	oneOffSchedule, err := core.NewOneOffSchedule("2026-03-29T02:10:00+01:00")
	require.NoError(t, err)

	dag := toDAG(&core.DAG{
		Name:     "typed-schedules",
		Schedule: []core.Schedule{cronSchedule, oneOffSchedule},
	})

	require.NotNil(t, dag.Schedule)
	require.Len(t, *dag.Schedule, 2)

	cronAPI := (*dag.Schedule)[0]
	require.NotNil(t, cronAPI.Kind)
	assert.Equal(t, openapi.ScheduleKindCron, *cronAPI.Kind)
	assert.Equal(t, "*/5 * * * *", cronAPI.Expression)
	assert.Nil(t, cronAPI.At)

	oneOffAPI := (*dag.Schedule)[1]
	require.NotNil(t, oneOffAPI.At)
	require.NotNil(t, oneOffAPI.Kind)
	assert.Equal(t, openapi.ScheduleKindAt, *oneOffAPI.Kind)
	assert.Empty(t, oneOffAPI.Expression)

	expectedAt, err := time.Parse(time.RFC3339, "2026-03-29T02:10:00+01:00")
	require.NoError(t, err)
	assert.True(t, expectedAt.Equal(*oneOffAPI.At))
}

func TestToDAGDetailsIncludesTypedSchedules(t *testing.T) {
	oneOffSchedule, err := core.NewOneOffSchedule("2026-03-29T02:10:00Z")
	require.NoError(t, err)

	details := toDAGDetails(&core.DAG{
		Name:     "typed-schedules",
		Schedule: []core.Schedule{oneOffSchedule},
	})

	require.NotNil(t, details.Schedule)
	require.Len(t, *details.Schedule, 1)
	require.NotNil(t, (*details.Schedule)[0].At)
	require.NotNil(t, (*details.Schedule)[0].Kind)
	assert.Equal(t, openapi.ScheduleKindAt, *(*details.Schedule)[0].Kind)
	assert.Empty(t, (*details.Schedule)[0].Expression)
}

func TestToNodeMapsStatuses(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		coreStatus  core.NodeStatus
		apiStatus   openapi.NodeStatus
		statusLabel openapi.NodeStatusLabel
	}{
		{
			name:        "running",
			coreStatus:  core.NodeRunning,
			apiStatus:   openapi.NodeStatusRunning,
			statusLabel: openapi.NodeStatusLabelRunning,
		},
		{
			name:        "retrying",
			coreStatus:  core.NodeRetrying,
			apiStatus:   openapi.NodeStatusRetrying,
			statusLabel: openapi.NodeStatusLabelRetrying,
		},
		{
			name:        "partial success",
			coreStatus:  core.NodePartiallySucceeded,
			apiStatus:   openapi.NodeStatusPartialSuccess,
			statusLabel: openapi.NodeStatusLabelPartiallySucceeded,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			node := &exec.Node{
				Status: tc.coreStatus,
				Step: core.Step{
					Name: "step-" + tc.name,
				},
			}

			converted := toNode(node)

			assert.Equal(t, tc.apiStatus, converted.Status)
			assert.Equal(t, tc.statusLabel, converted.StatusLabel)
		})
	}
}

func TestNodeStatusMappingIsExhaustive(t *testing.T) {
	t.Parallel()

	expected := map[openapi.NodeStatus]core.NodeStatus{
		openapi.NodeStatusNotStarted:     core.NodeNotStarted,
		openapi.NodeStatusRunning:        core.NodeRunning,
		openapi.NodeStatusFailed:         core.NodeFailed,
		openapi.NodeStatusAborted:        core.NodeAborted,
		openapi.NodeStatusSuccess:        core.NodeSucceeded,
		openapi.NodeStatusSkipped:        core.NodeSkipped,
		openapi.NodeStatusPartialSuccess: core.NodePartiallySucceeded,
		openapi.NodeStatusWaiting:        core.NodeWaiting,
		openapi.NodeStatusRejected:       core.NodeRejected,
		openapi.NodeStatusRetrying:       core.NodeRetrying,
	}

	assert.Len(t, nodeStatusMapping, len(expected))
	assert.Equal(t, expected, nodeStatusMapping)
}
