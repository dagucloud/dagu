// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runningGate stands for a write gate whose process runs: this one.
func runningGate() *writegate.Handle {
	return &writegate.Handle{URL: "http://127.0.0.1:1/gate", PID: os.Getpid()}
}

var blockedSave = writegate.Event{
	Kind: writegate.KindWrite, Method: "POST", URL: "https://books.example.com/api/deals", ResourceType: "XHR",
	Class: writegate.ClassWrite, Outcome: writegate.OutcomeBlocked, Signature: "POST https://books.example.com/api/deals? {amount}",
	Fields: []string{"amount"}, Body: `{"amount":1000}`,
}

const rehearsedSteps = `{
	"browser": {"write_gate": "rehearse"},
	"do": [
		{"extract": {"instruction": "The order number", "schema": {"type": "object", "properties": {"order": {"type": "string"}}}}},
		{"act": "Click the Save button"},
		{"act": "Click the Next button"},
		{"screenshot": "done"}
	]
}`

// A rehearsal stops at the first write it kept back and partially
// succeeds, saying what it would have sent and what did not run.
func TestRehearsalStopsAtTheFirstWrite(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(map[string]string{"The order number": `{"order":"A-100"}`}))
	run.engine.handle.WriteGate = runningGate()
	run.engine.actWrites = []writegate.Event{blockedSave}
	execution := run.execute(rehearsedSteps, nil)
	require.NoError(t, execution.err)

	status, err := execution.exec.DetermineNodeStatus()
	require.NoError(t, err)
	assert.Equal(t, ir.NodePartiallySucceeded, status)
	require.Len(t, run.launcher.launches, 1)
	require.NotNil(t, run.launcher.launches[0].WriteGate)
	assert.Equal(t, writegate.ModeRehearse, run.launcher.launches[0].WriteGate.Mode)
	assert.Equal(t, []string{"Click the Save button"}, run.engine.actInstructions(), "nothing runs after the write")
	assert.Equal(t, map[string]any{"order": "A-100"}, execution.exec.GetOutputs(), "what ran before is published")
	assert.True(t, run.engine.closed)

	session := execution.exec.GetAgentSession()
	assert.Equal(t, []string{"extract:completed", "act:completed", "write:blocked"}, eventNames(session))
	last := session.Events[len(session.Events)-1]
	assert.Equal(t, "would send POST https://books.example.com/api/deals; not sent; do[2]–do[3] did not run", last.Content)
	assert.Contains(t, execution.stderr.String(), "[2/4] write blocked POST https://books.example.com/api/deals")

	data, err := os.ReadFile(filepath.Join(run.artifacts, "browser", "shop", writeGateFile))
	require.NoError(t, err)
	var report writeGateReport
	require.NoError(t, json.Unmarshal(data, &report))
	assert.Equal(t, writegate.ModeRehearse, report.Mode)
	assert.True(t, report.Rehearsable)
	require.NotNil(t, report.StoppedAt)
	assert.Equal(t, 1, *report.StoppedAt)
	assert.Equal(t, []int{2, 3}, report.NotRun)
	require.NotNil(t, report.WouldSend)
	assert.Equal(t, blockedSave.Signature, report.WouldSend.Signature)
}

// The host's floor is the least strict mode: a step cannot turn the gate
// off below it.
func TestWriteGateFloorBeatsTheStep(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.hostWriteGate = writegate.ModeRehearse
	run.engine.handle.WriteGate = runningGate()
	run.engine.actWrites = []writegate.Event{blockedSave}
	execution := run.execute(`{"browser": {"write_gate": "off"}, "do": [{"act": "Click the Save button"}, {"act": "Click the Next button"}]}`, nil)
	require.NoError(t, execution.err)

	require.NotNil(t, run.launcher.launches[0].WriteGate)
	assert.Equal(t, writegate.ModeRehearse, run.launcher.launches[0].WriteGate.Mode)
	status, _ := execution.exec.DetermineNodeStatus()
	assert.Equal(t, ir.NodePartiallySucceeded, status)
}

// Beacons, reports, and sign-ins the policy lists are reported without
// stopping the rehearsal; a socket the gate cannot see marks it as one the
// site cannot pass.
func TestRehearsalGoesOnPastNoiseAndSignIn(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	beacon := writegate.Event{Kind: writegate.KindWrite, Method: "POST", URL: "https://books.example.com/log", ResourceType: "Ping", Class: writegate.ClassNoise, Outcome: writegate.OutcomeBlocked}
	signIn := writegate.Event{Kind: writegate.KindWrite, Method: "POST", URL: "https://login.example.com/session", Class: writegate.ClassSignIn, Outcome: writegate.OutcomeSent}
	socket := writegate.Event{Kind: writegate.KindSocket, URL: "wss://books.example.com/live", Target: "service_worker"}
	run.engine.actWrites = []writegate.Event{beacon, signIn, socket}
	execution := run.execute(`{"browser": {"write_gate": "rehearse"}, "do": [{"act": "Click the Sign in button"}, {"act": "Click the Next button"}]}`, nil)
	require.NoError(t, execution.err)

	status, _ := execution.exec.DetermineNodeStatus()
	assert.Equal(t, ir.NodeRunning, status, "the step succeeds")
	assert.Equal(t, []string{"Click the Sign in button", "Click the Next button"}, run.engine.actInstructions())
	assert.Equal(t, []string{"act:completed", "write:blocked", "write:sent", "write_gate:unseen", "act:completed"}, eventNames(execution.exec.GetAgentSession()))
	data, err := os.ReadFile(filepath.Join(run.artifacts, "browser", "shop", writeGateFile))
	require.NoError(t, err)
	var report writeGateReport
	require.NoError(t, json.Unmarshal(data, &report))
	assert.False(t, report.Rehearsable)
	assert.Nil(t, report.StoppedAt)
	assert.Len(t, report.Writes, 3)
}

// A gate that is gone leaves the browser's writes undecided, so the step
// fails and its browser is closed.
func TestLostWriteGateFailsTheStep(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = &writegate.Handle{URL: "http://127.0.0.1:1/gate"}
	run.engine.onAct = func() {
		run.engine.mu.Lock()
		run.engine.writesErr = assert.AnError
		run.engine.mu.Unlock()
	}
	execution := run.execute(`{"browser": {"write_gate": "rehearse"}, "do": [{"act": "Click the Save button"}]}`, nil)

	require.ErrorContains(t, execution.err, "do[0] write_gate failed: the write gate stopped answering, so the browser was closed")
	assert.True(t, run.engine.closed)
}

func TestWriteGateSecretsComeFromTheStep(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	policy := filepath.Join(t.TempDir(), "policy.yaml")
	require.NoError(t, os.WriteFile(policy, []byte("sign_in:\n  - host: login.example.com\n    path: /session\n    secret: PORTAL_PASSWORD\n"), 0o600))
	run.hostWritePolicy = policy
	run.secrets = map[string]string{"PORTAL_PASSWORD": "s3cret"}
	run.engine.handle.WriteGate = runningGate()
	execution := run.execute(`{"browser": {"write_gate": "rehearse"}, "do": [{"act": "Click the Sign in button"}]}`, nil)
	require.NoError(t, execution.err)

	gate := run.launcher.launches[0].WriteGate
	require.NotNil(t, gate)
	assert.Equal(t, []writegate.Rule{{Host: "login.example.com", Path: "/session", Secret: "PORTAL_PASSWORD"}}, gate.Policy.SignIn)
	assert.Equal(t, map[string]string{"PORTAL_PASSWORD": "s3cret"}, gate.Secrets)
}

func TestWriteGateIsOffByDefault(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	execution := run.execute(`{"do": [{"act": "Click the Save button"}]}`, nil)
	require.NoError(t, execution.err)
	assert.Nil(t, run.launcher.launches[0].WriteGate)
	assert.NoFileExists(t, filepath.Join(run.artifacts, "browser", "shop", writeGateFile))
}

// readReport returns the write gate report the step left.
func (r *testRun) readReport() writeGateReport {
	r.t.Helper()
	data, err := os.ReadFile(filepath.Join(r.artifacts, "browser", "shop", writeGateFile))
	require.NoError(r.t, err)
	var report writeGateReport
	require.NoError(r.t, json.Unmarshal(data, &report))
	return report
}

// A step that waited for an answer reports what the gate decided before
// the wait too, once each, and still knows the site cannot be rehearsed.
func TestWriteGateReportSpansAnAsk(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	beacon := writegate.Event{Kind: writegate.KindWrite, Method: "POST", URL: "https://books.example.com/log", ResourceType: "Ping", Class: writegate.ClassNoise, Outcome: writegate.OutcomeBlocked}
	socket := writegate.Event{Kind: writegate.KindSocket, URL: "wss://books.example.com/live", Target: "service_worker"}
	run.engine.actWrites = []writegate.Event{beacon, socket}
	steps := `{"browser": {"write_gate": "rehearse"}, "do": [
		{"act": "Click the Open button"},
		{"ask": {"prompt": "Which entry?", "as": "entry"}},
		{"act": "Click the Next button"}
	]}`
	waiting := run.execute(steps, nil)
	require.NoError(t, waiting.err)
	session := waiting.exec.GetAgentSession()
	session.Interactions[0].Status = ir.AgentInteractionAnswered
	session.Interactions[0].Answers = [][]string{{"A-1"}}
	resumed := run.execute(steps, session)
	require.NoError(t, resumed.err)

	report := run.readReport()
	assert.Len(t, report.Writes, 2)
	assert.False(t, report.Rehearsable)
	assert.NotContains(t, eventNames(resumed.exec.GetAgentSession())[2:], "write:blocked", "the resumed step does not report them again")
}

// A write the page sends while the last downloads settle stops the
// rehearsal as one sent during an operation does.
func TestRehearsalStopsAtAWriteAfterTheLastOperation(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	run.engine.downloadWrites = []writegate.Event{blockedSave}
	execution := run.execute(`{"browser": {"write_gate": "rehearse"}, "do": [{"act": "Click the Save button"}]}`, nil)
	require.NoError(t, execution.err)

	status, _ := execution.exec.DetermineNodeStatus()
	assert.Equal(t, ir.NodePartiallySucceeded, status)
	require.NotNil(t, run.readReport().WouldSend)
}

// A step cancelled after the gate decided a write still reports it.
func TestCancelledStepReportsItsWrites(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	run.engine.actWrites = []writegate.Event{blockedSave}
	base, cancel := context.WithCancel(t.Context())
	run.base = base
	run.engine.onAct = cancel
	execution := run.execute(`{"browser": {"write_gate": "rehearse"}, "do": [{"act": "Click the Save button"}, {"wait": {"duration": "10s"}}]}`, nil)
	require.ErrorContains(t, execution.err, context.Canceled.Error())

	report := run.readReport()
	require.Len(t, report.Writes, 1)
	assert.Equal(t, blockedSave.Signature, report.Writes[0].Signature)
}

// commitPoints returns the commit points the test run's step keeps.
func (r *testRun) commitPoints() []writegate.CommitPoint {
	r.t.Helper()
	points, err := writegate.NewCommitStore(filepath.Join(r.dataDir, "browser")).Load("orders", "shop")
	require.NoError(r.t, err)
	return points
}

const recordedSteps = `{
	"url": "https://books.example.com/deals",
	"browser": {"write_gate": "record"},
	"do": [
		{"act": "Click the Save button"},
		{"act": "Click the Next button"}
	]
}`

// In record mode an approved commit act replays what was approved, asking
// no model, while the gate lets it send its approved writes.
func TestRecordSendsTheApprovedCommitWrite(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	saved := recordedAction{Selector: "xpath=/html/body/button[2]", Method: "click"}
	actions, err := json.Marshal([]recordedAction{saved})
	require.NoError(t, err)
	store := writegate.NewCommitStore(filepath.Join(run.dataDir, "browser"))
	point := writegate.CommitPoint{Key: replayKey(0, "Click the Save button", "https://books.example.com/deals"), Op: 0, Operation: "Click the Save button", Signature: blockedSave.Signature, Actions: actions}
	require.NoError(t, store.Note(t.Context(), "orders", "shop", point))
	_, err = store.Approve(t.Context(), "orders", "shop", nil, func(writegate.CommitPoint) bool { return true }, time.Now())
	require.NoError(t, err)

	execution := run.execute(recordedSteps, nil)
	require.NoError(t, execution.err)

	assert.Equal(t, [][]string{{blockedSave.Signature}}, run.engine.allowed, "only the commit act may send, and only its write")
	assert.False(t, run.engine.allowing, "the allowance ends with the act")
	assert.Equal(t, []recordedAction{saved}, run.engine.replays)
	assert.Equal(t, []string{"Click the Next button"}, run.engine.actInstructions(), "the commit act asks no model")
}

// In record mode a write no approved commit point sends is blocked and
// fails the step with write_blocked, and the act is kept as a commit point
// to approve.
func TestRecordFailsAWriteNothingApproved(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	run.engine.actWrites = []writegate.Event{blockedSave}
	execution := run.execute(recordedSteps, nil)

	require.ErrorContains(t, execution.err, "do[0] act failed: write_blocked: the write gate blocked POST https://books.example.com/api/deals")
	assert.Equal(t, []string{"Click the Save button"}, run.engine.actInstructions())
	points := run.commitPoints()
	require.Len(t, points, 1)
	assert.False(t, points[0].Approved)
	assert.Equal(t, replayKey(0, "Click the Save button", "https://books.example.com/deals"), points[0].Key)
	assert.Equal(t, blockedSave.Signature, points[0].Signature)
	assert.NotEmpty(t, points[0].Actions)
	data, err := os.ReadFile(filepath.Join(run.artifacts, "browser", "shop", writeGateFile))
	require.NoError(t, err)
	var report writeGateReport
	require.NoError(t, json.Unmarshal(data, &report))
	require.NotNil(t, report.WouldSend)
	assert.Equal(t, blockedSave.Signature, report.WouldSend.Signature)
}

// A rehearsal keeps the act it stopped at as a commit point.
func TestRehearsalKeepsItsCommitPoint(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	run.engine.actWrites = []writegate.Event{blockedSave}
	execution := run.execute(`{"url": "https://books.example.com/deals", "browser": {"write_gate": "rehearse"}, "do": [{"act": "Click the Save button"}]}`, nil)
	require.NoError(t, execution.err)

	points := run.commitPoints()
	require.Len(t, points, 1)
	assert.Equal(t, "Click the Save button", points[0].Operation)
	assert.Equal(t, "https://books.example.com/deals", points[0].Page)
}
