// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ordersExtract = `{"extract": {"instruction": "The order numbers", "schema": {"type": "object", "properties": {"orders": {"type": "array", "items": {"type": "string"}}}}}}`

// isActRequest reports whether a model request picks an element for an act.
func isActRequest(req *llmpkg.ChatRequest) bool {
	properties, _ := req.Tools[0].Function.Parameters["properties"].(map[string]any)
	_, ok := properties["elementId"]
	return ok
}

func actRequests(provider *scriptedProvider) int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	count := 0
	for _, call := range provider.calls {
		if isActRequest(call) {
			count++
		}
	}
	return count
}

// A session's operations become a browser step: what succeeded and what
// its when skipped, in order, with every variable as a reference the DAG
// fills in. Run against the pages the session met, the step replays every
// act the session recorded without asking the model.
func TestExportedStepReplaysTheSession(t *testing.T) {
	const env = "DAGU_EXPORT_TEST_PASSWORD"
	t.Setenv(env, "s3cret-value")
	model := pageModel(map[string]string{"The order numbers": `{"orders":["PO-1","PO-2"]}`})

	ts := newTestSessions(t, model)
	ts.engine.pageText = "Orders"
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", Profile: "portal", LLM: testModel})
	for _, input := range []string{
		`{"act": "Type %user% into the Login ID field", "variables": {"user": "alice"}}`,
		`{"act": "Type %password% into the Password field", "variables": {"password": {"env": "` + env + `"}}}`,
		`{"act": "Click Sign in"}`,
		`{"expect": {"text": "Invoices"}, "timeout": "10ms"}`,
		`{"act": "Accept the cookies", "when": {"selector": "#cookies"}}`,
		`{"screenshot": "after-sign-in"}`,
		`{"expect": {"text": "Orders"}}`,
		ordersExtract,
	} {
		_, _ = ts.do(opened.ID, input)
	}

	exported, err := ts.sessions.Export(ts.context(), ExportRequest{ID: opened.ID, DAG: "orders", Step: "shop"})
	require.NoError(t, err)

	step, err := json.Marshal(exported.Step)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"id": "shop",
		"action": "browser.run",
		"with": {
			"url": "https://portal.example.com/login",
			"browser": {"profile": "portal"},
			"variables": {"password": "${`+env+`}", "user": "${user}"},
			"do": [
				{"act": "Type %user% into the Login ID field"},
				{"act": "Type %password% into the Password field"},
				{"act": "Click Sign in"},
				{"act": "Accept the cookies", "when": {"selector": "#cookies"}},
				{"expect": {"text": "Orders"}},
				`+ordersExtract+`
			]
		}
	}`, string(step))
	assert.Equal(t, []ExportedOperation{
		{SessionIndex: 0, Index: 0, Kind: opAct, Recorded: true},
		{SessionIndex: 1, Index: 1, Kind: opAct, Recorded: true},
		{SessionIndex: 2, Index: 2, Kind: opAct, Recorded: true},
		{SessionIndex: 4, Index: 3, Kind: opAct},
		{SessionIndex: 6, Index: 4, Kind: opExpect},
		{SessionIndex: 7, Index: 5, Kind: opExtract},
	}, exported.Ops)
	assert.Equal(t, 3, exported.Recordings)
	assert.Equal(t, []ExportedVariable{
		{Name: "password", Value: "${" + env + "}", Note: "declare " + env + " under the DAG's secrets, such as {name: " + env + ", provider: env, key: " + env + "}"},
		{Name: "user", Value: "${user}", Note: "give the DAG a parameter user holding the value the session was given"},
	}, exported.Variables)
	assert.Equal(t, testModel, exported.LLM)
	assert.Empty(t, exported.Warnings)
	assert.FileExists(t, exported.CacheFile)
	cache, err := os.ReadFile(exported.CacheFile)
	require.NoError(t, err)
	assert.NotContains(t, string(cache), "s3cret-value")

	// The DAG fills in the references; the session's profile is in use, so
	// the step runs on its own profile.
	ts.clock = ts.clock.Add(time.Second)
	require.NoError(t, ts.sessions.Close(ts.context(), CloseRequest{ID: opened.ID}))
	withJSON := strings.NewReplacer("${"+env+"}", "s3cret-value", "${user}", "alice").Replace(string(mustJSON(t, exported.Step.With)))
	run := newTestRun(t, model)
	run.dataDir = ts.dataDir
	run.engine.pageText = "Orders"
	execution := run.execute(withJSON, nil)
	require.NoError(t, execution.err, execution.stderr.String())

	assert.Zero(t, actRequests(run.provider), "every act replays the session's recording")
	assert.Len(t, run.engine.replays, 3)
	assert.Equal(t, map[string]any{"orders": []any{"PO-1", "PO-2"}}, execution.exec.GetOutputs())
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}

// An export names what may keep the first run from replaying: an act with
// no recording, and a page the session reached outside the exported
// operations.
func TestExportWarnsWhereTheFirstRunMayAsk(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", LLM: testModel})
	for _, input := range []string{
		`{"act": {"instruction": "Pick the newest invoice", "cache": false}}`,
		`{"goto": "https://portal.example.com/orders"}`,
		`{"act": "Click Next"}`,
	} {
		_, err := ts.do(opened.ID, input)
		require.NoError(t, err, input)
	}

	exported, err := ts.sessions.Export(ts.context(), ExportRequest{ID: opened.ID, DAG: "orders", Step: "shop", Skip: []int{1}, DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, []string{
		`operation 0 (act "Pick the newest invoice") has no recording, so every run asks the model for it`,
		"the page changed from https://portal.example.com/login to https://portal.example.com/orders before operation 2 outside the exported operations, " +
			"so the first run may ask the model for it; an expect: {url: ...} before it makes the step wait for that page",
	}, exported.Warnings)
	assert.Empty(t, exported.CacheFile, "a dry run writes nothing")
	_, err = os.Stat(filepath.Join(ts.dataDir, browserhost.DataDirName, "cache"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// An export a step could not run is refused, naming the session's
// operations, and leaving one out makes it valid.
func TestExportRefusesAStepThatCannotRun(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(map[string]string{"The order numbers": `{"orders":["PO-1"]}`}))
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/orders", LLM: testModel})
	for range 2 {
		_, err := ts.do(opened.ID, ordersExtract)
		require.NoError(t, err)
	}

	_, err := ts.sessions.Export(ts.context(), ExportRequest{ID: opened.ID, DAG: "orders", Step: "shop", DryRun: true})
	assert.Equal(t, CodeExportInvalid, sessionCode(err))
	assert.ErrorContains(t, err, `operation 1: output "orders" is already extracted by operation 0; leave an operation out with skip`)

	_, err = ts.sessions.Export(ts.context(), ExportRequest{ID: opened.ID, DAG: "orders", Step: "shop", Skip: []int{1}, DryRun: true})
	require.NoError(t, err)

	for _, req := range []ExportRequest{
		{ID: opened.ID, DAG: "", Step: "shop"},
		{ID: opened.ID, DAG: "bad name", Step: "shop"},
		{ID: opened.ID, DAG: "orders", Step: "fetch-orders"},
		{ID: opened.ID, DAG: "orders", Step: "env"},
		{ID: opened.ID, DAG: "orders", Step: "shop", Skip: []int{7}},
	} {
		_, err := ts.sessions.Export(ts.context(), req)
		assert.Equal(t, CodeInvalidInput, sessionCode(err), "%+v", req)
	}
	_, err = ts.sessions.Export(ts.context(), ExportRequest{ID: "ab2cd3ef4g", DAG: "orders", Step: "shop"})
	assert.Equal(t, CodeSessionNotFound, sessionCode(err))
}

// An act whose actions never used a variable its instruction names is
// exported with the value's digest, so the step replays it for that value.
// One a session recorded before acts noted the digest is exported without a
// recording, since it would fit no value.
func TestExportKeepsWhatAnActAssumed(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", LLM: testModel})
	_, err := ts.do(opened.ID, `{"act": "Sign in as %user%", "variables": {"user": "alice"}}`)
	require.NoError(t, err)
	exported, err := ts.sessions.Export(ts.context(), ExportRequest{ID: opened.ID, DAG: "orders", Step: "shop", DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, 1, exported.Recordings)
	assert.Empty(t, exported.Warnings)

	record, state := ts.record(opened.ID), ts.state(opened.ID)
	for i := range state.Ops {
		state.Ops[i].Assumed = nil
	}
	record.Interactive, err = state.encode()
	require.NoError(t, err)
	require.NoError(t, ts.store().Save(record))
	exported, err = ts.sessions.Export(ts.context(), ExportRequest{ID: opened.ID, DAG: "orders", Step: "shop", DryRun: true})
	require.NoError(t, err)
	assert.Zero(t, exported.Recordings)
	assert.False(t, exported.Ops[0].Recorded)
	assert.Equal(t, []string{`operation 0 (act "Sign in as %user%") never uses %user%, and the session did not note its value, so its recording is not exported and the first run may ask the model for it`}, exported.Warnings)
}

// A session whose browser closed can still be exported from its history.
func TestExportAfterTheSessionEnded(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", IdleTimeout: time.Minute, LLM: testModel})
	_, err := ts.do(opened.ID, `{"act": "Click Sign in"}`)
	require.NoError(t, err)
	ts.clock = ts.clock.Add(time.Hour)
	_, err = ts.do(opened.ID, `{"act": "Click Orders"}`)
	require.Equal(t, CodeSessionEnded, sessionCode(err))

	exported, err := ts.sessions.Export(ts.context(), ExportRequest{ID: opened.ID, DAG: "orders", Step: "shop"})
	require.NoError(t, err)
	assert.Equal(t, 1, exported.Recordings)
}

// An exported step reads as YAML in the order a person writes a step.
func TestExportedStepAsYAML(t *testing.T) {
	t.Parallel()

	step := ExportedStep{ID: "shop", Action: "browser.run", With: ExportedWith{
		URL:       "https://portal.example.com/login",
		Variables: map[string]string{"user": "${user}"},
		Do:        []json.RawMessage{json.RawMessage(`{"act":"Type %user% into the Login ID field"}`), json.RawMessage(`{"expect":{"text":"Orders"}}`)},
	}}
	text, err := step.YAML()
	require.NoError(t, err)
	assert.Equal(t, `- id: shop
  action: browser.run
  with:
    url: https://portal.example.com/login
    variables:
      user: ${user}
    do:
    - act: Type %user% into the Login ID field
    - expect:
        text: Orders
`, string(text))
}

// An exported upload replays the element the session uploaded into, and the
// export says the files are the ones the session was given.
func TestExportedStepReplaysUploads(t *testing.T) {
	t.Parallel()

	files := writeFiles(t, t.TempDir(), "invoice.pdf")
	ts := newTestSessions(t, pageModel(nil))
	ts.engine.snapshot = uploadPage("Upload receipt")
	opened := ts.open(SessionOptions{URL: "https://books.example.com/receipts", LLM: testModel})
	input, err := json.Marshal(map[string]any{"upload": map[string]any{"into": "0-5", "files": files[0]}})
	require.NoError(t, err)
	_, err = ts.do(opened.ID, string(input))
	require.NoError(t, err)

	exported, err := ts.sessions.Export(ts.context(), ExportRequest{ID: opened.ID, DAG: "orders", Step: "shop"})
	require.NoError(t, err)
	assert.Equal(t, []ExportedOperation{{SessionIndex: 0, Index: 0, Kind: opUpload, Recorded: true}}, exported.Ops)
	assert.Equal(t, 1, exported.Recordings)
	assert.Equal(t, []string{"operation 0 uploads " + files[0] + ", the files the session was given; give the step the files each run uploads, such as an earlier step's output"}, exported.Warnings)

	require.NoError(t, ts.sessions.Close(ts.context(), CloseRequest{ID: opened.ID}))
	run := newTestRun(t, pageModel(nil))
	run.dataDir = ts.dataDir
	run.engine.snapshot = uploadPage("Upload receipt")
	execution := run.execute(string(mustJSON(t, exported.Step.With)), nil)
	require.NoError(t, execution.err, execution.stderr.String())
	assert.Zero(t, run.provider.callCount(), "the upload replays the session's recording")
	assert.Equal(t, []string{"goto:completed", "upload:cache-hit"}, eventNames(execution.exec.GetAgentSession()))
}
