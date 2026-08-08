// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToExecStatus_MapsRemoteFieldsExplicitly(t *testing.T) {
	t.Parallel()

	detail := &api.DAGRunDetails{
		Name:           "example",
		DagRunId:       "run-1",
		RootDAGRunName: "example",
		RootDAGRunId:   "run-1",
		Status:         api.Status(ir.Running),
		StartedAt:      "2026-04-02T00:00:00Z",
		FinishedAt:     "",
		Log:            "/tmp/example.log",
		Params:         new("P1=foo"),
		WorkerId:       new("worker-a"),
		Labels:         &[]string{"env=prod"},
		Nodes: []api.Node{
			{
				Step: api.Step{
					Name: "step-1",
					Commands: &[]api.CommandEntry{
						{Command: "echo", Args: &[]string{"hello"}},
					},
				},
				Status:    api.NodeStatus(ir.NodeRunning),
				StartedAt: "2026-04-02T00:00:01Z",
				Stdout:    "/tmp/stdout",
				Stderr:    "/tmp/stderr",
			},
		},
	}

	status, err := toExecStatus(detail)
	require.NoError(t, err)
	assert.Equal(t, "example", status.Name)
	assert.Equal(t, "run-1", status.DAGRunID)
	assert.Equal(t, ir.Running, status.Status)
	assert.Equal(t, "/tmp/example.log", status.Log)
	require.Len(t, status.Nodes, 1)
	assert.Equal(t, "step-1", status.Nodes[0].Step.Name)
	require.Len(t, status.Nodes[0].Step.Commands, 1)
	assert.Equal(t, "echo", status.Nodes[0].Step.Commands[0].Command)
	assert.Equal(t, []string{"hello"}, status.Nodes[0].Step.Commands[0].Args)
}

func TestRemoteStatusValueRejectsNone(t *testing.T) {
	t.Parallel()

	_, err := remoteStatusValue("none")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

func TestBuildRemoteHistoryQueryRejectsMalformedLimit(t *testing.T) {
	t.Parallel()

	command := &cobra.Command{Use: "history"}
	initFlags(command, historyFlags...)
	require.NoError(t, command.Flags().Set("limit", "10foo"))

	ctx := &Context{Command: command}
	_, _, err := buildRemoteHistoryQuery(ctx, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be an integer")

	require.NoError(t, command.Flags().Set("limit", "0"))
	_, _, err = buildRemoteHistoryQuery(ctx, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "greater than 0")
}

func TestBuildRemoteHistoryQueryParsesMultipleStatuses(t *testing.T) {
	t.Parallel()

	command := &cobra.Command{Use: "history"}
	initFlags(command, historyFlags...)
	require.NoError(t, command.Flags().Set("status", "running,queued"))

	ctx := &Context{Command: command}
	query, limit, err := buildRemoteHistoryQuery(ctx, nil)
	require.NoError(t, err)

	assert.Equal(t, 100, limit)
	assert.Equal(t, []int{int(ir.Running), int(ir.Queued)}, query.Statuses)
}

func TestRemoteClientListDAGRunsUsesRepeatedStatusParams(t *testing.T) {
	t.Parallel()

	statusValues := make(chan []string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		statusValues <- append([]string(nil), r.URL.Query()["status"]...)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dagRuns":[]}`))
	}))
	defer server.Close()

	client := &remoteClient{
		baseURL: server.URL,
		client:  server.Client(),
	}

	_, err := client.listDAGRuns(context.Background(), remoteHistoryQuery{
		Statuses: []int{int(ir.Running), int(ir.Queued)},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "5"}, <-statusValues)
}

func TestRemoteClientRetryDAGRunSendsChildTarget(t *testing.T) {
	t.Parallel()

	type request struct {
		path string
		body api.RetryDAGRunJSONBody
	}
	requests := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body api.RetryDAGRunJSONBody
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		requests <- request{path: r.URL.Path, body: body}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &remoteClient{
		baseURL: server.URL,
		client:  server.Client(),
	}
	stepName := "target"
	childRunID := "child-run"
	require.NoError(t, client.retryDAGRun(
		context.Background(),
		"root",
		"root-run",
		api.RetryDAGRunJSONBody{
			DagRunId:    "root-run",
			StepName:    &stepName,
			SubDAGRunId: &childRunID,
		},
	))

	got := <-requests
	assert.Equal(t, "/dag-runs/root/root-run/retry", got.path)
	assert.Equal(t, "root-run", got.body.DagRunId)
	require.NotNil(t, got.body.StepName)
	require.NotNil(t, got.body.SubDAGRunId)
	assert.Equal(t, "target", *got.body.StepName)
	assert.Equal(t, "child-run", *got.body.SubDAGRunId)
}

func TestWaitForRemoteStopHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	ctx := &Context{
		Context: cancelled,
		Remote: &remoteClient{
			client: &http.Client{Timeout: time.Minute},
		},
	}

	err := waitForRemoteStop(ctx, "example", "run-1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}

func TestEnrichRemoteHistoryStatusPopulatesErrorAndMetadata(t *testing.T) {
	t.Parallel()

	status := &exec.DAGRunStatus{Name: "example", DAGRunID: "run-1"}
	detail := &api.DAGRunDetails{
		Name:           "example",
		DagRunId:       "run-1",
		RootDAGRunName: "example",
		RootDAGRunId:   "run-1",
		Status:         api.Status(ir.Failed),
		WorkerId:       new("worker-a"),
		Labels:         &[]string{"env=prod"},
		Nodes: []api.Node{
			{
				Step:   api.Step{Name: "step-1"},
				Status: api.NodeStatus(ir.NodeFailed),
				Error:  new("boom"),
			},
		},
	}

	require.NoError(t, enrichRemoteHistoryStatus(status, detail))
	assert.Equal(t, []string{"env=prod"}, status.Labels)
	assert.Equal(t, "worker-a", status.WorkerID)
	assert.Contains(t, status.Error, "boom")
}
