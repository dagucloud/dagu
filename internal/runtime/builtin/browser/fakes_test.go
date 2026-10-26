// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/dagucloud/dagu/v2/internal/browserhost/certproxy"
	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
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
	// actNavigatesTo is the page an act or a replay leaves the browser on,
	// if set.
	actNavigatesTo string
	// replayNavigatesTo, if set, is the page a performed replay leaves the
	// browser on in place of actNavigatesTo, so replays can load a page
	// while acts do not.
	replayNavigatesTo string
	// hidden lists the selectors of recorded actions whose element is on
	// the page but hidden, so a replay does not perform them.
	hidden []string
	// twoStepAct makes acts perform two actions, as a two-step act does.
	twoStepAct bool
	// actFills, if set, is what acts type into the element they choose, as
	// an act that types a value itself does.
	actFills string
	// actLosesPage is how many of the next acts lose the connection to the
	// page after their click lands.
	actLosesPage int
	// replayLosesPage is how many of the next replayed actions lose the
	// connection to the page after their click lands.
	replayLosesPage int
	// pageLoss, if set, is how acts and replays that lose the page report it
	// in place of errFakeSessionLost.
	pageLoss error
	// onAct runs while an act asks the model, for what happens meanwhile.
	onAct func()
	// actDialogs are the dialogs the next act opens and the browser accepts.
	actDialogs []dialog
	dialogs    []dialog
	// actBlocked counts the requests per host allowed_domains blocks while
	// the next act runs.
	actBlocked map[string]int
	blocked    map[string]int
	// blockedErr is the error every blocked-request count reports.
	blockedErr error
	// certificateEvents are what the certificate proxy reports next.
	certificateEvents []certproxy.Event
	// actWrites are the writes the write gate decides while the next act
	// runs, numbered as they are kept in writes.
	actWrites []writegate.Event
	writes    []writegate.Event
	// writesErr is the error every read of the write gate reports.
	writesErr error
	// downloadWrites are the writes the gate decides while the next
	// download wait runs.
	downloadWrites []writegate.Event
	// allowed are the signatures the write gate was told the operation
	// running may send, one entry per operation, and allowing whether it
	// still runs; endErr is the error telling the gate it ended reports.
	allowed  [][]string
	allowing bool
	endErr   error
	// outcome is what the browser shows after a page opened.
	outcome pageOutcome
	// pending are the connections the certificate proxy waits on.
	pending []certproxy.Pending
	// verifiedHosts are the hosts the certificate proxy vouches for.
	verifiedHosts []string
	// pageText and visible describe the page that fixed checks read.
	pageText string
	visible  []string
	// snapshot is the page's accessibility tree; its URL is the page's.
	snapshot pageSnapshot
	// downloads and downloadErr script what the next download wait reports,
	// and lateDownloads what only a wait that allows them to begin does.
	downloads     []string
	downloadErr   error
	lateDownloads []string
	// downloadWaits records the timeout of each download wait.
	downloadWaits []time.Duration
	// uploadHow is how uploads report the files reached the page;
	// uploadDirect when unset.
	uploadHow string
	// uploadErr, if set, is what uploads report in place of giving the
	// files.
	uploadErr error
	uploads   []fakeUpload
	mu        sync.Mutex
	generate  generateFunc
	url       string
	// loads counts the documents the page has loaded.
	loads    int
	acts     []fakeAct
	replays  []recordedAction
	detached bool
	closed   bool
}

type fakeAct struct {
	instruction string
	variables   map[string]string
	// secret names the variables the act was told hold a secret.
	secret []string
}

// fakeUpload is an upload the page was given: the element, or "" for the
// page's only file input, and the files' paths.
type fakeUpload struct {
	selector string
	paths    []string
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

func (e *fakeEngine) TakeCertificateEvents(context.Context) ([]certproxy.Event, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	events := e.certificateEvents
	e.certificateEvents = nil
	return events, nil
}

// keepWrites numbers writes as the gate keeps them, sending one the
// operation running may send. The caller holds e.mu.
func (e *fakeEngine) keepWrites(writes []writegate.Event) {
	for _, write := range writes {
		if e.allowing && slices.Contains(e.allowed[len(e.allowed)-1], write.Signature) {
			write.Class, write.Outcome = writegate.ClassApproved, writegate.OutcomeSent
		}
		write.Seq = int64(len(e.writes) + 1)
		e.writes = append(e.writes, write)
	}
}

func (e *fakeEngine) Writes(ctx context.Context, after int64) ([]writegate.Event, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// The runtime's calls end with their context, as the real engine's do.
	if err := cmp.Or(ctx.Err(), e.writesErr); err != nil {
		return nil, err
	}
	var events []writegate.Event
	for _, write := range e.writes {
		if write.Seq > after {
			events = append(events, write)
		}
	}
	return events, nil
}

func (e *fakeEngine) BeginWrites(_ context.Context, signatures []string, _ time.Duration) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.allowed = append(e.allowed, signatures)
	e.allowing = true
	return nil
}

func (e *fakeEngine) EndWrites(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.endErr != nil {
		return e.endErr
	}
	e.allowing = false
	return nil
}

func (e *fakeEngine) PageOutcome(context.Context) (pageOutcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.outcome, nil
}

func (e *fakeEngine) PendingConnections(context.Context) ([]certproxy.Pending, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pending, nil
}

func (e *fakeEngine) CertificateVerified(_ context.Context, host string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Contains(e.verifiedHosts, host), nil
}

func (e *fakeEngine) Goto(_ context.Context, url string, _ time.Duration) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.load(url)
	return nil
}

// load makes the page load a new document at url. The caller holds e.mu.
func (e *fakeEngine) load(url string) {
	e.url = url
	e.loads++
}

// errFakeSessionLost is how the browser runtime reports an operation whose
// page session was detached.
var errFakeSessionLost = fmt.Errorf("%w: Failed to perform act: -32001 Session with given id not found.", errPageSessionLost)

// errFakeContextLost is how the browser runtime reports an operation whose
// script context a new document replaced.
var errFakeContextLost = fmt.Errorf("%w: Failed to perform act: -32000 Cannot find context with specified id", errPageContextLost)

func (e *fakeEngine) Act(ctx context.Context, instruction string, variables map[string]string, secret func(string) bool, _ time.Duration) (actOutcome, error) {
	act := fakeAct{instruction: instruction, variables: maps.Clone(variables)}
	for _, name := range slices.Sorted(maps.Keys(variables)) {
		if secret == nil || secret(name) {
			act.secret = append(act.secret, name)
		}
	}
	e.mu.Lock()
	e.acts = append(e.acts, act)
	if e.actNavigatesTo != "" {
		e.load(e.actNavigatesTo)
	}
	e.dialogs = append(e.dialogs, e.actDialogs...)
	e.actDialogs = nil
	e.blocked, e.actBlocked = e.actBlocked, nil
	e.keepWrites(e.actWrites)
	e.actWrites = nil
	generate, onAct, twoStep, fills := e.generate, e.onAct, e.twoStepAct, e.actFills
	losesPage, pageLoss := e.actLosesPage > 0, cmp.Or(e.pageLoss, errFakeSessionLost)
	if losesPage {
		e.actLosesPage--
	}
	e.mu.Unlock()
	if onAct != nil {
		onAct()
	}
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
	if losesPage {
		return actOutcome{}, pageLoss
	}
	actions := []recordedAction{{Selector: "xpath=" + choice.ElementID, Method: "click"}}
	if fills != "" {
		actions[0] = recordedAction{Selector: "xpath=" + choice.ElementID, Method: "fill", Arguments: []string{fills}}
	}
	if twoStep {
		actions = append(actions, recordedAction{Selector: "xpath=" + choice.ElementID + "/next", Method: "click"})
	}
	return actOutcome{Success: true, Actions: actions}, nil
}

func (e *fakeEngine) Replay(_ context.Context, action recordedAction, _ map[string]string, _ time.Duration) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.replays = append(e.replays, action)
	if slices.Contains(e.hidden, action.Selector) {
		return false, nil
	}
	if target := cmp.Or(e.replayNavigatesTo, e.actNavigatesTo); target != "" {
		e.load(target)
	}
	if e.replayLosesPage > 0 {
		e.replayLosesPage--
		return false, cmp.Or(e.pageLoss, errFakeSessionLost)
	}
	return !e.replayFails, nil
}

func (e *fakeEngine) DocumentID(context.Context) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return fmt.Sprint(e.loads), nil
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

// Observe asks the model for the elements an instruction describes, as the
// browser runtime does.
func (e *fakeEngine) Observe(ctx context.Context, instruction string, _ time.Duration) ([]recordedAction, error) {
	e.mu.Lock()
	generate := e.generate
	e.mu.Unlock()
	resp, err := generate(ctx, generateRequest{
		SchemaName: "Observation",
		Schema:     json.RawMessage(`{"type":"object","properties":{"elements":{"type":"array"}}}`),
		Messages:   []generateMessage{{Role: "user", Text: "instruction: " + instruction}},
	})
	if err != nil {
		return nil, err
	}
	var answer struct {
		Elements []struct {
			ElementID   string `json:"elementId"`
			Description string `json:"description"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(resp.JSON, &answer); err != nil {
		return nil, err
	}
	found := make([]recordedAction, 0, len(answer.Elements))
	for _, element := range answer.Elements {
		found = append(found, recordedAction{Selector: "xpath=" + element.ElementID, Description: element.Description, Method: "click"})
	}
	return found, nil
}

func (e *fakeEngine) Upload(_ context.Context, selector string, files []uploadFile, _ time.Duration) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	e.uploads = append(e.uploads, fakeUpload{selector: selector, paths: paths})
	if e.uploadErr != nil {
		return "", e.uploadErr
	}
	return cmp.Or(e.uploadHow, uploadDirect), nil
}

func (e *fakeEngine) uploaded() []fakeUpload {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.uploads)
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

// Snapshot returns the scripted accessibility tree of the page.
func (e *fakeEngine) Snapshot(context.Context) (pageSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	snapshot := e.snapshot
	snapshot.URL = e.url
	return snapshot, nil
}

// WaitForDownloads reports the scripted downloads once, as if they finished
// after the operation that started them.
func (e *fakeEngine) WaitForDownloads(_ context.Context, grace time.Duration, timeout time.Duration) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.downloadWaits = append(e.downloadWaits, timeout)
	e.keepWrites(e.downloadWrites)
	e.downloadWrites = nil
	downloads := e.downloads
	e.downloads = nil
	if grace > 0 {
		downloads = append(downloads, e.lateDownloads...)
		e.lateDownloads = nil
	}
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
// An answer that is not JSON comes back as text, as from a model that
// ignored the tool.
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
	usage := llmpkg.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}
	if !json.Valid([]byte(arguments)) {
		return &llmpkg.ChatResponse{Content: arguments, Usage: usage}, nil
	}
	return &llmpkg.ChatResponse{
		ToolCalls: []llmpkg.ToolCall{{
			ID: "call-1", Type: "function",
			Function: llmpkg.ToolCallFunction{Name: agentstep.RespondToolName, Arguments: arguments},
		}},
		Usage: usage,
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
		if _, ok := properties["elements"]; ok {
			return `{"elements":[{"elementId":"/html/body/button","description":"Upload button"}]}`, nil
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
