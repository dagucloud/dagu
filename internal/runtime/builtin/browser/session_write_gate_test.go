// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// doAllowing runs input in the session, letting it send the writes of
// signatures.
func (ts *testSessions) doAllowing(id, input string, signatures ...string) (DoResult, error) {
	ts.t.Helper()
	op, variables, err := ParseSessionInput([]byte(input))
	require.NoError(ts.t, err)
	return ts.sessions.Do(ts.context(), DoRequest{ID: id, Operation: op, Variables: variables, AllowWrites: signatures})
}

// A gated session reports each write with the command it happened in, and
// one the page sent while it waited with the next command.
func TestSessionReportsWrites(t *testing.T) {
	ts := newTestSessions(t, pageModel(nil))
	ts.engine.handle.WriteGate = runningGate()
	opened := ts.open(SessionOptions{URL: "https://books.example.com/", Headless: true, LLM: testModel, WriteGate: writegate.ModeRehearse})
	assert.Equal(t, writegate.ModeRehearse, opened.WriteGate)
	require.NotNil(t, ts.launcher.launches[0].WriteGate)
	assert.Equal(t, writegate.ModeRehearse, ts.launcher.launches[0].WriteGate.Mode)
	assert.Equal(t, ts.engine.handle.WriteGate.URL, ts.record(opened.ID).WriteGateURL)

	t.Setenv("BOOKS_TOKEN", "tok-1234")
	save := blockedSave
	save.Body = `{"amount":1000,"note":"tok-1234"}`
	ts.engine.actWrites = []writegate.Event{save}
	result, err := ts.do(opened.ID, `{"act": "Click the Save button", "variables": {"token": {"env": "BOOKS_TOKEN"}}}`)
	require.NoError(t, err)
	require.Len(t, result.Writes, 1)
	assert.Equal(t, writegate.OutcomeBlocked, result.Writes[0].Outcome)
	assert.Equal(t, `{"amount":1000,"note":"*******"}`, result.Writes[0].Body, "a session's secrets are hidden")

	// The page writes on its own between commands.
	ts.engine.mu.Lock()
	idle := blockedSave
	idle.URL, idle.Seq = "https://books.example.com/api/autosave", int64(len(ts.engine.writes)+1)
	ts.engine.writes = append(ts.engine.writes, idle)
	ts.engine.mu.Unlock()
	result, err = ts.do(opened.ID, `{"wait": {"duration": "1ms"}}`)
	require.NoError(t, err)
	require.Len(t, result.Writes, 1)
	assert.Equal(t, idle.URL, result.Writes[0].URL)

	result, err = ts.do(opened.ID, `{"wait": {"duration": "1ms"}}`)
	require.NoError(t, err)
	assert.Empty(t, result.Writes, "a write is reported once")
}

// A person holding the browser is told to the gate, which then sends what
// they do; a session without a gate has nothing to tell.
func TestSessionGateTellsWhoHoldsTheBrowser(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var told []string
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		told = append(told, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(gate.Close)
	ts := newTestSessions(t, pageModel(nil))
	ts.engine.handle.WriteGate = &writegate.Handle{URL: gate.URL + "/token", PID: runningGate().PID}
	opened := ts.open(SessionOptions{URL: "https://books.example.com/", Headless: true, WriteGate: writegate.ModeRehearse})

	result, err := ts.sessions.Gate(ts.context(), GateRequest{ID: opened.ID, Person: true})
	require.NoError(t, err)
	assert.True(t, result.Person)
	assert.Equal(t, writegate.ModeRehearse, result.WriteGate)
	assert.True(t, ts.state(opened.ID).Person)
	_, err = ts.sessions.Gate(ts.context(), GateRequest{ID: opened.ID})
	require.NoError(t, err)
	mu.Lock()
	assert.Equal(t, []string{"POST /token/person?on=true", "POST /token/person?on=false"}, told)
	mu.Unlock()

	ungated := newTestSessions(t, pageModel(nil))
	plain := ungated.open(SessionOptions{URL: "https://books.example.com/", Headless: true})
	_, err = ungated.sessions.Gate(ungated.context(), GateRequest{ID: plain.ID, Person: true})
	assert.Equal(t, CodeInvalidInput, sessionCode(err))
}

// A session whose gate is gone ends, closing its browser.
func TestSessionEndsWhenItsGateIsLost(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	ts.engine.handle.WriteGate = &writegate.Handle{URL: "http://127.0.0.1:1/token"}
	opened := ts.open(SessionOptions{URL: "https://books.example.com/", Headless: true, LLM: testModel, WriteGate: writegate.ModeRehearse})
	ts.engine.mu.Lock()
	ts.engine.writesErr = assert.AnError
	ts.engine.mu.Unlock()

	_, err := ts.do(opened.ID, `{"wait": {"duration": "1ms"}}`)
	assert.Equal(t, CodeWriteGateLost, sessionCode(err))
	assert.Equal(t, browserhost.StateEnded, ts.record(opened.ID).State)
}

// A session whose gate records fails an operation whose write nothing
// allowed with write_blocked, and the same operation allowed that write
// sends it.
func TestSessionRecordSendsOnlyWhatItIsAllowed(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	ts.engine.handle.WriteGate = runningGate()
	opened := ts.open(SessionOptions{URL: "https://books.example.com/", Headless: true, LLM: testModel, WriteGate: writegate.ModeRecord})

	ts.engine.actWrites = []writegate.Event{blockedSave}
	result, err := ts.do(opened.ID, `{"act": "Click the Save button"}`)
	assert.Equal(t, CodeWriteBlocked, sessionCode(err))
	assert.Equal(t, OperationFailed, result.Status)
	assert.Contains(t, result.Error.Message, "--allow-write \""+blockedSave.Signature+"\"")
	require.Len(t, result.Writes, 1)

	sent := blockedSave
	sent.Class, sent.Outcome, sent.Status = writegate.ClassApproved, writegate.OutcomeSent, 201
	ts.engine.actWrites = []writegate.Event{sent}
	result, err = ts.doAllowing(opened.ID, `{"act": "Click the Save button"}`, blockedSave.Signature)
	require.NoError(t, err)
	assert.Equal(t, OperationDone, result.Status)
	assert.Equal(t, [][]string{{blockedSave.Signature}}, ts.engine.allowed)
	assert.False(t, ts.engine.allowing)

	// The step exported from it keeps the write as a commit point.
	exported, err := ts.sessions.Export(ts.context(), ExportRequest{ID: opened.ID, DAG: "books", Step: "enter"})
	require.NoError(t, err)
	assert.Equal(t, 1, exported.CommitPoints)
	points, err := writegate.NewCommitStore(filepath.Join(ts.dataDir, browserhost.DataDirName)).Load("books", "enter")
	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.Equal(t, blockedSave.Signature, points[0].Signature)
	assert.Equal(t, 0, points[0].Op)
	assert.False(t, points[0].Approved)
}

// A rehearsing session sends nothing, however it is asked.
func TestSessionRehearsalRefusesAllowedWrites(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	ts.engine.handle.WriteGate = runningGate()
	opened := ts.open(SessionOptions{URL: "https://books.example.com/", Headless: true, LLM: testModel, WriteGate: writegate.ModeRehearse})
	_, err := ts.doAllowing(opened.ID, `{"act": "Click the Save button"}`, blockedSave.Signature)
	assert.Equal(t, CodeInvalidInput, sessionCode(err))
	assert.Empty(t, ts.engine.allowed)
}

// An act's allowed write sent while its downloads settle is sent and
// reported with the act.
func TestSessionSendsAnAllowedWriteWhileDownloadsSettle(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	ts.engine.handle.WriteGate = runningGate()
	opened := ts.open(SessionOptions{URL: "https://books.example.com/", Headless: true, LLM: testModel, WriteGate: writegate.ModeRecord})
	ts.engine.downloadWrites = []writegate.Event{blockedSave}
	result, err := ts.doAllowing(opened.ID, `{"act": "Click the Save button"}`, blockedSave.Signature)
	require.NoError(t, err)

	assert.Equal(t, OperationDone, result.Status)
	require.Len(t, result.Writes, 1)
	assert.Equal(t, writegate.OutcomeSent, result.Writes[0].Outcome)
	assert.False(t, ts.engine.allowing)
}

// A session whose gate may still let a command's allowed writes through
// ends, closing its browser.
func TestSessionEndsWhenAnAllowanceMayNotEnd(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	ts.engine.handle.WriteGate = runningGate()
	opened := ts.open(SessionOptions{URL: "https://books.example.com/", Headless: true, LLM: testModel, WriteGate: writegate.ModeRecord})
	ts.engine.endErr = assert.AnError
	_, err := ts.doAllowing(opened.ID, `{"act": "Click the Save button"}`, blockedSave.Signature)

	assert.Equal(t, CodeWriteGateLost, sessionCode(err))
	assert.Equal(t, browserhost.StateEnded, ts.record(opened.ID).State)
}
