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
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/agentloop"
	"github.com/dagucloud/dagu/v2/internal/runtime/transform"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/dagucloud/dagu/v2/internal/llm/allproviders"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin"
)

// turn is one scripted agent decision returned by the fake model.
type turn struct {
	// tool and args produce a tool call; leave tool empty to answer with prose.
	tool    string
	args    map[string]any
	content string
}

// fakeModel serves the OpenAI-compatible chat completions API, replying with a
// fixed script of decisions so the agent loop can be driven deterministically.
type fakeModel struct {
	mu                     sync.Mutex
	turns                  []turn
	calls                  int
	requests               int
	contextFailureRequests map[int]bool
	failureStatus          int
	failureFromRequest     int
	promptTokens           int
	system                 string
	toolResults            []string
}

// lastSystemPrompt returns the system message of the most recent request.
func (m *fakeModel) lastSystemPrompt() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.system
}

// observations returns the tool results carried by the most recent request,
// which is the whole transcript the agent had built by that point.
func (m *fakeModel) observations() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.toolResults...)
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
	m.toolResults = m.toolResults[:0]
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			m.system = msg.Content
		case "tool":
			m.toolResults = append(m.toolResults, msg.Content)
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

func (m *fakeModel) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests
}

func (m *fakeModel) failContextOnRequests(requests ...int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.contextFailureRequests == nil {
		m.contextFailureRequests = make(map[int]bool)
	}
	for _, request := range requests {
		m.contextFailureRequests[request] = true
	}
}

func (m *fakeModel) failWithStatusFromRequest(status, request int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failureStatus = status
	m.failureFromRequest = request
}

func (m *fakeModel) setPromptTokens(tokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promptTokens = tokens
}

func (m *fakeModel) beginRequest() (failureStatus int, failContext bool, promptTokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests++
	if m.failureFromRequest > 0 && m.requests >= m.failureFromRequest {
		failureStatus = m.failureStatus
	}
	failContext = m.contextFailureRequests[m.requests]
	promptTokens = m.promptTokens
	if promptTokens == 0 {
		promptTokens = 1
	}
	return failureStatus, failContext, promptTokens
}

func (m *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.captureSystem(r)
	failureStatus, failContext, promptTokens := m.beginRequest()
	if failureStatus != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(failureStatus)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "injected model failure"},
		})
		return
	}
	if failContext {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "context_length_exceeded",
				"message": "request exceeded the model context",
			},
		})
		return
	}
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
		"usage": map[string]any{
			"prompt_tokens": promptTokens, "completion_tokens": 1, "total_tokens": promptTokens + 1,
		},
	})
}

// agentHelper runs an agent DAG end to end against a scripted model.
type agentHelper struct {
	test.Helper
	runner *runtime.Runner
	cfg    *runtime.Config
	dag    *ir.DAG
	plan   *runtime.Plan
	model  *fakeModel
	// runErr is what Run returned, which determines the process exit code.
	runErr error
}

func setupAgent(t *testing.T, yamlTemplate string, turns ...turn) *agentHelper {
	t.Helper()

	model := &fakeModel{turns: turns}
	return setupAgentModels(t, yamlTemplate, model)
}

func setupAgentModels(
	t *testing.T,
	yamlTemplate string,
	primary *fakeModel,
	fallbacks ...*fakeModel,
) *agentHelper {
	t.Helper()

	models := append([]*fakeModel{primary}, fallbacks...)
	formatArgs := make([]any, 0, len(models))
	for _, model := range models {
		server := httptest.NewServer(model)
		t.Cleanup(server.Close)
		formatArgs = append(formatArgs, server.URL)
	}

	th := test.Setup(t)
	dag, err := spec.LoadYAML(th.Context, fmt.Appendf(nil, yamlTemplate, formatArgs...))
	require.NoError(t, err)

	plan, err := runtime.NewPlan(dag.Steps...)
	require.NoError(t, err)

	cfg := &runtime.Config{
		LogDir:   th.Config.Paths.LogDir,
		DAGRunID: uuid.Must(uuid.NewV7()).String(),
	}

	return &agentHelper{
		Helper: th,
		runner: runtime.New(cfg),
		cfg:    cfg,
		dag:    dag,
		plan:   plan,
		model:  primary,
	}
}

func (ch *agentHelper) run(t *testing.T) ir.Status {
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

func (ch *agentHelper) node(t *testing.T, name string) *runtime.Node {
	t.Helper()
	node := ch.plan.GetNodeByName(name)
	require.NotNil(t, node, "step %q is not in the plan", name)
	return node
}

const agentDAG = `
type: agent
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

func TestAgentLoop_CompletesEveryTask(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentDAG,
		turn{tool: "alpha"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "alpha ran"}},
		turn{tool: "beta"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "beta ran"}},
	)

	require.Equal(t, ir.Succeeded, ch.run(t))
	assert.Equal(t, ir.NodeSucceeded, ch.node(t, "alpha").State().Status)
	assert.Equal(t, ir.NodeSucceeded, ch.node(t, "beta").State().Status)

	// The agent never chose boom, so it is skipped rather than left pending.
	assert.Equal(t, ir.NodeSkipped, ch.node(t, "boom").State().Status)
	assert.Equal(t, ir.NodeSucceeded, ch.node(t, ir.AgentStepName).State().Status)
}

func TestAgentLoop_RecoversFromFailedAction(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentDAG,
		turn{tool: "boom"},
		turn{tool: "alpha"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "alpha ran"}},
		turn{tool: "beta"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "beta ran"}},
	)

	// A failing action is reported to the agent instead of aborting the run.
	require.Equal(t, ir.PartiallySucceeded, ch.run(t))

	// The agent absorbed the failure, so the run itself did not error and
	// the process exits zero.
	require.NoError(t, ch.runErr)
	assert.Equal(t, ir.NodeFailed, ch.node(t, "boom").State().Status)
	assert.Equal(t, ir.NodeSucceeded, ch.node(t, "alpha").State().Status)

	messages := ch.node(t, ir.AgentStepName).GetChatMessages()
	require.NotEmpty(t, messages)
	assert.Contains(t, transcript(messages), "status: failed")
}

func TestAgentLoop_RerunsAnActionWithFreshArguments(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentDAG,
		turn{tool: "alpha"},
		turn{tool: "alpha"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "alpha ran twice"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "not needed"}},
	)

	require.Equal(t, ir.Succeeded, ch.run(t))
	alpha := ch.node(t, "alpha")
	assert.Equal(t, ir.NodeSucceeded, alpha.State().Status)
	assert.True(t, alpha.State().Repeated, "a re-run action is marked repeated")
}

func TestAgentLoop_ObservesTheOutputOfARerunAction(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentDAG,
		turn{tool: "alpha"},
		turn{tool: "alpha"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "alpha ran twice"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "not needed"}},
	)

	require.Equal(t, ir.Succeeded, ch.run(t))

	// The agent decides what to do next from what an action reported, so
	// every attempt has to come back with its output. An attempt that reports
	// nothing reads as one that ran fine and had nothing to say.
	reported := 0
	for _, observation := range ch.model.observations() {
		if strings.Contains(observation, "alpha") {
			reported++
		}
	}
	assert.Equal(t, 2, reported, "both attempts report their output")
}

const agentContextManagementDAG = `
type: agent
llm:
  provider: local
  model: test-model
  base_url: %s
  max_context_tokens: 10
  observation_keep_recent: 1
steps:
  - name: alpha
    run: echo alpha
  - name: beta
    run: echo beta
tasks:
  - name: first
    description: done when alpha ran
  - name: second
    description: done when beta ran
`

const agentOverflowRecoveryDAG = `
type: agent
llm:
  provider: local
  model: test-model
  base_url: %s
steps:
  - name: alpha
    run: echo alpha
  - name: beta
    run: echo beta
tasks:
  - name: first
    description: done when alpha ran
  - name: second
    description: done when beta ran
`

func TestAgentLoop_AgesObservationsAtPromptTokenThreshold(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentContextManagementDAG,
		turn{tool: "alpha"},
		turn{tool: "beta"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "first", "status": "completed", "reason": "alpha ran"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "second", "status": "completed", "reason": "beta ran"}},
	)
	ch.model.setPromptTokens(10)

	require.Equal(t, ir.Succeeded, ch.run(t))
	observations := ch.model.observations()
	require.Len(t, observations, 3)
	assert.Equal(t, "turn 1: alpha → succeeded", observations[0])
	assert.Equal(t, "turn 2: beta → succeeded", observations[1])
	assert.Contains(t, observations[2], `Task "first" is now completed`)

	agent := ch.node(t, ir.AgentStepName)
	restored, err := agentloop.LoadState(agent.State().AgentState, agent.GetChatMessages(), ch.dag)
	require.NoError(t, err)
	assert.True(t, restored.ObservationAging)
}

func TestAgentLoop_RecoversOnceFromContextOverflow(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentOverflowRecoveryDAG,
		turn{tool: "alpha"},
		turn{tool: "beta"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "first", "status": "completed", "reason": "alpha ran"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "second", "status": "completed", "reason": "beta ran"}},
	)
	ch.model.failContextOnRequests(3)

	require.Equal(t, ir.Succeeded, ch.run(t))
	assert.Equal(t, 5, ch.model.requestCount())
	observations := ch.model.observations()
	require.Len(t, observations, 3)
	assert.Equal(t, "turn 1: alpha → succeeded", observations[0])
	assert.Equal(t, "turn 2: beta → succeeded", observations[1])
}

func TestAgentLoop_FailsWhenCompactedRequestStillOverflows(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentOverflowRecoveryDAG,
		turn{tool: "alpha"},
	)
	ch.model.failContextOnRequests(2, 3)

	require.Equal(t, ir.Failed, ch.run(t))
	assert.Equal(t, 3, ch.model.requestCount())
	assert.ErrorContains(t, ch.runErr, "after aging observations")

	agent := ch.node(t, ir.AgentStepName)
	restored, err := agentloop.LoadState(agent.State().AgentState, agent.GetChatMessages(), ch.dag)
	require.NoError(t, err)
	assert.True(t, restored.ObservationAging)
}

func TestAgentLoop_DoesNotRetryUnchangedOverflowRequest(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentOverflowRecoveryDAG,
		turn{tool: "alpha"},
	)
	ch.model.failContextOnRequests(1)

	require.Equal(t, ir.Failed, ch.run(t))
	assert.Equal(t, 1, ch.model.requestCount())
	assert.ErrorIs(t, ch.runErr, llm.ErrContextTooLong)
}

const agentContextManagementDisabledDAG = `
type: agent
llm:
  provider: local
  model: test-model
  base_url: %s
  max_context_tokens: 0
  observation_max_bytes: 0
  observation_keep_recent: 0
steps:
  - name: alpha
    run: echo alpha
tasks:
  - name: first
    description: done when alpha ran
`

func TestAgentLoop_ContextManagementCanBeDisabled(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentContextManagementDisabledDAG,
		turn{tool: "alpha"},
	)
	ch.model.failContextOnRequests(1)

	require.Equal(t, ir.Failed, ch.run(t))
	assert.Equal(t, 1, ch.model.requestCount())
	assert.ErrorIs(t, ch.runErr, llm.ErrContextTooLong)
}

const agentObservationLimitDAG = `
type: agent
llm:
  provider: local
  model: test-model
  base_url: %s
  observation_max_bytes: 128
steps:
  - name: produce
    action: outputs.write
    with:
      values:
        BIG: "0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789"
tasks:
  - name: produced
    description: done when produce ran
`

func TestAgentLoop_LimitsObservationWithoutChangingStoredOutput(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentObservationLimitDAG,
		turn{tool: "produce"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "produced", "status": "completed", "reason": "produced"}},
	)

	require.Equal(t, ir.Succeeded, ch.run(t))
	observations := ch.model.observations()
	require.Len(t, observations, 1)
	assert.LessOrEqual(t, len(observations[0]), 128)
	assert.Contains(t, observations[0], "status: succeeded")
	assert.Contains(t, observations[0], "[observation truncated]")

	outputs := ch.node(t, "produce").State().OutputsValue
	require.NotNil(t, outputs)
	var stored map[string]string
	require.NoError(t, json.Unmarshal([]byte(*outputs), &stored))
	assert.Equal(t, strings.Repeat("0123456789", 37), stored["BIG"])
}

func TestAgentLoop_RejectsUnknownToolAndTask(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentDAG,
		turn{tool: "does_not_exist"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "nope", "status": "completed", "reason": "wrong"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "ok"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "ok"}},
	)

	require.Equal(t, ir.Succeeded, ch.run(t))

	text := transcript(ch.node(t, ir.AgentStepName).GetChatMessages())
	assert.Contains(t, text, `no such action "does_not_exist"`)
	assert.Contains(t, text, `unknown task "nope"`)
}

func TestAgentLoop_FailsWhenAgentStopsWithOpenTasks(t *testing.T) {
	t.Parallel()

	// Two consecutive turns without a tool call end the run.
	ch := setupAgent(t, agentDAG,
		turn{content: "I am done"},
		turn{content: "still done"},
	)

	require.Equal(t, ir.Failed, ch.run(t))
	assert.Equal(t, ir.NodeFailed, ch.node(t, ir.AgentStepName).State().Status)
}

func transcript(messages []ir.LLMMessage) string {
	var out strings.Builder
	for _, msg := range messages {
		out.WriteString(string(msg.Role) + ": " + msg.Content + "\n")
	}
	return out.String()
}

const agentHumanTaskDAG = `
type: agent
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

// TestAgentLoop_SuspendsForHumanTaskAndResumes covers the durable path: the
// agent opens a human task, the run reports Waiting and its state is
// persisted, and a later attempt picks the conversation back up.
func TestAgentLoop_SuspendsForHumanTaskAndResumes(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentHumanTaskDAG,
		turn{tool: "alpha"},
		turn{tool: "review"},
	)

	require.Equal(t, ir.Waiting, ch.run(t))
	require.Equal(t, ir.NodeWaiting, ch.node(t, "review").State().Status)

	// The agent itself must not be waiting, or completing the human task
	// would not release the run.
	require.Equal(t, ir.NodeSucceeded, ch.node(t, ir.AgentStepName).State().Status)

	// Stand in for the human task service, which records the submission on the
	// persisted node and marks the step complete before re-queueing the run.
	restored := roundTripNodes(t, ch, func(node *ir.Node) {
		if node.Step.Name == "review" {
			node.Status = ir.NodeSucceeded
			node.HumanTaskInput = json.RawMessage(`{"approved":true}`)
		}
	})

	resumed := resumeAgent(t, ch, restored,
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "shipped", "status": "completed", "reason": "approved"}},
	)

	require.Equal(t, ir.Succeeded, resumed.status)
	assert.Contains(t, resumed.transcript, `{"approved":true}`,
		"the submission is reported back to the agent")
	assert.Contains(t, resumed.transcript, "alpha",
		"the conversation from before the suspension is preserved")
}

// roundTripNodes serializes the plan's nodes the way a finished attempt is
// persisted and reads them back, so the test exercises real persistence rather
// than in-memory state.
func roundTripNodes(t *testing.T, ch *agentHelper, complete func(*ir.Node)) []*runtime.Node {
	t.Helper()

	nodeData := make([]runtime.NodeData, 0, len(ch.plan.Nodes()))
	for _, node := range ch.plan.Nodes() {
		nodeData = append(nodeData, node.NodeData())
	}
	status := ir.NewStatusBuilder(ch.dag).Create(
		ch.cfg.DAGRunID, ir.Waiting, 0, time.Now(), transform.WithNodes(nodeData))

	encoded, err := json.Marshal(status)
	require.NoError(t, err)
	var decoded ir.DAGRunStatus
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	nodes := make([]*runtime.Node, 0, len(decoded.Nodes))
	for _, node := range decoded.Nodes {
		complete(node)
		nodes = append(nodes, transform.ToNode(node))
	}
	return nodes
}

type resumeResult struct {
	status     ir.Status
	transcript string
	agentState json.RawMessage
}

func resumeAgent(t *testing.T, prev *agentHelper, nodes []*runtime.Node, turns ...turn) resumeResult {
	t.Helper()
	return resumeAgentWith(t, prev, agentHumanTaskDAG, nodes, turns...)
}

func resumeAgentWith(
	t *testing.T,
	prev *agentHelper,
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

	agent := plan.GetNodeByName(ir.AgentStepName)
	require.NotNil(t, agent)
	return resumeResult{
		status:     runner.Status(context.Background(), plan),
		transcript: transcript(agent.GetChatMessages()),
		agentState: agent.State().AgentState,
	}
}

// TestAgentLoop_StallCounterResetsOnAction guards the "consecutive" in the
// stall rule: a reply that uses a tool clears the count, so an occasional silent
// turn between real work does not end the run.
func TestAgentLoop_StallCounterResetsOnAction(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentDAG,
		turn{content: "thinking"}, // stall, gets the reminder
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "ok"}},
		turn{content: "thinking again"}, // stall again, must get a fresh reminder
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "ok"}},
	)

	require.Equal(t, ir.Succeeded, ch.run(t))
}

// TestAgentLoop_ReopensTaskAndRedoesWork covers the review-rejects-work
// cycle: a completed task is reopened and the action behind it runs again.
func TestAgentLoop_ReopensTaskAndRedoesWork(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentDAG,
		turn{tool: "alpha"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "built"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "open", "reason": "review rejected it"}},
		turn{tool: "alpha"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "rebuilt"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "ok"}},
	)

	require.Equal(t, ir.Succeeded, ch.run(t))
	assert.True(t, ch.node(t, "alpha").State().Repeated, "the redone action ran again")

	text := transcript(ch.node(t, ir.AgentStepName).GetChatMessages())
	assert.Contains(t, text, `Task "first" is now open`)
}

func TestAgentLoop_RejectsReopeningAnOpenTask(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentDAG,
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "open", "reason": "oops"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "ok"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "ok"}},
	)

	require.Equal(t, ir.Succeeded, ch.run(t))
	assert.Contains(t, transcript(ch.node(t, ir.AgentStepName).GetChatMessages()),
		`task "first" is already open`)
}

// TestAgentLoop_RecordsADecisionTimeline covers the ordering record the
// Status view renders: the dependency graph cannot express what an agent
// did, so the run keeps its own timeline.
func TestAgentLoop_RecordsADecisionTimeline(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentDAG,
		turn{tool: "boom"},
		turn{tool: "alpha"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "alpha ran"}},
		turn{tool: "alpha"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "again"}},
	)

	require.Equal(t, ir.PartiallySucceeded, ch.run(t))

	events := agentloop.EventsFromState(ch.node(t, ir.AgentStepName).State().AgentState)
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
		{agentloop.EventAction, "boom", "failed", 1},
		{agentloop.EventAction, "alpha", "succeeded", 1},
		{agentloop.EventTaskStatus, "first", "completed", 0},
		{agentloop.EventAction, "alpha", "succeeded", 2},
		{agentloop.EventTaskStatus, "second", "completed", 0},
	}, got)

	// Turn numbers let the timeline line up with the transcript.
	assert.Equal(t, 1, events[0].Turn)
	assert.Equal(t, 5, events[4].Turn)

	// An action carries timing so the view can show how long it took.
	assert.NotEmpty(t, events[0].StartedAt)
	assert.NotEmpty(t, events[0].FinishedAt)
	assert.Contains(t, events[0].Reason, "exit")
}

// TestAgentLoop_PreservesChildRunLinksAcrossReruns guards drill-down from a
// agent run: every attempt of a step must stay reachable, both from the
// step's own child-run list and from the timeline entry that produced it.
func TestAgentLoop_PreservesChildRunLinksAcrossReruns(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentDAG,
		turn{tool: "alpha"},
		turn{tool: "alpha"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "first", "status": "completed", "reason": "ok"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{"task": "second", "status": "completed", "reason": "ok"}},
	)
	require.Equal(t, ir.Succeeded, ch.run(t))

	// alpha is a plain command step, so it produces no child runs. The timeline
	// still records both attempts, which is what the view lists.
	events := agentloop.EventsFromState(ch.node(t, ir.AgentStepName).State().AgentState)
	actions := make([]agentloop.Event, 0, len(events))
	for _, e := range events {
		if e.Kind == agentloop.EventAction {
			actions = append(actions, e)
		}
	}
	require.Len(t, actions, 2)
	assert.Equal(t, 1, actions[0].Attempt)
	assert.Equal(t, 2, actions[1].Attempt)
}

// TestAgentLoop_SkippedTaskStillSucceeds covers a goal the agent
// judged unnecessary: nothing went wrong, so the run succeeds.
func TestAgentLoop_SkippedTaskStillSucceeds(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentDAG,
		turn{tool: "alpha"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "first", "status": "completed", "reason": "alpha ran"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "second", "status": "skipped", "reason": "nothing to do"}},
	)

	require.Equal(t, ir.Succeeded, ch.run(t))
	require.NoError(t, ch.runErr)
}

// TestAgentLoop_FailedTaskFailsTheRun covers a goal the agent could
// not achieve: it settles the task, but the run must not report success.
func TestAgentLoop_FailedTaskFailsTheRun(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentDAG,
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "first", "status": "completed", "reason": "ok"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "second", "status": "failed", "reason": "no such environment"}},
	)

	require.Equal(t, ir.Failed, ch.run(t))
	require.Error(t, ch.runErr)
	assert.Contains(t, ch.runErr.Error(), "second (no such environment)")
}

// TestAgentLoop_RejectsAnUnknownTaskStatus keeps a bad status from ending
// the run: it is reported back so the agent can correct itself.
func TestAgentLoop_RejectsAnUnknownTaskStatus(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentDAG,
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "first", "status": "done-ish", "reason": "?"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "first", "status": "completed", "reason": "ok"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "second", "status": "skipped", "reason": "ok"}},
	)

	require.Equal(t, ir.Succeeded, ch.run(t))
	assert.Contains(t, transcript(ch.node(t, ir.AgentStepName).GetChatMessages()),
		`"done-ish" is not a task status`)
}

const agentParamsDAG = `
type: agent
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

// TestAgentLoop_ResolvesPromptVariables covers parameterised instructions:
// the system prompt and the task descriptions are author-written prompt text, so
// a run can be steered by its params rather than by editing the DAG.
func TestAgentLoop_ResolvesPromptVariables(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentParamsDAG,
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "only", "status": "completed", "reason": "ok"}},
	)
	require.Equal(t, ir.Succeeded, ch.run(t))

	// The description the agent judged against is the resolved one, and it
	// is what gets persisted.
	tasks := agentloop.TasksFromState(
		ch.node(t, ir.AgentStepName).State().AgentState)
	require.Len(t, tasks, 1)
	assert.Equal(t, "Finished when shipped is true.", tasks[0].Description)
	assert.NotContains(t, tasks[0].Description, "${")

	// The prompt reached the model with the parameter expanded.
	system := ch.model.lastSystemPrompt()
	assert.Contains(t, system, "The operator's instruction is shipped.")
	assert.NotContains(t, system, "${params.GOAL}")
}

// TestAgentLoop_AsksTheUserAndResumesWithTheAnswer covers a question the
// DAG author never wrote: the agent composes it, the run waits on a person,
// and the reply comes back as the next observation.
func TestAgentLoop_AsksTheUserAndResumesWithTheAnswer(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentDAG,
		turn{tool: agentloop.AskUserTool, args: map[string]any{
			"question": "Which config should alpha use?"}},
	)

	require.Equal(t, ir.Waiting, ch.run(t))

	ask := ch.node(t, ir.AskUserStepName)
	require.Equal(t, ir.NodeWaiting, ask.State().Status)

	// The question the agent wrote is what a person is shown.
	events := agentloop.EventsFromState(
		ch.node(t, ir.AgentStepName).State().AgentState)
	last := events[len(events)-1]
	assert.Equal(t, agentloop.EventAskUser, last.Kind)
	assert.Equal(t, "Which config should alpha use?", last.Reason)

	// Answering it is an ordinary human task completion.
	restored := roundTripNodes(t, ch, func(node *ir.Node) {
		if node.Step.Name == ir.AskUserStepName {
			node.Status = ir.NodeSucceeded
			node.HumanTaskInput = json.RawMessage(`{"answer":"use config-b"}`)
		}
	})

	resumed := resumeAgentWith(t, ch, agentDAG, restored,
		turn{tool: "alpha"},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "first", "status": "completed", "reason": "ran with config-b"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "second", "status": "skipped", "reason": "not needed"}},
	)

	require.Equal(t, ir.Succeeded, resumed.status)
	assert.Contains(t, resumed.transcript, "answer: use config-b",
		"the reply reaches the agent as prose, not as a form payload")
}

// TestAgentLoop_RefusesToAskTheSameQuestionTwice guards the person on the
// other end: each question suspends the run, so an agent that ignores the
// answer it was given must not be able to ask again.
func TestAgentLoop_RefusesToAskTheSameQuestionTwice(t *testing.T) {
	t.Parallel()

	const question = "Which environment?"
	ch := setupAgent(t, agentDAG,
		turn{tool: agentloop.AskUserTool, args: map[string]any{"question": question}},
	)
	require.Equal(t, ir.Waiting, ch.run(t))

	restored := roundTripNodes(t, ch, func(node *ir.Node) {
		if node.Step.Name == ir.AskUserStepName {
			node.Status = ir.NodeSucceeded
			node.HumanTaskInput = json.RawMessage(`{"answer":"staging"}`)
		}
	})

	resumed := resumeAgentWith(t, ch, agentDAG, restored,
		// The model asks again instead of using the answer it was given.
		turn{tool: agentloop.AskUserTool, args: map[string]any{"question": question}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "first", "status": "completed", "reason": "staging it is"}},
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "second", "status": "skipped", "reason": "not needed"}},
	)

	// The run finishes rather than suspending a second time.
	require.Equal(t, ir.Succeeded, resumed.status)
	assert.Contains(t, resumed.transcript, "You already asked this and were told: staging")
}

// TestAgentLoop_FinalizesTheSuspendedActionEvent covers the timeline after a
// resume: an action recorded as waiting must not stay that way once it finished.
func TestAgentLoop_FinalizesTheSuspendedActionEvent(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentHumanTaskDAG,
		turn{tool: "review"},
	)
	require.Equal(t, ir.Waiting, ch.run(t))

	restored := roundTripNodes(t, ch, func(node *ir.Node) {
		if node.Step.Name == "review" {
			node.Status = ir.NodeSucceeded
			node.HumanTaskInput = json.RawMessage(`{"approved":true}`)
			node.FinishedAt = stringutil.FormatTime(time.Now())
		}
	})

	resumed := resumeAgentWith(t, ch, agentHumanTaskDAG, restored,
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "shipped", "status": "completed", "reason": "approved"}},
	)
	require.Equal(t, ir.Succeeded, resumed.status)

	events := agentloop.EventsFromState(resumed.agentState)
	var review *agentloop.Event
	for i := range events {
		if events[i].Name == "review" {
			review = &events[i]
		}
	}
	require.NotNil(t, review)
	assert.Equal(t, ir.NodeSucceeded.String(), review.Status,
		"the waiting entry is updated once the answer arrives")
	assert.NotEmpty(t, review.FinishedAt)
}

const agentArrayModelDAG = `
type: agent
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

// TestAgentLoop_UsesArrayFormModel covers llm.model written as a list with
// value references. The legacy provider and model fields are empty in that shape,
// so an agent that reads them directly cannot reach a provider at all.
func TestAgentLoop_UsesArrayFormModel(t *testing.T) {
	t.Parallel()

	ch := setupAgent(t, agentArrayModelDAG,
		turn{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "only", "status": "completed", "reason": "ok"}},
	)

	require.Equal(t, ir.Succeeded, ch.run(t))
	require.NoError(t, ch.runErr)
}

const agentFallbackModelsDAG = `
type: agent
llm:
  model:
    - provider: local
      name: primary-model
      base_url: %s
    - provider: local
      name: fallback-model
      base_url: %s
  max_tool_iterations: 5
steps:
  - name: alpha
    run: echo alpha
  - name: beta
    run: echo beta
tasks:
  - name: finished
    description: Finished when alpha and beta ran.
`

const agentThreeModelsDAG = `
type: agent
llm:
  model:
    - provider: local
      name: primary-model
      base_url: %s
    - provider: local
      name: fallback-one
      base_url: %s
    - provider: local
      name: fallback-two
      base_url: %s
  max_tool_iterations: 5
steps:
  - name: alpha
    run: echo alpha
  - name: beta
    run: echo beta
tasks:
  - name: finished
    description: Finished when alpha and beta ran.
`

func TestAgentLoop_FallsBackMidConversation(t *testing.T) {
	t.Parallel()

	primary := &fakeModel{turns: []turn{{tool: "alpha"}}}
	primary.failWithStatusFromRequest(http.StatusUnauthorized, 2)
	fallback := &fakeModel{turns: []turn{
		{tool: "beta"},
		{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "finished", "status": "completed", "reason": "both ran"}},
	}}
	ch := setupAgentModels(t, agentFallbackModelsDAG, primary, fallback)

	require.Equal(t, ir.Succeeded, ch.run(t))
	require.NoError(t, ch.runErr)
	assert.Equal(t, 2, primary.requestCount())
	assert.Equal(t, 2, fallback.requestCount())

	var models []string
	for _, msg := range ch.node(t, ir.AgentStepName).GetChatMessages() {
		if msg.Role == ir.LLMRoleAssistant && msg.Metadata != nil {
			models = append(models, msg.Metadata.Model)
		}
	}
	assert.Equal(t, []string{"primary-model", "fallback-model", "fallback-model"}, models)
	assert.Contains(t, strings.Join(fallback.observations(), "\n"), "alpha")
}

func TestAgentLoop_RecoversContextBeforeFallback(t *testing.T) {
	t.Parallel()

	primary := &fakeModel{turns: []turn{
		{tool: "alpha"},
		{tool: "beta"},
		{tool: agentloop.SetTaskStatusTool, args: map[string]any{
			"task": "finished", "status": "completed", "reason": "both ran"}},
	}}
	primary.failContextOnRequests(3)
	fallback := &fakeModel{}
	ch := setupAgentModels(t, agentFallbackModelsDAG, primary, fallback)

	require.Equal(t, ir.Succeeded, ch.run(t))
	require.NoError(t, ch.runErr)
	assert.Equal(t, 4, primary.requestCount())
	assert.Zero(t, fallback.requestCount())
}

func TestAgentLoop_FailsAfterAllModelsAreExhausted(t *testing.T) {
	t.Parallel()

	primary := &fakeModel{turns: []turn{{tool: "alpha"}}}
	primary.failWithStatusFromRequest(http.StatusUnauthorized, 2)
	fallbackOne := &fakeModel{turns: []turn{{tool: "beta"}}}
	fallbackOne.failWithStatusFromRequest(http.StatusUnauthorized, 2)
	fallbackTwo := &fakeModel{}
	fallbackTwo.failWithStatusFromRequest(http.StatusUnauthorized, 1)
	ch := setupAgentModels(
		t, agentThreeModelsDAG, primary, fallbackOne, fallbackTwo)

	require.Equal(t, ir.Failed, ch.run(t))
	require.Error(t, ch.runErr)
	var apiErr *llm.APIError
	require.ErrorAs(t, ch.runErr, &apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
	assert.ErrorContains(t, ch.runErr, "all 3 agent models exhausted")
	assert.ErrorContains(t, ch.runErr, "local/primary-model")
	assert.ErrorContains(t, ch.runErr, "local/fallback-one")
	assert.ErrorContains(t, ch.runErr, "local/fallback-two")
	assert.Equal(t, 2, primary.requestCount())
	assert.Equal(t, 2, fallbackOne.requestCount())
	assert.Equal(t, 1, fallbackTwo.requestCount())
}
