// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	persiststore "github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

const (
	decisionDAGName  = "release"
	decisionDAGRunID = "run-1"
)

// decisionDAGSpec parks on a human task and two independent approval gates,
// so deciding one step leaves the run waiting on the others.
const decisionDAGSpec = `name: release
steps:
  - name: build
    id: build
    run: "true"

  - name: review
    id: review
    depends: [build]
    action: human.task
    with:
      prompt: Choose the release target
      form:
        type: object
        properties:
          environment:
            type: string
            enum: [staging, production]
        required: [environment]
      push_back:
        rewind_to: build
        form:
          type: object
          properties:
            feedback:
              type: string

  - name: gate_a
    id: gate_a
    run: "true"
    approval:
      prompt: Approve the deployment
      input: [ticket]
      required: [ticket]

  - name: gate_b
    id: gate_b
    run: "true"
    approval:
      prompt: Approve the announcement
`

// decisionFixture serves the Dagu API over file stores holding one run parked
// at a waiting checkpoint. The run is written directly, so no process runs.
type decisionFixture struct {
	svc       *Service
	runs      *persis.DAGRunRepository
	attemptID string
}

func newDecisionFixture(t *testing.T, opts ...frontendapi.APIOption) *decisionFixture {
	t.Helper()

	ctx := context.Background()
	dataDir := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.DataDir = dataDir
	cfg.Paths.DAGRunsDir = filepath.Join(dataDir, "dag-runs")
	cfg.Paths.ProcDir = filepath.Join(dataDir, "proc")
	cfg.Paths.QueueDir = filepath.Join(dataDir, "queue")
	cfg.Server.Permissions = map[config.Permission]bool{config.PermissionRunDAGs: true}
	// A queue for the DAG makes a resume enqueue instead of launching a process.
	cfg.Queues.Enabled = true
	cfg.Queues.Config = []config.QueueConfig{{Name: decisionDAGName, MaxActiveRuns: 1}}

	runs := persisfile.NewDAGRunRepository(cfg)
	procs := persisfile.NewProcRepository(cfg)
	queueStore := persiststore.NewQueueStore(persisfile.NewBackend(cfg.Paths).Collection(persis.CollectionQueue))
	dags := testutil.NewFileDAGRepository(t.TempDir(), filedag.WithSkipExamples(true))
	api := frontendapi.New(dags, runs, queueStore, procs, runtime.NewManager(runs, procs, cfg), cfg, nil, nil, prometheus.NewRegistry(), nil, opts...)

	dag, err := spec.LoadYAML(ctx, []byte(decisionDAGSpec))
	require.NoError(t, err)
	attempt, err := runs.CreateAttempt(ctx, dag, time.Now(), decisionDAGRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	status := ir.InitialStatus(dag)
	status.DAGRunID = decisionDAGRunID
	status.AttemptID = attempt.ID()
	status.Status = ir.Waiting
	status.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	for _, node := range status.Nodes {
		node.Status = ir.NodeWaiting
		if node.Step.Name == "build" {
			node.Status = ir.NodeSucceeded
		}
	}
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))

	return &decisionFixture{svc: &Service{api: api}, runs: runs, attemptID: attempt.ID()}
}

// decide calls dagu_execute for the fixture run and returns whether the call
// failed and its structured output.
func (f *decisionFixture) decide(t *testing.T, ctx context.Context, args map[string]any) (bool, map[string]any) {
	t.Helper()

	args["name"] = decisionDAGName
	args["dagRunId"] = decisionDAGRunID
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	result, err := f.svc.executeTool(ctx, &mcpsdk.CallToolRequest{
		Params: &mcpsdk.CallToolParamsRaw{Name: toolExecute, Arguments: raw},
	})
	require.NoError(t, err)
	output, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	return result.IsError, output
}

func (f *decisionFixture) node(t *testing.T, stepName string) *ir.Node {
	t.Helper()

	attempt, err := f.runs.FindAttempt(context.Background(), ir.NewDAGRunRef(decisionDAGName, decisionDAGRunID))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(context.Background())
	require.NoError(t, err)
	node, err := status.NodeByName(stepName)
	require.NoError(t, err)
	return node
}

func TestApproveApprovalGate(t *testing.T) {
	fixture := newDecisionFixture(t)

	failed, output := fixture.decide(t, context.Background(), map[string]any{
		"action":    executeActionApprove,
		"stepName":  "gate_a",
		"inputs":    map[string]any{"ticket": "CHG-42"},
		"attemptId": fixture.attemptID,
	})
	require.False(t, failed, output)
	require.Equal(t, executeActionApprove, output["action"])
	require.Equal(t, "gate_a", output["stepName"])
	require.Equal(t, "dagu://runs/release/run-1", output["runUri"])
	// The other steps still wait, so the run stays at its checkpoint.
	require.EqualValues(t, ir.Waiting, output["status"])

	node := fixture.node(t, "gate_a")
	require.Equal(t, ir.NodeSucceeded, node.Status)
	require.Equal(t, map[string]string{"ticket": "CHG-42"}, node.ApprovalInputs)
}

func TestRejectApprovalGate(t *testing.T) {
	fixture := newDecisionFixture(t)

	failed, output := fixture.decide(t, context.Background(), map[string]any{
		"action":   executeActionReject,
		"stepName": "gate_b",
		"reason":   "announcement is premature",
	})
	require.False(t, failed, output)
	require.EqualValues(t, ir.Rejected, output["status"])

	node := fixture.node(t, "gate_b")
	require.Equal(t, ir.NodeRejected, node.Status)
	require.Equal(t, "announcement is premature", node.RejectionReason)
}

func TestPushBackApprovalGate(t *testing.T) {
	fixture := newDecisionFixture(t)

	failed, output := fixture.decide(t, context.Background(), map[string]any{
		"action":   executeActionPushBack,
		"stepName": "gate_a",
		"inputs":   map[string]any{"ticket": "CHG-43"},
	})
	require.False(t, failed, output)
	require.Equal(t, 1, fixture.node(t, "gate_a").ApprovalIteration)
}

func TestApproveCompletesHumanTask(t *testing.T) {
	fixture := newDecisionFixture(t)

	failed, output := fixture.decide(t, context.Background(), map[string]any{
		"action":   executeActionApprove,
		"stepName": "review",
		"inputs":   map[string]any{"environment": "production"},
	})
	require.False(t, failed, output)

	node := fixture.node(t, "review")
	require.Equal(t, ir.NodeSucceeded, node.Status)
	require.JSONEq(t, `{"environment":"production"}`, string(node.HumanTaskInput))
}

func TestPushBackHumanTask(t *testing.T) {
	fixture := newDecisionFixture(t)

	failed, output := fixture.decide(t, context.Background(), map[string]any{
		"action":   executeActionPushBack,
		"stepName": "review",
		"inputs":   map[string]any{"feedback": "add release notes"},
	})
	require.False(t, failed, output)
	require.Equal(t, 1, fixture.node(t, "review").ApprovalIteration)
}

func TestRejectHumanTaskRefused(t *testing.T) {
	fixture := newDecisionFixture(t)

	failed, output := fixture.decide(t, context.Background(), map[string]any{
		"action":   executeActionReject,
		"stepName": "review",
	})
	require.True(t, failed)
	require.Equal(t, executeErrorInvalidToolInput, output["code"])
	require.Equal(t, executeFieldAction, output["field"])
	require.Equal(t, ir.NodeWaiting, fixture.node(t, "review").Status)
}

func TestDecideStepNotWaiting(t *testing.T) {
	fixture := newDecisionFixture(t)

	failed, output := fixture.decide(t, context.Background(), map[string]any{
		"action":   executeActionApprove,
		"stepName": "build",
	})
	require.True(t, failed)
	require.Equal(t, executeErrorConflict, output["code"])
	require.Contains(t, output["message"], "succeeded")
}

// A decision made from a read of an earlier attempt must change nothing.
func TestDecideStaleAttempt(t *testing.T) {
	fixture := newDecisionFixture(t)

	failed, output := fixture.decide(t, context.Background(), map[string]any{
		"action":    executeActionApprove,
		"stepName":  "gate_b",
		"attemptId": "earlier-attempt",
	})
	require.True(t, failed)
	require.Equal(t, executeErrorConflict, output["code"])
	require.Equal(t, map[string]any{"attemptId": fixture.attemptID}, output["details"])
	require.Equal(t, ir.NodeWaiting, fixture.node(t, "gate_b").Status)
}

// viewerAuth enables authorization without serving any auth operation.
type viewerAuth struct {
	frontendapi.AuthService
}

func TestApproveRequiresExecuteRole(t *testing.T) {
	fixture := newDecisionFixture(t, frontendapi.WithAuthService(viewerAuth{}))
	ctx := auth.WithUser(context.Background(), auth.NewUser("viewer", "", auth.RoleViewer))

	failed, output := fixture.decide(t, ctx, map[string]any{
		"action":   executeActionApprove,
		"stepName": "gate_b",
	})
	require.True(t, failed)
	require.Equal(t, executeErrorUnauthorized, output["code"])
	require.Equal(t, ir.NodeWaiting, fixture.node(t, "gate_b").Status)
}

// A decision from Dagu Cloud needs the server to allow remote approvals, and
// the attempt the decision was read from.
func TestRemoteDecisionNeedsApprovalsAndAttempt(t *testing.T) {
	fixture := newDecisionFixture(t)
	approvals := false
	fixture.svc.remote = &remoteCalls{approvals: func(context.Context) bool { return approvals }}
	approve := func(args map[string]any) (bool, map[string]any) {
		args["action"], args["stepName"] = executeActionApprove, "gate_b"
		return fixture.decide(t, context.Background(), args)
	}

	failed, output := approve(map[string]any{"attemptId": fixture.attemptID})
	require.True(t, failed)
	require.Equal(t, executeErrorUnauthorized, output["code"])
	require.Contains(t, output["message"], "Remote approvals are off")

	approvals = true
	failed, output = approve(map[string]any{})
	require.True(t, failed)
	require.Equal(t, executeFieldAttemptID, output["field"])
	require.Equal(t, ir.NodeWaiting, fixture.node(t, "gate_b").Status)

	failed, output = approve(map[string]any{"attemptId": fixture.attemptID})
	require.False(t, failed, output)
	require.Equal(t, ir.NodeSucceeded, fixture.node(t, "gate_b").Status)
}

// A server without users of its own still holds a user Dagu Cloud vouches
// for to their role.
func TestRemoteViewerCannotDecide(t *testing.T) {
	fixture := newDecisionFixture(t)
	fixture.svc.remote = &remoteCalls{approvals: func(context.Context) bool { return true }}
	viewer := auth.WithUser(context.Background(), &auth.User{ID: "cloud:user-1", Role: auth.RoleViewer, AuthProvider: auth.AuthProviderCloud})

	failed, output := fixture.decide(t, viewer, map[string]any{
		"action":    executeActionApprove,
		"stepName":  "gate_b",
		"attemptId": fixture.attemptID,
	})
	require.True(t, failed)
	require.Equal(t, executeErrorUnauthorized, output["code"])
	require.Equal(t, ir.NodeWaiting, fixture.node(t, "gate_b").Status)
}
