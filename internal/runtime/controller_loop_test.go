// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
	mu     sync.Mutex
	turns  []turn
	calls  int
	system string
}

// lastSystemPrompt returns the system message of the most recent request.
func (m *fakeModel) lastSystemPrompt() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.system
}

// captureSystem records the system message so tests can assert on the prompt.
func (m *fakeModel) captureSystem(r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			m.system = msg.Content
		}
	}
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

func (m *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.captureSystem(r)
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
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "alpha ran"}},
		turn{tool: "beta"},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "beta ran"}},
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
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "alpha ran"}},
		turn{tool: "beta"},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "beta ran"}},
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
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "alpha ran twice"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "not needed"}},
	)

	require.Equal(t, core.Succeeded, ch.run(t))
	alpha := ch.node(t, "alpha")
	assert.Equal(t, core.NodeSucceeded, alpha.State().Status)
	assert.True(t, alpha.State().Repeated, "a re-run action is marked repeated")
}

func TestControllerLoop_ObservesTheOutputOfARerunAction(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{tool: "alpha"},
		turn{tool: "alpha"},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "alpha ran twice"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "not needed"}},
	)

	require.Equal(t, core.Succeeded, ch.run(t))

	// Each attempt writes its own log, and the controller decides what to do
	// next from what it reads there. An attempt whose output never reached the
	// file leaves the controller guessing.
	stdout := ch.node(t, "alpha").State().Stdout
	require.NotEmpty(t, stdout)
	written, err := os.ReadFile(stdout)
	require.NoError(t, err)
	assert.Equal(t, "alpha", strings.TrimSpace(string(written)))
}

func TestControllerLoop_RejectsUnknownToolAndTask(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{tool: "does_not_exist"},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "nope", "status": "completed", "reason": "wrong"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "ok"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "ok"}},
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
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "shipped", "status": "completed", "reason": "approved"}},
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
	status          core.Status
	transcript      string
	controllerState json.RawMessage
}

func resumeController(t *testing.T, prev *controllerHelper, nodes []*runtime.Node, turns ...turn) resumeResult {
	t.Helper()
	return resumeControllerWith(t, prev, controllerHumanTaskDAG, nodes, turns...)
}

func resumeControllerWith(
	t *testing.T,
	prev *controllerHelper,
	yamlTemplate string,
	nodes []*runtime.Node,
	turns ...turn,
) resumeResult {
	t.Helper()

	model := &fakeModel{turns: turns}
	server := httptest.NewServer(model)
	t.Cleanup(server.Close)

	dag, err := spec.LoadYAML(prev.Context, fmt.Appendf(nil, yamlTemplate, server.URL))
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
		status:          runner.Status(context.Background(), plan),
		transcript:      transcript(ctrl.GetChatMessages()),
		controllerState: ctrl.State().ControllerState,
	}
}

// TestControllerLoop_StallCounterResetsOnAction guards the "consecutive" in the
// stall rule: a reply that uses a tool clears the count, so an occasional silent
// turn between real work does not end the run.
func TestControllerLoop_StallCounterResetsOnAction(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{content: "thinking"}, // stall, gets the reminder
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "ok"}},
		turn{content: "thinking again"}, // stall again, must get a fresh reminder
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "ok"}},
	)

	require.Equal(t, core.Succeeded, ch.run(t))
}

// TestControllerLoop_ReopensTaskAndRedoesWork covers the review-rejects-work
// cycle: a completed task is reopened and the action behind it runs again.
func TestControllerLoop_ReopensTaskAndRedoesWork(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{tool: "alpha"},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "built"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "open", "reason": "review rejected it"}},
		turn{tool: "alpha"},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "rebuilt"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "ok"}},
	)

	require.Equal(t, core.Succeeded, ch.run(t))
	assert.True(t, ch.node(t, "alpha").State().Repeated, "the redone action ran again")

	text := transcript(ch.node(t, core.ControllerStepName).GetChatMessages())
	assert.Contains(t, text, `Task "first" is now open`)
}

func TestControllerLoop_RejectsReopeningAnOpenTask(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "open", "reason": "oops"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "ok"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "ok"}},
	)

	require.Equal(t, core.Succeeded, ch.run(t))
	assert.Contains(t, transcript(ch.node(t, core.ControllerStepName).GetChatMessages()),
		`task "first" is already open`)
}

// TestControllerLoop_RecordsADecisionTimeline covers the ordering record the
// Status view renders: the dependency graph cannot express what a controller
// did, so the run keeps its own timeline.
func TestControllerLoop_RecordsADecisionTimeline(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{tool: "boom"},
		turn{tool: "alpha"},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "alpha ran"}},
		turn{tool: "alpha"},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "again"}},
	)

	require.Equal(t, core.PartiallySucceeded, ch.run(t))

	events := controller.EventsFromState(ch.node(t, core.ControllerStepName).State().ControllerState)
	require.Len(t, events, 5)

	type row struct {
		kind    string
		name    string
		status  string
		attempt int
	}
	got := make([]row, 0, len(events))
	for _, e := range events {
		got = append(got, row{e.Kind, e.Name, e.Status, e.Attempt})
	}

	assert.Equal(t, []row{
		{controller.EventAction, "boom", "failed", 1},
		{controller.EventAction, "alpha", "succeeded", 1},
		{controller.EventTaskStatus, "first", "completed", 0},
		{controller.EventAction, "alpha", "succeeded", 2},
		{controller.EventTaskStatus, "second", "completed", 0},
	}, got)

	// Turn numbers let the timeline line up with the transcript.
	assert.Equal(t, 1, events[0].Turn)
	assert.Equal(t, 5, events[4].Turn)

	// An action carries timing so the view can show how long it took.
	assert.NotEmpty(t, events[0].StartedAt)
	assert.NotEmpty(t, events[0].FinishedAt)
	assert.Contains(t, events[0].Reason, "exit")
}

// TestControllerLoop_PreservesChildRunLinksAcrossReruns guards drill-down from a
// controller run: every attempt of a step must stay reachable, both from the
// step's own child-run list and from the timeline entry that produced it.
func TestControllerLoop_PreservesChildRunLinksAcrossReruns(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{tool: "alpha"},
		turn{tool: "alpha"},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "ok"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "ok"}},
	)
	require.Equal(t, core.Succeeded, ch.run(t))

	// alpha is a plain command step, so it produces no child runs. The timeline
	// still records both attempts, which is what the view lists.
	events := controller.EventsFromState(ch.node(t, core.ControllerStepName).State().ControllerState)
	actions := make([]controller.Event, 0, len(events))
	for _, e := range events {
		if e.Kind == controller.EventAction {
			actions = append(actions, e)
		}
	}
	require.Len(t, actions, 2)
	assert.Equal(t, 1, actions[0].Attempt)
	assert.Equal(t, 2, actions[1].Attempt)
}

// TestControllerLoop_SkippedTaskStillSucceeds covers a goal the controller
// judged unnecessary: nothing went wrong, so the run succeeds.
func TestControllerLoop_SkippedTaskStillSucceeds(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{tool: "alpha"},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{
			"task": "first", "status": "completed", "reason": "alpha ran"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{
			"task": "second", "status": "skipped", "reason": "nothing to do"}},
	)

	require.Equal(t, core.Succeeded, ch.run(t))
	require.NoError(t, ch.runErr)
}

// TestControllerLoop_FailedTaskFailsTheRun covers a goal the controller could
// not achieve: it settles the task, but the run must not report success.
func TestControllerLoop_FailedTaskFailsTheRun(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{
			"task": "first", "status": "completed", "reason": "ok"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{
			"task": "second", "status": "failed", "reason": "no such environment"}},
	)

	require.Equal(t, core.Failed, ch.run(t))
	require.Error(t, ch.runErr)
	assert.Contains(t, ch.runErr.Error(), "second (no such environment)")
}

// TestControllerLoop_RejectsAnUnknownTaskStatus keeps a bad status from ending
// the run: it is reported back so the controller can correct itself.
func TestControllerLoop_RejectsAnUnknownTaskStatus(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{
			"task": "first", "status": "done-ish", "reason": "?"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{
			"task": "first", "status": "completed", "reason": "ok"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{
			"task": "second", "status": "skipped", "reason": "ok"}},
	)

	require.Equal(t, core.Succeeded, ch.run(t))
	assert.Contains(t, transcript(ch.node(t, core.ControllerStepName).GetChatMessages()),
		`"done-ish" is not a task status`)
}

const controllerParamsDAG = `
type: controller
params:
  - GOAL: shipped
llm:
  provider: local
  model: test-model
  base_url: %s
  system: |
    The operator's instruction is ${params.GOAL}.
steps:
  - name: alpha
    run: echo alpha
tasks:
  - name: only
    description: Finished when ${params.GOAL} is true.
`

// TestControllerLoop_ResolvesPromptVariables covers parameterised instructions:
// the system prompt and the task descriptions are author-written prompt text, so
// a run can be steered by its params rather than by editing the DAG.
func TestControllerLoop_ResolvesPromptVariables(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerParamsDAG,
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{
			"task": "only", "status": "completed", "reason": "ok"}},
	)
	require.Equal(t, core.Succeeded, ch.run(t))

	// The description the controller judged against is the resolved one, and it
	// is what gets persisted.
	tasks := controller.TasksFromState(
		ch.node(t, core.ControllerStepName).State().ControllerState)
	require.Len(t, tasks, 1)
	assert.Equal(t, "Finished when shipped is true.", tasks[0].Description)
	assert.NotContains(t, tasks[0].Description, "${")

	// The prompt reached the model with the parameter expanded.
	system := ch.model.lastSystemPrompt()
	assert.Contains(t, system, "The operator's instruction is shipped.")
	assert.NotContains(t, system, "${params.GOAL}")
}

// TestControllerLoop_AsksTheUserAndResumesWithTheAnswer covers a question the
// DAG author never wrote: the controller composes it, the run waits on a person,
// and the reply comes back as the next observation.
func TestControllerLoop_AsksTheUserAndResumesWithTheAnswer(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerDAG,
		turn{tool: controller.AskUserTool, args: map[string]any{
			"question": "Which config should alpha use?"}},
	)

	require.Equal(t, core.Waiting, ch.run(t))

	ask := ch.node(t, core.AskUserStepName)
	require.Equal(t, core.NodeWaiting, ask.State().Status)

	// The question the controller wrote is what a person is shown.
	events := controller.EventsFromState(
		ch.node(t, core.ControllerStepName).State().ControllerState)
	last := events[len(events)-1]
	assert.Equal(t, controller.EventAskUser, last.Kind)
	assert.Equal(t, "Which config should alpha use?", last.Reason)

	// Answering it is an ordinary human task completion.
	restored := roundTripNodes(t, ch, func(node *exec.Node) {
		if node.Step.Name == core.AskUserStepName {
			node.Status = core.NodeSucceeded
			node.HumanTaskInput = json.RawMessage(`{"answer":"use config-b"}`)
		}
	})

	resumed := resumeControllerWith(t, ch, controllerDAG, restored,
		turn{tool: "alpha"},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{
			"task": "first", "status": "completed", "reason": "ran with config-b"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{
			"task": "second", "status": "skipped", "reason": "not needed"}},
	)

	require.Equal(t, core.Succeeded, resumed.status)
	assert.Contains(t, resumed.transcript, "answer: use config-b",
		"the reply reaches the controller as prose, not as a form payload")
}

// TestControllerLoop_RefusesToAskTheSameQuestionTwice guards the person on the
// other end: each question suspends the run, so a controller that ignores the
// answer it was given must not be able to ask again.
func TestControllerLoop_RefusesToAskTheSameQuestionTwice(t *testing.T) {
	t.Parallel()

	const question = "Which environment?"
	ch := setupController(t, controllerDAG,
		turn{tool: controller.AskUserTool, args: map[string]any{"question": question}},
	)
	require.Equal(t, core.Waiting, ch.run(t))

	restored := roundTripNodes(t, ch, func(node *exec.Node) {
		if node.Step.Name == core.AskUserStepName {
			node.Status = core.NodeSucceeded
			node.HumanTaskInput = json.RawMessage(`{"answer":"staging"}`)
		}
	})

	resumed := resumeControllerWith(t, ch, controllerDAG, restored,
		// The model asks again instead of using the answer it was given.
		turn{tool: controller.AskUserTool, args: map[string]any{"question": question}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{
			"task": "first", "status": "completed", "reason": "staging it is"}},
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{
			"task": "second", "status": "skipped", "reason": "not needed"}},
	)

	// The run finishes rather than suspending a second time.
	require.Equal(t, core.Succeeded, resumed.status)
	assert.Contains(t, resumed.transcript, "You already asked this and were told: staging")
}

// TestControllerLoop_FinalizesTheSuspendedActionEvent covers the timeline after a
// resume: an action recorded as waiting must not stay that way once it finished.
func TestControllerLoop_FinalizesTheSuspendedActionEvent(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerHumanTaskDAG,
		turn{tool: "review"},
	)
	require.Equal(t, core.Waiting, ch.run(t))

	restored := roundTripNodes(t, ch, func(node *exec.Node) {
		if node.Step.Name == "review" {
			node.Status = core.NodeSucceeded
			node.HumanTaskInput = json.RawMessage(`{"approved":true}`)
			node.FinishedAt = exec.FormatTime(time.Now())
		}
	})

	resumed := resumeControllerWith(t, ch, controllerHumanTaskDAG, restored,
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{
			"task": "shipped", "status": "completed", "reason": "approved"}},
	)
	require.Equal(t, core.Succeeded, resumed.status)

	events := controller.EventsFromState(resumed.controllerState)
	var review *controller.Event
	for i := range events {
		if events[i].Name == "review" {
			review = &events[i]
		}
	}
	require.NotNil(t, review)
	assert.Equal(t, core.NodeSucceeded.String(), review.Status,
		"the waiting entry is updated once the answer arrives")
	assert.NotEmpty(t, review.FinishedAt)
}

const controllerArrayModelDAG = `
type: controller
params:
  - PROVIDER: local
llm:
  model:
    - provider: ${params.PROVIDER}
      name: test-model
      base_url: %s
  max_tool_iterations: 3
steps:
  - name: alpha
    run: echo alpha
tasks:
  - name: only
    description: Finished immediately.
`

// TestControllerLoop_UsesArrayFormModel covers llm.model written as a list with
// value references. The legacy provider and model fields are empty in that shape,
// so a controller that reads them directly cannot reach a provider at all.
func TestControllerLoop_UsesArrayFormModel(t *testing.T) {
	t.Parallel()

	ch := setupController(t, controllerArrayModelDAG,
		turn{tool: controller.SetTaskStatusTool, args: map[string]any{
			"task": "only", "status": "completed", "reason": "ok"}},
	)

	require.Equal(t, core.Succeeded, ch.run(t))
	require.NoError(t, ch.runErr)
}
