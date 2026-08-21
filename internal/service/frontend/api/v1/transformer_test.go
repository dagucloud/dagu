// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	openapi "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/ir"
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
	status := ir.DAGRunStatus{
		Name:           "test-dag",
		DAGRunID:       "run-1",
		AutoRetryCount: 2,
		AutoRetryLimit: 5,
		ArchiveDir:     writeArtifactFile(t),
		Status:         ir.Queued,
		ScheduleTime:   "2026-03-13T00:00:00Z",
		TriggerActor:   "alice",
	}

	summary := toDAGRunSummary(status)
	require.NotNil(t, summary.ScheduleTime)
	assert.Equal(t, status.ScheduleTime, *summary.ScheduleTime)
	assert.Equal(t, status.AutoRetryCount, summary.AutoRetryCount)
	require.NotNil(t, summary.AutoRetryLimit)
	assert.Equal(t, status.AutoRetryLimit, *summary.AutoRetryLimit)
	assert.True(t, summary.ArtifactsAvailable)
	require.NotNil(t, summary.TriggerActor)
	assert.Equal(t, "alice", *summary.TriggerActor)
}

func TestToDAGRunDetailsIncludesScheduleTime(t *testing.T) {
	status := ir.DAGRunStatus{
		Name:           "test-dag",
		DAGRunID:       "run-1",
		AutoRetryCount: 3,
		AutoRetryLimit: 5,
		ArchiveDir:     writeArtifactFile(t),
		Status:         ir.Queued,
		QueuedAt:       "2026-03-13T00:01:00Z",
		ScheduleTime:   "2026-03-13T00:00:00Z",
		TriggerActor:   "alice",
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
	require.NotNil(t, details.TriggerActor)
	assert.Equal(t, "alice", *details.TriggerActor)
}

func TestTriggerActorFromContext(t *testing.T) {
	t.Parallel()

	ctx := auth.WithUser(context.Background(), &auth.User{Username: "alice"})
	assert.Equal(t, "alice", triggerActorFromContext(ctx))
	assert.Empty(t, triggerActorFromContext(context.Background()))
}

func TestToDAGRunDetailsIncludesHumanTaskContract(t *testing.T) {
	status := ir.DAGRunStatus{
		Name:     "test-dag",
		DAGRunID: "run-1",
		Status:   ir.Waiting,
		Nodes: []*ir.Node{{
			Step: ir.Step{
				ID:   "review",
				Name: "Review",
				HumanTask: &ir.HumanTaskConfig{
					Prompt: "Confirm the release",
					Form:   json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer","maximum":9007199254740993}}}`),
				},
			},
			Status:         ir.NodeSucceeded,
			HumanTaskInput: json.RawMessage(`{}`),
		}},
	}

	details := ToDAGRunDetails(status)
	require.Len(t, details.Nodes, 1)
	require.NotNil(t, details.Nodes[0].Step.HumanTask)
	assert.Equal(t, "Confirm the release", details.Nodes[0].Step.HumanTask.Prompt)
	require.NotNil(t, details.Nodes[0].Step.HumanTask.Form)
	assert.Equal(t, "object", (*details.Nodes[0].Step.HumanTask.Form)["type"])
	properties := (*details.Nodes[0].Step.HumanTask.Form)["properties"].(map[string]any)
	count := properties["count"].(map[string]any)
	assert.Equal(t, json.Number("9007199254740993"), count["maximum"])
	require.NotNil(t, details.HumanTaskResumePending)
	assert.True(t, *details.HumanTaskResumePending)
}

func TestToDAGRunDetailsTreatsNullHumanTaskFormAsAbsent(t *testing.T) {
	status := ir.DAGRunStatus{
		Name:     "test-dag",
		DAGRunID: "run-1",
		Nodes: []*ir.Node{{
			Step: ir.Step{HumanTask: &ir.HumanTaskConfig{Form: json.RawMessage(`null`)}},
		}},
	}

	details := ToDAGRunDetails(status)

	require.Len(t, details.Nodes, 1)
	require.NotNil(t, details.Nodes[0].Step.HumanTask)
	assert.Nil(t, details.Nodes[0].Step.HumanTask.Form)
}

func TestToDAGRunDetailsTreatsHumanTaskFormWithTrailingDataAsAbsent(t *testing.T) {
	status := ir.DAGRunStatus{
		Name:     "test-dag",
		DAGRunID: "run-1",
		Nodes: []*ir.Node{{
			Step: ir.Step{HumanTask: &ir.HumanTaskConfig{
				Form: json.RawMessage(`{"type":"object"} trailing`),
			}},
		}},
	}

	details := ToDAGRunDetails(status)

	require.Len(t, details.Nodes, 1)
	require.NotNil(t, details.Nodes[0].Step.HumanTask)
	assert.Nil(t, details.Nodes[0].Step.HumanTask.Form)
}

func TestToDAGRunSummaryOmitsAutoRetryLimitWhenUnconfigured(t *testing.T) {
	status := ir.DAGRunStatus{
		Name:           "test-dag",
		DAGRunID:       "run-1",
		AutoRetryCount: 0,
		AutoRetryLimit: 0,
		Status:         ir.Failed,
	}

	summary := toDAGRunSummary(status)
	assert.Nil(t, summary.AutoRetryLimit)
	assert.False(t, summary.ArtifactsAvailable)
}

func TestToDAGRunDetailsOmitsAutoRetryLimitWhenUnconfigured(t *testing.T) {
	status := ir.DAGRunStatus{
		Name:           "test-dag",
		DAGRunID:       "run-1",
		AutoRetryCount: 0,
		AutoRetryLimit: 0,
		Status:         ir.Failed,
	}

	details := ToDAGRunDetails(status)
	assert.Nil(t, details.AutoRetryLimit)
	assert.False(t, details.ArtifactsAvailable)
}

func TestToDAGRunSummarySetsProfileNameWhenPresent(t *testing.T) {
	status := ir.DAGRunStatus{
		Name:        "test-dag",
		DAGRunID:    "run-1",
		Status:      ir.Succeeded,
		ProfileName: "prod",
	}

	summary := toDAGRunSummary(status)
	require.NotNil(t, summary.ProfileName)
	assert.Equal(t, "prod", string(*summary.ProfileName))
}

func TestToDAGRunSummaryOmitsProfileNameWhenEmpty(t *testing.T) {
	status := ir.DAGRunStatus{
		Name:     "test-dag",
		DAGRunID: "run-1",
		Status:   ir.Succeeded,
	}

	summary := toDAGRunSummary(status)
	assert.Nil(t, summary.ProfileName)
}

func TestToDAGRunDetailsSetsProfileNameWhenPresent(t *testing.T) {
	status := ir.DAGRunStatus{
		Name:        "test-dag",
		DAGRunID:    "run-1",
		Status:      ir.Succeeded,
		ProfileName: "prod",
	}

	details := ToDAGRunDetails(status)
	require.NotNil(t, details.ProfileName)
	assert.Equal(t, "prod", string(*details.ProfileName))
}

func TestToDAGRunDetailsOmitsProfileNameWhenEmpty(t *testing.T) {
	status := ir.DAGRunStatus{
		Name:     "test-dag",
		DAGRunID: "run-1",
		Status:   ir.Succeeded,
	}

	details := ToDAGRunDetails(status)
	assert.Nil(t, details.ProfileName)
}

func TestToDAGDetailsIncludesParamDefDescriptions(t *testing.T) {
	details := toDAGDetails(&ir.DAG{
		Name: "described-params",
		ParamDefs: []ir.ParamDef{
			{
				Name:        "notes",
				Type:        ir.ParamDefTypeString,
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

func TestToDAGDetailsIncludesHistoryRetentionRuns(t *testing.T) {
	details := toDAGDetails(&ir.DAG{
		Name:              "retention-runs",
		HistRetentionRuns: 3,
	})

	require.NotNil(t, details)
	require.NotNil(t, details.HistRetentionRuns)
	assert.Equal(t, 3, *details.HistRetentionRuns)
}

func TestToDAGIncludesResources(t *testing.T) {
	limits, err := ir.NewResourceLimits("500m", "1Gi")
	require.NoError(t, err)

	dag := toDAG(&ir.DAG{
		Name:      "limited-dag",
		Resources: &ir.Resources{Limits: limits},
	})

	require.NotNil(t, dag.Resources)
	require.NotNil(t, dag.Resources.Limits)
	require.NotNil(t, dag.Resources.Limits.Cpu)
	assert.Equal(t, "500m", *dag.Resources.Limits.Cpu)
	require.NotNil(t, dag.Resources.Limits.Memory)
	assert.Equal(t, "1Gi", *dag.Resources.Limits.Memory)
}

func TestToDAGDetailsIncludesResources(t *testing.T) {
	limits, err := ir.NewResourceLimits("750m", "512Mi")
	require.NoError(t, err)

	details := toDAGDetails(&ir.DAG{
		Name:      "limited-dag",
		Resources: &ir.Resources{Limits: limits},
	})

	require.NotNil(t, details)
	require.NotNil(t, details.Resources)
	require.NotNil(t, details.Resources.Limits)
	require.NotNil(t, details.Resources.Limits.Cpu)
	assert.Equal(t, "750m", *details.Resources.Limits.Cpu)
	require.NotNil(t, details.Resources.Limits.Memory)
	assert.Equal(t, "512Mi", *details.Resources.Limits.Memory)
}

func TestToDAGDetailsIncludesParamSchema(t *testing.T) {
	details := toDAGDetails(&ir.DAG{
		Name:        "schema-params",
		ParamSchema: json.RawMessage(`{"type":"object","properties":{"region":{"type":"string"}}}`),
	})

	require.NotNil(t, details)
	require.NotNil(t, details.ParamSchema)

	properties, ok := (*details.ParamSchema)["properties"].(map[string]any)
	require.True(t, ok)

	region, ok := properties["region"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "string", region["type"])
}

func TestToDAGDetailsOmitsInvalidParamSchema(t *testing.T) {
	t.Run("missing schema", func(t *testing.T) {
		details := toDAGDetails(&ir.DAG{Name: "no-schema"})

		require.NotNil(t, details)
		assert.Nil(t, details.ParamSchema)
	})

	t.Run("malformed schema", func(t *testing.T) {
		details := toDAGDetails(&ir.DAG{
			Name:        "bad-schema",
			ParamSchema: json.RawMessage(`{"type":"object"`),
		})

		require.NotNil(t, details)
		assert.Nil(t, details.ParamSchema)
	})
}

func TestToDAGDetailsIncludesArtifactsDir(t *testing.T) {
	details := toDAGDetails(&ir.DAG{
		Name: "artifacts-dir",
		Artifacts: &ir.ArtifactsConfig{
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

func TestToDAGRunDetailsIncludesLifecycleHandlers(t *testing.T) {
	handler := func(name string) *ir.Node {
		return &ir.Node{
			Step:      ir.Step{Name: name},
			Status:    ir.NodeSucceeded,
			Stdout:    name + ".out",
			StartedAt: "2026-07-25T12:50:56Z",
		}
	}

	status := ir.DAGRunStatus{
		Name:      "test-dag",
		DAGRunID:  "run-1",
		Status:    ir.Succeeded,
		OnInit:    handler("onInit"),
		OnWait:    handler("onWait"),
		OnSuccess: handler("onSuccess"),
		OnFailure: handler("onFailure"),
		OnAbort:   handler("onAbort"),
		OnExit:    handler("onExit"),
	}

	details := ToDAGRunDetails(status)

	require.NotNil(t, details.OnInit)
	assert.Equal(t, "onInit", details.OnInit.Step.Name)
	assert.Equal(t, "onInit.out", details.OnInit.Stdout)
	require.NotNil(t, details.OnWait)
	assert.Equal(t, "onWait", details.OnWait.Step.Name)
	require.NotNil(t, details.OnSuccess)
	require.NotNil(t, details.OnFailure)
	require.NotNil(t, details.OnAbort)
	require.NotNil(t, details.OnExit)
}

func TestToNodeIncludesNormalizedPushBackHistory(t *testing.T) {
	node := &ir.Node{
		Step: ir.Step{
			Name: "review",
			Approval: &ir.ApprovalConfig{
				Input: []string{"FEEDBACK"},
			},
		},
		Status:                 ir.NodeWaiting,
		StartedAt:              "2026-04-26T06:00:00Z",
		FinishedAt:             "2026-04-26T06:01:00Z",
		HumanTaskCompletedBy:   "operator",
		HumanTaskCompletedByID: "user-1",
		ApprovedBy:             "approver",
		ApprovedByID:           "user-2",
		RejectedBy:             "reviewer",
		RejectedByID:           "user-3",
		Stdout:                 "stdout.log",
		Stderr:                 "stderr.log",
		ApprovalIteration:      1,
		PushBackInputs:         map[string]string{"FEEDBACK": "revise the summary", "IGNORED": "x"},
		PushBackHistory: []ir.PushBackEntry{{
			Iteration: 1,
			By:        "reviewer",
			ByID:      "user-3",
			At:        "2026-04-26T06:02:00Z",
			Inputs:    map[string]string{"FEEDBACK": "revise the summary", "IGNORED": "x"},
		}},
	}

	result := toNode(node)
	require.NotNil(t, result.HumanTaskCompletedBy)
	assert.Equal(t, "operator", *result.HumanTaskCompletedBy)
	require.NotNil(t, result.HumanTaskCompletedById)
	assert.Equal(t, "user-1", *result.HumanTaskCompletedById)
	require.NotNil(t, result.ApprovedById)
	assert.Equal(t, "user-2", *result.ApprovedById)
	require.NotNil(t, result.RejectedById)
	assert.Equal(t, "user-3", *result.RejectedById)

	require.NotNil(t, result.PushBackHistory)
	require.Len(t, *result.PushBackHistory, 1)
	entry := (*result.PushBackHistory)[0]
	assert.Equal(t, 1, entry.Iteration)
	require.NotNil(t, entry.By)
	assert.Equal(t, "reviewer", *entry.By)
	require.NotNil(t, entry.ById)
	assert.Equal(t, "user-3", *entry.ById)
	require.NotNil(t, entry.At)
	assert.Equal(t, "2026-04-26T06:02:00Z", entry.At.UTC().Format(time.RFC3339))
	require.NotNil(t, entry.Inputs)
	assert.Equal(t, "revise the summary", (*entry.Inputs)["FEEDBACK"])
	_, ok := (*entry.Inputs)["IGNORED"]
	assert.False(t, ok)
}

func TestToDAGIncludesTypedSchedules(t *testing.T) {
	cronSchedule, err := ir.NewCronSchedule("*/5 * * * *")
	require.NoError(t, err)
	cronSchedule.Profile = "prod"

	oneOffSchedule, err := ir.NewOneOffSchedule("2026-03-29T02:10:00+01:00")
	require.NoError(t, err)

	dag := toDAG(&ir.DAG{
		Name:     "typed-schedules",
		Schedule: []ir.Schedule{cronSchedule, oneOffSchedule},
	})

	require.NotNil(t, dag.Schedule)
	require.Len(t, *dag.Schedule, 2)

	cronAPI := (*dag.Schedule)[0]
	require.NotNil(t, cronAPI.Kind)
	assert.Equal(t, openapi.ScheduleKindCron, *cronAPI.Kind)
	assert.Equal(t, "*/5 * * * *", cronAPI.Expression)
	require.NotNil(t, cronAPI.Profile)
	assert.Equal(t, "prod", string(*cronAPI.Profile))
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
	oneOffSchedule, err := ir.NewOneOffSchedule("2026-03-29T02:10:00Z")
	require.NoError(t, err)

	details := toDAGDetails(&ir.DAG{
		Name:     "typed-schedules",
		Schedule: []ir.Schedule{oneOffSchedule},
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
		coreStatus  ir.NodeStatus
		apiStatus   openapi.NodeStatus
		statusLabel openapi.NodeStatusLabel
	}{
		{
			name:        "running",
			coreStatus:  ir.NodeRunning,
			apiStatus:   openapi.NodeStatusRunning,
			statusLabel: openapi.NodeStatusLabelRunning,
		},
		{
			name:        "retrying",
			coreStatus:  ir.NodeRetrying,
			apiStatus:   openapi.NodeStatusRetrying,
			statusLabel: openapi.NodeStatusLabelRetrying,
		},
		{
			name:        "partial success",
			coreStatus:  ir.NodePartiallySucceeded,
			apiStatus:   openapi.NodeStatusPartialSuccess,
			statusLabel: openapi.NodeStatusLabelPartiallySucceeded,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			node := &ir.Node{
				Status: tc.coreStatus,
				Step: ir.Step{
					Name: "step-" + tc.name,
				},
			}

			converted := toNode(node)

			assert.Equal(t, tc.apiStatus, converted.Status)
			assert.Equal(t, tc.statusLabel, converted.StatusLabel)
		})
	}
}

func TestToDAGRunDetailsIncludesBuildMetadata(t *testing.T) {
	t.Parallel()

	status := ir.DAGRunStatus{
		Name:     "build-dag",
		DAGRunID: "run-2",
		Status:   ir.Succeeded,
		NoReuse:  true,
		Nodes: []*ir.Node{{
			Step: ir.Step{
				ID:           "build",
				Name:         "build",
				Inputs:       []ir.StepInputDeclaration{{Name: "source", Path: "/data/source.txt"}},
				Outputs:      []ir.StepOutputDeclaration{{Name: "artifact", Path: "/data/artifact.txt"}},
				Dependencies: []string{"scripts/**", "config/app.yaml"},
			},
			Status: ir.NodeSucceeded,
			Build: &ir.BuildExecution{
				Decision:    "reuse",
				Phase:       "complete",
				Reason:      "matched",
				ProducerRun: ir.NewDAGRunRef("build-dag", "run-1"),
			},
		}},
	}

	details := ToDAGRunDetails(status)
	require.NotNil(t, details.NoReuse)
	assert.True(t, *details.NoReuse)
	require.Len(t, details.Nodes, 1)
	require.NotNil(t, details.Nodes[0].Build)
	assert.Equal(t, openapi.BuildExecutionDecision("reuse"), details.Nodes[0].Build.Decision)
	require.NotNil(t, details.Nodes[0].Build.ProducerRun)
	assert.Equal(t, "run-1", *details.Nodes[0].Build.ProducerRun.Id)
	require.NotNil(t, details.Nodes[0].Step.Inputs)
	assert.Equal(t, "/data/source.txt", (*details.Nodes[0].Step.Inputs)[0].Path)
	require.NotNil(t, details.Nodes[0].Step.Dependencies)
	assert.Equal(t, []string{"scripts/**", "config/app.yaml"}, *details.Nodes[0].Step.Dependencies)
	require.NotNil(t, details.Nodes[0].Step.Outputs)
	require.NotNil(t, (*details.Nodes[0].Step.Outputs)[0].Path)
	assert.Equal(t, "/data/artifact.txt", *(*details.Nodes[0].Step.Outputs)[0].Path)
}

func TestNodeStatusMappingIsExhaustive(t *testing.T) {
	t.Parallel()

	expected := map[openapi.NodeStatus]ir.NodeStatus{
		openapi.NodeStatusNotStarted:     ir.NodeNotStarted,
		openapi.NodeStatusRunning:        ir.NodeRunning,
		openapi.NodeStatusFailed:         ir.NodeFailed,
		openapi.NodeStatusAborted:        ir.NodeAborted,
		openapi.NodeStatusSuccess:        ir.NodeSucceeded,
		openapi.NodeStatusSkipped:        ir.NodeSkipped,
		openapi.NodeStatusPartialSuccess: ir.NodePartiallySucceeded,
		openapi.NodeStatusWaiting:        ir.NodeWaiting,
		openapi.NodeStatusRejected:       ir.NodeRejected,
		openapi.NodeStatusRetrying:       ir.NodeRetrying,
	}

	assert.Len(t, nodeStatusMapping, len(expected))
	assert.Equal(t, expected, nodeStatusMapping)
}

// Agent DAGs were previously called controller DAGs. Run status files written
// before the rename name the synthesized step __controller__ and store its
// progress under controllerState; the agent detail view must still populate
// from them.
func TestToDAGRunDetailsReadsLegacyControllerRunState(t *testing.T) {
	legacyStatus := `{
		"name": "cleanup",
		"dagRunId": "run-1",
		"nodes": [{
			"step": {"name": "__controller__", "executorConfig": {"type": "controller"}},
			"controllerState": {
				"tasks": [{"name": "vocabulary", "description": "Completed when clean.", "status": "done"}],
				"events": [{"turn": 1, "kind": "action", "name": "check_vocabulary", "status": "succeeded"}]
			}
		}]
	}`

	status, err := ir.StatusFromJSON(legacyStatus)
	require.NoError(t, err)

	details := ToDAGRunDetails(*status)

	require.NotNil(t, details.AgentTasks)
	require.Len(t, *details.AgentTasks, 1)
	assert.Equal(t, "vocabulary", (*details.AgentTasks)[0].Name)

	require.NotNil(t, details.AgentEvents)
	require.Len(t, *details.AgentEvents, 1)
	assert.Equal(t, "check_vocabulary", *(*details.AgentEvents)[0].Name)
}

// A node may carry agentState written as an explicit JSON null. That is no more
// a value than a missing field, so legacy progress recorded alongside it still
// has to reach the detail view.
func TestToDAGRunDetailsPrefersLegacyStateOverNullAgentState(t *testing.T) {
	legacyStatus := `{
		"name": "cleanup",
		"dagRunId": "run-1",
		"nodes": [{
			"step": {"name": "__controller__"},
			"agentState": null,
			"controllerState": {
				"tasks": [{"name": "vocabulary", "description": "Completed when clean.", "status": "done"}]
			}
		}]
	}`

	status, err := ir.StatusFromJSON(legacyStatus)
	require.NoError(t, err)

	details := ToDAGRunDetails(*status)

	require.NotNil(t, details.AgentTasks)
	require.Len(t, *details.AgentTasks, 1)
	assert.Equal(t, "vocabulary", (*details.AgentTasks)[0].Name)
}
