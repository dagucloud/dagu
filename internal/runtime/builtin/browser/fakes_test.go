// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
)

// fakeLauncher hands out one shared fakeEngine so a test can observe a
// session across launch, detach, and reattach.
type fakeLauncher struct {
	engine          *fakeEngine
	mu              sync.Mutex
	launches        []launchOptions
	reattaches      []browserHandle
	reattachOptions []launchOptions
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
	l.reattachOptions = append(l.reattachOptions, opts)
	l.engine.attach(opts.Generate)
	return l.engine, nil
}

// fakeEngine performs acts and extracts by asking the model through the
// generate function, like the real browser runtime does.
type fakeEngine struct {
	handle browserHandle
	// replayFails makes recorded actions fail, as if the page changed.
	replayFails bool
	// actNavigatesTo is the page an act leaves the browser on, if set.
	actNavigatesTo string
	// actDialogs are the dialogs the next act opens and the browser accepts.
	actDialogs []dialog
	dialogs    []dialog
	// actBlocked counts the requests per host allowed_domains blocks while
	// the next act runs.
	actBlocked map[string]int
	blocked    map[string]int
	// blockedErr is the error every blocked-request count reports.
	blockedErr error
	// pageText and visible describe the page that fixed checks read.
	pageText string
	visible  []string
	// downloads and downloadErr script what the next download wait reports.
	downloads   []string
	downloadErr error
	// downloadWaits records the timeout of each download wait.
	downloadWaits []time.Duration
	mu            sync.Mutex
	generate      generateFunc
	url           string
	acts          []fakeAct
	replays       [][]recordedAction
	detached      bool
	closed        bool
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

func (e *fakeEngine) TakeDialogs() []dialog {
	e.mu.Lock()
	defer e.mu.Unlock()
	dialogs := e.dialogs
	e.dialogs = nil
	return dialogs
}

func (e *fakeEngine) TakeBlockedRequests() (map[string]int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	blocked := e.blocked
	e.blocked = nil
	return blocked, e.blockedErr
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
	if e.actNavigatesTo != "" {
		e.url = e.actNavigatesTo
	}
	e.dialogs = append(e.dialogs, e.actDialogs...)
	e.actDialogs = nil
	e.blocked, e.actBlocked = e.actBlocked, nil
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
		// The browser runtime reports a model that chose no element this way.
		return actOutcome{Message: "Failed to perform act: No action found"}, nil
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

// setPageText changes the page text while a step runs.
func (e *fakeEngine) setPageText(text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pageText = text
}

func (e *fakeEngine) PageText(context.Context) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pageText, nil
}

func (e *fakeEngine) SelectorVisible(_ context.Context, selector string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Contains(e.visible, selector), nil
}

// WaitForDownloads reports the scripted downloads once, as if they finished
// after the operation that started them.
func (e *fakeEngine) WaitForDownloads(_ context.Context, _ time.Duration, timeout time.Duration) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.downloadWaits = append(e.downloadWaits, timeout)
	downloads := e.downloads
	e.downloads = nil
	return downloads, e.downloadErr
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

// serveHungBrowser serves the DevTools endpoint of a browser that
// acknowledges every command, including Browser.close, and keeps running.
func serveHungBrowser(t *testing.T) string {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/hung"
			_ = json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": wsURL})
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var request struct {
			ID int `json:"id"`
		}
		_ = json.Unmarshal(data, &request)
		response, _ := json.Marshal(map[string]any{"id": request.ID, "result": map[string]any{}})
		_ = conn.Write(r.Context(), websocket.MessageText, response)
	}))
	t.Cleanup(server.Close)
	return server.URL
}
