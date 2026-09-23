// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
)

// fakeLauncher hands out one shared fakeEngine so a test can observe a
// session across launch, detach, and reattach.
type fakeLauncher struct {
	engine     *fakeEngine
	mu         sync.Mutex
	launches   []launchOptions
	reattaches []browserHandle
}

func (l *fakeLauncher) Launch(_ context.Context, opts launchOptions) (engine, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.launches = append(l.launches, opts)
	l.engine.attach(opts.Generate)
	return l.engine, nil
}

func (l *fakeLauncher) Reattach(_ context.Context, handle browserHandle, opts launchOptions) (engine, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reattaches = append(l.reattaches, handle)
	l.engine.attach(opts.Generate)
	return l.engine, nil
}

// fakeEngine performs acts and extracts by asking the model through the
// generate function, like the real browser runtime does.
type fakeEngine struct {
	handle browserHandle
	// replayFails makes recorded actions fail, as if the page changed.
	replayFails bool
	mu          sync.Mutex
	generate    generateFunc
	url         string
	acts        []fakeAct
	replays     [][]recordedAction
	detached    bool
	closed      bool
}

type fakeAct struct {
	instruction string
	variables   map[string]string
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{handle: browserHandle{CDPURL: "http://127.0.0.1:1", ExtensionID: "ext"}}
}

func (e *fakeEngine) attach(generate generateFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.generate = generate
	e.detached = false
}

func (e *fakeEngine) Goto(_ context.Context, url string, _ time.Duration) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.url = url
	return nil
}

func (e *fakeEngine) Act(ctx context.Context, instruction string, variables map[string]string, _ time.Duration) (actOutcome, error) {
	e.mu.Lock()
	e.acts = append(e.acts, fakeAct{instruction: instruction, variables: maps.Clone(variables)})
	generate := e.generate
	e.mu.Unlock()
	resp, err := generate(ctx, generateRequest{
		SchemaName: "Act",
		Schema:     json.RawMessage(`{"type":"object","properties":{"elementId":{"type":"string"}}}`),
		Messages:   []generateMessage{{Role: "user", Text: "instruction: " + instruction}},
	})
	if err != nil {
		return actOutcome{}, err
	}
	var choice struct {
		ElementID string `json:"elementId"`
	}
	if err := json.Unmarshal(resp.JSON, &choice); err != nil || choice.ElementID == "" {
		return actOutcome{Message: "no element matched"}, nil
	}
	return actOutcome{
		Success: true,
		Actions: []recordedAction{{Selector: "xpath=" + choice.ElementID, Method: "click"}},
	}, nil
}

func (e *fakeEngine) Replay(_ context.Context, actions []recordedAction, _ map[string]string, _ time.Duration) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.replays = append(e.replays, actions)
	return !e.replayFails, nil
}

func (e *fakeEngine) Extract(ctx context.Context, instruction string, schema json.RawMessage, _ time.Duration) (json.RawMessage, error) {
	resp, err := e.generate(ctx, generateRequest{
		SchemaName: "Extraction",
		Schema:     schema,
		Messages:   []generateMessage{{Role: "user", Text: "Instruction: " + instruction}},
	})
	if err != nil {
		return nil, err
	}
	return resp.JSON, nil
}

func (e *fakeEngine) WaitForSelector(context.Context, string, time.Duration) error {
	return nil
}

func (e *fakeEngine) Screenshot(context.Context) ([]byte, error) {
	return []byte("png"), nil
}

func (e *fakeEngine) CurrentURL(context.Context) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.url, nil
}

func (e *fakeEngine) Handle() browserHandle {
	return e.handle
}

func (e *fakeEngine) Detach(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.detached = true
	return nil
}

func (e *fakeEngine) Close(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	return nil
}

func (e *fakeEngine) actInstructions() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	instructions := make([]string, 0, len(e.acts))
	for _, act := range e.acts {
		instructions = append(instructions, act.instruction)
	}
	return instructions
}

// scriptedProvider answers each model request through a respond tool call.
type scriptedProvider struct {
	answer func(req *llmpkg.ChatRequest) (string, error)
	mu     sync.Mutex
	calls  []*llmpkg.ChatRequest
}

func (p *scriptedProvider) Chat(_ context.Context, req *llmpkg.ChatRequest) (*llmpkg.ChatResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, req)
	p.mu.Unlock()
	arguments, err := p.answer(req)
	if err != nil {
		return nil, err
	}
	return &llmpkg.ChatResponse{
		ToolCalls: []llmpkg.ToolCall{{
			ID: "call-1", Type: "function",
			Function: llmpkg.ToolCallFunction{Name: respondToolName, Arguments: arguments},
		}},
		Usage: llmpkg.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
	}, nil
}

func (p *scriptedProvider) ChatStream(context.Context, *llmpkg.ChatRequest) (<-chan llmpkg.StreamEvent, error) {
	return nil, errors.New("streaming is not used")
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// lastUserText returns the user text of a request.
func lastUserText(req *llmpkg.ChatRequest) string {
	for _, v := range slices.Backward(req.Messages) {
		if v.Role == llmpkg.RoleUser {
			return v.Content
		}
	}
	return ""
}

// pageModel answers like a model looking at a fixed page: acts pick an
// element, statements are judged by keyword, and extracts return fixtures.
func pageModel(extracts map[string]string, trueStatements ...string) func(req *llmpkg.ChatRequest) (string, error) {
	return func(req *llmpkg.ChatRequest) (string, error) {
		text := lastUserText(req)
		properties, _ := req.Tools[0].Function.Parameters["properties"].(map[string]any)
		if _, ok := properties["elementId"]; ok {
			return `{"elementId":"/html/body/button"}`, nil
		}
		if _, ok := properties["answer"]; ok {
			for _, statement := range trueStatements {
				if strings.Contains(text, statement) {
					return `{"answer":true,"reason":"the page shows it"}`, nil
				}
			}
			return `{"answer":false,"reason":"the page does not show it"}`, nil
		}
		for instruction, data := range extracts {
			if strings.Contains(text, instruction) {
				return data, nil
			}
		}
		return "", errors.New("unexpected request: " + text)
	}
}
