// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/browserhost/certproxy"
	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSessions is one host's browser sessions over a fake browser and a
// scripted model, on a clock the test moves.
type testSessions struct {
	t        *testing.T
	sessions *Sessions
	engine   *fakeEngine
	launcher *fakeLauncher
	provider *scriptedProvider
	dataDir  string
	clock    time.Time
}

func newTestSessions(t *testing.T, answer func(*llmpkg.ChatRequest) (string, error)) *testSessions {
	t.Helper()
	engine := newFakeEngine()
	engine.handle.BrowserPID = 4242
	engine.handle.BrowserStartedAt = 1_700_000_000_000
	ts := &testSessions{
		t:        t,
		engine:   engine,
		launcher: &fakeLauncher{engine: engine},
		provider: &scriptedProvider{answer: answer},
		dataDir:  t.TempDir(),
		clock:    time.Now(),
	}
	ts.sessions = &Sessions{
		launcher: ts.launcher,
		newProvider: func(context.Context, *ir.LLMConfig) (llmpkg.Provider, error) {
			return ts.provider, nil
		},
		now:  func() time.Time { return ts.clock },
		wait: sleepContext,
	}
	return ts
}

func (ts *testSessions) context() context.Context {
	return cmnconfig.WithConfig(ts.t.Context(), &cmnconfig.Config{Paths: cmnconfig.PathsConfig{DataDir: ts.dataDir}})
}

func (ts *testSessions) store() *browserhost.Store {
	return browserhost.NewInteractiveStore(filepath.Join(ts.dataDir, browserhost.DataDirName))
}

func (ts *testSessions) record(id string) browserhost.Record {
	ts.t.Helper()
	record, err := ts.store().Load(id)
	require.NoError(ts.t, err)
	return record
}

func (ts *testSessions) state(id string) sessionState {
	ts.t.Helper()
	state, err := decodeSessionState(ts.record(id).Interactive)
	require.NoError(ts.t, err)
	return state
}

var testModel = &ir.LLMConfig{Provider: "openai", Model: "test-model"}

// tls_verified reports whether the certificate proxy vouches for the final
// page's host.
func TestSessionTLSVerification(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		verified []string
		want     bool
	}{
		"PageHost":   {verified: []string{"portal.example.com"}, want: true},
		"OtherHost":  {verified: []string{"redirect.example.com"}},
		"NoEvidence": {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ts := newTestSessions(t, pageModel(nil))
			ts.engine.handle.CertProxy = &certproxy.Handle{URL: "http://127.0.0.1:1/token"}
			ts.engine.verifiedHosts = tt.verified
			opened := ts.open(SessionOptions{URL: "https://portal.example.com/", Headless: true})
			assert.Equal(t, tt.want, opened.TLSVerified)
		})
	}
}

func (ts *testSessions) open(opts SessionOptions) OpenResult {
	ts.t.Helper()
	opened, err := ts.sessions.Open(ts.context(), opts)
	require.NoError(ts.t, err)
	return opened
}

// do runs one operation, given as a session command reads it.
func (ts *testSessions) do(id, input string) (DoResult, error) {
	ts.t.Helper()
	op, variables, err := ParseSessionInput([]byte(input))
	require.NoError(ts.t, err)
	return ts.sessions.Do(ts.context(), DoRequest{ID: id, Operation: op, Variables: variables, OutlineChars: DefaultOutlineChars})
}

func sessionCode(err error) string {
	if sessionErr, ok := errors.AsType[*SessionError](err); ok {
		return sessionErr.Code
	}
	return ""
}

// A session opens its browser, runs one operation per command against the
// same browser, and leaves the browser waiting between commands.
func TestSessionWorksThePageBetweenCommands(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	ts.engine.snapshot = fixtureSnapshot(t, "login")
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", Headless: true, LLM: testModel, OutlineChars: DefaultOutlineChars})

	assert.Regexp(t, `^[a-z2-7]{10}$`, opened.ID)
	assert.Equal(t, "https://portal.example.com/login", opened.URL)
	assert.Contains(t, opened.Outline, `textbox "パスワード"`)
	assert.Equal(t, "openai/test-model", opened.Model)
	assert.Equal(t, ts.engine.handle.CDPURL, opened.CDPURL)
	assert.True(t, ts.engine.detached, "the browser waits for the next command")
	record := ts.record(opened.ID)
	assert.Equal(t, browserhost.StateInteractive, record.State)
	assert.WithinDuration(t, ts.clock.Add(DefaultSessionIdleTimeout), record.Deadline, time.Millisecond)
	assert.Zero(t, record.OwnerPID)
	require.Len(t, ts.launcher.launches, 1)
	launch := ts.launcher.launches[0]
	assert.True(t, launch.Headless)
	assert.Equal(t, filepath.Join(record.WorkDir, "downloads"), launch.DownloadsDir)

	ts.clock = ts.clock.Add(time.Minute)
	ts.engine.actNavigatesTo = "https://portal.example.com/orders"
	result, err := ts.do(opened.ID, `{"act": "Click ログイン"}`)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Index)
	assert.Equal(t, opAct, result.Kind)
	assert.Equal(t, OperationDone, result.Status)
	assert.Equal(t, []SessionAction{{Selector: "xpath=/html/body/button", Method: "click"}}, result.Actions)
	assert.True(t, result.Recorded)
	assert.Equal(t, "https://portal.example.com/orders", result.URL)
	assert.WithinDuration(t, ts.clock.Add(DefaultSessionIdleTimeout), result.IdleDeadline, time.Millisecond)
	assert.Equal(t, []browserHandle{ts.engine.handle}, ts.launcher.reattaches, "the command reattaches to the session's browser")
	assert.True(t, ts.engine.detached)
	assert.False(t, ts.engine.closed)

	state := ts.state(opened.ID)
	require.Len(t, state.Ops, 1)
	assert.WithinDuration(t, ts.clock, state.Ops[0].At, time.Millisecond)
	assert.JSONEq(t, `{"act":"Click ログイン"}`, string(state.Ops[0].Op), "the operation is kept as it was written")
	state.Ops[0].At, state.Ops[0].Op = time.Time{}, nil
	assert.Equal(t, sessionOp{
		Kind: opAct, Status: OperationDone,
		URLBefore: "https://portal.example.com/login", URLAfter: "https://portal.example.com/orders",
		Actions: []recordedAction{{Selector: "xpath=/html/body/button", Method: "click"}}, Recordable: true,
	}, state.Ops[0])
	assert.Equal(t, browserhost.StateInteractive, ts.record(opened.ID).State)
}

// A variable read from the environment is kept by name for later commands
// and masked everywhere; a literal value serves its own command only.
func TestSessionVariables(t *testing.T) {
	const env = "DAGU_SESSION_TEST_PASSWORD"
	t.Setenv(env, "s3cret-value")

	ts := newTestSessions(t, pageModel(map[string]string{"The greeting": `{"greeting":"Welcome s3cret-value"}`}))
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", LLM: testModel})

	_, err := ts.do(opened.ID, `{"act": "Type %user% into the ID field", "variables": {"user": "alice"}}`)
	require.NoError(t, err)
	_, err = ts.do(opened.ID, `{"act": "Type %password% into the Password field", "variables": {"password": {"env": "`+env+`"}}}`)
	require.NoError(t, err)
	assert.Equal(t, "s3cret-value", ts.engine.acts[1].variables["password"], "the browser types the value")
	assert.Empty(t, ts.engine.acts[0].secret)
	assert.Equal(t, []string{"password"}, ts.engine.acts[1].secret)

	// The password is still known to the next command; the user is not.
	_, err = ts.do(opened.ID, `{"act": "Type %password% again"}`)
	require.NoError(t, err)
	_, err = ts.do(opened.ID, `{"act": "Type %user% again"}`)
	assert.Equal(t, CodeInvalidInput, sessionCode(err))
	assert.ErrorContains(t, err, "the instruction uses %user%, which no variable sets")

	result, err := ts.do(opened.ID, `{"extract": {"instruction": "The greeting", "schema": {"type": "object", "properties": {"greeting": {"type": "string"}}}}}`)
	require.NoError(t, err)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "s3cret-value")
	assert.Contains(t, string(encoded), "Welcome *******")

	record, err := os.ReadFile(filepath.Join(ts.dataDir, browserhost.DataDirName, "interactive", opened.ID+".json"))
	require.NoError(t, err)
	assert.NotContains(t, string(record), "s3cret-value", "no value is kept")
	assert.NotContains(t, string(record), "alice")
	assert.Equal(t, map[string]string{"password": env}, ts.state(opened.ID).EnvVariables)

	_, err = ts.do(opened.ID, `{"act": "Type s3cret-value into the box"}`)
	assert.Equal(t, CodeInvalidInput, sessionCode(err), "a secret value never goes to the model")
}

// An extract whose link field has no format: uri warns while the step is
// being built, since a step with that schema gets the link's element id.
func TestSessionLinkFieldWarning(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(map[string]string{"The first listing": `{"link":"0-11"}`}))
	opened := ts.open(SessionOptions{URL: "https://shop.example.com/listings", LLM: testModel})

	result, err := ts.do(opened.ID, `{"extract": {"instruction": "The first listing", "schema": {"type": "object", "properties": {"link": {"type": "string"}}}}}`)
	require.NoError(t, err)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], `extract field "link" has no format: uri`)
}

// An act that typed a variable's value itself reports its actions with
// secrets masked, and the session keeps neither the actions nor a recording
// of them.
func TestSessionActTypingAValue(t *testing.T) {
	const env = "DAGU_SESSION_TEST_PASSWORD"
	t.Setenv(env, "s3cret-value")

	ts := newTestSessions(t, pageModel(nil))
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", LLM: testModel})
	recordFile := filepath.Join(ts.dataDir, browserhost.DataDirName, "interactive", opened.ID+".json")

	ts.engine.actFills = "s3cret-value"
	result, err := ts.do(opened.ID, `{"act": "Type %password% into the Password field", "variables": {"password": {"env": "`+env+`"}}}`)
	require.NoError(t, err)
	assert.False(t, result.Recorded)
	assert.Equal(t, []string{"*******"}, result.Actions[0].Arguments)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "s3cret-value")

	ts.engine.actFills = "alice-literal"
	result, err = ts.do(opened.ID, `{"act": "Type %user% into the ID field", "variables": {"user": "alice-literal"}}`)
	require.NoError(t, err)
	assert.False(t, result.Recorded)

	record, err := os.ReadFile(recordFile)
	require.NoError(t, err)
	assert.NotContains(t, string(record), "s3cret-value", "no value is kept")
	assert.NotContains(t, string(record), "alice-literal", "no value is kept")
}

// An operation that fails is reported and kept in the history, and the
// session stays open for the next command; one whose when does not hold is
// skipped.
func TestSessionFailedAndSkippedOperations(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	ts.engine.pageText = "Orders"
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/orders", LLM: testModel})

	result, err := ts.do(opened.ID, `{"expect": {"text": "Invoices"}, "timeout": "10ms"}`)
	assert.Equal(t, CodeOperationFailed, sessionCode(err))
	assert.Equal(t, OperationFailed, result.Status)
	assert.Contains(t, result.Error.Message, `expect failed: expectation not met: the page text does not contain "Invoices"`)
	assert.Equal(t, browserhost.StateInteractive, ts.record(opened.ID).State)

	result, err = ts.do(opened.ID, `{"act": "Accept the cookies", "when": {"selector": "#cookies"}}`)
	require.NoError(t, err)
	assert.Equal(t, OperationSkipped, result.Status)
	assert.Equal(t, `"#cookies" is not visible`, result.Detail)
	assert.Empty(t, ts.engine.acts)

	result, err = ts.do(opened.ID, `{"expect": {"text": "Orders"}}`)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Index)
	statuses := []string{}
	for _, op := range ts.state(opened.ID).Ops {
		statuses = append(statuses, op.Status)
	}
	assert.Equal(t, []string{OperationFailed, OperationSkipped, OperationDone}, statuses)
}

// A page the site refused is reported by every command while the browser
// shows it, and named in the failure of an operation on it. A bot check an
// earlier command saw stays named while the page is the same, though a
// reattached browser cannot tell it, as after a person reloads the page.
func TestSessionReportsRefusedPage(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	ts.engine.pageText = "Just a moment..."
	ts.engine.outcome = pageOutcome{Host: "shop.example.com", Status: 403, BotCheck: browserhost.BotCheckCloudflare}
	botCheck := &SessionRefusal{
		Host: "shop.example.com", Status: 403, BotCheck: browserhost.BotCheckCloudflare,
		Message: "shop.example.com answered with a Cloudflare bot check (HTTP 403) in place of the page",
	}
	opened := ts.open(SessionOptions{URL: "https://shop.example.com/cart", LLM: testModel})
	assert.Equal(t, botCheck, opened.Refused)

	result, err := ts.do(opened.ID, `{"expect": {"text": "Cart"}, "timeout": "10ms"}`)
	assert.Equal(t, CodeOperationFailed, sessionCode(err))
	assert.Equal(t, botCheck, result.Refused)
	assert.True(t, strings.HasSuffix(result.Error.Message, "; "+botCheck.Message), result.Error.Message)

	ts.engine.outcome.BotCheck = ""
	described, err := ts.sessions.Describe(ts.context(), DescribeRequest{ID: opened.ID})
	require.NoError(t, err)
	assert.Equal(t, botCheck, described.Refused)

	ts.engine.outcome = pageOutcome{Host: "shop.example.com", Status: 200}
	result, err = ts.do(opened.ID, `{"goto": "https://shop.example.com/cart"}`)
	require.NoError(t, err)
	assert.Nil(t, result.Refused)
}

// An operation the browser stopped answering leaves the page unread: the
// refusal seen on it stays, for the commands that follow.
func TestSessionKeepsRefusalWhenBrowserStopsAnswering(t *testing.T) {
	t.Parallel()

	files := writeFiles(t, t.TempDir(), "invoice.pdf")
	ts := newTestSessions(t, pageModel(nil))
	ts.engine.outcome = pageOutcome{Host: "shop.example.com", Status: 403, BotCheck: browserhost.BotCheckCloudflare}
	opened := ts.open(SessionOptions{URL: "https://shop.example.com/receipts"})
	require.NotNil(t, opened.Refused)

	// The page now reads as fine, which only a read would report.
	ts.engine.outcome = pageOutcome{Host: "shop.example.com", Status: 200}
	ts.engine.uploadErr = fmt.Errorf("%w within 30s", errBrowserUnresponsive)
	input, err := json.Marshal(map[string]any{"upload": map[string]any{"files": files}})
	require.NoError(t, err)
	result, err := ts.do(opened.ID, string(input))
	assert.Equal(t, CodeOperationFailed, sessionCode(err))
	assert.Equal(t, opened.Refused, result.Refused)
	assert.Equal(t, browserhost.BotCheckCloudflare, ts.state(opened.ID).Page.Refused.BotCheck)
}

// A session opened without a model refuses the operations that ask one and
// runs the rest.
func TestSessionWithoutModel(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	ts.engine.pageText = "Sign in"
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login"})
	assert.Empty(t, opened.Model)

	_, err := ts.do(opened.ID, `{"act": "Click Sign in"}`)
	assert.Equal(t, CodeModelRequired, sessionCode(err))
	_, err = ts.do(opened.ID, `{"expect": "The sign-in form is shown"}`)
	assert.Equal(t, CodeModelRequired, sessionCode(err))
	_, err = ts.do(opened.ID, `{"expect": {"text": "Sign in"}}`)
	require.NoError(t, err)
	_, err = ts.do(opened.ID, `{"goto": "https://portal.example.com/help"}`)
	require.NoError(t, err)
	assert.Zero(t, ts.provider.callCount())
}

// A session idle past its timeout ends: its browser closes and its history
// stays for a day.
func TestSessionEndsWhenIdle(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", IdleTimeout: time.Minute})
	_, err := ts.do(opened.ID, `{"goto": "https://portal.example.com/help"}`)
	require.NoError(t, err)

	ts.clock = ts.clock.Add(2 * time.Minute)
	_, err = ts.do(opened.ID, `{"goto": "https://portal.example.com/"}`)
	assert.Equal(t, CodeSessionEnded, sessionCode(err))
	assert.ErrorContains(t, err, "it was idle past its timeout")
	record := ts.record(opened.ID)
	assert.Equal(t, browserhost.StateEnded, record.State)
	assert.WithinDuration(t, ts.clock.Add(browserhost.SessionRetention), record.Deadline, time.Millisecond)
	assert.Len(t, ts.state(opened.ID).Ops, 1, "the history is kept")
	assert.NoDirExists(t, ts.store().WorkDir(opened.ID))

	summaries, err := ts.sessions.List(ts.context())
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.Equal(t, SessionEnded, summaries[0].State)
}

// A command fails at once while another command holds the session, and a
// session whose command died is used again.
func TestSessionBusyAndRecovered(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login"})

	lock := ts.store().SessionLock(opened.ID)
	require.NoError(t, lock.TryLock())
	_, err := ts.do(opened.ID, `{"goto": "https://portal.example.com/help"}`)
	assert.Equal(t, CodeSessionBusy, sessionCode(err))
	require.NoError(t, lock.Unlock())

	// A command that died left the session running under a process that is
	// gone, and released its lock.
	record := ts.record(opened.ID)
	record.State, record.OwnerPID, record.OwnerStartedAt = browserhost.StateRunning, 1<<30, 0
	require.NoError(t, ts.store().Save(record))
	_, err = ts.do(opened.ID, `{"goto": "https://portal.example.com/help"}`)
	require.NoError(t, err)
	assert.Equal(t, browserhost.StateInteractive, ts.record(opened.ID).State)
}

// A session that does not exist, or an ID no session could have, is
// reported as not found; input a session cannot run is refused before the
// browser is touched.
func TestSessionRefusesBadInput(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	_, err := ts.do("ab2cd3ef4g", `{"goto": "https://example.com"}`)
	assert.Equal(t, CodeSessionNotFound, sessionCode(err))
	_, err = ts.do("../../etc", `{"goto": "https://example.com"}`)
	assert.Equal(t, CodeSessionNotFound, sessionCode(err))

	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", LLM: testModel})
	for _, input := range []string{
		`{"ask": {"prompt": "Enter the code", "as": "code"}}`,
		`{"act": "Click", "goto": "https://example.com"}`,
		`{"wait": {"duration": "soon"}}`,
		`{"screenshot": "../escape"}`,
	} {
		_, err := ts.do(opened.ID, input)
		assert.Equal(t, CodeInvalidInput, sessionCode(err), input)
	}
	assert.Len(t, ts.launcher.reattaches, 0)

	for _, opts := range []SessionOptions{
		{IdleTimeout: 25 * time.Hour},
		{Viewport: &SessionViewport{Width: 0, Height: 600}},
		{Profile: "../shared"},
		{AllowedDomains: []string{"localhost"}},
		{URL: "https://evil.example.org", AllowedDomains: []string{"portal.example.com"}},
	} {
		_, err := ts.sessions.Open(ts.context(), opts)
		assert.Equal(t, CodeInvalidInput, sessionCode(err), "%+v", opts)
	}
}

// A session keeps a bounded history.
func TestSessionHistoryIsBounded(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login"})
	record := ts.record(opened.ID)
	state := ts.state(opened.ID)
	state.Ops = make([]sessionOp, maxSessionOperations)
	require.NoError(t, saveSessionRecord(ts.store(), &record, state, browserhost.StateInteractive, record.Deadline))

	_, err := ts.do(opened.ID, `{"goto": "https://portal.example.com/help"}`)
	assert.Equal(t, CodeInvalidInput, sessionCode(err))
	assert.ErrorContains(t, err, "export it or close it")
}

// Closing a session closes its browser and removes it; list shows the
// sessions still open, oldest first.
func TestSessionCloseAndList(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	first := ts.open(SessionOptions{URL: "https://portal.example.com/login", Profile: "portal"})
	ts.clock = ts.clock.Add(time.Second)
	second := ts.open(SessionOptions{URL: "https://portal.example.com/help"})

	summaries, err := ts.sessions.List(ts.context())
	require.NoError(t, err)
	require.Len(t, summaries, 2)
	assert.Equal(t, first.ID, summaries[0].ID)
	assert.Equal(t, SessionIdle, summaries[0].State)
	assert.Equal(t, "portal", summaries[0].Profile)
	assert.Equal(t, ts.engine.handle.CDPURL, summaries[0].CDPURL)

	require.NoError(t, ts.sessions.Close(ts.context(), CloseRequest{ID: first.ID}))
	_, err = ts.store().Load(first.ID)
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.NoDirExists(t, ts.store().WorkDir(first.ID))

	summaries, err = ts.sessions.List(ts.context())
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.Equal(t, second.ID, summaries[0].ID)

	err = ts.sessions.Close(ts.context(), CloseRequest{ID: first.ID})
	assert.Equal(t, CodeSessionNotFound, sessionCode(err))
}

// Closing with keep ends the session: its browser closes and its profile is
// free, while its history can still be exported until it is removed.
func TestSessionCloseKeepsHistory(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", Profile: "portal", LLM: testModel})
	_, err := ts.do(opened.ID, `{"act": "Click Sign in"}`)
	require.NoError(t, err)

	require.NoError(t, ts.sessions.Close(ts.context(), CloseRequest{ID: opened.ID, Keep: true}))
	assert.Equal(t, browserhost.StateEnded, ts.record(opened.ID).State)
	_, err = ts.do(opened.ID, `{"act": "Click Orders"}`)
	assert.Equal(t, CodeSessionEnded, sessionCode(err))
	exported, err := ts.sessions.Export(ts.context(), ExportRequest{ID: opened.ID, DAG: "orders", Step: "shop", DryRun: true})
	require.NoError(t, err)
	assert.Len(t, exported.Ops, 1)
	ts.open(SessionOptions{URL: "https://portal.example.com/login", Profile: "portal"})

	require.NoError(t, ts.sessions.Close(ts.context(), CloseRequest{ID: opened.ID, Keep: true}), "an ended session stays ended")
	require.NoError(t, ts.sessions.Close(ts.context(), CloseRequest{ID: opened.ID}))
	_, err = ts.store().Load(opened.ID)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// A session holds its profile between commands, so a second session cannot
// open on it.
func TestSessionHoldsItsProfile(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	first := ts.open(SessionOptions{URL: "https://portal.example.com/login", Profile: "portal"})
	_, err := ts.sessions.Open(ts.context(), SessionOptions{URL: "https://portal.example.com/login", Profile: "portal"})
	assert.Equal(t, CodeProfileInUse, sessionCode(err))
	assert.ErrorContains(t, err, "held by browser session "+first.ID)
}

// A command reads one operation and its variables as JSON or YAML.
func TestParseSessionInput(t *testing.T) {
	t.Parallel()

	op, variables, err := ParseSessionInput([]byte("act: Type %password%\nvariables:\n  password: {env: PORTAL_PASSWORD}\n  user: alice\n"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"act":"Type %password%"}`, string(op))
	assert.Equal(t, map[string]SessionVariable{"password": {Env: "PORTAL_PASSWORD"}, "user": {Value: "alice"}}, variables)

	op, variables, err = ParseSessionInput([]byte(`{"expect": {"text": "Orders", "within": "5s"}}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"expect":{"text":"Orders","within":"5s"}}`, string(op))
	assert.Empty(t, variables)

	for _, input := range []string{"", "[1, 2]", `{"act": "x", "variables": {"p": {"file": "/etc/passwd"}}}`} {
		_, _, err := ParseSessionInput([]byte(input))
		assert.Equal(t, CodeInvalidInput, sessionCode(err), input)
	}
}

// A session's model is asked with the API key held by the environment
// variable its settings name, which no DAG declares for a session.
func TestSessionModelReadsItsKeyFromTheEnvironment(t *testing.T) {
	t.Setenv("DAGU_SESSION_TEST_KEY", "sk-session-1234")
	var mu sync.Mutex
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authorization = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		arguments, _ := json.Marshal(`{"elementId":"/html/body/button"}`)
		_, _ = w.Write([]byte(`{"id":"r1","object":"chat.completion","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant",` +
			`"tool_calls":[{"id":"c1","type":"function","function":{"name":"` + agentstep.RespondToolName + `","arguments":` + string(arguments) + `}}]}}],` +
			`"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`))
	}))
	t.Cleanup(server.Close)

	ts := newTestSessions(t, nil)
	ts.sessions.newProvider = nil
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", LLM: &ir.LLMConfig{
		Provider: "openai", Model: "test-model", BaseURL: server.URL, APIKeyName: "DAGU_SESSION_TEST_KEY",
	}})
	result, err := ts.do(opened.ID, `{"act": "Click Sign in"}`)
	require.NoError(t, err)
	assert.Equal(t, OperationDone, result.Status)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "Bearer sk-session-1234", authorization)
}

// An upload into an element the outline names runs without the model and
// is kept as the upload a step writes, into the element in words, with its
// action as the recording. An upload into words asks the model.
func TestSessionUploads(t *testing.T) {
	t.Parallel()

	files := writeFiles(t, t.TempDir(), "invoice.pdf")
	ts := newTestSessions(t, pageModel(nil))
	ts.engine.snapshot = uploadPage("Upload receipt")
	opened := ts.open(SessionOptions{URL: "https://books.example.com/receipts", LLM: testModel})

	input, err := json.Marshal(map[string]any{"upload": map[string]any{"into": "[0-5]", "files": files[0]}, "timeout": "30s"})
	require.NoError(t, err)
	result, err := ts.do(opened.ID, string(input))
	require.NoError(t, err)
	assert.Equal(t, opUpload, result.Kind)
	assert.Equal(t, `the "Upload receipt" button`, result.Into)
	assert.True(t, result.Recorded)
	assert.Equal(t, []SessionAction{{Selector: "xpath=/html/body/button", Description: `button "Upload receipt"`, Method: uploadMethod}}, result.Actions)
	assert.Contains(t, result.Detail, "invoice.pdf into xpath=/html/body/button")
	assert.Zero(t, ts.provider.callCount(), "an element upload asks no model")
	op, err := json.Marshal(map[string]any{"upload": map[string]any{"files": files[0], "into": `the "Upload receipt" button`}, "timeout": "30s"})
	require.NoError(t, err)
	assert.JSONEq(t, string(op), string(ts.state(opened.ID).Ops[0].Op))

	input, err = json.Marshal(map[string]any{"upload": map[string]any{"into": "the receipt box", "files": files}})
	require.NoError(t, err)
	result, err = ts.do(opened.ID, string(input))
	require.NoError(t, err)
	assert.Empty(t, result.Into)
	assert.True(t, result.Recorded)
	assert.Equal(t, 1, ts.provider.callCount())
	assert.Equal(t, []fakeUpload{
		{selector: "xpath=/html/body/button", paths: files},
		{selector: "xpath=/html/body/button", paths: files},
	}, ts.engine.uploaded())

	_, err = ts.do(opened.ID, `{"upload": {"into": "0-5", "files": "invoice.pdf", "cache": false}}`)
	assert.Equal(t, CodeInvalidInput, sessionCode(err))
	assert.ErrorContains(t, err, "an upload into an element by its ID replays that element, so it takes no cache")

	_, err = ts.do(opened.ID, `{"upload": {"into": "0-6", "files": "invoice.pdf"}}`)
	require.Error(t, err)
	assert.Equal(t, CodeInvalidInput, sessionCode(err))
	assert.ErrorContains(t, err, "element 0-6 has no name a step could find it by again; describe it in into, or leave into out when it is the page's only file input")
}

// A session without a model uploads into the page's only file input and
// into elements by ID, and refuses an upload into words.
func TestSessionUploadsWithoutModel(t *testing.T) {
	t.Parallel()

	files := writeFiles(t, t.TempDir(), "invoice.pdf")
	ts := newTestSessions(t, pageModel(nil))
	ts.engine.snapshot = uploadPage("Upload receipt")
	opened := ts.open(SessionOptions{URL: "https://books.example.com/receipts"})

	for _, upload := range []map[string]any{{"files": files}, {"files": files, "into": "0-5"}} {
		input, err := json.Marshal(map[string]any{"upload": upload})
		require.NoError(t, err)
		_, err = ts.do(opened.ID, string(input))
		require.NoError(t, err)
	}
	input, err := json.Marshal(map[string]any{"upload": map[string]any{"files": files, "into": "the receipt box"}})
	require.NoError(t, err)
	_, err = ts.do(opened.ID, string(input))
	assert.Equal(t, CodeModelRequired, sessionCode(err))
	assert.Len(t, ts.engine.uploaded(), 2)
}
