// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The model sees a screenshot scaled to its limit; its positions are
// mapped back to display pixels.
func TestActDrivesDesktop(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	session := &scriptedSession{
		limit: computeruse.ImageLimit{LongEdge: 200},
		turns: []*computeruse.Turn{
			actions(clickAt(10, 20), computeruse.Action{Kind: computeruse.KindKey, Keys: []string{"ctrl", "s"}}),
			done("Saved the document"),
		},
	}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"do": [{"launch": {"command": "notepad.exe", "args": ["a.txt"]}}, {"act": "Save the document"}]}`, nil)
	require.NoError(t, execution.err)

	assert.Equal(t, [][]string{{run.workDir, "notepad.exe", "a.txt"}}, run.launches, "applications start in the step's working directory")
	assert.Equal(t, []string{"move 20,40", "left down #1", "key ctrl", "key s"}, run.backend.inputs())
	require.Len(t, session.observations, 2)
	assert.Equal(t, 200, session.observations[0].Screen.Width, "the screenshot is scaled to the session limit")
	assert.Equal(t, 100, session.observations[0].Screen.Height)
	assert.Equal(t, []computeruse.Result{{CallID: "c"}, {}}, session.observations[1].Results)

	result := execution.exec.GetAgentSession()
	assert.Equal(t, ir.AgentSessionSucceeded, result.State)
	assert.Equal(t, []string{"launch:completed", "act:completed"}, eventNames(result))
	assert.Equal(t, int64(24), result.Usage.TotalTokens)
	assert.Contains(t, execution.stderr.String(), "Saved the document (2 actions)")
}

// Variables reach the desktop only when typed; the model and the log see
// the placeholder.
func TestActTypesVariables(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.secrets = map[string]string{"SAP_PASSWORD": "hunter2-secret"}
	session := &scriptedSession{turns: []*computeruse.Turn{
		actions(computeruse.Action{Kind: computeruse.KindType, Text: "%password%\n"}),
		done("Logged in"),
	}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"variables": {"password": "hunter2-secret"}, "do": [{"act": "Log in with %password%"}]}`, nil)
	require.NoError(t, execution.err)

	assert.Equal(t, []string{"type hunter2-secret", "key enter"}, run.backend.inputs())
	assert.NotContains(t, execution.stderr.String(), "hunter2-secret")
	assert.Contains(t, execution.stderr.String(), "%password%")
}

func TestExtractPublishesOutputs(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.vision.extract = `{"total": 42.5, "extra": "ignored"}`
	execution := run.execute(`{"do": [{"extract": {"instruction": "The invoice total", "schema": {"type": "object", "properties": {"total": {"type": "number"}}}}}]}`, nil)
	require.NoError(t, execution.err)

	assert.Equal(t, map[string]any{"total": 42.5}, execution.exec.GetOutputs())
	assert.JSONEq(t, `{"total": 42.5}`, execution.stdout.String())
	require.Len(t, run.vision.requests, 1)
	assert.Len(t, run.vision.requests[0].Messages[1].Images, 1, "the model reads a screenshot")
}

func TestExpectAndWhen(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.vision.truths = map[string]bool{"Invoice posted": true, "An error dialog": false}
	execution := run.execute(`{"do": [
		{"expect": "Invoice posted"},
		{"screenshot": "posted", "when": "An error dialog is shown"}
	]}`, nil)
	require.NoError(t, execution.err)
	assert.Equal(t, []string{"expect:completed", "screenshot:skipped"}, eventNames(execution.exec.GetAgentSession()))

	failed := run.execute(`{"do": [{"expect": "An error dialog is shown"}]}`, nil)
	require.ErrorContains(t, failed.err, "do[0] expect failed: expectation not met")
}

// An ask pauses the step and frees the desktop; the answer resumes at the
// next operation with the answer as a variable.
func TestAskWaitsAndResumes(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [
		{"extract": {"instruction": "The account", "schema": {"type": "object", "properties": {"account": {"type": "string"}}}}},
		{"ask": {"prompt": "Enter the one-time code", "as": "otp"}},
		{"act": "Type %otp% into the code field"}
	]}`
	run := newTestRun(t)
	run.vision.extract = `{"account": "acme"}`
	waiting := run.execute(steps, nil)
	require.NoError(t, waiting.err)

	status, err := waiting.exec.DetermineNodeStatus()
	require.NoError(t, err)
	assert.Equal(t, ir.NodeWaiting, status)
	session := waiting.exec.GetAgentSession()
	assert.Equal(t, ir.AgentSessionWaiting, session.State)
	require.Len(t, session.Interactions, 1)
	assert.Equal(t, "Enter the one-time code", session.Interactions[0].Questions[0].Question)

	store := computerhost.NewStore(filepath.Join(run.dataDir, computerhost.DataDirName))
	record, err := store.Load("run-1", "post")
	require.NoError(t, err)
	assert.Equal(t, 2, record.Cursor)
	assert.Equal(t, map[string]any{"account": "acme"}, record.Outputs)

	// Another computer step can use the desktop while this one waits.
	quiet := &agentstep.Timeline{Log: io.Discard, Masker: agentstep.NewMasker(nil, nil), Update: func(func(*ir.AgentSession)) {}}
	lease, err := acquireDesktop(t.Context(), run.desktopLock, quiet)
	require.NoError(t, err)
	lease.release()

	session.Interactions[0].Status = ir.AgentInteractionAnswered
	session.Interactions[0].Answers = [][]string{{"731902"}}
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{
		actions(computeruse.Action{Kind: computeruse.KindType, Text: "%otp%"}),
		done("Entered the code"),
	}}}
	resumed := run.execute(steps, session)
	require.NoError(t, resumed.err)

	assert.Equal(t, []string{"type 731902"}, run.backend.inputs())
	assert.Equal(t, map[string]any{"account": "acme"}, resumed.exec.GetOutputs())
	assert.NotContains(t, resumed.stderr.String(), "731902", "answers are masked")
	assert.True(t, resumed.exec.GetAgentSession().Interactions[0].Applied)
	assert.Equal(t, int64(30), resumed.exec.GetAgentSession().Usage.TotalTokens, "tokens used before the pause still count")
	_, err = store.Load("run-1", "post")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// An answer whose waiting record cannot be read for now stays pending, so
// a retry resumes the step once the record is readable again.
func TestAskResumeAfterUnreadableRecord(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"ask": {"prompt": "Code?", "as": "otp"}}, {"act": "Type %otp%"}]}`
	run := newTestRun(t)
	waiting := run.execute(steps, nil)
	require.NoError(t, waiting.err)
	session := waiting.exec.GetAgentSession()
	session.Interactions[0].Status = ir.AgentInteractionAnswered
	session.Interactions[0].Answers = [][]string{{"731902"}}

	restore := blockRead(t, filepath.Join(run.dataDir, computerhost.DataDirName, "sessions"))
	failed := run.execute(steps, session)
	restore()
	require.ErrorContains(t, failed.err, "read the paused step's record")
	retry := failed.exec.GetAgentSession()
	assert.False(t, retry.Interactions[0].Applied, "the answer stays pending")

	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{
		actions(computeruse.Action{Kind: computeruse.KindType, Text: "%otp%"}),
		done("Entered the code"),
	}}}
	require.NoError(t, run.execute(steps, retry).err)
	assert.Equal(t, []string{"type 731902"}, run.backend.inputs())
}

// A paused step whose record is gone cannot resume; its answer is used up,
// so the next attempt starts the step over.
func TestAskResumeWithoutRecord(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"ask": {"prompt": "Code?", "as": "otp"}}, {"wait": "1ms"}]}`
	run := newTestRun(t)
	waiting := run.execute(steps, nil)
	require.NoError(t, waiting.err)
	session := waiting.exec.GetAgentSession()
	session.Interactions[0].Status = ir.AgentInteractionAnswered
	session.Interactions[0].Answers = [][]string{{"731902"}}
	require.NoError(t, computerhost.NewStore(filepath.Join(run.dataDir, computerhost.DataDirName)).Delete("run-1", "post"))

	failed := run.execute(steps, session)
	require.ErrorContains(t, failed.err, "can no longer be resumed")
	assert.True(t, failed.exec.GetAgentSession().Interactions[0].Applied)

	again := run.execute(steps, failed.exec.GetAgentSession())
	require.NoError(t, again.err)
	status, err := again.exec.DetermineNodeStatus()
	require.NoError(t, err)
	assert.Equal(t, ir.NodeWaiting, status, "the step started over and asks again")
}

func TestAskRejected(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"ask": {"prompt": "Continue?", "as": "ok"}}, {"wait": "1ms"}]}`
	run := newTestRun(t)
	waiting := run.execute(steps, nil)
	require.NoError(t, waiting.err)
	session := waiting.exec.GetAgentSession()
	session.Interactions[0].Status = ir.AgentInteractionRejected

	rejected := run.execute(steps, session)
	require.ErrorContains(t, rejected.err, "the input request was rejected")
}

// A successful act is recorded and replayed without a model while the
// screens match; a changed screen hands the task back to the model.
func TestReplayCache(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Open the report"}]}`
	run := newTestRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(30, 40)), done("Opened")}}}
	require.NoError(t, run.execute(steps, nil).err)
	recorded := run.backend.inputs()

	run.backend.events = nil
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err, "no session is left, so a model call would fail")
	assert.Equal(t, recorded, run.backend.inputs())
	assert.Equal(t, []string{"act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()))

	run.backend.show(pattern(400, 200, 150))
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(50, 60)), done("Opened")}}}
	healed := run.execute(steps, nil)
	require.NoError(t, healed.err)
	assert.Equal(t, []string{"act:healed"}, eventNames(healed.exec.GetAgentSession()))

	uncached := run.execute(`{"cache": false, "do": [{"act": "Open the report"}]}`, nil)
	require.ErrorContains(t, uncached.err, "no scripted session left")
}

// When a replay diverges partway, the model continues from there, and the
// stored recording keeps the turns that replayed, so the next run replays
// the whole act again.
func TestReplayCacheKeepsReplayedTurns(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Post the invoice"}]}`
	start, form, changedForm, posted := stripes(400, 200, 2), stripes(400, 200, 6), stripes(400, 200, 12), stripes(400, 200, 30)
	run := newTestRun(t)

	run.backend.script(start, form, posted)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(10, 10)), actions(clickAt(20, 20)), done("Posted")}}}
	require.NoError(t, run.execute(steps, nil).err)

	// The form changed, so the second recorded turn no longer matches.
	run.backend.script(start, changedForm, posted)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(30, 30)), done("Posted")}}}
	healed := run.execute(steps, nil)
	require.NoError(t, healed.err)
	assert.Equal(t, []string{"act:healed"}, eventNames(healed.exec.GetAgentSession()))
	assert.Equal(t, []string{"move 10,10", "left down #1", "move 30,30", "left down #1"}, run.backend.inputs())

	run.backend.script(start, changedForm, posted)
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err, "no session is left, so a model call would fail")
	assert.Equal(t, []string{"act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()))
	assert.Equal(t, []string{"move 10,10", "left down #1", "move 30,30", "left down #1"}, run.backend.inputs())
}

// An act's recording is kept only when the whole step succeeds, and a step
// that fails on the screen after a replay drops the recording, so an act
// that did the wrong thing is not repeated.
func TestReplayKeptOnlyWhenStepSucceeds(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Open the report"}, {"expect": "The report is shown"}]}`
	shown := map[string]bool{"The report is shown": true}
	opens := func() []*scriptedSession {
		return []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(30, 40)), done("Opened")}}}
	}
	run := newTestRun(t)

	run.sessions = opens()
	require.ErrorContains(t, run.execute(steps, nil).err, "expectation not met")

	run.vision.truths = shown
	run.sessions = opens()
	recorded := run.execute(steps, nil)
	require.NoError(t, recorded.err)
	assert.Equal(t, []string{"act:completed", "expect:completed"}, eventNames(recorded.exec.GetAgentSession()), "the failed step left no recording")

	run.vision.truths = nil
	failed := run.execute(steps, nil)
	require.ErrorContains(t, failed.err, "expectation not met")
	assert.Equal(t, []string{"act:cache-hit"}, eventNames(failed.exec.GetAgentSession()))

	run.vision.truths = shown
	uncached := run.execute(steps, nil)
	require.ErrorContains(t, uncached.err, "no scripted session left", "the replayed recording was dropped")
}

// A step that fails because a model did not answer keeps the recordings it
// replayed, so an outage does not wipe the cache.
func TestReplayKeptWhenModelFails(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Open the report"}, {"act": {"instruction": "Print it", "cache": false}}]}`
	run := newTestRun(t)
	run.sessions = []*scriptedSession{
		{turns: []*computeruse.Turn{actions(clickAt(30, 40)), done("Opened")}},
		{turns: []*computeruse.Turn{done("Printed")}},
	}
	require.NoError(t, run.execute(steps, nil).err)

	run.sessions = []*scriptedSession{{err: errors.New("overloaded")}}
	failed := run.execute(steps, nil)
	require.ErrorContains(t, failed.err, "overloaded")
	assert.Equal(t, []string{"act:cache-hit"}, eventNames(failed.exec.GetAgentSession()))

	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{done("Printed")}}}
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"act:cache-hit", "act:completed"}, eventNames(replayed.exec.GetAgentSession()))
}

// What a step recorded before an ask is kept when the resumed step
// succeeds.
func TestReplayAcrossAsk(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Open the report"}, {"ask": {"prompt": "Continue?", "as": "ok"}}, {"wait": "1ms"}]}`
	run := newTestRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(30, 40)), done("Opened")}}}
	waiting := run.execute(steps, nil)
	require.NoError(t, waiting.err)
	session := waiting.exec.GetAgentSession()
	session.Interactions[0].Status = ir.AgentInteractionAnswered
	session.Interactions[0].Answers = [][]string{{"yes"}}
	require.NoError(t, run.execute(steps, session).err)

	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err, "no session is left, so a model call would fail")
	assert.Equal(t, []string{"act:cache-hit", "ask:waiting"}, eventNames(replayed.exec.GetAgentSession()))
}

// A replay applies the step's current max_actions and on_confirmation, so
// tightening them sends the task back to the model instead of repeating the
// recorded input.
func TestReplayFollowsCurrentSettings(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		recorded string
		turns    []*computeruse.Turn
		replayed string
	}{
		{
			name:     "max actions",
			recorded: `{"do": [{"act": "Click twice"}]}`,
			turns:    []*computeruse.Turn{actions(clickAt(1, 1), clickAt(2, 2)), done("Clicked")},
			replayed: `{"max_actions": 1, "do": [{"act": "Click twice"}]}`,
		},
		{
			name:     "confirmation",
			recorded: `{"on_confirmation": "allow", "do": [{"act": "Pay"}]}`,
			turns:    []*computeruse.Turn{{Actions: []computeruse.Action{clickAt(1, 1)}, Confirmation: "Submits a payment"}, done("Paid")},
			replayed: `{"do": [{"act": "Pay"}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := newTestRun(t)
			run.sessions = []*scriptedSession{{turns: tc.turns}}
			require.NoError(t, run.execute(tc.recorded, nil).err)
			run.backend.events = nil

			failed := run.execute(tc.replayed, nil)
			require.ErrorContains(t, failed.err, "no scripted session left", "the model is asked instead")
			assert.Empty(t, run.backend.inputs(), "no recorded input is repeated")
		})
	}
}

// Actions a replay ran before one failed count toward max_actions for the
// model that continues.
func TestReplayCountsCutShortTurn(t *testing.T) {
	t.Parallel()

	const steps = `{"max_actions": 2, "do": [{"act": "Fill the form"}]}`
	typeText := func(text string) computeruse.Action {
		return computeruse.Action{Kind: computeruse.KindType, Text: text}
	}
	run := newTestRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(1, 1), typeText("acme")), done("Filled")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.backend.typeErr = errors.New("input blocked")
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(typeText("acme")), done("Filled")}}}
	failed := run.execute(steps, nil)
	require.ErrorContains(t, failed.err, "more than max_actions (2)")
}

// Dagu processes with different data directories operate one desktop, so a
// step waits while another one holds it.
func TestDesktopSharedAcrossDataDirs(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Click"}]}`
	holding, release := make(chan struct{}), make(chan struct{})
	first := newTestRun(t)
	first.sessions = []*scriptedSession{{
		turns:  []*computeruse.Turn{done("Clicked")},
		onNext: func() { close(holding); <-release },
	}}
	started := make(chan struct{})
	second := newTestRun(t)
	second.desktopLock = first.desktopLock
	second.sessions = []*scriptedSession{{
		turns:  []*computeruse.Turn{done("Clicked")},
		onNext: func() { close(started) },
	}}

	firstDone, secondDone := make(chan *stepExecution, 1), make(chan *stepExecution, 1)
	go func() { firstDone <- first.execute(steps, nil) }()
	<-holding
	go func() { secondDone <- second.execute(steps, nil) }()
	select {
	case <-started:
		t.Fatal("the second step operated the desktop while the first one held it")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)

	require.NoError(t, (<-firstDone).err)
	waited := <-secondDone
	require.NoError(t, waited.err)
	assert.Contains(t, lifecycleMessages(waited.exec.GetAgentSession()), "Waiting for another computer step to finish using the desktop")
}

// A step waits until nobody has used the desktop for the idle period before
// it launches, acts, or replays.
func TestWaitsForIdleDesktop(t *testing.T) {
	t.Parallel()

	const with = `{"idle": "100ms", "do": [{"act": "Click"}]}`
	clicks := func() []*scriptedSession {
		return []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(1, 1)), done("Clicked")}}}
	}
	for _, tc := range []struct {
		name    string
		with    string
		prepare func(t *testing.T, run *testRun)
		want    []string
	}{
		{
			name: "launch",
			with: `{"idle": "100ms", "do": [{"launch": "notepad.exe"}]}`,
			want: []string{"launch:completed"},
		},
		{
			name:    "act",
			with:    with,
			prepare: func(_ *testing.T, run *testRun) { run.sessions = clicks() },
			want:    []string{"act:completed"},
		},
		{
			name: "replay",
			with: with,
			// Another desktop records the act, so this one has sent no input.
			prepare: func(t *testing.T, run *testRun) {
				recorder := newTestRun(t)
				recorder.dataDir = run.dataDir
				recorder.sessions = clicks()
				require.NoError(t, recorder.execute(with, nil).err)
			},
			want: []string{"act:cache-hit"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := newTestRun(t)
			if tc.prepare != nil {
				tc.prepare(t, run)
			}
			run.backend.personKeepsUsing(2)
			execution := run.execute(tc.with, nil)
			require.NoError(t, execution.err)
			session := execution.exec.GetAgentSession()
			assert.Equal(t, tc.want, eventNames(session))
			assert.Contains(t, lifecycleMessages(session), "Waiting until nobody has used the desktop for 100ms")
		})
	}
}

// Actions the model chose on a screen a person has since used are not run;
// the model sees the new screen and why.
func TestPersonInputSkipsStaleTurn(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	used := false
	session := &scriptedSession{
		turns: []*computeruse.Turn{actions(clickAt(10, 10)), actions(clickAt(20, 20)), done("Clicked")},
		onNext: func() {
			if !used {
				used = true
				run.backend.personUses()
			}
		},
	}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"idle": "100ms", "do": [{"act": "Click the button"}]}`, nil)
	require.NoError(t, execution.err)

	assert.Equal(t, []string{"move 20,20", "left down #1"}, run.backend.inputs())
	require.Len(t, session.observations, 3)
	assert.Equal(t, []computeruse.Result{{CallID: "c", Skipped: true}}, session.observations[1].Results)
	assert.Equal(t, personNote, session.observations[1].Note)
	assert.Contains(t, execution.stderr.String(), "Clicked (1 actions)")
}

// The step that last held the desktop leaves when it sent input, so the next
// step does not take that input for a person's and wait for it.
func TestNextStepIgnoresEarlierInput(t *testing.T) {
	t.Parallel()

	const with = `{"idle": "1h", "cache": false, "do": [{"act": "Click", "timeout": "2s"}]}`
	run := newTestRun(t)
	for range 2 {
		run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(1, 1)), done("Clicked")}}}
		require.NoError(t, run.execute(with, nil).err)
	}
}

// With idle 0, the step neither waits for nor skips around a person.
func TestIdleZeroIgnoresPerson(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.backend.personUses()
	run.sessions = []*scriptedSession{{
		turns:  []*computeruse.Turn{actions(clickAt(10, 10)), done("Clicked")},
		onNext: run.backend.personUses,
	}}
	execution := run.execute(`{"idle": "0", "do": [{"act": "Click the button"}]}`, nil)
	require.NoError(t, execution.err)

	assert.Equal(t, []string{"move 10,10", "left down #1"}, run.backend.inputs())
	assert.NotContains(t, strings.Join(lifecycleMessages(execution.exec.GetAgentSession()), "\n"), "Waiting until nobody")
}

// A later model takes over only while the desktop is untouched.
func TestModelFallback(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.llm = &ir.LLMConfig{Models: []ir.ModelEntry{
		{Provider: "anthropic", Name: "first"},
		{Provider: "openai", Name: "second"},
	}}
	run.sessionErrors = map[string]error{"first": errors.New("overloaded")}
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(1, 1)), done("ok")}}}
	require.NoError(t, run.execute(`{"do": [{"act": "Click"}]}`, nil).err)

	run.sessionErrors = nil
	run.sessions = []*scriptedSession{
		{turns: []*computeruse.Turn{actions(clickAt(1, 1))}},
		{turns: []*computeruse.Turn{done("should not run")}},
	}
	failed := run.execute(`{"cache": false, "do": [{"act": "Click"}]}`, nil)
	require.ErrorContains(t, failed.err, "no scripted turn left")
}

func TestActLimits(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		with  string
		turns []*computeruse.Turn
		want  string
	}{
		{
			name:  "max actions",
			with:  `{"max_actions": 2, "cache": false, "do": [{"act": "Click around"}]}`,
			turns: []*computeruse.Turn{actions(clickAt(1, 1), clickAt(2, 2)), actions(clickAt(3, 3))},
			want:  "more than max_actions (2)",
		},
		{
			name:  "model gives up",
			with:  `{"do": [{"act": "Open the file"}]}`,
			turns: []*computeruse.Turn{{Done: &computeruse.Done{Success: false, Summary: "The file is missing"}}},
			want:  "the model could not complete the task: The file is missing",
		},
		{
			name:  "model stops without done",
			with:  `{"do": [{"act": "Open the file"}]}`,
			turns: []*computeruse.Turn{{Text: "I think it is open."}, {Text: "Still open."}},
			want:  "the model stopped without reporting the task done",
		},
		{
			name:  "confirmation",
			with:  `{"do": [{"act": "Submit the form"}]}`,
			turns: []*computeruse.Turn{{Actions: []computeruse.Action{clickAt(1, 1)}, Confirmation: "Submits a payment"}},
			want:  "asks a person to confirm the next actions (Submits a payment)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := newTestRun(t)
			run.sessions = []*scriptedSession{{turns: tc.turns}}
			execution := run.execute(tc.with, nil)
			require.ErrorContains(t, execution.err, tc.want)
			assert.Equal(t, ir.AgentSessionFailed, execution.exec.GetAgentSession().State)
		})
	}
}

// A model provider's confirmation request is approved with
// on_confirmation: allow, and the approval is sent with the next screen.
func TestConfirmationAllowed(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	session := &scriptedSession{turns: []*computeruse.Turn{
		{Actions: []computeruse.Action{clickAt(1, 1)}, Confirmation: "Submits a payment"},
		done("Paid"),
	}}
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(`{"on_confirmation": "allow", "do": [{"act": "Pay"}]}`, nil).err)
	assert.True(t, session.observations[1].Acknowledged)
}

// A failed action skips the rest of the turn, and the model sees why
// before it may finish.
func TestFailedActionIsReported(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	session := &scriptedSession{turns: []*computeruse.Turn{
		{Actions: []computeruse.Action{
			{CallID: "a", Kind: computeruse.KindKey, Keys: []string{"hyper"}},
			{CallID: "b", Kind: computeruse.KindType, Text: "x"},
		}, Done: &computeruse.Done{Success: true, Summary: "early"}},
		done("Done"),
	}}
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(`{"do": [{"act": "Press a key"}]}`, nil).err)

	require.Len(t, session.observations, 2)
	assert.Equal(t, []computeruse.Result{
		{CallID: "a", Error: `unknown key "hyper"`},
		{CallID: "b", Skipped: true},
	}, session.observations[1].Results)
	assert.Empty(t, run.backend.inputs())
}

func TestFailureScreenshot(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	execution := run.execute(`{"do": [{"act": "Do something"}]}`, nil)
	require.Error(t, execution.err)

	session := execution.exec.GetAgentSession()
	last := session.Events[len(session.Events)-1]
	assert.Equal(t, agentstep.StatusFailed, last.Status)
	require.Len(t, last.Files, 1)
	assert.FileExists(t, filepath.Join(run.artifacts, filepath.FromSlash(last.Files[0])))
}

func TestOpenDesktopFailure(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	step := ir.Step{Name: "post", ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: map[string]any{"do": []any{map[string]any{"wait": "1ms"}}}}, LLM: run.llm}
	created, err := newExecutor(t.Context(), step)
	require.NoError(t, err)
	exec := created.(*computerExecutor)
	exec.openDesktop = func() (*desktop.Driver, error) { return nil, errors.New("Screen Recording permission is missing") }
	exec.newProvider = func(context.Context, *ir.LLMConfig) (llmpkg.Provider, error) { return run.vision, nil }
	exec.SetStderr(io.Discard)
	err = exec.Run(run.context())
	require.ErrorContains(t, err, "open the desktop: Screen Recording permission is missing")
}
