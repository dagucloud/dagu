// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	signInStep = `{
	"url": "https://shop.example.com/login",
	"browser": {"share": "shop"},
	"do": [{"act": "Sign in"}]
}`
	// ordersStep has no url, so it works on the page the step before left.
	ordersStep = `{
	"browser": {"share": "shop"},
	"do": [{"expect": {"url": "/login"}}, {"act": "Open the orders"}]
}`
)

// shareRun returns a test run whose steps run as the steps of one DAG run,
// and the function that run calls when it ends.
func shareRun(t *testing.T) (*testRun, func() error) {
	t.Helper()
	run := newTestRun(t, pageModel(nil))
	ctx, closeBrowsers := WithSharedBrowsers(t.Context())
	run.base = ctx
	return run, closeBrowsers
}

// The step after the one that started a shared browser continues in it, on
// the page it left, and the browser closes when the run ends.
func TestSharedBrowserCarriesOverToNextStep(t *testing.T) {
	t.Parallel()

	run, closeBrowsers := shareRun(t)
	first := run.executeStep("sign_in", signInStep, nil)
	require.NoError(t, first.err)
	assert.True(t, run.engine.detached, "the step leaves the browser open")
	assert.False(t, run.engine.closed)
	records := run.records()
	require.Len(t, records, 1)
	assert.Equal(t, browserhost.SharedRecordID("run-1", "shop"), records[0].ID)
	assert.Equal(t, browserhost.StateRunning, records[0].State)

	second := run.executeStep("orders", ordersStep, nil)
	require.NoError(t, second.err)
	assert.Len(t, run.launcher.launches, 1, "the next step starts no browser")
	assert.Equal(t, []browserHandle{run.engine.handle}, run.launcher.reattaches)
	assert.Equal(t, []string{"Sign in", "Open the orders"}, run.engine.actInstructions())
	assert.False(t, run.engine.closed)
	require.Len(t, run.records(), 1)

	require.NoError(t, closeBrowsers())
	assert.Empty(t, run.records(), "the run's end closes the browser")
}

// A step that fails leaves the shared browser open, so the step after it
// still finds the sign-in.
func TestSharedBrowserOutlivesFailedStep(t *testing.T) {
	t.Parallel()

	run, closeBrowsers := shareRun(t)
	t.Cleanup(func() { _ = closeBrowsers() })
	require.NoError(t, run.executeStep("sign_in", signInStep, nil).err)
	failed := run.executeStep("check", `{"browser": {"share": "shop"}, "do": [{"expect": {"url": "/orders"}, "timeout": "100ms"}]}`, nil)
	require.ErrorContains(t, failed.err, "expectation not met")

	require.NoError(t, run.executeStep("orders", ordersStep, nil).err)
	assert.Len(t, run.launcher.launches, 1)
	assert.Len(t, run.launcher.reattaches, 2)
}

// A shared browser that no longer answers is replaced by a new one.
func TestSharedBrowserThatStoppedAnsweringIsReplaced(t *testing.T) {
	t.Parallel()

	run, closeBrowsers := shareRun(t)
	t.Cleanup(func() { _ = closeBrowsers() })
	require.NoError(t, run.executeStep("sign_in", signInStep, nil).err)
	run.launcher.reattachErr = errors.New("connection refused")

	next := run.executeStep("orders", `{"url": "https://shop.example.com/orders", "browser": {"share": "shop"}, "do": [{"act": "Open the first order"}]}`, nil)
	require.NoError(t, next.err)
	assert.Len(t, run.launcher.launches, 2)
	assert.Contains(t, next.stderr.String(), `the browser shared as "shop" no longer answers, so a new one starts`)
}

// Steps sharing a browser start it alike; a step whose browser would start
// otherwise fails and names the step that started it.
func TestSharedBrowserSettingsMustMatch(t *testing.T) {
	t.Parallel()

	run, closeBrowsers := shareRun(t)
	t.Cleanup(func() { _ = closeBrowsers() })
	require.NoError(t, run.executeStep("sign_in", signInStep, nil).err)

	headed := run.executeStep("orders", `{"browser": {"share": "shop", "headless": false}, "do": [{"act": "Open the orders"}]}`, nil)
	require.ErrorContains(t, headed.err, `browser.share "shop": step "sign_in" started the shared browser with other browser settings`)
	assert.Empty(t, run.launcher.reattaches)

	require.NoError(t, run.executeStep("orders", ordersStep, nil).err, "the browser stays for the steps that match")
}

// A shared browser keeps its profile until the run ends, so a step of the
// run that names the profile without sharing the browser fails at once
// instead of waiting for its own run.
func TestSharedBrowserHoldsProfileUntilRunEnds(t *testing.T) {
	t.Parallel()

	run, closeBrowsers := shareRun(t)
	require.NoError(t, run.executeStep("sign_in", `{"url": "https://shop.example.com/login", "browser": {"share": "shop", "profile": "shop"}, "do": [{"act": "Sign in"}]}`, nil).err)

	alone := run.executeStep("report", `{"url": "https://shop.example.com/report", "browser": {"profile": "shop"}, "do": [{"act": "Open the report"}]}`, nil)
	require.ErrorContains(t, alone.err, `browser profile "shop" is held by the browser shared as "shop" in this run, which keeps it until the run ends; give this step browser.share: shop`)

	require.NoError(t, closeBrowsers())
	browserDir := filepath.Join(run.dataDir, browserhost.DataDirName)
	lease, err := acquireProfile(t.Context(), browserDir, "shop", "", false)
	require.NoError(t, err, "the run's end releases the profile")
	lease.release()
}

// A step sharing a browser that waits for an answer keeps the browser under
// its own record, where its resumed execution finds it, and once it ends
// leaves the browser to the next step.
func TestSharedBrowserWaitsForAnAnswer(t *testing.T) {
	t.Parallel()

	run, closeBrowsers := shareRun(t)
	t.Cleanup(func() { _ = closeBrowsers() })
	login := `{"url": "https://shop.example.com/login", "browser": {"share": "shop"}, "do": [
		{"ask": {"prompt": "Enter the code sent to your phone", "as": "otp"}},
		{"act": "Type %otp% and submit"}
	]}`
	waiting := run.executeStep("sign_in", login, nil)
	require.NoError(t, waiting.err)
	records := run.records()
	require.Len(t, records, 1)
	assert.Equal(t, browserhost.RecordID("run-1", "", "sign_in"), records[0].ID)
	assert.Equal(t, browserhost.StateDetached, records[0].State)

	session := waiting.exec.GetAgentSession()
	session.Interactions[0].Status = ir.AgentInteractionAnswered
	session.Interactions[0].Answers = [][]string{{"123456"}}
	require.NoError(t, run.executeStep("sign_in", login, session).err)
	records = run.records()
	require.Len(t, records, 1)
	assert.Equal(t, browserhost.SharedRecordID("run-1", "shop"), records[0].ID)

	require.NoError(t, run.executeStep("orders", ordersStep, nil).err)
	assert.Len(t, run.launcher.launches, 1)
	assert.Len(t, run.launcher.reattaches, 2, "the answer and the next step each reattach")
	assert.False(t, run.engine.closed)
}

// Steps sharing a browser take turns: one waits while another uses it.
func TestSharedBrowserTakesTurns(t *testing.T) {
	t.Parallel()

	shared := &sharedBrowsers{browsers: map[string]*sharedBrowser{}}
	first, err := shared.take(t.Context(), "shop", "sign_in", func(string) { t.Error("the first step waits") })
	require.NoError(t, err)

	waitedFor := make(chan string, 1)
	taken := make(chan error, 1)
	go func() {
		second, err := shared.take(t.Context(), "shop", "orders", func(holder string) { waitedFor <- holder })
		if err == nil {
			second.release()
		}
		taken <- err
	}()
	assert.Equal(t, "sign_in", <-waitedFor)
	select {
	case <-taken:
		t.Fatal("the second step took the browser while the first used it")
	case <-time.After(50 * time.Millisecond):
	}
	first.release()
	require.NoError(t, <-taken)

	ctx, cancel := context.WithCancel(t.Context())
	held, err := shared.take(ctx, "shop", "sign_in", nil)
	require.NoError(t, err)
	defer held.release()
	cancel()
	_, err = shared.take(ctx, "shop", "orders", func(string) {})
	require.ErrorIs(t, err, context.Canceled)
}
