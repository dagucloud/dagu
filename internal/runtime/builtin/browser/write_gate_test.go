// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
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

// A host's policy names the sign-ins of every site; a step that holds the
// secret of only one keeps that site's rule and still runs.
func TestWriteGateKeepsOnlyTheSignInsAStepHolds(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	policy := filepath.Join(t.TempDir(), "policy.yaml")
	require.NoError(t, os.WriteFile(policy, []byte(`sign_in:
  - host: login.example.com
    secret: PORTAL_PASSWORD
  - host: books.example.com
    secret: BOOKS_PASSWORD
`), 0o600))
	run.hostWritePolicy = policy
	run.secrets = map[string]string{"PORTAL_PASSWORD": "s3cret"}
	run.engine.handle.WriteGate = runningGate()
	execution := run.execute(`{"browser": {"write_gate": "rehearse"}, "do": [{"act": "Click the Sign in button"}]}`, nil)
	require.NoError(t, execution.err)

	gate := run.launcher.launches[0].WriteGate
	require.NotNil(t, gate)
	assert.Equal(t, []writegate.Rule{{Host: "login.example.com", Secret: "PORTAL_PASSWORD"}}, gate.Policy.SignIn)
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

// saveKey is the commit point key of recordedSteps' first act.
var saveKey = replayKey(0, "Click the Save button", "https://books.example.com/deals")

// approve keeps point as an approved commit point of the test run's step.
func (r *testRun) approve(point writegate.CommitPoint) {
	r.t.Helper()
	store := writegate.NewCommitStore(filepath.Join(r.dataDir, "browser"))
	require.NoError(r.t, store.Note(r.t.Context(), "orders", "shop", point))
	_, err := store.Approve(r.t.Context(), "orders", "shop", nil, func(writegate.CommitPoint) bool { return true }, time.Now())
	require.NoError(r.t, err)
}

// In record mode an approved commit act replays what was approved, asking
// no model, while the gate lets it send its approved writes.
func TestRecordSendsTheApprovedCommitWrite(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	saved := recordedAction{Selector: "xpath=/html/body/button[2]", Method: "click"}
	actions, err := json.Marshal([]recordedAction{saved})
	require.NoError(t, err)
	run.approve(writegate.CommitPoint{Key: saveKey, Op: 0, Operation: "Click the Save button", Signature: blockedSave.Signature, Actions: actions})

	execution := run.execute(recordedSteps, nil)
	require.NoError(t, execution.err)

	assert.Equal(t, [][]string{{blockedSave.Signature}}, run.engine.allowed, "only the commit act may send, and only its write")
	assert.False(t, run.engine.allowing, "the allowance ends before the next operation")
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
	assert.Equal(t, saveKey, points[0].Key)
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

// In record mode a write nothing approved, sent while the last downloads
// settle, fails the step as one sent during an operation does.
func TestRecordFailsAWriteAfterTheLastOperation(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	run.engine.downloadWrites = []writegate.Event{blockedSave}
	execution := run.execute(`{"url": "https://books.example.com/deals", "browser": {"write_gate": "record"}, "do": [{"act": "Click the Save button"}]}`, nil)

	require.ErrorContains(t, execution.err, "do[0] act failed: write_blocked")
	require.Len(t, run.commitPoints(), 1)
}

// The approved write a commit act's page sends while the last downloads
// settle is sent, as one sent during the act is.
func TestRecordSendsAnApprovedWriteAfterTheLastOperation(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	run.approve(writegate.CommitPoint{Key: saveKey, Operation: "Click the Save button", Signature: blockedSave.Signature})
	run.engine.downloadWrites = []writegate.Event{blockedSave}
	execution := run.execute(`{"url": "https://books.example.com/deals", "browser": {"write_gate": "record"}, "do": [{"act": "Click the Save button"}]}`, nil)
	require.NoError(t, execution.err)

	writes := run.readReport().Writes
	require.Len(t, writes, 1)
	assert.Equal(t, writegate.OutcomeSent, writes[0].Outcome)
}

// A write the page sends after an operation returned, before the next one
// starts, is that operation's.
func TestRecordKeepsALateWriteWithItsOperation(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	run.engine.downloadWrites = []writegate.Event{blockedSave}
	execution := run.execute(recordedSteps, nil)

	require.ErrorContains(t, execution.err, "do[0] act failed: write_blocked")
	assert.Equal(t, []string{"Click the Save button"}, run.engine.actInstructions(), "the next operation does not start")
	points := run.commitPoints()
	require.Len(t, points, 1)
	assert.Equal(t, saveKey, points[0].Key)
}

// A write sent while the last downloads settle, after the last operation
// was skipped, is kept with the operation that ran.
func TestRehearsalKeepsALateWriteWithTheOperationThatRan(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	run.engine.downloadWrites = []writegate.Event{blockedSave}
	execution := run.execute(`{"url": "https://books.example.com/deals", "browser": {"write_gate": "rehearse"}, "do": [
		{"act": "Click the Save button"},
		{"act": "Close the banner", "when": {"text": "Cookies"}}
	]}`, nil)
	require.NoError(t, execution.err)

	points := run.commitPoints()
	require.Len(t, points, 1)
	assert.Equal(t, 0, points[0].Op)
	assert.Equal(t, "Click the Save button", points[0].Operation)
}

// A gate that may still let an operation's approved writes through fails
// the step before the next operation starts.
func TestRecordFailsWhenAnAllowanceMayNotEnd(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	run.approve(writegate.CommitPoint{Key: saveKey, Operation: "Click the Save button", Signature: blockedSave.Signature})
	run.engine.endErr = errors.New("the write gate answered 500 Internal Server Error")
	execution := run.execute(recordedSteps, nil)

	require.ErrorContains(t, execution.err, "the write gate answered 500 Internal Server Error")
	assert.Equal(t, []string{"Click the Save button"}, run.engine.actInstructions(), "the next operation does not start")
}

const askedSteps = `{
	"url": "https://books.example.com/deals",
	"browser": {"write_gate": "ask"},
	"do": [
		{"act": "Click the Save button"},
		{"act": "Click the Next button"}
	]
}`

// noteSavePoint keeps the Save act of askedSteps as a commit point, not yet
// approved, with the action it performs.
func (r *testRun) noteSavePoint() recordedAction {
	r.t.Helper()
	saved := recordedAction{Selector: "xpath=/html/body/button[2]", Method: "click"}
	actions, err := json.Marshal([]recordedAction{saved})
	require.NoError(r.t, err)
	point := writegate.CommitPoint{
		Key: saveKey, Op: 0, Operation: "Click the Save button",
		Signature: blockedSave.Signature, Actions: actions, Example: &blockedSave,
	}
	require.NoError(r.t, writegate.NewCommitStore(filepath.Join(r.dataDir, "browser")).Note(r.t.Context(), "orders", "shop", point))
	return saved
}

// answerCommit answers the pending permission of a waiting step.
func answerCommit(t *testing.T, session *ir.AgentSession, decision string) *ir.AgentSession {
	t.Helper()
	require.Len(t, session.Interactions, 1)
	session.Interactions[0].Status = ir.AgentInteractionAnswered
	if decision == "reject" {
		session.Interactions[0].Status = ir.AgentInteractionRejected
	}
	session.Interactions[0].Decision = decision
	return session
}

// In ask mode a step waits before a commit act, showing the page it is
// about to send from, and asks for the write the act sends.
func TestAskWaitsBeforeACommitAct(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	run.noteSavePoint()
	waiting := run.execute(askedSteps, nil)
	require.NoError(t, waiting.err)

	status, _ := waiting.exec.DetermineNodeStatus()
	assert.Equal(t, ir.NodeWaiting, status)
	assert.Empty(t, run.engine.actInstructions(), "the commit act waits")
	assert.Empty(t, run.engine.replays)
	assert.True(t, run.engine.detached)
	session := waiting.exec.GetAgentSession()
	require.Len(t, session.Interactions, 1)
	interaction := session.Interactions[0]
	assert.Equal(t, ir.AgentInteractionPermission, interaction.Kind)
	assert.Equal(t, ir.AgentPermissionBrowserWrite, interaction.Permission)
	assert.Equal(t, []string{blockedSave.Signature}, interaction.Patterns)
	assert.Contains(t, interaction.Questions[0].Question, `do[0] "Click the Save button" is about to send POST https://books.example.com/api/deals`)
	last := session.Events[len(session.Events)-1]
	assert.Equal(t, kindWriteGate+":"+agentstep.StatusWaiting, last.Name+":"+last.Status)
	assert.Contains(t, last.Files, "browser/shop/commit-0.txt")
	assert.FileExists(t, filepath.Join(run.artifacts, "browser", "shop", "commit-0.txt"))
	records := run.records()
	require.Len(t, records, 1)
	assert.Equal(t, 0, records[0].Cursor, "the commit act runs once answered")
}

// Answered once, the commit act replays its approved actions without a
// model and sends only its write; answered for the session, its point is
// approved for later runs too.
func TestAskSendsTheWriteItWasAnswered(t *testing.T) {
	t.Parallel()

	for _, decision := range []string{"once", "session"} {
		t.Run(decision, func(t *testing.T) {
			t.Parallel()

			run := newTestRun(t, pageModel(nil))
			run.engine.handle.WriteGate = runningGate()
			saved := run.noteSavePoint()
			waiting := run.execute(askedSteps, nil)
			require.NoError(t, waiting.err)
			resumed := run.execute(askedSteps, answerCommit(t, waiting.exec.GetAgentSession(), decision))
			require.NoError(t, resumed.err)

			assert.Equal(t, []recordedAction{saved}, run.engine.replays)
			assert.Equal(t, []string{"Click the Next button"}, run.engine.actInstructions())
			assert.Equal(t, [][]string{{blockedSave.Signature}}, run.engine.allowed)
			points := run.commitPoints()
			require.Len(t, points, 1)
			assert.Equal(t, decision == "session", points[0].Approved)
		})
	}
}

// Skipped, the commit act does not run, nothing after it runs, and the
// step is skipped rather than failed.
func TestAskSkipEndsTheStepWithoutSending(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	run.noteSavePoint()
	waiting := run.execute(askedSteps, nil)
	require.NoError(t, waiting.err)
	resumed := run.execute(askedSteps, answerCommit(t, waiting.exec.GetAgentSession(), ir.AgentDecisionSkip))
	require.NoError(t, resumed.err)

	status, _ := resumed.exec.DetermineNodeStatus()
	assert.Equal(t, ir.NodeSkipped, status)
	assert.Empty(t, run.engine.actInstructions())
	assert.Empty(t, run.engine.replays)
	assert.Empty(t, run.engine.allowed)
	assert.True(t, run.engine.closed)
	data, err := os.ReadFile(filepath.Join(run.artifacts, "browser", "shop", writeGateFile))
	require.NoError(t, err)
	var report writeGateReport
	require.NoError(t, json.Unmarshal(data, &report))
	assert.True(t, report.Skipped)
	assert.Equal(t, []int{0, 1}, report.NotRun)

	rejected := newTestRun(t, pageModel(nil))
	rejected.engine.handle.WriteGate = runningGate()
	rejected.noteSavePoint()
	waiting = rejected.execute(askedSteps, nil)
	require.NoError(t, waiting.err)
	failed := rejected.execute(askedSteps, answerCommit(t, waiting.exec.GetAgentSession(), "reject"))
	require.ErrorContains(t, failed.err, agentstep.ErrAskRejected.Error())
	assert.Empty(t, rejected.engine.actInstructions())
}

// An act no commit point names runs as in record mode, without asking.
func TestAskRunsOtherActsWithoutAsking(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	execution := run.execute(askedSteps, nil)
	require.NoError(t, execution.err)
	assert.Equal(t, []string{"Click the Save button", "Click the Next button"}, run.engine.actInstructions())
	assert.Empty(t, execution.exec.GetAgentSession().Interactions)
}

// An answer to a commit act that is neither once nor session sends
// nothing.
func TestAskSendsNothingForAnotherAnswer(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	run.noteSavePoint()
	waiting := run.execute(askedSteps, nil)
	require.NoError(t, waiting.err)
	resumed := run.execute(askedSteps, answerCommit(t, waiting.exec.GetAgentSession(), ""))
	require.NoError(t, resumed.err)

	assert.Empty(t, run.engine.replays)
	assert.Empty(t, run.engine.allowed)
	assert.Empty(t, run.engine.actInstructions())
}

// The downloads the operations before a commit act started settle before
// the step waits for a person.
func TestAskSettlesDownloadsBeforeItWaits(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.handle.WriteGate = runningGate()
	point := writegate.CommitPoint{Key: replayKey(1, "Click the Save button", "https://books.example.com/deals"), Op: 1, Operation: "Click the Save button", Signature: blockedSave.Signature}
	require.NoError(t, writegate.NewCommitStore(filepath.Join(run.dataDir, "browser")).Note(t.Context(), "orders", "shop", point))
	run.engine.lateDownloads = []string{"deals.csv"}
	waiting := run.execute(`{"url": "https://books.example.com/deals", "browser": {"write_gate": "ask"}, "do": [
		{"act": "Click the Export button"},
		{"act": "Click the Save button"}
	]}`, nil)
	require.NoError(t, waiting.err)

	assert.Equal(t, []string{"goto:completed", "act:completed", "download:completed", "write_gate:waiting"}, eventNames(waiting.exec.GetAgentSession()))
}
