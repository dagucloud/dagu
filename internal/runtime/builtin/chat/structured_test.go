// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// classifySchema is an output_schema a model can answer through respond.
func classifySchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"category": map[string]any{"type": "string", "enum": []any{"refund", "complaint", "question"}},
			"amount":   map[string]any{"type": "number"},
		},
		"required": []any{"category"},
	}
}

func TestValidateStep(t *testing.T) {
	t.Parallel()

	withSchema := func(mutate func(map[string]any)) map[string]any {
		schema := classifySchema()
		mutate(schema)
		return schema
	}
	tests := []struct {
		name    string
		schema  map[string]any
		llm     *ir.LLMConfig
		wantErr string
	}{
		{name: "NoSchema", llm: &ir.LLMConfig{Tools: []string{"respond"}}},
		{name: "Valid", schema: classifySchema(), llm: &ir.LLMConfig{Tools: []string{"lookup"}}},
		{
			name:    "MissingType",
			schema:  withSchema(func(s map[string]any) { delete(s, "type") }),
			wantErr: "type: object",
		},
		{
			name:    "ArrayType",
			schema:  withSchema(func(s map[string]any) { s["type"] = "array" }),
			wantErr: "type: object",
		},
		{
			name:    "NoProperties",
			schema:  map[string]any{"type": "object"},
			wantErr: "at least one property",
		},
		{
			name:    "RequiredNotListed",
			schema:  withSchema(func(s map[string]any) { s["required"] = []any{"category", "currency"} }),
			wantErr: "currency",
		},
		{
			name:    "WebSearch",
			schema:  classifySchema(),
			llm:     &ir.LLMConfig{WebSearch: &ir.WebSearchConfig{Enabled: true}},
			wantErr: "web search",
		},
		{
			name:    "RespondTool",
			schema:  classifySchema(),
			llm:     &ir.LLMConfig{Tools: []string{"lookup", "respond"}},
			wantErr: "reserved",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateStep(ir.Step{OutputSchema: tt.schema, LLM: tt.llm})
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// Handler steps are not validated at load time, so the executor applies the
// same checks.
func TestNewChatExecutorValidatesOutputSchema(t *testing.T) {
	t.Parallel()

	_, err := newChatExecutor(chatRuntimeContext(t, nil), ir.Step{
		LLM:          &ir.LLMConfig{Provider: "openai", Model: "gpt-4o"},
		OutputSchema: map[string]any{"type": "object"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one property")
}

// A tool DAG whose name is respond is found only once the DAG loads.
func TestNewChatExecutorRejectsRespondToolDAG(t *testing.T) {
	t.Parallel()

	ctx := chatRuntimeContext(t, nil)
	rCtx := runtime.GetDAGContext(ctx)
	rCtx.DAG.LocalDAGs = map[string]*ir.DAG{"answer-tool": {Name: "respond"}}

	step := ir.Step{
		LLM: &ir.LLMConfig{Provider: "openai", Model: "gpt-4o", Tools: []string{"answer-tool"}},
	}
	_, err := newChatExecutor(ctx, step)
	require.NoError(t, err, "the name is free without output_schema")

	step.OutputSchema = classifySchema()
	_, err = newChatExecutor(ctx, step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved")
}

// scriptedProvider answers chat requests with scripted responses in order and
// records the requests.
type scriptedProvider struct {
	mu        sync.Mutex
	responses []*llmpkg.ChatResponse
	requests  []*llmpkg.ChatRequest
}

func (p *scriptedProvider) Chat(_ context.Context, req *llmpkg.ChatRequest) (*llmpkg.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	if len(p.responses) == 0 {
		return nil, errors.New("no scripted response left")
	}
	resp := p.responses[0]
	p.responses = p.responses[1:]
	return resp, nil
}

func (p *scriptedProvider) ChatStream(context.Context, *llmpkg.ChatRequest) (<-chan llmpkg.StreamEvent, error) {
	return nil, errors.New("a structured answer must not stream")
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) Requests() []*llmpkg.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.requests)
}

// respondWith is a model turn that calls the respond tool with arguments.
func respondWith(arguments string) *llmpkg.ChatResponse {
	return &llmpkg.ChatResponse{ToolCalls: []llmpkg.ToolCall{respondCall(arguments)}}
}

func respondCall(arguments string) llmpkg.ToolCall {
	return llmpkg.ToolCall{ID: "call-respond", Type: "function", Function: llmpkg.ToolCallFunction{Name: "respond", Arguments: arguments}}
}

// structuredRun is a chat step with an output_schema whose models are
// answered by scripted providers, keyed by model name.
type structuredRun struct {
	executor *Executor
	stdout   bytes.Buffer
	stderr   bytes.Buffer
}

func newStructuredRun(t *testing.T, step ir.Step, providers map[string]*scriptedProvider) *structuredRun {
	t.Helper()
	ctx := chatRuntimeContext(t, nil)
	exec, err := newChatExecutor(ctx, step)
	require.NoError(t, err)
	run := &structuredRun{executor: exec.(*Executor)}
	run.executor.SetStdout(&run.stdout)
	run.executor.SetStderr(&run.stderr)
	run.executor.newProvider = func(_ context.Context, cfg *ir.LLMConfig) (llmpkg.Provider, error) {
		provider, ok := providers[cfg.Model]
		require.True(t, ok, "no provider for model %s", cfg.Model)
		return provider, nil
	}
	return run
}

func (r *structuredRun) Run(t *testing.T) error {
	t.Helper()
	return r.executor.Run(chatRuntimeContext(t, nil))
}

func classifyStep() ir.Step {
	return ir.Step{
		LLM:          &ir.LLMConfig{Provider: "openai", Model: "gpt-4o", System: "You classify notes."},
		Messages:     []ir.PromptMessage{{Role: ir.LLMRoleUser, Content: "Refund my 12.50"}},
		OutputSchema: classifySchema(),
	}
}

// The answer is the respond call's arguments, limited to the listed
// properties, and the request forces the respond tool without streaming.
func TestStructuredOutputAnswer(t *testing.T) {
	t.Parallel()

	provider := &scriptedProvider{responses: []*llmpkg.ChatResponse{
		respondWith(`{"category":"refund","amount":12.5,"note":"<b>dropped</b>"}`),
	}}
	run := newStructuredRun(t, classifyStep(), map[string]*scriptedProvider{"gpt-4o": provider})

	require.NoError(t, run.Run(t))
	assert.Equal(t, `{"amount":12.5,"category":"refund"}`+"\n", run.stdout.String())

	requests := provider.Requests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "required", req.ToolChoice)
	require.Len(t, req.Tools, 1)
	assert.Equal(t, "respond", req.Tools[0].Function.Name)
	assert.Equal(t, classifySchema(), req.Tools[0].Function.Parameters)
	assert.Equal(t, llmpkg.RoleSystem, req.Messages[0].Role)
	assert.Equal(t, "You classify notes.\n\n"+respondInstruction, req.Messages[0].Content)

	saved := run.executor.GetMessages()
	require.Len(t, saved, 3)
	assert.Equal(t, "You classify notes.", saved[0].Content, "the instruction is not saved")
	assert.Equal(t, `{"amount":12.5,"category":"refund"}`, saved[2].Content)
	assert.Empty(t, saved[2].ToolCalls)
	require.Len(t, run.executor.GetToolDefinitions(), 1)
}

// A model that answers in text instead of calling the tool is accepted when
// the text is the JSON answer.
func TestStructuredOutputTextAnswer(t *testing.T) {
	t.Parallel()

	provider := &scriptedProvider{responses: []*llmpkg.ChatResponse{
		{Content: "```json\n{\"category\":\"question\"}\n```"},
	}}
	step := classifyStep()
	step.LLM.System = ""
	run := newStructuredRun(t, step, map[string]*scriptedProvider{"gpt-4o": provider})

	require.NoError(t, run.Run(t))
	assert.Equal(t, `{"category":"question"}`+"\n", run.stdout.String())
	first := provider.Requests()[0].Messages[0]
	assert.Equal(t, llmpkg.RoleSystem, first.Role)
	assert.Equal(t, respondInstruction, first.Content)
}

// An unusable answer gets one correction: the answer comes back as plain
// assistant text, followed by the reason.
func TestStructuredOutputCorrection(t *testing.T) {
	t.Parallel()

	provider := &scriptedProvider{responses: []*llmpkg.ChatResponse{
		respondWith(`{"category":"praise"}`),
		respondWith(`{"category":"complaint"}`),
	}}
	run := newStructuredRun(t, classifyStep(), map[string]*scriptedProvider{"gpt-4o": provider})

	require.NoError(t, run.Run(t))
	assert.Equal(t, `{"category":"complaint"}`+"\n", run.stdout.String())

	requests := provider.Requests()
	require.Len(t, requests, 2)
	second := requests[1].Messages
	require.Len(t, second, 4)
	assert.Equal(t, llmpkg.RoleAssistant, second[2].Role)
	assert.Equal(t, `{"category":"praise"}`, second[2].Content)
	assert.Empty(t, second[2].ToolCalls)
	assert.Equal(t, llmpkg.RoleUser, second[3].Role)
	assert.Contains(t, second[3].Content, "Call the respond tool again")
	assert.Contains(t, run.stderr.String(), `{"category":"praise"}`)
}

// After the correction, an answer that still cannot be used fails the step
// with an error that carries none of the answer.
func TestStructuredOutputRejectsAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		answer *llmpkg.ChatResponse
		reason string
	}{
		{name: "OutsideEnum", answer: respondWith(`{"category":"secret-praise"}`), reason: "secret-praise"},
		{name: "MissingRequired", answer: respondWith(`{"amount":4}`), reason: "category"},
		{name: "NotAnObject", answer: respondWith(`["secret-praise"]`), reason: "must be a JSON object"},
		{name: "NoJSON", answer: &llmpkg.ChatResponse{Content: "secret-praise"}, reason: "no JSON answer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			provider := &scriptedProvider{responses: []*llmpkg.ChatResponse{tt.answer, tt.answer}}
			run := newStructuredRun(t, classifyStep(), map[string]*scriptedProvider{"gpt-4o": provider})

			err := run.Run(t)
			require.ErrorIs(t, err, errNoAnswer)
			assert.NotContains(t, err.Error(), "secret-praise")
			assert.Empty(t, run.stdout.String())
			assert.Contains(t, run.stderr.String(), tt.reason)
			assert.Len(t, provider.Requests(), 2)
		})
	}
}

// Integers beyond float64 keep their digits in the published answer.
func TestStructuredOutputLargeInteger(t *testing.T) {
	t.Parallel()

	step := classifyStep()
	step.OutputSchema = map[string]any{
		"type":       "object",
		"properties": map[string]any{"id": map[string]any{"type": "integer"}},
	}
	provider := &scriptedProvider{responses: []*llmpkg.ChatResponse{respondWith(`{"id":12345678901234567890123}`)}}
	run := newStructuredRun(t, step, map[string]*scriptedProvider{"gpt-4o": provider})

	require.NoError(t, run.Run(t))
	assert.Equal(t, `{"id":12345678901234567890123}`+"\n", run.stdout.String())
}

// A model that cannot answer after its correction gives way to the next one.
func TestStructuredOutputFallback(t *testing.T) {
	t.Parallel()

	step := classifyStep()
	step.LLM = &ir.LLMConfig{Models: []ir.ModelEntry{
		{Provider: "openai", Name: "weak"},
		{Provider: "openai", Name: "strong"},
	}}
	weak := &scriptedProvider{responses: []*llmpkg.ChatResponse{{Content: "no idea"}, {Content: "still no idea"}}}
	strong := &scriptedProvider{responses: []*llmpkg.ChatResponse{respondWith(`{"category":"refund"}`)}}
	run := newStructuredRun(t, step, map[string]*scriptedProvider{"weak": weak, "strong": strong})

	require.NoError(t, run.Run(t))
	assert.Equal(t, `{"category":"refund"}`+"\n", run.stdout.String())
	assert.Len(t, weak.Requests(), 2)
	assert.Len(t, strong.Requests(), 1)
}

// toolRegistry offers one tool that the tests never run.
func toolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools:    map[string]*toolInfo{"lookup": {Name: "lookup", Description: "Look up a note"}},
		dagNames: map[string]string{"lookup": "lookup"},
	}
}

// Tools are offered next to respond. A turn that calls respond ends the
// session, and the other calls of that turn are not run.
func TestStructuredOutputWithTools(t *testing.T) {
	t.Parallel()

	missing := llmpkg.ToolCall{ID: "call-missing", Type: "function", Function: llmpkg.ToolCallFunction{Name: "missing", Arguments: "{}"}}
	provider := &scriptedProvider{responses: []*llmpkg.ChatResponse{
		{ToolCalls: []llmpkg.ToolCall{missing}},
		{ToolCalls: []llmpkg.ToolCall{{ID: "call-skipped", Type: "function", Function: llmpkg.ToolCallFunction{Name: "missing"}}, respondCall(`{"category":"refund"}`)}},
	}}
	run := newStructuredRun(t, classifyStep(), map[string]*scriptedProvider{"gpt-4o": provider})
	run.executor.toolRegistry = toolRegistry()

	require.NoError(t, run.Run(t))
	assert.Equal(t, `{"category":"refund"}`+"\n", run.stdout.String())

	requests := provider.Requests()
	require.Len(t, requests, 2)
	names := make([]string, 0, len(requests[0].Tools))
	for _, tool := range requests[0].Tools {
		names = append(names, tool.Function.Name)
	}
	assert.Equal(t, []string{"lookup", "respond"}, names)
	assert.Equal(t, "required", requests[0].ToolChoice)

	var toolResults []string
	for _, msg := range run.executor.GetMessages() {
		if msg.Role == ir.LLMRoleTool {
			toolResults = append(toolResults, msg.ToolCallID)
		}
	}
	assert.Equal(t, []string{"call-missing"}, toolResults)
}

// A tool loop that never answers fails at the iteration limit.
func TestStructuredOutputToolLimit(t *testing.T) {
	t.Parallel()

	missing := &llmpkg.ChatResponse{ToolCalls: []llmpkg.ToolCall{{ID: "call-missing", Type: "function", Function: llmpkg.ToolCallFunction{Name: "missing"}}}}
	provider := &scriptedProvider{responses: []*llmpkg.ChatResponse{missing, missing, missing}}
	step := classifyStep()
	step.LLM.MaxToolIterations = new(2)
	run := newStructuredRun(t, step, map[string]*scriptedProvider{"gpt-4o": provider})
	run.executor.toolRegistry = toolRegistry()

	err := run.Run(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max tool iterations (2)")
	assert.Len(t, provider.Requests(), 2)
	assert.Empty(t, run.stdout.String())
}

// A model that reaches the tool iteration limit has failed like one that
// cannot answer, so the next model starts over.
func TestStructuredOutputToolLimitFallback(t *testing.T) {
	t.Parallel()

	missing := &llmpkg.ChatResponse{ToolCalls: []llmpkg.ToolCall{{ID: "call-missing", Type: "function", Function: llmpkg.ToolCallFunction{Name: "missing"}}}}
	looping := &scriptedProvider{responses: []*llmpkg.ChatResponse{missing, missing}}
	answering := &scriptedProvider{responses: []*llmpkg.ChatResponse{respondWith(`{"category":"question"}`)}}
	step := classifyStep()
	step.LLM = &ir.LLMConfig{
		Models: []ir.ModelEntry{
			{Provider: "openai", Name: "looping"},
			{Provider: "openai", Name: "answering"},
		},
		MaxToolIterations: new(2),
	}
	run := newStructuredRun(t, step, map[string]*scriptedProvider{"looping": looping, "answering": answering})
	run.executor.toolRegistry = toolRegistry()

	require.NoError(t, run.Run(t))
	assert.Equal(t, `{"category":"question"}`+"\n", run.stdout.String())
	assert.Len(t, looping.Requests(), 2)
	assert.Len(t, answering.Requests(), 1)
}
