// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testRun is one DAG run whose browser step executions share a data
// directory, a browser, and a model.
type testRun struct {
	t         *testing.T
	dataDir   string
	artifacts string
	engine    *fakeEngine
	launcher  *fakeLauncher
	provider  *scriptedProvider
	secrets   map[string]string
}

func newTestRun(t *testing.T, answer func(*llmpkg.ChatRequest) (string, error)) *testRun {
	t.Helper()
	engine := newFakeEngine()
	return &testRun{
		t:         t,
		dataDir:   t.TempDir(),
		artifacts: t.TempDir(),
		engine:    engine,
		launcher:  &fakeLauncher{engine: engine},
		provider:  &scriptedProvider{answer: answer},
	}
}

// stepExecution is one execution of the browser step.
type stepExecution struct {
	exec   *browserExecutor
	stdout bytes.Buffer
	stderr bytes.Buffer
	err    error
}

func (r *testRun) execute(withJSON string, session *ir.AgentSession) *stepExecution {
	r.t.Helper()
	var with map[string]any
	require.NoError(r.t, json.Unmarshal([]byte(withJSON), &with))
	step := ir.Step{
		ID:             "shop",
		Name:           "shop",
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: with},
		LLM:            &ir.LLMConfig{Provider: "openai", Model: "test-model"},
	}
	created, err := newExecutor(r.t.Context(), step)
	require.NoError(r.t, err)
	execution := &stepExecution{exec: created.(*browserExecutor)}
	execution.exec.launcher = r.launcher
	execution.exec.askSupported = true
	execution.exec.newProvider = func(context.Context, *ir.LLMConfig) (llmpkg.Provider, error) {
		return r.provider, nil
	}
	execution.exec.SetStdout(&execution.stdout)
	execution.exec.SetStderr(&execution.stderr)
	execution.exec.SetAgentSession(session)
	execution.err = execution.exec.Run(r.context())
	return execution
}

func (r *testRun) context() context.Context {
	scope := value.NewEnvScope(nil, false).WithEntry(runenv.EnvKeyDAGRunArtifactsDir, r.artifacts, value.EnvSourceDAGEnv)
	for name, secret := range r.secrets {
		scope = scope.WithEntry(name, secret, value.EnvSourceSecret)
	}
	ctx := cmnconfig.WithConfig(r.t.Context(), &cmnconfig.Config{Paths: cmnconfig.PathsConfig{DataDir: r.dataDir}})
	return runtime.WithEnv(ctx, runtime.Env{
		Context: runtime.Context{
			DAG:      &ir.DAG{Name: "orders"},
			DAGRunID: "run-1",
			WorkerID: "worker-a",
		},
		Scope: scope,
	})
}

func (r *testRun) records() []browserhost.Record {
	r.t.Helper()
	records, err := browserhost.NewStore(filepath.Join(r.dataDir, browserhost.DataDirName)).List()
	require.NoError(r.t, err)
	return records
}

func eventNames(session *ir.AgentSession) []string {
	names := make([]string, 0, len(session.Events))
	for _, event := range session.Events {
		if event.Type == eventOperation {
			names = append(names, event.Name+":"+event.Status)
		}
	}
	return names
}

const checkoutSteps = `{
	"url": "https://shop.example.com/cart",
	"do": [
		{"act": "Click the checkout button"},
		{"expect": "The order is confirmed"},
		{"extract": {"instruction": "The order number", "schema": {"type": "object", "properties": {"order": {"type": "string"}}}}}
	]
}`

func TestRunPublishesExtractedOutputs(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(map[string]string{"The order number": `{"order":"A-100"}`}, "The order is confirmed"))
	execution := run.execute(checkoutSteps, nil)
	require.NoError(t, execution.err)

	assert.Equal(t, map[string]any{"order": "A-100"}, execution.exec.GetOutputs())
	assert.JSONEq(t, `{"order":"A-100"}`, execution.stdout.String())
	assert.Contains(t, execution.stderr.String(), `[1/3] act "Click the checkout button"`)

	session := execution.exec.GetAgentSession()
	assert.Equal(t, ir.AgentSessionSucceeded, session.State)
	assert.Equal(t, []string{"goto:completed", "act:completed", "expect:completed", "extract:completed"}, eventNames(session))
	assert.Positive(t, session.Usage.TotalTokens)

	assert.True(t, run.engine.closed)
	assert.Empty(t, run.records(), "a finished step keeps no browser session")
	assert.NoFileExists(t, filepath.Join(run.artifacts, "browser", "shop", "01-final.png"), "a successful step keeps no screenshot by default")
}

func TestWhenSkipsOperation(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	execution := run.execute(`{"do": [
		{"act": "Close the cookie banner", "when": "A cookie banner is visible"},
		{"act": "Click the checkout button"}
	]}`, nil)
	require.NoError(t, execution.err)

	assert.Equal(t, []string{"Click the checkout button"}, run.engine.actInstructions())
	assert.Equal(t, []string{"act:skipped", "act:completed"}, eventNames(execution.exec.GetAgentSession()))
}

func TestExpectFailureCapturesPage(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	execution := run.execute(`{"do": [{"expect": "The order is confirmed"}]}`, nil)

	require.ErrorContains(t, execution.err, "do[0] expect failed: expectation not met: the page does not show it")
	session := execution.exec.GetAgentSession()
	assert.Equal(t, ir.AgentSessionFailed, session.State)
	assert.Contains(t, session.LastError, "expectation not met")
	assert.True(t, run.engine.closed)
	assert.FileExists(t, filepath.Join(run.artifacts, "browser", "shop", "01-failure.png"))
}

// A browser a previous attempt left open that does not close keeps its
// record, so the reaper can retry, and the new attempt fails instead of
// replacing that record with its own browser.
func TestStaleBrowserThatWillNotCloseFailsTheStep(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	stale := browserhost.Record{
		ID:       browserhost.RecordID("run-1", "shop"),
		DAGRunID: "run-1",
		StepName: "shop",
		State:    browserhost.StateDetached,
		Deadline: time.Now().Add(time.Hour),
		CDPURL:   serveHungBrowser(t),
	}
	require.NoError(t, browserhost.NewStore(filepath.Join(run.dataDir, browserhost.DataDirName)).Save(stale))

	execution := run.execute(`{"do": [{"act": "Click the checkout button"}]}`, nil)

	require.ErrorContains(t, execution.err, "close the browser a previous attempt left open")
	assert.Empty(t, run.launcher.launches, "no browser starts")
	records := run.records()
	require.Len(t, records, 1)
	assert.Equal(t, stale.CDPURL, records[0].CDPURL)
}

// Dialogs the browser accepted appear in the timeline after the operation
// that opened them, masked like other page text.
func TestAcceptedDialogsAreReported(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.secrets = map[string]string{"TOKEN": "s3cr3t-token"}
	run.engine.actDialogs = []dialog{{Type: "confirm", Message: "Send s3cr3t-token?"}}
	execution := run.execute(`{"do": [{"act": "Click the checkout button"}, {"screenshot": "after"}]}`, nil)
	require.NoError(t, execution.err)

	session := execution.exec.GetAgentSession()
	assert.Equal(t, []string{"act:completed", "dialog:completed", "screenshot:completed"}, eventNames(session))
	assert.Contains(t, execution.stderr.String(), `[1/2] dialog "Send *******?" → accepted confirm`)
}

func TestSecretInInstructionIsRejected(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.secrets = map[string]string{"VENDOR_PASSWORD": "hunter2-secret"}
	execution := run.execute(`{"do": [{"act": "Type hunter2-secret into the password field"}]}`, nil)

	require.ErrorContains(t, execution.err, "do[0].act contains the value of secret VENDOR_PASSWORD")
	assert.Empty(t, run.launcher.launches, "no browser starts")
	assert.Zero(t, run.provider.callCount(), "no model call is made")
}

func TestVariablesReachTheBrowserOnly(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	execution := run.execute(`{
		"variables": {"password": "hunter2-secret"},
		"do": [{"act": "Type %password% into the password field"}]
	}`, nil)
	require.NoError(t, execution.err)

	require.Len(t, run.engine.acts, 1)
	assert.Equal(t, map[string]string{"password": "hunter2-secret"}, run.engine.acts[0].variables)
	for _, call := range run.provider.calls {
		for _, message := range call.Messages {
			assert.NotContains(t, message.Content, "hunter2-secret")
		}
	}
}

// A repeated run replays recorded actions instead of asking the model, and
// a replay that no longer works falls back to the model and is re-recorded.
func TestReplayCache(t *testing.T) {
	t.Parallel()

	const steps = `{"url": "https://shop.example.com/cart", "do": [{"act": "Click the checkout button"}]}`
	run := newTestRun(t, pageModel(nil))

	first := run.execute(steps, nil)
	require.NoError(t, first.err)
	modelCalls := run.provider.callCount()
	require.Positive(t, modelCalls)

	second := run.execute(steps, nil)
	require.NoError(t, second.err)
	assert.Equal(t, modelCalls, run.provider.callCount(), "a cache hit makes no model call")
	assert.Equal(t, []string{"goto:completed", "act:cache-hit"}, eventNames(second.exec.GetAgentSession()))

	run.engine.replayFails = true
	third := run.execute(steps, nil)
	require.NoError(t, third.err)
	assert.Greater(t, run.provider.callCount(), modelCalls)
	assert.Equal(t, []string{"goto:completed", "act:healed"}, eventNames(third.exec.GetAgentSession()))
}

func TestReplayCacheCanBeDisabled(t *testing.T) {
	t.Parallel()

	const steps = `{"cache": false, "do": [{"act": "Click the checkout button"}]}`
	run := newTestRun(t, pageModel(nil))
	require.NoError(t, run.execute(steps, nil).err)
	require.NoError(t, run.execute(steps, nil).err)

	assert.Empty(t, run.engine.replays)
	assert.Len(t, run.engine.acts, 2)
}

const loginSteps = `{
	"do": [
		{"act": "Sign in"},
		{"ask": {"prompt": "Enter the code sent to your phone", "as": "otp"}},
		{"act": "Type %otp% and submit"},
		{"extract": {"instruction": "The account name", "schema": {"type": "object", "properties": {"account": {"type": "string"}}}}}
	]
}`

// An ask operation parks the step in Waiting with the browser left open,
// and the answer resumes the same browser at the next operation.
func TestAskWaitsAndResumesSameBrowser(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(map[string]string{"The account name": `{"account":"acme"}`}))
	waiting := run.execute(loginSteps, nil)
	require.NoError(t, waiting.err)

	status, err := waiting.exec.DetermineNodeStatus()
	require.NoError(t, err)
	assert.Equal(t, ir.NodeWaiting, status)
	assert.True(t, run.engine.detached)
	assert.False(t, run.engine.closed)
	assert.Empty(t, waiting.stdout.String())

	session := waiting.exec.GetAgentSession()
	assert.Equal(t, ir.AgentSessionWaiting, session.State)
	require.Len(t, session.Interactions, 1)
	interaction := session.Interactions[0]
	assert.Equal(t, ir.AgentInteractionPending, interaction.Status)
	assert.Equal(t, "Enter the code sent to your phone", interaction.Questions[0].Question)

	records := run.records()
	require.Len(t, records, 1)
	assert.Equal(t, browserhost.StateDetached, records[0].State)

	session.Interactions[0].Status = ir.AgentInteractionAnswered
	session.Interactions[0].Answers = [][]string{{"123456"}}
	resumed := run.execute(loginSteps, session)
	require.NoError(t, resumed.err)

	assert.Equal(t, []browserHandle{run.engine.handle}, run.launcher.reattaches)
	assert.Len(t, run.launcher.launches, 1, "the resumed step reuses the browser")
	assert.Equal(t, []string{"Sign in", "Type %otp% and submit"}, run.engine.actInstructions())
	assert.Equal(t, "123456", run.engine.acts[1].variables["otp"])
	assert.Equal(t, map[string]any{"account": "acme"}, resumed.exec.GetOutputs())
	assert.True(t, resumed.exec.GetAgentSession().Interactions[0].Applied)
	assert.True(t, run.engine.closed)
	assert.Empty(t, run.records())
}

// A resumed step applies allowed_domains to the browser it reattaches, as the
// first launch did.
func TestAskResumeKeepsAllowedDomains(t *testing.T) {
	t.Parallel()

	const steps = `{
		"browser": {"allowed_domains": ["shop.example.com"]},
		"do": [
			{"ask": {"prompt": "Enter the code", "as": "otp"}},
			{"act": "Type %otp% and submit"}
		]
	}`
	run := newTestRun(t, pageModel(nil))
	waiting := run.execute(steps, nil)
	require.NoError(t, waiting.err)
	session := waiting.exec.GetAgentSession()
	require.Len(t, session.Interactions, 1)
	session.Interactions[0].Status = ir.AgentInteractionAnswered
	session.Interactions[0].Answers = [][]string{{"123456"}}
	require.NoError(t, run.execute(steps, session).err)

	require.Len(t, run.launcher.reattachOptions, 1)
	assert.Equal(t, []string{"shop.example.com"}, run.launcher.reattachOptions[0].AllowedDomains)
}

func TestAskUnsupportedOnWindows(t *testing.T) {
	t.Parallel()
	if goruntime.GOOS != "windows" {
		t.Skip("Windows only")
	}

	run := newTestRun(t, pageModel(nil))
	var with map[string]any
	require.NoError(t, json.Unmarshal([]byte(loginSteps), &with))
	created, err := newExecutor(t.Context(), ir.Step{
		Name:           "shop",
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: with},
		LLM:            &ir.LLMConfig{Provider: "openai", Model: "test-model"},
	})
	require.NoError(t, err)
	exec := created.(*browserExecutor)
	exec.launcher = run.launcher
	exec.SetStderr(io.Discard)

	require.ErrorContains(t, exec.Run(run.context()), "ask operations are not supported on Windows")
	assert.Empty(t, run.launcher.launches)
}

func TestAskRejectionFailsStep(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	waiting := run.execute(loginSteps, nil)
	require.NoError(t, waiting.err)

	session := waiting.exec.GetAgentSession()
	session.Interactions[0].Status = ir.AgentInteractionRejected
	rejected := run.execute(loginSteps, session)

	require.ErrorContains(t, rejected.err, "the input request was rejected")
	assert.Empty(t, run.launcher.reattaches)
	assert.Empty(t, run.records(), "the waiting browser is released")
}

func TestAskAnswerAfterBrowserExpired(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	waiting := run.execute(loginSteps, nil)
	require.NoError(t, waiting.err)
	for _, record := range run.records() {
		require.NoError(t, os.Remove(filepath.Join(run.dataDir, browserhost.DataDirName, "sessions", record.ID+".json")))
	}

	session := waiting.exec.GetAgentSession()
	session.Interactions[0].Status = ir.AgentInteractionAnswered
	session.Interactions[0].Answers = [][]string{{"123456"}}
	expired := run.execute(loginSteps, session)

	require.ErrorContains(t, expired.err, "no longer running; retry the step")
}

func TestModelBridgeFallsBackAndMasks(t *testing.T) {
	t.Parallel()

	failing := &scriptedProvider{answer: func(*llmpkg.ChatRequest) (string, error) {
		return "", assert.AnError
	}}
	working := &scriptedProvider{answer: func(*llmpkg.ChatRequest) (string, error) {
		return `{"ok":true}`, nil
	}}
	cfg := &ir.LLMConfig{Models: []ir.ModelEntry{
		{Provider: "openai", Name: "primary"},
		{Provider: "openai", Name: "backup"},
	}}
	factory := func(_ context.Context, cfg *ir.LLMConfig) (llmpkg.Provider, error) {
		if cfg.Model == "primary" {
			return failing, nil
		}
		return working, nil
	}
	ctx := runtime.WithEnv(t.Context(), runtime.Env{Scope: value.NewEnvScope(nil, false)})
	bridge, err := newModelBridge(ctx, cfg, newMasker(map[string]string{"TOKEN": "s3cr3t-token"}, nil), factory)
	require.NoError(t, err)

	resp, err := bridge.generate(ctx, generateRequest{
		System:   "system",
		Messages: []generateMessage{{Role: "user", Text: "tree contains s3cr3t-token"}},
		Schema:   json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`),
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(resp.JSON))
	assert.Equal(t, "backup", bridge.modelName())

	require.Len(t, working.calls, 1)
	request := working.calls[0]
	assert.Equal(t, toolChoiceRequired, request.ToolChoice)
	assert.NotContains(t, request.Tools[0].Function.Parameters, "$schema")
	assert.Equal(t, "tree contains *******", lastUserText(request))
}

func TestStructuredAnswerFromText(t *testing.T) {
	t.Parallel()

	answer, err := structuredAnswer(&llmpkg.ChatResponse{Content: "```json\n{\"ok\":true}\n```"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(answer))

	_, err = structuredAnswer(&llmpkg.ChatResponse{Content: "I cannot help"})
	assert.Error(t, err)
}

func TestCheckAllowedDomain(t *testing.T) {
	t.Parallel()

	allowed := []string{"Example.com.", "*.vendor.io"}
	for _, tc := range []struct {
		url     string
		allowed bool
	}{
		{"https://example.com/a", true},
		{"https://shop.example.com/a", false},
		{"https://portal.vendor.io/", true},
		{"https://a.b.vendor.io/", true},
		{"https://vendor.io/", false},
		{"https://evil.test/", false},
		{"about:blank", true},
		{"data:text/html,hi", true},
	} {
		err := checkAllowedDomain(tc.url, allowed)
		if tc.allowed {
			assert.NoError(t, err, tc.url)
		} else {
			assert.ErrorContains(t, err, "outside browser.allowed_domains", tc.url)
		}
	}
	assert.NoError(t, checkAllowedDomain("https://anything.test/", nil))
}

// An act can navigate away without a goto; the step fails once the page is
// outside the allowed domains.
func TestNavigationOutsideAllowedDomainsFailsStep(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.actNavigatesTo = "https://evil.test/collect"
	execution := run.execute(`{
		"url": "https://shop.example.com/cart",
		"browser": {"allowed_domains": ["shop.example.com"]},
		"do": [{"act": "Click the checkout button"}]
	}`, nil)

	require.ErrorContains(t, execution.err, "do[0] act failed: the page navigated away: evil.test is outside browser.allowed_domains")
	assert.True(t, run.engine.closed)
}

func TestDownloadsAreRecorded(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.downloads = []string{"invoice.pdf"}
	execution := run.execute(`{"do": [{"act": "Download the latest invoice"}]}`, nil)
	require.NoError(t, execution.err)

	var files []string
	for _, event := range execution.exec.GetAgentSession().Events {
		if event.Name == kindDownload {
			files = append(files, event.Files...)
		}
	}
	assert.Equal(t, []string{"browser/shop/downloads/invoice.pdf"}, files)
}

func TestUnfinishedDownloadFailsStep(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.downloadErr = errors.New("download of invoice.pdf did not finish within 2m0s")
	execution := run.execute(`{"do": [{"act": "Download the latest invoice"}]}`, nil)

	require.ErrorContains(t, execution.err, "do[0] download failed: download of invoice.pdf did not finish")
	assert.True(t, run.engine.closed)
}

// Only declared secrets and ask answers of at least four characters are
// masked, so short values such as a quantity leave page text and element
// IDs intact. Length counts characters, not bytes.
func TestMaskerHidesSecretsAndAnswers(t *testing.T) {
	t.Parallel()

	masker := newMasker(
		map[string]string{"TOKEN": "s3cr3t-token", "PIN": "12", "SHORT": "éé", "WORD": "パスワード"},
		map[string]string{"otp": "424242", "choice": "2"},
	)
	assert.Equal(t, "[0-23] ******* ******* qty 2 pin 12 éé *******",
		masker.MaskString("[0-23] s3cr3t-token 424242 qty 2 pin 12 éé パスワード"))
}

// When an ask is skipped, an act that needs its answer fails instead of
// typing the literal %name%.
func TestActNeedingSkippedAskFails(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	execution := run.execute(`{"do": [
		{"ask": {"prompt": "Enter the code", "as": "otp"}, "when": "The page asks for a code"},
		{"act": "Type %otp% into the code field"}
	]}`, nil)

	require.ErrorContains(t, execution.err, "do[1] act failed: the instruction uses %otp%, but the ask that sets it did not run")
	assert.Empty(t, run.engine.actInstructions())
}

// Fixed checks read the page directly, so they give the same answer on
// every run and make no model call.
func TestFixedConditions(t *testing.T) {
	t.Parallel()

	t.Run("expect passes", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t, pageModel(nil))
		run.engine.pageText = "Order confirmed: A-100"
		execution := run.execute(`{"do": [{"expect": {"text": "Order confirmed"}}]}`, nil)
		require.NoError(t, execution.err)
		assert.Zero(t, run.provider.callCount())
	})

	t.Run("expect times out", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t, pageModel(nil))
		execution := run.execute(`{"do": [{"expect": {"selector": "#done"}, "timeout": "300ms"}]}`, nil)
		require.ErrorContains(t, execution.err, `do[0] expect failed: expectation not met: "#done" is not visible`)
		assert.Zero(t, run.provider.callCount())
	})

	t.Run("when skips", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t, pageModel(nil))
		run.engine.url = "https://shop.example.com/cart"
		execution := run.execute(`{"do": [
			{"act": "Close the cookie banner", "when": {"selector": "#banner"}},
			{"act": "Open the account page", "when": {"url": "/cart"}}
		]}`, nil)
		require.NoError(t, execution.err)
		assert.Equal(t, []string{"Open the account page"}, run.engine.actInstructions())
	})
}

func TestFinalScreenshotPolicy(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	execution := run.execute(`{"browser": {"screenshots": "final"}, "do": [{"act": "Click the checkout button"}]}`, nil)
	require.NoError(t, execution.err)
	assert.FileExists(t, filepath.Join(run.artifacts, "browser", "shop", "01-final.png"))
}

// A retried step keeps the screenshots of earlier attempts and numbers its
// own after them.
func TestRetryKeepsEarlierScreenshots(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	require.Error(t, run.execute(`{"do": [{"screenshot": "cart"}, {"expect": "The order is confirmed"}]}`, nil).err)
	require.Error(t, run.execute(`{"do": [{"expect": "The order is confirmed"}]}`, nil).err)

	entries, err := os.ReadDir(filepath.Join(run.artifacts, "browser", "shop"))
	require.NoError(t, err)
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	assert.Equal(t, []string{"01-cart.png", "02-failure.png", "03-failure.png"}, names)
}

// A download gets the timeout of the act that can have started it, even when
// a later operation is shorter, and a step without acts does not wait.
func TestDownloadWaitTimeouts(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(map[string]string{"The title": `{"title":"Shop"}`}))
	execution := run.execute(`{"do": [
		{"act": "Download the yearly export", "timeout": "10m"},
		{"extract": {"instruction": "The title", "schema": {"type": "object", "properties": {"title": {"type": "string"}}}}, "timeout": "30s"}
	]}`, nil)
	require.NoError(t, execution.err)
	assert.Equal(t, []time.Duration{10 * time.Minute, 10 * time.Minute, 10 * time.Minute}, run.engine.downloadWaits)

	extractOnly := newTestRun(t, pageModel(map[string]string{"The title": `{"title":"Shop"}`}))
	require.NoError(t, extractOnly.execute(`{"url": "https://shop.example.com", "do": [
		{"extract": {"instruction": "The title", "schema": {"type": "object", "properties": {"title": {"type": "string"}}}}}
	]}`, nil).err)
	assert.Empty(t, extractOnly.engine.downloadWaits)
}

// A fixed when with a within window keeps reading a page that is still
// loading instead of skipping the operation on the first look.
func TestFixedWhenWaitsWithin(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	go func() {
		time.Sleep(300 * time.Millisecond)
		run.engine.setPageText("Enter the verification code")
	}()
	execution := run.execute(`{"do": [
		{"act": "Open the code form", "when": {"text": "verification code", "within": "5s"}}
	]}`, nil)
	require.NoError(t, execution.err)
	assert.Equal(t, []string{"Open the code form"}, run.engine.actInstructions())
}
