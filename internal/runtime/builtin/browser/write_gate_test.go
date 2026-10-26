// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
