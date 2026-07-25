// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/controller"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/dagucloud/dagu/internal/llm/allproviders"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin"
)

// turn is one scripted controller decision returned by the fake model.
type turn struct {
	// tool and args produce a tool call; leave tool empty to answer with prose.
	tool    string
	args    map[string]any
	content string
}

// fakeModel serves the OpenAI-compatible chat completions API, replying with a
// fixed script of decisions so the controller loop can be driven deterministically.
type fakeModel struct {
	mu    sync.Mutex
	turns []turn
	calls int
}

func (m *fakeModel) next() (turn, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.calls >= len(m.turns) {
		return turn{}, false
	}
	t := m.turns[m.calls]
	m.calls++
	return t, true
}

func (m *fakeModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *fakeModel) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	t, ok := m.next()
	if !ok {
		// Exhausted script: answer without acting so the loop cannot spin.
		t = turn{content: "no further actions"}
	}

	message := map[string]any{"role": "assistant", "content": t.content}
	finish := "stop"
	if t.tool != "" {
		args, _ := json.Marshal(t.args)
		message["tool_calls"] = []map[string]any{{
			"id":   fmt.Sprintf("call_%d", m.callCount()),
			"type": "function",
			"function": map[string]any{
				"name":      t.tool,
				"arguments": string(args),
			},
		}}
		finish = "tool_calls"
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
}

// controllerHelper runs a controller DAG end to end against a scripted model.
type controllerHelper struct {
	test.Helper
	runner *runtime.Runner
	cfg    *runtime.Config
	dag    *core.DAG
	plan   *runtime.Plan
	model  *fakeModel
	// runErr is what Run returned, which determines the process exit code.
	runErr error
}

func setupController(t *testing.T, yamlTemplate string, turns ...turn) *controllerHelper {
	t.Helper()

	model := &fakeModel{turns: turns}
	server := httptest.NewServer(model)
	t.Cleanup(server.Close)

	th := test.Setup(t)
	dag, err := spec.LoadYAML(th.Context, fmt.Appendf(nil, yamlTemplate, server.URL))
	require.NoError(t, err)

	plan, err := runtime.NewPlan(dag.Steps...)
	require.NoError(t, err)

	cfg := &runtime.Config{
		LogDir:   th.Config.Paths.LogDir,
		DAGRunID: uuid.Must(uuid.NewV7()).String(),
	}

	return &controllerHelper{
		Helper: th,
		runner: runtime.New(cfg),
		cfg:    cfg,
		dag:    dag,
		plan:   plan,
		model:  model,
	}
}

func (ch *controllerHelper) run(t *testing.T) core.Status {
	t.Helper()

	ch.dag.WorkingDir = t.TempDir()
	logPath := path.Join(ch.cfg.LogDir, fmt.Sprintf("%s_%s.log", ch.dag.Name, ch.cfg.DAGRunID))
	ctx := runtime.NewContext(ch.Context, ch.dag, ch.cfg.DAGRunID, logPath)

	progressCh := make(chan *runtime.Node)
	drained := make(chan struct{})
	go func() {
		for range progressCh {
		}
		close(drained)
	}()

	ch.runErr = ch.runner.Run(ctx, ch.plan, progressCh)
	close(progressCh)
	<-drained

	return ch.runner.Status(context.Background(), ch.plan)
}

func (ch *controllerHelper) node(t *testing.T, name string) *runtime.Node {
	t.Helper()
	node := ch.plan.GetNodeByName(name)
	require.NotNil(t, node, "step %q is not in the plan", name)
	return node
}

const controllerDAG = `
type: controller
llm:
  provider: local
  model: test-model
  base_url: %s
  system: drive the workflow
steps:
  - name: alpha
    run: echo alpha
  - name: beta
    run: echo beta
  - name: boom
    run: exit 3
tasks:
  - name: first
    description: done when alpha ran
  - name: second
    description: done when beta ran
`

func TestControllerLoop_CompletesEveryTask(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{tool: "alpha"},
		turn{tool: controller.CompleteTaskTool, args: map[string]any{"task": "first", "reason": "alpha ran"}},
		turn{tool: "beta"},
		turn{tool: controller.CompleteTaskTool, args: map[string]any{"task": "second", "reason": "beta ran"}},
	)

	require.Equal(t, core.Succeeded, ch.run(t))
	assert.Equal(t, core.NodeSucceeded, ch.node(t, "alpha").State().Status)
	assert.Equal(t, core.NodeSucceeded, ch.node(t, "beta").State().Status)

	// The controller never chose boom, so it is skipped rather than left pending.
	assert.Equal(t, core.NodeSkipped, ch.node(t, "boom").State().Status)
	assert.Equal(t, core.NodeSucceeded, ch.node(t, core.ControllerStepName).State().Status)
}

func TestControllerLoop_RecoversFromFailedAction(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{tool: "boom"},
		turn{tool: "alpha"},
		turn{tool: controller.CompleteTaskTool, args: map[string]any{"task": "first", "reason": "alpha ran"}},
		turn{tool: "beta"},
		turn{tool: controller.CompleteTaskTool, args: map[string]any{"task": "second", "reason": "beta ran"}},
	)

	// A failing action is reported to the controller instead of aborting the run.
	require.Equal(t, core.PartiallySucceeded, ch.run(t))

	// The controller absorbed the failure, so the run itself did not error and
	// the process exits zero.
	require.NoError(t, ch.runErr)
	assert.Equal(t, core.NodeFailed, ch.node(t, "boom").State().Status)
	assert.Equal(t, core.NodeSucceeded, ch.node(t, "alpha").State().Status)

	messages := ch.node(t, core.ControllerStepName).GetChatMessages()
	require.NotEmpty(t, messages)
	assert.Contains(t, transcript(messages), "status: failed")
}

func TestControllerLoop_RerunsAnActionWithFreshArguments(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{tool: "alpha"},
		turn{tool: "alpha"},
		turn{tool: controller.CompleteTaskTool, args: map[string]any{"task": "first", "reason": "alpha ran twice"}},
		turn{tool: controller.CompleteTaskTool, args: map[string]any{"task": "second", "reason": "not needed"}},
	)

	require.Equal(t, core.Succeeded, ch.run(t))
	alpha := ch.node(t, "alpha")
	assert.Equal(t, core.NodeSucceeded, alpha.State().Status)
	assert.True(t, alpha.State().Repeated, "a re-run action is marked repeated")
}

func TestControllerLoop_RejectsUnknownToolAndTask(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{tool: "does_not_exist"},
		turn{tool: controller.CompleteTaskTool, args: map[string]any{"task": "nope", "reason": "wrong"}},
		turn{tool: controller.CompleteTaskTool, args: map[string]any{"task": "first", "reason": "ok"}},
		turn{tool: controller.CompleteTaskTool, args: map[string]any{"task": "second", "reason": "ok"}},
	)

	require.Equal(t, core.Succeeded, ch.run(t))

	text := transcript(ch.node(t, core.ControllerStepName).GetChatMessages())
	assert.Contains(t, text, `no such action "does_not_exist"`)
	assert.Contains(t, text, `unknown task "nope"`)
}

func TestControllerLoop_FailsWhenControllerStopsWithOpenTasks(t *testing.T) {
	t.Parallel()

	// Two consecutive turns without a tool call end the run.
	ch := setupController(t, controllerDAG,
		turn{content: "I am done"},
		turn{content: "still done"},
	)

	require.Equal(t, core.Failed, ch.run(t))
	assert.Equal(t, core.NodeFailed, ch.node(t, core.ControllerStepName).State().Status)
}

func transcript(messages []exec.LLMMessage) string {
	var out strings.Builder
	for _, msg := range messages {
		out.WriteString(string(msg.Role) + ": " + msg.Content + "\n")
	}
	return out.String()
}

const controllerHumanTaskDAG = `
type: controller
llm:
  provider: local
  model: test-model
  base_url: %s
steps:
  - name: alpha
    run: echo alpha
  - id: review
    name: review
    action: human.task
    with:
      prompt: approve alpha?
      form:
        type: object
        properties:
          approved: { type: boolean }
        required: [approved]
tasks:
  - name: shipped
    description: done when alpha ran and a person approved it
`

// TestControllerLoop_SuspendsForHumanTaskAndResumes covers the durable path: the
// controller opens a human task, the run reports Waiting and its state is
// persisted, and a later attempt picks the conversation back up.
func TestControllerLoop_SuspendsForHumanTaskAndResumes(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerHumanTaskDAG,
		turn{tool: "alpha"},
		turn{tool: "review"},
	)

	require.Equal(t, core.Waiting, ch.run(t))
	require.Equal(t, core.NodeWaiting, ch.node(t, "review").State().Status)

	// The controller itself must not be waiting, or completing the human task
	// would not release the run.
	require.Equal(t, core.NodeSucceeded, ch.node(t, core.ControllerStepName).State().Status)

	// Stand in for the human task service, which records the submission on the
	// persisted node and marks the step complete before re-queueing the run.
	restored := roundTripNodes(t, ch, func(node *exec.Node) {
		if node.Step.Name == "review" {
			node.Status = core.NodeSucceeded
			node.HumanTaskInput = json.RawMessage(`{"approved":true}`)
		}
	})

	resumed := resumeController(t, ch, restored,
		turn{tool: controller.CompleteTaskTool, args: map[string]any{"task": "shipped", "reason": "approved"}},
	)

	require.Equal(t, core.Succeeded, resumed.status)
	assert.Contains(t, resumed.transcript, `{"approved":true}`,
		"the submission is reported back to the controller")
	assert.Contains(t, resumed.transcript, "alpha",
		"the conversation from before the suspension is preserved")
}

// roundTripNodes serializes the plan's nodes the way a finished attempt is
// persisted and reads them back, so the test exercises real persistence rather
// than in-memory state.
func roundTripNodes(t *testing.T, ch *controllerHelper, complete func(*exec.Node)) []*runtime.Node {
	t.Helper()

	nodeData := make([]runtime.NodeData, 0, len(ch.plan.Nodes()))
	for _, node := range ch.plan.Nodes() {
		nodeData = append(nodeData, node.NodeData())
	}
	status := transform.NewStatusBuilder(ch.dag).Create(
		ch.cfg.DAGRunID, core.Waiting, 0, time.Now(), transform.WithNodes(nodeData))

	encoded, err := json.Marshal(status)
	require.NoError(t, err)
	var decoded exec.DAGRunStatus
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	nodes := make([]*runtime.Node, 0, len(decoded.Nodes))
	for _, node := range decoded.Nodes {
		complete(node)
		nodes = append(nodes, transform.ToNode(node))
	}
	return nodes
}

type resumeResult struct {
	status     core.Status
	transcript string
}

func resumeController(t *testing.T, prev *controllerHelper, nodes []*runtime.Node, turns ...turn) resumeResult {
	t.Helper()

	model := &fakeModel{turns: turns}
	server := httptest.NewServer(model)
	t.Cleanup(server.Close)

	dag, err := spec.LoadYAML(prev.Context, fmt.Appendf(nil, controllerHumanTaskDAG, server.URL))
	require.NoError(t, err)
	dag.WorkingDir = t.TempDir()

	plan, err := runtime.NewPlanFromNodes(nodes...)
	require.NoError(t, err)

	cfg := &runtime.Config{LogDir: prev.cfg.LogDir, DAGRunID: prev.cfg.DAGRunID}
	runner := runtime.New(cfg)

	logPath := path.Join(cfg.LogDir, fmt.Sprintf("%s_resume.log", dag.Name))
	ctx := runtime.NewContext(prev.Context, dag, cfg.DAGRunID, logPath)

	progressCh := make(chan *runtime.Node)
	drained := make(chan struct{})
	go func() {
		for range progressCh {
		}
		close(drained)
	}()
	_ = runner.Run(ctx, plan, progressCh)
	close(progressCh)
	<-drained

	ctrl := plan.GetNodeByName(core.ControllerStepName)
	require.NotNil(t, ctrl)
	return resumeResult{
		status:     runner.Status(context.Background(), plan),
		transcript: transcript(ctrl.GetChatMessages()),
	}
}

// TestControllerLoop_StallCounterResetsOnAction guards the "consecutive" in the
// stall rule: a reply that uses a tool clears the count, so an occasional silent
// turn between real work does not end the run.
func TestControllerLoop_StallCounterResetsOnAction(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{content: "thinking"}, // stall, gets the reminder
		turn{tool: controller.CompleteTaskTool, args: map[string]any{"task": "first", "reason": "ok"}},
		turn{content: "thinking again"}, // stall again, must get a fresh reminder
		turn{tool: controller.CompleteTaskTool, args: map[string]any{"task": "second", "reason": "ok"}},
	)

	require.Equal(t, core.Succeeded, ch.run(t))
}
