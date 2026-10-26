// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"image"
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

	// Each operation event says how it ran and what it cost.
	events := operationEvents(result)
	assert.Empty(t, events[0].Via, "a launch decides nothing")
	assert.Equal(t, agentstep.ViaModel, events[1].Via)
	assert.Equal(t, int64(24), events[1].Tokens)
	assert.GreaterOrEqual(t, events[1].DurationMs, int64(0))
}

// A recording from before recordings carried their position gains it on
// its next full replay, so it can then be forgotten on its own.
func TestReplayUpgradesRecordingPosition(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"wait": "1ms"}, {"act": "Open the report"}]}`
	run := newTestRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(30, 40)), done("Opened")}}}
	require.NoError(t, run.execute(steps, nil).err)

	cache := openReplayCache(filepath.Join(run.dataDir, computerhost.DataDirName), "invoices", "post")
	key, entry, ok := cache.Find(func(string, recording) bool { return true })
	require.True(t, ok)
	assert.Equal(t, 1, entry.Op)
	entry.Op = 0
	cache.Stage(key, entry)
	require.NoError(t, cache.Commit(t.Context()))

	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"wait:completed", "act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()))
	_, upgraded, ok := openReplayCache(filepath.Join(run.dataDir, computerhost.DataDirName), "invoices", "post").Find(func(string, recording) bool { return true })
	require.True(t, ok)
	assert.Equal(t, 1, upgraded.Op)
}

// A model that reports the task done in the same turn as its actions is
// shown the screen those actions produced and asked again, so a task is
// never finished on a claim the model could not have checked.
func TestDoneWithActionsIsVerified(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	session := &scriptedSession{turns: []*computeruse.Turn{
		{Actions: []computeruse.Action{clickAt(10, 20)}, Done: &computeruse.Done{Success: true, Summary: "Closed it"}, Usage: llmpkg.Usage{PromptTokens: 10, CompletionTokens: 2}},
		done("Closed it"),
	}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"do": [{"act": "Close the window"}]}`, nil)
	require.NoError(t, execution.err)

	assert.Equal(t, []string{"move 10,20", "left down #1"}, run.backend.inputs(), "the actions ran before the question")
	require.Len(t, session.observations, 2)
	assert.Equal(t, verifyNote, session.observations[1].Note)
	assert.Equal(t, []computeruse.Result{{CallID: "c"}}, session.observations[1].Results)
	events := operationEvents(execution.exec.GetAgentSession())
	assert.Equal(t, 1, events[0].Position)
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

// extractStep reads the invoice amount into a number output.
const extractStep = `{"do": [{"extract": {"instruction": "The invoice amount", "schema": {"type": "object", "properties": {"amount": {"type": "number"}}}}}]}`

// A successful extract binds each field to the element it read from, so a
// later run under never reads it with no model and reports via element.
func TestExtractBindsAndReadsWithoutAModel(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	// amountBox is the third element of the window, value 12,000.
	run.vision.extract = `{"amount": {"value": 12000, "element": "e3"}}`
	first := run.execute(extractStep, nil)
	require.NoError(t, first.err)
	assert.EqualValues(t, 12000, first.exec.GetOutputs()["amount"])
	assert.Equal(t, agentstep.ViaModel, operationEvents(first.exec.GetAgentSession())[0].Via)
	entry := lookupRecording(t, run, "The invoice amount")
	require.Len(t, entry.Fields, 1)
	assert.Equal(t, "amountBox", entry.Fields[0].Element.Selector.ID)
	assert.Equal(t, "number", entry.Fields[0].Kind)

	run.vision.extract = ""
	run.vision.requests = nil
	replayed := run.execute(`{"ai": "never", "do": [{"extract": {"instruction": "The invoice amount", "schema": {"type": "object", "properties": {"amount": {"type": "number"}}}}}]}`, nil)
	require.NoError(t, replayed.err)
	assert.EqualValues(t, 12000, replayed.exec.GetOutputs()["amount"])
	events := operationEvents(replayed.exec.GetAgentSession())
	require.Len(t, events, 1)
	assert.Equal(t, agentstep.StatusCacheHit, events[0].Status)
	assert.Equal(t, agentstep.ViaElement, events[0].Via)
	assert.Empty(t, run.vision.requests, "no model request")
}

// A value the model read from an element binds; one it read from the
// screen, naming no element, does not, and cannot run under never.
func TestExtractDoesNotBindAScreenReadField(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.vision.extract = `{"amount": {"value": 999, "element": ""}}`
	require.NoError(t, run.execute(extractStep, nil).err)

	// Nothing bound, so a later never run has no binding to read.
	failed := run.execute(`{"ai": "never", "do": [{"extract": {"instruction": "The invoice amount", "schema": {"type": "object", "properties": {"amount": {"type": "number"}}}}}]}`, nil)
	require.ErrorContains(t, failed.err, `the extract cannot run without AI: there is no binding`)
	assert.Equal(t, agentstep.ViaScreen, operationEvents(failed.exec.GetAgentSession())[0].Via)
}

// A value two elements share does not bind, since the binding would be
// ambiguous, so a never run cannot read it.
func TestExtractDoesNotBindAnAmbiguousValue(t *testing.T) {
	t.Parallel()

	const label = `{"do": [{"extract": {"instruction": "The button label", "schema": {"type": "object", "properties": {"label": {"type": "string"}}}}}]}`
	run := elementRun(t)
	// Both save buttons carry the same name 保存.
	run.vision.extract = `{"label": {"value": "保存", "element": "e4"}}`
	require.NoError(t, run.execute(label, nil).err)

	failed := run.execute(`{"ai": "never", "do": [{"extract": {"instruction": "The button label", "schema": {"type": "object", "properties": {"label": {"type": "string"}}}}}]}`, nil)
	require.ErrorContains(t, failed.err, `the extract cannot run without AI: there is no binding`)
}

// Under on_miss, a binding whose element is gone is reread by the model.
func TestExtractHealsAMissingBinding(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.vision.extract = `{"amount": {"value": 12000, "element": "e3"}}`
	require.NoError(t, run.execute(extractStep, nil).err)

	// The field is gone, so the binding no longer resolves; on_miss asks the
	// model again, which reads it from the screen this time.
	run.elements.elements = withoutElement(expenseElements(), "amountBox")
	run.vision.extract = `{"amount": {"value": 8500, "element": ""}}`
	run.vision.requests = nil
	healed := run.execute(extractStep, nil)
	require.NoError(t, healed.err)
	assert.EqualValues(t, 8500, healed.exec.GetOutputs()["amount"])
	assert.Equal(t, agentstep.ViaModel, operationEvents(healed.exec.GetAgentSession())[0].Via)
	assert.Len(t, run.vision.requests, 1, "the model reread the extract")
}

// A boolean field binds to a checkbox and reads its state without a model.
func TestExtractBindsACheckbox(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"extract": {"instruction": "Whether the box is checked", "schema": {"type": "object", "properties": {"agreed": {"type": "boolean"}}}}}]}`
	run := elementRun(t)
	run.elements.elements = append(expenseElements(), desktop.Element{Role: desktop.RoleCheckbox, Name: "同意", ID: "agreeBox", Value: "true", Bounds: image.Rect(120, 250, 300, 274), Path: []desktop.PathStep{{Role: desktop.RoleCheckbox, Name: "同意", Index: 0}}})
	run.vision.extract = `{"agreed": {"value": true, "element": "e7"}}`
	first := run.execute(steps, nil)
	require.NoError(t, first.err)
	assert.Equal(t, true, first.exec.GetOutputs()["agreed"])

	run.vision.extract = ""
	run.vision.requests = nil
	replayed := run.execute(`{"ai": "never", "do": [{"extract": {"instruction": "Whether the box is checked", "schema": {"type": "object", "properties": {"agreed": {"type": "boolean"}}}}}]}`, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, true, replayed.exec.GetOutputs()["agreed"])
	assert.Equal(t, agentstep.ViaElement, operationEvents(replayed.exec.GetAgentSession())[0].Via)
	assert.Empty(t, run.vision.requests, "no model request")
}

// extractTable reads invoice lines into an array of objects.
const extractTable = `{"do": [{"extract": {"instruction": "The invoice lines", "schema": {"type": "object", "properties": {"lines": {"type": "array", "items": {"type": "object", "properties": {"item": {"type": "string"}, "amount": {"type": "number"}}}}}}}}]}`

// extractTableNever is extractTable under never.
const extractTableNever = `{"ai": "never", "do": [{"extract": {"instruction": "The invoice lines", "schema": {"type": "object", "properties": {"lines": {"type": "array", "items": {"type": "object", "properties": {"item": {"type": "string"}, "amount": {"type": "number"}}}}}}}}]}`

func withTable(run *testRun) {
	run.elements.elements = append(expenseElements(), desktop.Element{Role: desktop.RoleList, Name: "明細", ID: "lines", Bounds: image.Rect(120, 250, 660, 440), Path: []desktop.PathStep{{Role: desktop.RoleList, Name: "明細", Index: 0}}})
	run.elements.tableHeaders = []string{"品目", "金額"}
}

// A table field binds to its container and reads every row by header,
// converting each cell, with no model on a later run.
func TestExtractBindsATable(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	withTable(run)
	run.elements.tableRows = [][]string{{"りんご", "1,200"}, {"みかん", "3,400"}}
	run.vision.extract = `{"lines": {"container": {"role": "list", "name": "明細"}, "columns": {"item": "品目", "amount": "金額"}, "rows": [{"item": "りんご", "amount": 1200}, {"item": "みかん", "amount": 3400}]}}`
	first := run.execute(extractTable, nil)
	require.NoError(t, first.err)
	wantRows := []any{
		map[string]any{"item": "りんご", "amount": int64(1200)},
		map[string]any{"item": "みかん", "amount": int64(3400)},
	}
	assert.Equal(t, wantRows, first.exec.GetOutputs()["lines"])
	entry := lookupRecording(t, run, "The invoice lines")
	require.Len(t, entry.Fields, 1)
	require.NotNil(t, entry.Fields[0].Table)
	assert.Equal(t, "明細", entry.Fields[0].Table.Container.Name)

	run.vision.extract = ""
	run.vision.requests = nil
	replayed := run.execute(extractTableNever, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, wantRows, replayed.exec.GetOutputs()["lines"])
	assert.Equal(t, agentstep.ViaElement, operationEvents(replayed.exec.GetAgentSession())[0].Via)
	assert.Empty(t, run.vision.requests, "no model request")
}

// A table whose read does not reproduce the model's rows does not bind.
func TestExtractDoesNotBindAMismatchedTable(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	withTable(run)
	run.elements.tableRows = [][]string{{"りんご", "1,200"}}
	run.vision.extract = `{"lines": {"container": {"role": "list", "name": "明細"}, "columns": {"item": "品目", "amount": "金額"}, "rows": [{"item": "ぶどう", "amount": 9999}]}}`
	require.NoError(t, run.execute(extractTable, nil).err)

	failed := run.execute(extractTableNever, nil)
	require.ErrorContains(t, failed.err, "there is no binding")
}

// A moved-away column header misses on a later read.
func TestExtractTableMissesOnAMovedHeader(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	withTable(run)
	run.elements.tableRows = [][]string{{"りんご", "1,200"}}
	run.vision.extract = `{"lines": {"container": {"role": "list", "name": "明細"}, "columns": {"item": "品目", "amount": "金額"}, "rows": [{"item": "りんご", "amount": 1200}]}}`
	require.NoError(t, run.execute(extractTable, nil).err)

	run.elements.tableHeaders = []string{"品目", "単価"}
	failed := run.execute(extractTableNever, nil)
	require.ErrorContains(t, failed.err, "no column is headed")
}

// A bound table read while no window is in front misses, so a step looks
// again, rather than failing as an error.
func TestExtractTableMissesWithNoWindowInFront(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	withTable(run)
	run.elements.tableRows = [][]string{{"りんご", "1,200"}}
	run.vision.extract = `{"lines": {"container": {"role": "list", "name": "明細"}, "columns": {"item": "品目", "amount": "金額"}, "rows": [{"item": "りんご", "amount": 1200}]}}`
	require.NoError(t, run.execute(extractTable, nil).err)

	run.elements.tableErr = desktop.ErrNoWindow
	failed := run.execute(extractTableNever, nil)
	require.ErrorContains(t, failed.err, `table "lines" cannot be read: no window is in front`)
}

// A model that answers the plain value, ignoring the reading schema's
// {value, element} wrapper, still publishes; the field just does not bind.
func TestExtractToleratesAPlainAnswer(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.vision.extract = `{"amount": 42}`
	published := run.execute(extractStep, nil)
	require.NoError(t, published.err)
	assert.EqualValues(t, 42, published.exec.GetOutputs()["amount"])

	failed := run.execute(`{"ai": "never", "do": [{"extract": {"instruction": "The invoice amount", "schema": {"type": "object", "properties": {"amount": {"type": "number"}}}}}]}`, nil)
	require.ErrorContains(t, failed.err, "there is no binding", "a plain answer binds nothing")
}

// A full-width value reads as the number a person sees.
func TestExtractConvertsFullWidthValue(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.elements.elements[2].Value = "１２，３４５"
	run.vision.extract = `{"amount": {"value": 12345, "element": "e3"}}`
	bound := run.execute(extractStep, nil)
	require.NoError(t, bound.err)
	assert.EqualValues(t, 12345, bound.exec.GetOutputs()["amount"])

	run.vision.extract = ""
	replayed := run.execute(`{"ai": "never", "do": [{"extract": {"instruction": "The invoice amount", "schema": {"type": "object", "properties": {"amount": {"type": "number"}}}}}]}`, nil)
	require.NoError(t, replayed.err)
	assert.EqualValues(t, 12345, replayed.exec.GetOutputs()["amount"])
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
	assert.Nil(t, run.elements, "a statement never opens the elements reader")
}

// An exact check reads the front window's elements, calls no model, takes
// no screenshot, and reports how it was decided.
// A check on the step's data decides at once, reading no screen and calling
// no model: a when on a value runs its operation or skips it, an empty value
// can be asked about, values compare as names do, and an expect on a value
// fails with the reason.
func TestConditionsOnValues(t *testing.T) {
	t.Parallel()

	t.Run("when runs only the matching operation", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		run.llm = nil
		began := time.Now()
		execution := run.execute(`{"ai": "never", "variables": {"same": "あり", "second": ""}, "do": [
			{"wait": "1ms", "when": {"value": "%same%", "equals": "あり"}},
			{"act": {"instruction": "Clear the same-as box", "recorded": {"windows": [{"click": {"role": "button", "id": "saveButton"}}]}}, "when": {"value": "%same%", "equals": "なし"}},
			{"wait": "1ms", "when": {"value": "%second%", "not_equals": ""}},
			{"wait": "1ms", "when": {"value": "有", "equals": "有"}},
			{"wait": "1ms", "when": {"value": "ＡＢＣ", "equals": "ABC"}}
		]}`, nil)
		require.NoError(t, execution.err)
		events := operationEvents(execution.exec.GetAgentSession())
		require.Len(t, events, 5)
		assert.Equal(t, agentstep.StatusCompleted, events[0].Status)
		assert.Equal(t, agentstep.StatusSkipped, events[1].Status)
		assert.Equal(t, agentstep.ViaExact, events[1].Via)
		assert.Contains(t, events[1].Content, `Clear the same-as box`, "the skipped act is named by what it would do")
		assert.Contains(t, events[1].Content, `"あり" is not "なし"`)
		assert.Equal(t, agentstep.StatusSkipped, events[2].Status, "an empty value is asked about, not refused")
		assert.Contains(t, events[2].Content, `"" is ""`)
		assert.Equal(t, agentstep.StatusCompleted, events[3].Status, "a value given as written, as a parameter arrives")
		assert.Equal(t, agentstep.StatusCompleted, events[4].Status, "full-width and half-width letters compare equal")
		assert.Less(t, time.Since(began), exactWhenWait, "nothing waits for a value")
		assert.Zero(t, run.elements.opens, "no elements are read")
		assert.Zero(t, run.backend.captures, "no screenshot")
	})

	t.Run("expect fails with the reason", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		execution := run.execute(`{"ai": "never", "variables": {"same": "あり"}, "do": [{"expect": {"value": "%same%", "equals": "なし"}}]}`, nil)
		require.ErrorContains(t, execution.err, `"あり" is not "なし"`)
	})
}

func TestExactConditions(t *testing.T) {
	t.Parallel()

	t.Run("text holds", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		run.llm = nil
		execution := run.execute(`{"ai": "never", "screenshots": "never", "do": [{"expect": {"text": "保存しました"}}, {"expect": {"text": "12,000"}}]}`, nil)
		require.NoError(t, execution.err)
		events := operationEvents(execution.exec.GetAgentSession())
		require.Len(t, events, 2)
		assert.Equal(t, agentstep.StatusCompleted, events[0].Status)
		assert.Equal(t, agentstep.ViaExact, events[0].Via)
		assert.Zero(t, events[0].Tokens)
		assert.Contains(t, events[0].Content, `text "保存しました"`)
		assert.Contains(t, events[0].Content, `the window shows "保存しました"`)
		assert.Contains(t, events[1].Content, "12,000", "a field's value counts as shown")
		assert.Empty(t, run.vision.requests, "no model request")
		assert.Equal(t, 1, run.elements.opens)
		assert.Zero(t, run.backend.captures, "no screenshot")
		assert.True(t, run.elements.closed, "the reader is closed with the desktop")
	})

	t.Run("a grayed-out element", func(t *testing.T) {
		t.Parallel()
		// The reporter button is grayed out once reporters are entered; the
		// step checks for that and skips entering them, with no model.
		run := newTestRun(t)
		run.elements = newFakeElements()
		run.elements.elements = append(run.elements.elements, desktop.Element{Role: desktop.RoleButton, Name: "通報者", ID: "reporterButton",
			Bounds: image.Rect(500, 140, 580, 168), Path: []desktop.PathStep{{Role: desktop.RoleButton, Name: "通報者", Index: 2}}, Disabled: true})
		run.llm = nil
		execution := run.execute(`{"ai": "never", "screenshots": "never", "do": [
			{"expect": {"element": {"role": "button", "id": "reporterButton", "enabled": false}}},
			{"act": {"instruction": "Enter the reporters", "recorded": {"windows": [{"click": {"role": "button", "id": "reporterButton"}}]}},
			 "when": {"element": {"role": "button", "id": "reporterButton", "enabled": true}}}
		]}`, nil)
		require.NoError(t, execution.err)
		events := operationEvents(execution.exec.GetAgentSession())
		require.Len(t, events, 2)
		assert.Equal(t, agentstep.StatusCompleted, events[0].Status)
		assert.Equal(t, agentstep.StatusSkipped, events[1].Status, "the act is skipped while the button is grayed out")
		assert.Empty(t, run.backend.inputs())
		assert.Empty(t, run.vision.requests, "no model request")
	})

	t.Run("text fails with the reason", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		// The timeout spans several exact polls, so a second read is sure even
		// where the first one is slow to come back on a loaded runner.
		execution := run.execute(`{"do": [{"expect": {"text": "エラー"}, "timeout": "1s"}]}`, nil)
		require.EqualError(t, execution.err, `computer: do[0] expect failed: expectation not met: no element of "経費精算 - 請求書 1042" shows "エラー"`)
		events := operationEvents(execution.exec.GetAgentSession())
		require.Len(t, events, 1)
		assert.Equal(t, agentstep.StatusFailed, events[0].Status)
		assert.Equal(t, agentstep.ViaExact, events[0].Via)
		assert.GreaterOrEqual(t, run.elements.reads, 2, "an expect keeps looking until the operation timeout")
	})

	t.Run("element when skips", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		execution := run.execute(`{"do": [{"wait": "1ms", "when": {"element": {"role": "button", "name": "削除"}}}, {"wait": "1ms", "when": {"element": {"role": "button", "name": "保存", "in": {"role": "group", "name": "支払情報"}}}}]}`, nil)
		require.NoError(t, execution.err)
		events := operationEvents(execution.exec.GetAgentSession())
		require.Len(t, events, 2)
		assert.Equal(t, agentstep.StatusSkipped, events[0].Status)
		assert.Equal(t, agentstep.ViaExact, events[0].Via)
		assert.Contains(t, events[0].Content, `{"role":"button","name":"削除"} is not there`)
		assert.Equal(t, agentstep.StatusCompleted, events[1].Status, "the button in the group is there, so the wait ran")
	})

	t.Run("ambiguity is a miss", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		execution := run.execute(`{"do": [{"expect": {"element": {"role": "button", "name": "保存"}}, "timeout": "200ms"}]}`, nil)
		require.ErrorContains(t, execution.err, `{"role":"button","name":"保存"} is not there; 2 elements match`)
	})

	t.Run("window holds and fails", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		execution := run.execute(`{"do": [{"expect": {"window": "請求書 *"}}]}`, nil)
		require.NoError(t, execution.err)
		assert.Contains(t, operationEvents(execution.exec.GetAgentSession())[0].Content, `the window "経費精算 - 請求書 1042" is in front`)

		failed := run.execute(`{"do": [{"expect": {"window": "給与"}, "timeout": "200ms"}]}`, nil)
		require.ErrorContains(t, failed.err, `"経費精算 - 請求書 1042" is in front, not "給与"`)
	})

	t.Run("no window in front is looked at again", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		run.elements.noWindow = 2
		execution := run.execute(`{"do": [{"expect": {"window": "請求書 *", "within": "5s"}}]}`, nil)
		require.NoError(t, execution.err, "the window opened while the check kept looking")

		run.elements.noWindow = 1000
		failed := run.execute(`{"do": [{"expect": {"window": "請求書 *", "within": "300ms"}}]}`, nil)
		require.ErrorContains(t, failed.err, "no window is in front")
	})

	t.Run("within keeps looking", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		run.elements.arriveAfter(3, append(expenseElements(), desktop.Element{Role: desktop.RoleText, Name: "完了", Bounds: image.Rect(120, 460, 200, 480), Path: []desktop.PathStep{{Role: desktop.RoleText, Name: "完了", Index: 2}}}))
		execution := run.execute(`{"do": [{"wait": "1ms", "when": {"text": "完了", "within": "5s"}}]}`, nil)
		require.NoError(t, execution.err)
		assert.Equal(t, []string{"wait:completed"}, eventNames(execution.exec.GetAgentSession()), "the text arrived within the window")
		assert.GreaterOrEqual(t, run.elements.reads, 4)
	})

	t.Run("when keeps looking for a moment", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		run.elements.arriveAfter(1, append(expenseElements(), desktop.Element{Role: desktop.RoleText, Name: "完了"}))
		execution := run.execute(`{"do": [{"wait": "1ms", "when": {"text": "完了"}}]}`, nil)
		require.NoError(t, execution.err)
		assert.Equal(t, []string{"wait:completed"}, eventNames(execution.exec.GetAgentSession()), "the text arrived on the second look")

		run.elements.arriveAfter(30, append(expenseElements(), desktop.Element{Role: desktop.RoleText, Name: "終了"}))
		gaveUp := run.execute(`{"do": [{"wait": "1ms", "when": {"text": "終了", "within": "300ms"}}]}`, nil)
		require.NoError(t, gaveUp.err)
		assert.Equal(t, []string{"wait:skipped"}, eventNames(gaveUp.exec.GetAgentSession()), "within bounds the looking")
	})

	t.Run("no elements is a miss until the window passes", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		run.elements.err = desktop.ErrNoElements
		execution := run.execute(`{"do": [{"expect": {"text": "完了"}, "timeout": "200ms"}]}`, nil)
		require.ErrorContains(t, execution.err, "expectation not met")
	})

	t.Run("unsupported host", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		execution := run.execute(`{"do": [{"expect": {"text": "完了"}}]}`, nil)
		require.ErrorContains(t, execution.err, "exact checks need macOS or 64-bit Windows: accessible elements are supported on macOS and 64-bit Windows only")
	})

	t.Run("placeholders from a variable and an ask", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		const steps = `{"variables": {"doc": "1042"}, "do": [
			{"expect": {"window": "請求書 %doc%"}},
			{"ask": {"prompt": "Section?", "as": "section"}},
			{"expect": {"element": {"role": "button", "name": "保存", "in": {"role": "group", "name": "%section%"}}}}
		]}`
		waiting := run.execute(steps, nil)
		require.NoError(t, waiting.err)
		session := waiting.exec.GetAgentSession()
		assert.Contains(t, operationEvents(session)[0].Content, `window "請求書 %doc%"`, "the subject shows the check as written")
		assert.Contains(t, operationEvents(session)[0].Content, "請求書 1042", "the reason shows the value")
		session.Interactions[0].Status = ir.AgentInteractionAnswered
		session.Interactions[0].Answers = [][]string{{"支払情報"}}
		resumed := run.execute(steps, session)
		require.NoError(t, resumed.err)
		events := operationEvents(resumed.exec.GetAgentSession())
		last := events[len(events)-1]
		assert.Contains(t, last.Content, `"%section%"`, "the subject keeps the placeholder")
		assert.NotContains(t, last.Content, "支払情報", "the answer is masked in the reason")
	})

	t.Run("a placeholder without a value", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		empty := run.execute(`{"variables": {"doc": ""}, "do": [{"expect": {"text": "%doc%"}}]}`, nil)
		require.ErrorContains(t, empty.err, "the check uses %doc%, but its value is empty")

		skipped := run.execute(`{"do": [
			{"ask": {"prompt": "Code?", "as": "code"}, "when": {"window": "給与"}},
			{"wait": "1ms", "when": {"text": "%code%"}}
		]}`, nil)
		require.ErrorContains(t, skipped.err, "evaluate when: the check uses %code%, but the ask that sets it did not run")
		events := operationEvents(skipped.exec.GetAgentSession())
		assert.Equal(t, agentstep.StatusFailed, events[len(events)-1].Status)
	})

	t.Run("a secret in a selector stays on the host", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		run.secrets = map[string]string{"BOT": "paymentSaveButton"}
		execution := run.execute(`{"do": [{"expect": {"element": {"role": "button", "id": "paymentSaveButton"}}}]}`, nil)
		require.NoError(t, execution.err, "the selector never reaches the model")
		assert.Empty(t, run.vision.requests)
		assert.NotContains(t, execution.stderr.String(), "paymentSaveButton", "the value is masked in the log")

		statement := run.execute(`{"do": [{"expect": "The paymentSaveButton is shown"}]}`, nil)
		require.ErrorContains(t, statement.err, "contains the value of secret BOT")
	})
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

// Under ai: never an act replays its recording without a model, and fails
// when there is none or the screen no longer matches, without a model
// request. The recording it missed is kept for a run that lets the model
// repair it.
func TestNeverReplaysOrFails(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Open the report"}]}`
	const never = `{"ai": "never", "do": [{"act": "Open the report"}]}`
	run := newTestRun(t)

	missing := run.execute(never, nil)
	require.ErrorContains(t, missing.err, "the act cannot run without AI: there is no recording of it on this host")

	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(30, 40)), done("Opened")}}}
	require.NoError(t, run.execute(steps, nil).err)
	recorded := run.backend.inputs()

	run.backend.events = nil
	run.llm = nil
	replayed := run.execute(never, nil)
	require.NoError(t, replayed.err, "the step runs with no model configured")
	assert.Equal(t, recorded, run.backend.inputs())
	events := operationEvents(replayed.exec.GetAgentSession())
	require.Len(t, events, 1)
	assert.Equal(t, agentstep.StatusCacheHit, events[0].Status)
	assert.Equal(t, agentstep.ViaScreen, events[0].Via)
	assert.Zero(t, events[0].Tokens)

	run.backend.events = nil
	run.backend.show(pattern(400, 200, 150))
	failed := run.execute(never, nil)
	require.ErrorContains(t, failed.err, "the act cannot run without AI: the screen differs from the recording at turn 1 of 1")
	assert.Empty(t, run.backend.inputs(), "nothing is replayed on a screen that differs")
	assert.Equal(t, ir.AgentSessionFailed, failed.exec.GetAgentSession().State)
	missed := operationEvents(failed.exec.GetAgentSession())
	require.Len(t, missed, 1)
	assert.Equal(t, agentstep.StatusFailed, missed[0].Status)
	assert.Equal(t, agentstep.ViaScreen, missed[0].Via, "a miss ran without AI")
	assert.Zero(t, missed[0].Tokens)

	run.llm = &ir.LLMConfig{Provider: "openai", Model: "test-model"}
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(50, 60)), done("Opened")}}}
	healed := run.execute(steps, nil)
	require.NoError(t, healed.err)
	assert.Equal(t, []string{"act:healed"}, eventNames(healed.exec.GetAgentSession()), "the recording the never run missed was kept")
}

// A never act whose recording no longer fits at a later turn names that
// turn, after the earlier turns replayed.
func TestNeverMissNamesTurn(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Post the invoice"}]}`
	const never = `{"ai": "never", "do": [{"act": "Post the invoice"}]}`
	start, form, changedForm, posted := stripes(400, 200, 2), stripes(400, 200, 6), stripes(400, 200, 12), stripes(400, 200, 30)
	run := newTestRun(t)

	run.backend.script(start, form, posted)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(10, 10)), actions(clickAt(20, 20)), done("Posted")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.backend.script(start, changedForm, posted)
	failed := run.execute(never, nil)
	require.ErrorContains(t, failed.err, "the screen differs from the recording at turn 2 of 2")
	assert.Equal(t, []string{"move 10,10", "left down #1"}, run.backend.inputs(), "the first turn replayed")
}

// Under ai: every_run the model decides every run, and a successful act
// still records, so a later run under never has a recording to replay.
// cache: false means the same.
func TestEveryRunRecords(t *testing.T) {
	t.Parallel()

	for name, with := range map[string]string{
		"ai":    `{"ai": "every_run", "do": [{"act": "Open the report"}]}`,
		"cache": `{"cache": false, "do": [{"act": "Open the report"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			opens := func() *scriptedSession {
				return &scriptedSession{turns: []*computeruse.Turn{actions(clickAt(30, 40)), done("Opened")}}
			}
			run := newTestRun(t)
			run.sessions = []*scriptedSession{opens()}
			require.NoError(t, run.execute(with, nil).err)

			run.sessions = []*scriptedSession{opens()}
			again := run.execute(with, nil)
			require.NoError(t, again.err)
			assert.Equal(t, []string{"act:completed"}, eventNames(again.exec.GetAgentSession()), "the model decided again")
			assert.Empty(t, run.sessions)

			replayed := run.execute(`{"ai": "never", "do": [{"act": "Open the report"}]}`, nil)
			require.NoError(t, replayed.err)
			assert.Equal(t, []string{"act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()))
		})
	}
}

// elementRun is a desktop with the expense form, on a screen big enough
// to hold it, for recording and replaying by element.
// A pinned act replays its authored steps on a device with no recording of
// its own, with no model, reporting a cache hit by element.
func TestPinnedActReplays(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "do": [{"act": {"instruction": "Save the invoice", "recorded": {"windows": [{"click": {"role": "button", "name": "保存", "id": "saveButton"}}]}}}]}`
	run := elementRun(t)
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err, "the pin runs with no recording and no model")
	events := operationEvents(replayed.exec.GetAgentSession())
	require.Len(t, events, 1)
	assert.Equal(t, agentstep.StatusCacheHit, events[0].Status)
	assert.Equal(t, agentstep.ViaElement, events[0].Via)
	assert.Equal(t, []string{"move 235,194", "left down #1"}, run.backend.inputs())
}

// A pinned act substitutes a variable into a typed step and waits.
func TestPinnedActTypesAndWaits(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.focusOn("amountBox")
	const steps = `{"ai": "never", "variables": {"amount": "12,000"}, "do": [{"act": {"instruction": "Enter the amount", "recorded": {"windows": [{"type": {"into": {"role": "text_field", "id": "amountBox"}, "text": "%amount%"}}, {"wait": "10ms"}, {"key": "ctrl+s"}]}}}]}`
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"focus amountBox", "type 12,000", "key ctrl", "key s"}, run.backend.inputs())
}

// A pinned key step focuses the element its keys went to before pressing
// them, and misses, pressing nothing, when that element is not there.
func TestPinnedKeyFocusesItsElement(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	replayed := run.execute(`{"ai": "never", "do": [{"act": {"instruction": "Submit", "recorded": {"windows": [{"key": "enter", "focus": {"role": "text_field", "id": "amountBox"}}]}}}]}`, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"focus amountBox", "key enter"}, run.backend.inputs())

	run.backend.events = nil
	missed := run.execute(`{"ai": "never", "do": [{"act": {"instruction": "Submit", "recorded": {"windows": [{"key": "enter", "focus": {"role": "text_field", "id": "noteBox"}}]}}}]}`, nil)
	require.ErrorContains(t, missed.err, "was not found")
	assert.Empty(t, run.backend.inputs(), "keys never go to whatever else has the focus")
}

// A pinned act waits for its element through a moment with no window in
// front, as when the act before it closed one window and the next has yet
// to open.
func TestPinnedActWaitsForAWindowInFront(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.elements.noWindow = 3
	const steps = `{"ai": "never", "input": "direct", "find_within": "5s", "do": [{"act": {"instruction": "Save the invoice", "recorded": {"windows": [{"click": {"role": "button", "name": "保存", "id": "saveButton"}}]}}}]}`
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"invoke saveButton"}, run.backend.inputs())
}

// A pinned act under input: direct drives the element's pattern.
func TestPinnedActDirect(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "input": "direct", "do": [{"act": {"instruction": "Save the invoice", "recorded": {"windows": [{"click": {"role": "button", "name": "保存", "id": "saveButton"}}]}}}]}`
	run := elementRun(t)
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"invoke saveButton"}, run.backend.inputs())
}

// confirmDialog is a confirmation an application shows only sometimes.
func confirmDialog() (desktop.Element, []desktop.Element) {
	window := desktop.Element{Role: desktop.RoleDialog, Name: "確認", App: "expense", Window: "確認", Bounds: image.Rect(250, 200, 550, 350)}
	return window, []desktop.Element{
		{Role: desktop.RoleText, Name: "すでに入力中のものがあります。クリアしていいですか？", Window: "確認", Bounds: image.Rect(270, 220, 530, 250), Path: []desktop.PathStep{{Role: desktop.RoleText, Index: 0}}},
		{Role: desktop.RoleButton, Name: "はい", Window: "確認", Bounds: image.Rect(300, 300, 380, 330), Path: []desktop.PathStep{{Role: desktop.RoleButton, Name: "はい", Index: 0}}},
	}
}

// A handler clears a screen that comes up only sometimes, whenever the step
// meets it: after an action, while a check waits, or before an action, even
// a modal drawn over the page, which leaves the elements behind it readable
// to an action or a check. A run that never meets it pays nothing for it.
func TestHandlerClearsAScreenThatComesUpSometimes(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "input": "direct", "find_within": "300ms",
		"handle": [{"name": "入力中クリアの確認", "when": {"text": "クリアしていいですか"}, "recorded": {"windows": [{"click": {"role": "button", "name": "はい"}}]}}],
		"do": [
			{"act": {"instruction": "Reset", "recorded": {"windows": [{"click": {"role": "button", "id": "saveButton"}}]}}},
			{"expect": {"element": {"role": "text_field", "id": "amountBox"}, "within": "2s"}}
		]}`
	for _, tc := range []struct {
		name      string
		before    bool
		after     bool
		overCheck bool
		handled   bool
		inputs    []string
	}{
		{name: "after the click", after: true, handled: true, inputs: []string{"invoke saveButton", "invoke はい"}},
		{name: "over the page before the click", before: true, handled: true, inputs: []string{"invoke はい", "invoke saveButton"}},
		{name: "over the page the check reads", overCheck: true, handled: true, inputs: []string{"invoke saveButton", "invoke はい"}},
		{name: "never", inputs: []string{"invoke saveButton"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := elementRun(t)
			form := expenseWindow()
			dialog, dialogElements := confirmDialog()
			show := run.elements.show
			if tc.before {
				show(form, append(expenseElements(), dialogElements...))
			}
			run.elements.onApply = func(e desktop.Element, pattern desktop.Pattern, _ string) {
				run.backend.recordAccessibility(string(pattern) + " " + focusedID(e))
				switch {
				case e.Name == "はい":
					show(form, expenseElements())
				case tc.after && e.ID == "saveButton":
					show(dialog, dialogElements)
				case tc.overCheck && e.ID == "saveButton":
					show(form, append(expenseElements(), dialogElements...))
				}
			}
			began := time.Now()
			execution := run.execute(steps, nil)
			require.NoError(t, execution.err)
			assert.Equal(t, tc.inputs, run.backend.inputs())
			assert.Equal(t, tc.handled, strings.Contains(execution.stderr.String(), "handling 入力中クリアの確認"))
			if !tc.handled {
				assert.Less(t, time.Since(began), time.Second, "a run without the screen does not wait for it")
			}
		})
	}
}

// A screen its handler does not clear fails the step after a bounded number
// of tries instead of coming back forever. The check waits long enough for
// every try, however long the step lets the screen settle after each.
func TestHandlerThatDoesNotClearItsScreenFails(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "input": "direct",
		"handle": [{"name": "入力中クリアの確認", "when": {"text": "クリアしていいですか"}, "recorded": {"windows": [{"click": {"role": "button", "name": "はい"}}]}}],
		"do": [{"expect": {"element": {"role": "text_field", "id": "amountBox"}, "within": "1m"}}]}`
	run := elementRun(t)
	dialog, dialogElements := confirmDialog()
	run.elements.window = dialog
	run.elements.elements = dialogElements
	execution := run.execute(steps, nil)
	require.ErrorContains(t, execution.err, `handle "入力中クリアの確認": the screen was still there after 20 times`)
	assert.Len(t, run.backend.inputs(), maxHandled)
}

// confirmHandler clears confirmDialog by pressing its はい, then waits
// for wait when that is set.
func confirmHandler(wait string) string {
	steps := `[{"click": {"role": "button", "name": "はい"}}]`
	if wait != "" {
		steps = `[{"click": {"role": "button", "name": "はい"}}, {"wait": "` + wait + `"}]`
	}
	return `[{"name": "入力中クリアの確認", "when": {"text": "クリアしていいですか"}, "recorded": {"windows": ` + steps + `}}]`
}

// pressingYesShows makes pressing the dialog's はい show the form, after
// reads more looks at the window when reads is set, and records each pattern.
func pressingYesShows(run *testRun, reads int) {
	run.elements.onApply = func(e desktop.Element, pattern desktop.Pattern, _ string) {
		run.backend.recordAccessibility(string(pattern) + " " + focusedID(e))
		if e.Name != "はい" {
			return
		}
		run.backend.focusOn(desktop.WindowID{Handle: 2, Title: expenseWindow().Name, App: "expense"})
		if reads == 0 {
			run.elements.show(expenseWindow(), expenseElements())
			return
		}
		run.elements.show(expenseWindow(), []desktop.Element{})
		run.elements.arriveAfter(reads, expenseElements())
	}
}

// A check keeps looking for its whole window after a handler ran, however
// long the handler took, so a screen that settles a moment after the
// confirmation closes still passes.
func TestHandledCheckLooksForItsWholeWindowAgain(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	dialog, dialogElements := confirmDialog()
	run.elements.show(dialog, dialogElements)
	pressingYesShows(run, 2)
	steps := `{"ai": "never", "input": "direct", "handle": ` + confirmHandler("1200ms") + `,
		"do": [{"expect": {"element": {"role": "text_field", "id": "amountBox"}, "within": "1s"}}]}`
	require.NoError(t, run.execute(steps, nil).err)
	assert.Equal(t, []string{"invoke はい"}, run.backend.inputs())
}

// A confirmation in front of the window a pinned step names is handled
// before that window is brought forward, which would hide it.
func TestHandlerRunsBeforeTheRecordedWindowComesBack(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	form := expenseWindow()
	dialog, dialogElements := confirmDialog()
	dialogWindow := desktop.WindowID{Handle: 3, Title: dialog.Name, App: "expense"}
	formWindow := desktop.WindowID{Handle: 2, Title: form.Name, App: "expense"}
	run.backend.windows = []desktop.WindowID{dialogWindow, formWindow}
	run.backend.focusOn(dialogWindow)
	run.elements.show(dialog, dialogElements)
	// Raising the form puts it over the confirmation.
	run.backend.onRaise = func(w desktop.WindowID) {
		if w.Handle == formWindow.Handle {
			run.elements.show(form, expenseElements())
		}
	}
	pressingYesShows(run, 0)
	steps := `{"ai": "never", "input": "direct", "handle": ` + confirmHandler("") + `,
		"do": [{"act": {"instruction": "Save", "recorded": {"windows": [{"click": {"window": "経費精算*", "role": "button", "id": "saveButton"}}]}}}]}`
	require.NoError(t, run.execute(steps, nil).err)
	assert.Equal(t, []string{"invoke はい", "invoke saveButton"}, run.backend.inputs())
}

// A pinned step that meets a dialog no handler clears stops there and
// says what the dialog asks, rather than raise its window over the dialog
// and act past the question. A dialog of another application holds
// nothing of this one, so the window is raised as usual.
func TestPinnedStepStopsAtADialogItWasNotToldAbout(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "input": "direct", "find_within": "300ms",
		"do": [{"act": {"instruction": "Save", "recorded": {"windows": [{"click": {"window": "経費精算*", "role": "button", "id": "saveButton"}}]}}}]}`
	for _, tc := range []struct {
		name, app, err string
	}{
		{"of the application", "expense", `the dialog "確認" is in front, showing "すでに入力中のものがあります。クリアしていいですか？"`},
		{"of another application", "notifier", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := elementRun(t)
			form := expenseWindow()
			dialog, dialogElements := confirmDialog()
			dialog.App = tc.app
			dialogWindow := desktop.WindowID{Handle: 3, Title: dialog.Name, App: tc.app}
			formWindow := desktop.WindowID{Handle: 2, Title: form.Name, App: "expense"}
			run.backend.windows = []desktop.WindowID{dialogWindow, formWindow}
			run.backend.focusOn(dialogWindow)
			run.elements.show(dialog, dialogElements)
			run.backend.onRaise = func(w desktop.WindowID) {
				if w.Handle == formWindow.Handle {
					run.elements.show(form, expenseElements())
				}
			}
			execution := run.execute(steps, nil)
			if tc.err != "" {
				require.ErrorContains(t, execution.err, tc.err)
				assert.Empty(t, run.backend.raised, "the window stays behind the dialog")
				assert.Empty(t, run.backend.inputs())
				return
			}
			require.NoError(t, execution.err)
			assert.Equal(t, []string{"invoke saveButton"}, run.backend.inputs())
		})
	}
}

// An element recorded in the window behind a dialog is not looked for in
// the dialog, though a window matches by its application alone and the
// dialog has a button that matches: pressing it would answer a question
// the recording never saw. That holds when the dialog is the only window
// of the application listed, which a lookup by application alone finds.
func TestRecordedElementIsNotLookedForInADialog(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "input": "direct", "find_within": "300ms",
		"do": [{"act": {"instruction": "Agree", "recorded": {"windows": [{"click": {"app": "expense", "window": "経費精算*", "role": "button", "name": "はい"}}]}}}]}`
	for _, tc := range []struct {
		name    string
		windows []desktop.WindowID
	}{
		{"with no window listed", nil},
		{"with only the dialog listed", []desktop.WindowID{{Handle: 3, Title: "確認", App: "expense"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := elementRun(t)
			run.backend.windows = tc.windows
			run.elements.show(confirmDialog())
			execution := run.execute(steps, nil)
			require.ErrorContains(t, execution.err, `the dialog "確認" is in front`)
			assert.Empty(t, run.backend.inputs())
		})
	}
}

// A dialog of another application holds nothing of the recorded one, even
// when the recorded window is found by its title alone because its
// application cannot be read, so that window is raised and the step acts.
func TestDialogOfAnotherApplicationDoesNotHoldAWindowFoundByTitle(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	form := expenseWindow()
	dialog, dialogElements := confirmDialog()
	dialog.App = "notifier"
	formWindow := desktop.WindowID{Handle: 2, Title: form.Name}
	run.backend.windows = []desktop.WindowID{{Handle: 3, Title: dialog.Name, App: "notifier"}, formWindow}
	run.backend.focusOn(run.backend.windows[0])
	run.elements.show(dialog, dialogElements)
	run.backend.onRaise = func(w desktop.WindowID) {
		if w.Handle == formWindow.Handle {
			run.elements.show(form, expenseElements())
		}
	}
	steps := `{"ai": "never", "input": "direct", "find_within": "300ms",
		"do": [{"act": {"instruction": "Save", "recorded": {"windows": [{"click": {"app": "expense", "window": "経費精算*", "role": "button", "id": "saveButton"}}]}}}]}`
	require.NoError(t, run.execute(steps, nil).err)
	assert.Equal(t, []string{"invoke saveButton"}, run.backend.inputs())
}

// A person who leaves a dialog of the step's application in front holds
// the act until they stop; the act then leaves the dialog in front rather
// than bring back the window behind it, and stops on the dialog.
func TestActDoesNotReturnBehindADialogAfterAPerson(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "idle": "100ms", "input": "direct", "find_within": "300ms", "do": [{"act": {"instruction": "Save the invoice and the payment", "recorded": {"windows": [
		{"click": {"role": "button", "id": "saveButton"}},
		{"click": {"role": "button", "id": "paymentSaveButton"}}]}}}]}`
	run := elementRun(t)
	dialog, dialogElements := confirmDialog()
	expense := desktop.WindowID{Handle: 2, Title: expenseWindow().Name, App: "expense"}
	confirm := desktop.WindowID{Handle: 3, Title: dialog.Name, App: "expense"}
	run.backend.windows = []desktop.WindowID{expense, confirm}
	run.elements.onApply = func(e desktop.Element, _ desktop.Pattern, _ string) {
		if e.ID != "saveButton" {
			return
		}
		run.backend.personUsesAfterEcho()
		run.backend.focusOn(confirm)
		run.elements.show(dialog, dialogElements)
	}
	execution := run.execute(steps, nil)
	require.ErrorContains(t, execution.err, `the dialog "確認" is in front`)
	assert.Empty(t, run.backend.raised, "the form stays behind the dialog")
}

// A check that fails while a dialog is in front names the dialog, which is
// often why what it looks for is not there.
// The dialog's text is read from an element's name, or from its value
// when the name is blank.
func TestCheckNamesTheDialogInFront(t *testing.T) {
	t.Parallel()

	const question = "すでに入力中のものがあります。クリアしていいですか？"
	for _, tc := range []struct {
		name, textName, textValue string
	}{
		{"by its name", question, ""},
		{"by its value", " ", question},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := elementRun(t)
			dialog, _ := confirmDialog()
			run.elements.show(dialog, []desktop.Element{{Role: desktop.RoleText, Name: tc.textName, Value: tc.textValue, Window: dialog.Name, Bounds: image.Rect(270, 220, 530, 250)}})
			execution := run.execute(`{"ai": "never", "do": [{"expect": {"text": "保存しました", "within": "300ms"}}]}`, nil)
			require.ErrorContains(t, execution.err, `the dialog "確認" is in front, showing "`+question+`"; no element of "確認" shows "保存しました"`)
		})
	}
}

// A window in front that cannot be read may be a dialog, so the step does
// not raise its window over it.
func TestUnreadableFrontWindowIsNotCoveredByARaise(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	form := desktop.WindowID{Handle: 2, Title: expenseWindow().Name, App: "expense"}
	run.backend.windows = []desktop.WindowID{{Handle: 3, Title: "確認", App: "expense"}, form}
	run.backend.focusOn(run.backend.windows[0])
	run.elements.frontErr = errors.New("the window did not answer")
	steps := `{"ai": "never", "input": "direct", "find_within": "300ms",
		"do": [{"act": {"instruction": "Save", "recorded": {"windows": [{"click": {"window": "経費精算*", "role": "button", "id": "saveButton"}}]}}}]}`
	require.ErrorContains(t, run.execute(steps, nil).err, "the window did not answer")
	assert.Empty(t, run.backend.raised)
}

// A confirmation over the window a learned pixel turn replays in is
// handled while the turn waits for its screen.
func TestHandlerClearsAScreenOverAPixelReplay(t *testing.T) {
	t.Parallel()

	const learn = `{"do": [{"act": "Click the canvas"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(10, 10)), done("Clicked")}}}
	require.NoError(t, run.execute(learn, nil).err)
	require.Nil(t, lookupRecording(t, run, "Click the canvas").Turns[0].Actions[0].Element, "the turn replays by pixels")

	recorded := pattern(800, 600, 0)
	dialog, dialogElements := confirmDialog()
	run.backend.show(pattern(800, 600, 150))
	run.elements.show(dialog, dialogElements)
	pressingYesShows(run, 0)
	onApply := run.elements.onApply
	run.elements.onApply = func(e desktop.Element, p desktop.Pattern, v string) {
		onApply(e, p, v)
		if e.Name == "はい" {
			run.backend.show(recorded)
		}
	}
	run.backend.events = nil
	steps := `{"ai": "never", "input": "direct", "handle": ` + confirmHandler("") + `, "do": [{"act": "Click the canvas"}]}`
	require.NoError(t, run.execute(steps, nil).err)
	assert.Equal(t, []string{"invoke はい", "move 10,10", "left down #1"}, run.backend.inputs())
}

// A confirmation is handled before the model looks at the screen, so the
// model never sees it or acts on it.
func TestHandlerClearsAScreenBeforeTheModelLooks(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	dialog, dialogElements := confirmDialog()
	run.elements.show(dialog, dialogElements)
	pressingYesShows(run, 0)
	session := &scriptedSession{turns: []*computeruse.Turn{done("Nothing left to do")}}
	run.sessions = []*scriptedSession{session}
	steps := `{"idle": "0", "input": "direct", "handle": ` + confirmHandler("") + `, "do": [{"act": "Save the form"}]}`
	require.NoError(t, run.execute(steps, nil).err)
	assert.Equal(t, []string{"invoke はい"}, run.backend.inputs())
	require.NotEmpty(t, session.observations)
	for _, e := range session.observations[0].Elements {
		assert.NotEqual(t, "はい", e.Name, "the model's first look shows no confirmation")
	}
}

// A screen its handler does not clear stops an act before the model ever
// sees it, through the handler's limit.
func TestHandlerThatDoesNotClearItsScreenStopsTheModel(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	dialog, dialogElements := confirmDialog()
	run.elements.show(dialog, dialogElements)
	session := &scriptedSession{turns: []*computeruse.Turn{done("Nothing left to do")}}
	run.sessions = []*scriptedSession{session}
	steps := `{"idle": "0", "input": "direct", "handle": ` + confirmHandler("") + `, "do": [{"act": "Save the form"}]}`
	execution := run.execute(steps, nil)
	require.ErrorContains(t, execution.err, `handle "入力中クリアの確認": the screen was still there after 20 times`)
	assert.Empty(t, session.observations, "the model never saw the screen")
}

// A confirmation that comes up while the screen settles, after the step
// first checked for one, is still cleared before the model sees the screen,
// whether the model acts on it or reads it for an extract.
func TestHandlerClearsAScreenThatComesUpWhileTheScreenSettles(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		op   string
	}{
		{"the model's look", `{"act": "Save the form"}`},
		{"a model extract", `{"extract": {"instruction": "The amount", "schema": {"type": "object", "properties": {"amount": {"type": "number"}}}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := elementRun(t)
			_, dialogElements := confirmDialog()
			// The confirmation is there from the second look at the window.
			run.elements.arriveAfter(1, append(expenseElements(), dialogElements...))
			pressingYesShows(run, 0)
			session := &scriptedSession{turns: []*computeruse.Turn{done("Nothing left to do")}}
			run.sessions = []*scriptedSession{session}
			run.vision.extract = `{"amount": {"value": 12000, "element": "e3"}}`
			steps := `{"idle": "0", "input": "direct", "handle": ` + confirmHandler("") + `, "do": [` + tc.op + `]}`
			require.NoError(t, run.execute(steps, nil).err)
			assert.Equal(t, []string{"invoke はい"}, run.backend.inputs(), "the confirmation was cleared")
			for _, observation := range session.observations {
				for _, e := range observation.Elements {
					assert.NotEqual(t, "はい", e.Name, "the model never sees the confirmation")
				}
			}
		})
	}
}

// A confirmation that comes up while the model decides is cleared before
// the model's actions run, so they never land on it.
func TestHandlerClearsAScreenThatComesUpWhileTheModelDecides(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	_, dialogElements := confirmDialog()
	pressingYesShows(run, 0)
	shown := false
	session := &scriptedSession{
		turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")},
		onNext: func() {
			if !shown {
				shown = true
				run.elements.show(expenseWindow(), append(expenseElements(), dialogElements...))
			}
		},
	}
	run.sessions = []*scriptedSession{session}
	steps := `{"idle": "0", "input": "direct", "handle": ` + confirmHandler("") + `, "do": [{"act": "Save the form"}]}`
	require.NoError(t, run.execute(steps, nil).err)
	assert.Equal(t, []string{"invoke はい", "move 235,194", "left down #1"}, run.backend.inputs())
}

// A confirmation that comes up while the model reads the screen for an
// extract is cleared, and the extract read again, so its answer binds to the
// window it read rather than one the confirmation covers.
func TestHandlerClearsAScreenThatComesUpWhileTheModelExtracts(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	_, dialogElements := confirmDialog()
	pressingYesShows(run, 0)
	asked := 0
	run.vision.onChat = func() {
		asked++
		if asked == 1 {
			run.elements.show(expenseWindow(), append(expenseElements(), dialogElements...))
		}
	}
	run.vision.extract = `{"amount": {"value": 12000, "element": "e3"}}`
	steps := `{"idle": "0", "input": "direct", "handle": ` + confirmHandler("") + `, "do": [{"extract": {"instruction": "The amount", "schema": {"type": "object", "properties": {"amount": {"type": "number"}}}}}]}`
	execution := run.execute(steps, nil)
	require.NoError(t, execution.err)
	assert.Equal(t, []string{"invoke はい"}, run.backend.inputs())
	assert.Equal(t, 2, asked, "the extract was read again after the confirmation was cleared")
	assert.EqualValues(t, 12000, execution.exec.GetOutputs()["amount"])
}

// A confirmation that one replayed pixel click brings up is cleared before
// the turn's next click, which would otherwise land on it.
func TestHandlerClearsAScreenBetweenPixelClicks(t *testing.T) {
	t.Parallel()

	const learn = `{"do": [{"act": "Click the canvas twice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(10, 10), clickAt(20, 20)), done("Clicked")}}}
	require.NoError(t, run.execute(learn, nil).err)
	require.Len(t, lookupRecording(t, run, "Click the canvas twice").Turns[0].Actions, 2)

	_, dialogElements := confirmDialog()
	pressed := 0
	run.backend.onPress = func() {
		pressed++
		if pressed == 1 {
			run.elements.show(expenseWindow(), append(expenseElements(), dialogElements...))
		}
	}
	pressingYesShows(run, 0)
	run.backend.events = nil
	steps := `{"ai": "never", "input": "direct", "handle": ` + confirmHandler("") + `, "do": [{"act": "Click the canvas twice"}]}`
	require.NoError(t, run.execute(steps, nil).err)
	assert.Equal(t, []string{"move 10,10", "left down #1", "invoke はい", "move 20,20", "left down #1"}, run.backend.inputs())
}

// A handler that moves the window between two replayed pixel clicks leaves
// the second click where the turn landed on the window, not where the
// window was.
func TestHandlerThatMovesTheWindowBetweenPixelClicks(t *testing.T) {
	t.Parallel()

	const learn = `{"maximize": false, "do": [{"act": "Click the report twice"}]}`
	run := windowRun(t, reportWindow.Bounds)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(150, 160), clickAt(200, 200)), done("Clicked")}}}
	require.NoError(t, run.execute(learn, nil).err)
	require.Len(t, lookupRecording(t, run, "Click the report twice").Turns[0].Actions, 2)

	run.elements = newFakeElements()
	dialog, dialogElements := confirmDialog()
	pressed := 0
	run.backend.onPress = func() {
		pressed++
		if pressed == 1 {
			run.elements.show(dialog, dialogElements)
		}
	}
	run.elements.onApply = func(e desktop.Element, pattern desktop.Pattern, _ string) {
		run.backend.recordAccessibility(string(pattern) + " " + focusedID(e))
		if e.Name == "はい" {
			run.placeWindow(reportWindow.Bounds.Add(image.Pt(120, 80)))
			run.elements.show(expenseWindow(), expenseElements())
		}
	}
	run.backend.events = nil
	steps := `{"ai": "never", "input": "direct", "maximize": false, "handle": ` + confirmHandler("") + `, "do": [{"act": "Click the report twice"}]}`
	require.NoError(t, run.execute(steps, nil).err)
	assert.Equal(t, []string{"move 150,160", "left down #1", "invoke はい", "move 320,280", "left down #1"}, run.backend.inputs())
}

// A confirmation that a replayed click brings up is cleared before the
// replay compares the screen it ended on with the recording.
func TestHandlerClearsAScreenAReplayEndsOn(t *testing.T) {
	t.Parallel()

	const learn = `{"do": [{"act": "Click the canvas"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(10, 10)), done("Clicked")}}}
	require.NoError(t, run.execute(learn, nil).err)
	require.Nil(t, lookupRecording(t, run, "Click the canvas").Turns[0].Actions[0].Element, "the turn replays by pixels")

	recorded := pattern(800, 600, 0)
	_, dialogElements := confirmDialog()
	run.backend.onPress = func() {
		run.backend.show(pattern(800, 600, 150))
		run.elements.show(expenseWindow(), append(expenseElements(), dialogElements...))
	}
	pressingYesShows(run, 0)
	onApply := run.elements.onApply
	run.elements.onApply = func(e desktop.Element, p desktop.Pattern, v string) {
		onApply(e, p, v)
		if e.Name == "はい" {
			run.backend.show(recorded)
		}
	}
	run.backend.events = nil
	steps := `{"ai": "never", "input": "direct", "handle": ` + confirmHandler("") + `, "do": [{"act": "Click the canvas"}]}`
	require.NoError(t, run.execute(steps, nil).err)
	assert.Equal(t, []string{"move 10,10", "left down #1", "invoke はい"}, run.backend.inputs())
}

// A confirmation is handled before a bound table is read from the window.
func TestHandlerClearsAScreenBeforeATableIsRead(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	withTable(run)
	run.elements.tableRows = [][]string{{"りんご", "1,200"}}
	table := run.elements.elements
	dialog, dialogElements := confirmDialog()
	run.elements.show(dialog, dialogElements)
	pressingYesShows(run, 0)
	onApply := run.elements.onApply
	run.elements.onApply = func(e desktop.Element, p desktop.Pattern, v string) {
		onApply(e, p, v)
		if e.Name == "はい" {
			run.elements.show(expenseWindow(), table)
		}
	}
	steps := `{"ai": "never", "input": "direct", "handle": ` + confirmHandler("") + `, "do": [{"extract": {"instruction": "The invoice lines",
		"schema": {"type": "object", "properties": {"lines": {"type": "array", "items": {"type": "object", "properties": {"item": {"type": "string"}, "amount": {"type": "number"}}}}}},
		"fields": {"windows": {"lines": {"container": {"role": "list", "name": "明細"}, "columns": {"item": "品目", "amount": "金額"}}}}}}]}`
	execution := run.execute(steps, nil)
	require.NoError(t, execution.err)
	assert.Equal(t, []string{"invoke はい"}, run.backend.inputs())
	assert.Equal(t, []any{map[string]any{"item": "りんご", "amount": int64(1200)}}, execution.exec.GetOutputs()["lines"])
}

// A pinned choice from a list box clicks the box open on the screen and
// chooses the option through its own pattern: the open menu is drawn apart
// from the window, where the option has no place a click could aim at.
func TestPinnedOptionIsChosenThroughItsPattern(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	box := desktop.PathStep{Role: desktop.RoleComboBox, Name: "支払方法", Index: 0}
	run.elements.elements = append(run.elements.elements,
		desktop.Element{Role: desktop.RoleComboBox, Name: "支払方法", ID: "methodBox", Bounds: image.Rect(190, 230, 390, 256), Path: []desktop.PathStep{box}},
		desktop.Element{Role: desktop.RoleMenuItem, Name: "クレジット", Bounds: image.Rect(190, 230, 390, 235), Path: []desktop.PathStep{box, {Role: desktop.RoleMenuItem, Name: "クレジット", Index: 1}}},
	)
	const steps = `{"ai": "never", "do": [{"act": {"instruction": "Pay by credit", "recorded": {"windows": [{"click": {"role": "combo_box", "id": "methodBox"}}, {"click": {"role": "menu_item", "name": "クレジット"}}]}}}]}`
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"move 290,243", "left down #1", "invoke クレジット"}, run.backend.inputs())
}

// The keys a pattern sends, as choosing an option of Chromium's list box
// does, are the step's own input: the next action does not wait for a
// person to stop using the desktop.
func TestPatternInputIsTheSteps(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.elements.onApply = func(desktop.Element, desktop.Pattern, string) {
		run.backend.mu.Lock()
		run.backend.personInputAt = time.Now()
		run.backend.mu.Unlock()
	}
	const steps = `{"ai": "never", "input": "direct", "idle": "1s", "do": [` +
		`{"act": {"instruction": "Save", "recorded": {"windows": [{"click": {"role": "button", "id": "saveButton"}}]}}},` +
		`{"act": {"instruction": "Save the payment", "recorded": {"windows": [{"click": {"role": "button", "id": "paymentSaveButton"}}]}}}]}`
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.NotContains(t, strings.Join(lifecycleMessages(replayed.exec.GetAgentSession()), "\n"), "Waiting until nobody")
}

// A pattern that may have acted counts as the step's input; one refused
// before it acted does not, so a person's input just before it still holds
// the step back.
func TestApplyPatternCountsOnlyInputItMayHaveSent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		err    error
		counts bool
	}{
		{"done", nil, true},
		{"failed after acting", errors.New("the menu stayed open after its option was chosen"), true},
		{"not there", desktop.ErrNotFound, false},
		{"disabled", desktop.ErrDisabled, false},
		{"no such pattern", desktop.ErrNoPattern, false},
		{"a read-only field", fmt.Errorf("%w: the field is read-only", desktop.ErrRefused), false},
		{"nothing to expand", fmt.Errorf("%w: the element has nothing to expand", desktop.ErrRefused), false},
	} {
		els := newFakeElements()
		if tc.err != nil {
			els.failPattern("saveButton", tc.err)
		}
		save, err := els.Find(desktop.Selector{Role: desktop.RoleButton, ID: "saveButton"})
		require.NoError(t, err)
		r := &run{driver: desktop.New(newFakeBackend(800, 600))}
		_ = r.applyPattern(els, save[0], desktop.PatternInvoke, "")
		assert.Equal(t, tc.counts, !r.driver.InputSentAt().IsZero(), tc.name)
	}
}

// A pinned step that misses fails under never, naming why, and clicks
// nothing.
func TestPinnedActMissFailsUnderNever(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "do": [{"act": {"instruction": "Save the invoice", "recorded": {"windows": [{"click": {"role": "button", "name": "送信", "id": "submit"}}]}}}]}`
	run := elementRun(t)
	failed := run.execute(steps, nil)
	require.ErrorContains(t, failed.err, "the act cannot run without AI")
	assert.Equal(t, agentstep.ViaElement, operationEvents(failed.exec.GetAgentSession())[0].Via)
	assert.Empty(t, run.backend.inputs())
}

// Under on_miss, a pinned step that misses hands off to the model.
func TestPinnedActHealsUnderOnMiss(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": {"instruction": "Save the invoice", "recorded": {"windows": [{"click": {"role": "button", "name": "送信", "id": "submit"}}]}}}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	healed := run.execute(steps, nil)
	require.NoError(t, healed.err)
	assert.Equal(t, agentstep.StatusHealed, operationEvents(healed.exec.GetAgentSession())[0].Status)
}

// A pin for another system is ignored, so a never act with only a macos pin
// misses on this Windows host as if unpinned.
func TestPinnedActIgnoresOtherSystem(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "do": [{"act": {"instruction": "Save the invoice", "recorded": {"macos": [{"click": {"role": "button", "name": "保存", "id": "saveButton"}}]}}}]}`
	run := elementRun(t)
	failed := run.execute(steps, nil)
	require.ErrorContains(t, failed.err, "there is no recording of it on this host")
}

// A pinned extract reads its fields with no model under never.
func TestPinnedExtractReads(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "do": [{"extract": {"instruction": "The amount", "schema": {"type": "object", "properties": {"amount": {"type": "number"}}}, "fields": {"windows": {"amount": {"role": "text_field", "id": "amountBox"}}}}}]}`
	run := elementRun(t)
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.EqualValues(t, 12000, replayed.exec.GetOutputs()["amount"])
	assert.Equal(t, agentstep.ViaElement, operationEvents(replayed.exec.GetAgentSession())[0].Via)
	assert.Empty(t, run.vision.requests, "no model request")
}

// A host that reads no elements, such as a 32-bit Windows, misses a pinned
// act rather than failing it: under never it reports the miss
// naming the cause, so on_miss can still hand the act to the model.
func TestPinnedActMissesWithoutElements(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "do": [{"act": {"instruction": "Save the invoice", "recorded": {"windows": [{"click": {"role": "button", "name": "保存", "id": "saveButton"}}]}}}]}`
	run := newTestRun(t) // no elements reader on this host
	failed := run.execute(steps, nil)
	require.ErrorContains(t, failed.err, "pinned steps need the window's elements")
	assert.Equal(t, agentstep.ViaElement, operationEvents(failed.exec.GetAgentSession())[0].Via)
}

// Under on_miss, a pinned act on a host that reads no elements hands off to
// the model rather than failing.
func TestPinnedActWithoutElementsHealsUnderOnMiss(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": {"instruction": "Save the invoice", "recorded": {"windows": [{"click": {"role": "button", "name": "保存", "id": "saveButton"}}]}}}]}`
	run := newTestRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(1, 1)), done("Saved")}}}
	healed := run.execute(steps, nil)
	require.NoError(t, healed.err)
	assert.Equal(t, agentstep.StatusHealed, operationEvents(healed.exec.GetAgentSession())[0].Status)
}

// A pinned act waits until nobody has used the desktop for the idle window
// before each input, as a learned replay does, so its click never lands in a
// person's window.
func TestPinnedActWaitsForIdleDesktop(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "idle": "100ms", "do": [{"act": {"instruction": "Save the invoice", "recorded": {"windows": [{"click": {"role": "button", "name": "保存", "id": "saveButton"}}]}}}]}`
	run := elementRun(t)
	run.backend.personKeepsUsing(2)
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	session := replayed.exec.GetAgentSession()
	assert.Contains(t, strings.Join(lifecycleMessages(session), "\n"), "Waiting until nobody has used the desktop for 100ms")
	assert.Equal(t, []string{waitReasonPerson}, waitReasons(session))
	assert.Equal(t, []string{"move 235,194", "left down #1"}, run.backend.inputs(), "the pinned click lands once the person stops")
}

// A person who leaves another application in front partway through a
// pinned act holds the act until they stop; the act then brings back the
// application it was working in and carries on there. Without a person, a
// change of application is the workflow's own, and the act leaves it.
func TestPinnedActReturnsToItsApplicationAfterAPerson(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "idle": "100ms", "input": "direct", "find_within": "300ms", "do": [{"act": {"instruction": "Save the invoice and the payment", "recorded": {"windows": [
		{"click": {"role": "button", "id": "saveButton"}},
		{"click": {"role": "button", "id": "paymentSaveButton"}}]}}}]}`
	for _, tc := range []struct {
		name   string
		person bool
	}{
		{"after a person", true},
		{"without a person", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := elementRun(t)
			form := expenseWindow()
			expense := desktop.WindowID{Handle: 2, Title: form.Name, App: "expense"}
			notes := desktop.WindowID{Handle: 1, Title: "メモ", App: "notes"}
			run.backend.windows = []desktop.WindowID{notes, expense}
			var applied []string
			run.elements.onApply = func(e desktop.Element, _ desktop.Pattern, _ string) {
				applied = append(applied, e.ID)
				if len(applied) > 1 {
					return
				}
				// After the first save, another application comes to the front;
				// its window shows none of the form's elements.
				if tc.person {
					run.backend.personUsesAfterEcho()
				}
				run.backend.focusOn(notes)
				run.elements.asApp(notes.App)
				run.elements.retitle(notes.Title)
				run.elements.arriveAfter(0, []desktop.Element{})
			}
			run.backend.onRaise = func(w desktop.WindowID) {
				if w.Handle == expense.Handle {
					run.elements.asApp(expense.App)
					run.elements.retitle(expense.Title)
					run.elements.arriveAfter(0, expenseElements())
				}
			}

			execution := run.execute(steps, nil)
			if !tc.person {
				require.Error(t, execution.err, "the payment's button is not in the window in front")
				assert.Empty(t, run.backend.raised, "the step leaves the application its own click brought forward")
				assert.Equal(t, []string{"saveButton"}, applied)
				return
			}
			require.NoError(t, execution.err)
			assert.Contains(t, strings.Join(lifecycleMessages(execution.exec.GetAgentSession()), "\n"), "Waiting until nobody has used the desktop for 100ms")
			require.Len(t, run.backend.raised, 1)
			assert.Equal(t, expense.Handle, run.backend.raised[0].Handle, "the application the act was working in came back")
			assert.Equal(t, []string{"saveButton", "paymentSaveButton"}, applied)
		})
	}
}

// A person who leaves another application in front between two steps of a
// run holds the second step until they stop; it then brings back the
// application the first step worked in. A step of another run has no such
// application to bring back.
func TestNextStepReturnsToTheRunsApplicationAfterAPerson(t *testing.T) {
	t.Parallel()

	const first = `{"ai": "never", "idle": "100ms", "find_within": "300ms", "do": [{"act": {"instruction": "Save the invoice", "recorded": {"windows": [{"click": {"role": "button", "id": "saveButton"}}]}}}]}`
	const second = `{"ai": "never", "idle": "100ms", "find_within": "300ms", "do": [{"act": {"instruction": "Save the payment", "recorded": {"windows": [{"click": {"role": "button", "id": "paymentSaveButton"}}]}}}]}`
	for _, tc := range []struct {
		name     string
		otherRun bool
		between  bool
	}{
		{"in the same run", false, false},
		{"in another run", true, false},
		{"after another run held the desktop", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := elementRun(t)
			form := expenseWindow()
			expense := desktop.WindowID{Handle: 2, Title: form.Name, App: "expense"}
			notes := desktop.WindowID{Handle: 1, Title: "メモ", App: "notes"}
			run.backend.windows = []desktop.WindowID{notes, expense}
			run.backend.onRaise = func(w desktop.WindowID) {
				if w.Handle == expense.Handle {
					run.elements.asApp(expense.App)
					run.elements.retitle(expense.Title)
					run.elements.arriveAfter(0, expenseElements())
				}
			}
			require.NoError(t, run.execute(first, nil).err)
			if tc.between {
				// A step of another run holds the desktop and finds nothing.
				run.runID = "run-2"
				require.NoError(t, run.execute(`{"ai": "never", "do": [{"wait": "10ms"}]}`, nil).err)
				run.runID = ""
			}

			// Between the steps, a person works in another application.
			run.backend.personUsesAfterEcho()
			run.backend.focusOn(notes)
			run.elements.asApp(notes.App)
			run.elements.retitle(notes.Title)
			run.elements.arriveAfter(0, []desktop.Element{})
			if tc.otherRun {
				run.runID = "run-2"
			}
			execution := run.execute(second, nil)
			if tc.otherRun || tc.between {
				require.Error(t, execution.err, "the payment's button is not in the window in front")
				assert.Empty(t, run.backend.raised)
				return
			}
			require.NoError(t, execution.err)
			require.Len(t, run.backend.raised, 1)
			assert.Equal(t, expense.Handle, run.backend.raised[0].Handle, "the application the first step worked in came back")
		})
	}
}

// A person's use of the desktop does not count against a check's or an
// extract's timeout: either waits for them and then reads, however long
// they took beyond its timeout.
func TestPersonDoesNotRunOutACheckOrAnExtract(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		op   string
	}{
		{"expect", `{"expect": {"element": {"role": "button", "id": "saveButton"}}, "timeout": "100ms"}`},
		{"extract", `{"extract": {"instruction": "The amount", "schema": {"type": "object", "properties": {"amount": {"type": "string"}}}, "fields": {"windows": {"amount": {"role": "text_field", "id": "amountBox"}}}}, "timeout": "100ms"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := elementRun(t)
			// The person keeps using the desktop for well over the timeout.
			run.backend.personKeepsUsing(500)
			began := time.Now()
			execution := run.execute(`{"ai": "never", "idle": "20ms", "do": [`+tc.op+`]}`, nil)
			require.NoError(t, execution.err)
			require.Greater(t, time.Since(began), 200*time.Millisecond, "the person used the desktop beyond the timeout")
			assert.Contains(t, strings.Join(lifecycleMessages(execution.exec.GetAgentSession()), "\n"), "Waiting until nobody has used the desktop")
		})
	}
}

// A wait between two looks at the screen ends as soon as a person starts
// using the desktop, so the next look makes way for them at once; with
// nobody there it waits its whole time.
func TestPollSleepEndsWhenAPersonStarts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		person bool
	}{
		{"a person starts", true},
		{"nobody", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			backend := newFakeBackend(800, 600)
			r := &run{exec: &computerExecutor{}, cfg: config{Idle: "100ms"}, driver: desktop.New(backend)}
			if tc.person {
				time.AfterFunc(100*time.Millisecond, backend.personUsesAfterEcho)
			}
			began := time.Now()
			require.NoError(t, r.pollSleep(t.Context(), 2*time.Second))
			if tc.person {
				assert.Less(t, time.Since(began), time.Second, "the wait ended when the person started")
				return
			}
			assert.GreaterOrEqual(t, time.Since(began), 2*time.Second)
		})
	}
}

func elementRun(t *testing.T) *testRun {
	t.Helper()
	run := newTestRun(t)
	run.backend = newFakeBackend(800, 600)
	run.backend.app = "expense" // the application a recording's actions land on
	run.elements = newFakeElements()
	return run
}

// focusOn gives an element of the fake window the keyboard focus, as a
// click would in a real application.
func (r *testRun) focusOn(id string) {
	r.elements.mu.Lock()
	defer r.elements.mu.Unlock()
	for _, e := range r.elements.elements {
		if e.ID == id {
			r.elements.focused, r.elements.hasFocus = e, true
			return
		}
	}
	panic("no element " + id)
}

// lookupRecording reads what the step recorded for its first act.
func lookupRecording(t *testing.T, run *testRun, instruction string) recording {
	t.Helper()
	cache := openReplayCache(filepath.Join(run.dataDir, computerhost.DataDirName), "invoices", "post")
	entry, ok := cache.Lookup(replayKey(0, instruction))
	require.True(t, ok, "no recording for %q", instruction)
	return entry
}

func typing(text string) computeruse.Action {
	return computeruse.Action{Kind: computeruse.KindType, Text: text}
}

func withoutElement(elements []desktop.Element, id string) []desktop.Element {
	var out []desktop.Element
	for _, e := range elements {
		if e.ID != id {
			out = append(out, e)
		}
	}
	return out
}

// A recording keeps the element each action landed on or typed into,
// beside the pixels, and the landmarks of the screen it ended on.
func TestRecordsElementsBesidePixels(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Post the invoice"}]}`
	run := elementRun(t)
	run.focusOn("amountBox")
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(290, 152)), actions(typing("12,000")), actions(clickAt(235, 194)), done("Posted")}}}
	require.NoError(t, run.execute(steps, nil).err)

	entry := lookupRecording(t, run, "Post the invoice")
	assert.Equal(t, recordingVersion, entry.Version)
	require.Len(t, entry.Turns, 3)
	field := entry.Turns[0].Actions[0]
	require.NotNil(t, field.Element)
	assert.Equal(t, desktop.Selector{Role: desktop.RoleTextField, ID: "amountBox"}, field.Element.Selector)
	assert.InDelta(t, 0.5, field.Element.FractionX, 0.01)
	assert.InDelta(t, 0.5, field.Element.FractionY, 0.01)
	assert.Equal(t, []desktop.PathStep{{Role: desktop.RoleTextField, Index: 0}}, field.Element.Path)
	assert.Equal(t, "経費精算 - 請求書 1042", field.Element.Window)
	assert.NotNil(t, field.Target, "the pixels are kept too")
	typed := entry.Turns[1].Actions[0]
	require.NotNil(t, typed.Element, "typing records the focused element")
	assert.Equal(t, "amountBox", typed.Element.Selector.ID)
	assert.Zero(t, typed.Element.FractionX)
	button := entry.Turns[2].Actions[0]
	require.NotNil(t, button.Element)
	assert.Equal(t, desktop.Selector{Role: desktop.RoleButton, Name: "保存", ID: "saveButton"}, button.Element.Selector)
	assert.Equal(t, "経費精算 - 請求書 1042", entry.Window)
	require.NotEmpty(t, entry.Landmarks)
	assert.Equal(t, "saveButton", entry.Landmarks[0].Selector.ID, "the element touched last comes first")
}

// A replay finds its elements where they are now, so a moved window and
// changed pixels still replay, by element.
func TestReplaysByElementWhenPixelsChanged(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.backend.show(pattern(800, 600, 150))
	run.elements.shift(37, 11)
	run.backend.events = nil
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err, "no session is left, so a model call would fail")
	events := operationEvents(replayed.exec.GetAgentSession())
	require.Len(t, events, 1)
	assert.Equal(t, agentstep.StatusCacheHit, events[0].Status)
	assert.Equal(t, agentstep.ViaElement, events[0].Via)
	assert.Equal(t, []string{"move 272,205", "left down #1"}, run.backend.inputs(), "the click follows the button")
	assert.Contains(t, replayed.stderr.String(), `replay by element: click button "保存" at 272,205`)
}

// A replay brings the recorded window to the front when it lost the focus,
// rather than counting the step a miss.
func TestReplayBringsTheWindowForward(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	// Before the replay a different application is in front, with the
	// recorded one behind it; the recorded window is found by its app among
	// the open windows and raised, which brings its elements back.
	run.elements.asApp("other")
	run.elements.retitle("別のアプリ")
	run.backend.focus = desktop.WindowID{Handle: 1, Title: "別のアプリ", App: "other"}
	run.backend.windows = []desktop.WindowID{
		{Handle: 1, Title: "別のアプリ", App: "other"},
		{Handle: 2, Title: "経費精算 - 請求書 1042", App: "expense"},
	}
	run.backend.onRaise = func(w desktop.WindowID) {
		if w.Handle == 2 {
			run.elements.asApp("expense")
			run.elements.retitle("経費精算 - 請求書 1042")
		}
	}
	run.backend.events = nil

	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err, "the recorded window was raised by its app, so the replay resolved its button")
	events := operationEvents(replayed.exec.GetAgentSession())
	require.Len(t, events, 1)
	assert.Equal(t, agentstep.StatusCacheHit, events[0].Status)
	assert.Equal(t, agentstep.ViaElement, events[0].Via)
	require.Len(t, run.backend.raised, 1)
	assert.Equal(t, uint64(2), run.backend.raised[0].Handle, "the window of the recorded app was brought forward")
	assert.Equal(t, []string{"move 235,194", "left down #1"}, run.backend.inputs())
}

// When two windows of the recorded application are open, the one whose title
// still matches the recording is raised, even if a different document of the
// same application has the focus, so a replay acts on the right one.
func TestReplayPrefersRecordedTitleAmongSameApp(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	// A second document of the same application has the focus; the recorded
	// one is behind it with its recorded title.
	const recorded = "経費精算 - 請求書 1042"
	run.elements.retitle("経費精算 - 請求書 9999")
	run.backend.focus = desktop.WindowID{Handle: 1, Title: "経費精算 - 請求書 9999", App: "expense"}
	run.backend.windows = []desktop.WindowID{
		{Handle: 1, Title: "経費精算 - 請求書 9999", App: "expense"},
		{Handle: 2, Title: recorded, App: "expense"},
	}
	run.backend.onRaise = func(w desktop.WindowID) {
		if w.Handle == 2 {
			run.elements.retitle(recorded)
		}
	}
	run.backend.events = nil

	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	events := operationEvents(replayed.exec.GetAgentSession())
	require.Len(t, events, 1)
	assert.Equal(t, agentstep.StatusCacheHit, events[0].Status)
	assert.Equal(t, agentstep.ViaElement, events[0].Via)
	require.Len(t, run.backend.raised, 1)
	assert.Equal(t, uint64(2), run.backend.raised[0].Handle, "the recorded-title window was raised, not the focused one")
}

// A replay matches the recorded window by its application, so a title that
// changed since the recording — a document name, a per-run id — still
// replays without the model.
func TestReplayMatchesWindowByApp(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	// The window shows a different number now; its title no longer contains
	// what was recorded, but it is the same application.
	run.elements.retitle("経費精算 - 請求書 9999")
	run.backend.events = nil

	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err, "the app still matches, so the element resolved")
	events := operationEvents(replayed.exec.GetAgentSession())
	require.Len(t, events, 1)
	assert.Equal(t, agentstep.StatusCacheHit, events[0].Status)
	assert.Equal(t, agentstep.ViaElement, events[0].Via)
	assert.Empty(t, run.backend.raised, "the window was already in front")
	assert.Equal(t, []string{"move 235,194", "left down #1"}, run.backend.inputs())
}

// A replay that finds a different application in front, with no window of
// the recorded app to raise, misses and names the app rather than acting in
// the wrong program.
func TestReplayMissesOnDifferentApp(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	// A different application is in front and there is nothing of the
	// recorded app to bring forward.
	run.elements.asApp("other")
	run.elements.retitle(" browser ")
	run.backend.events = nil

	failed := run.execute(`{"ai": "never", "do": [{"act": "Save the invoice"}]}`, nil)
	require.ErrorContains(t, failed.err, `the app in front is "other", not "expense"`)
	assert.Empty(t, run.backend.inputs(), "nothing was clicked in the wrong app")
}

// A recording from before window matching knew the application — or a host
// that cannot read it — has no app, and still replays by matching the
// window title, so older recordings keep working without re-authoring.
func TestReplayFallsBackToTitleWithoutApp(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	// The host reports no application, for the recording and for replay.
	run.backend.app = ""
	run.elements.asApp("")
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.backend.events = nil
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err, "with no app, the title still matches")
	events := operationEvents(replayed.exec.GetAgentSession())
	require.Len(t, events, 1)
	assert.Equal(t, agentstep.StatusCacheHit, events[0].Status)
	assert.Equal(t, agentstep.ViaElement, events[0].Via)
	assert.Equal(t, []string{"move 235,194", "left down #1"}, run.backend.inputs())

	// With the app unknown, a changed title now misses, as before.
	run.elements.retitle("経費精算 - 請求書 9999")
	failed := run.execute(`{"ai": "never", "do": [{"act": "Save the invoice"}]}`, nil)
	require.ErrorContains(t, failed.err, "the window in front is")
}

// The display size is not part of the recording's key, so an element
// replay runs on another display.
func TestReplaysOnAnotherDisplaySize(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.backend = newFakeBackend(1024, 768)
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()))
	assert.Equal(t, []string{"move 235,194", "left down #1"}, run.backend.inputs())
}

// A replay waits up to find_within for an element that is not there yet.
func TestReplayWaitsForElement(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": {"instruction": "Save the invoice", "find_within": "2s"}}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	all := expenseElements()
	run.elements.elements = withoutElement(all, "saveButton")
	run.elements.arriveAfter(2, all)
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()))
	assert.GreaterOrEqual(t, run.elements.reads, 3)
}

// Under never, a missing element fails the act naming the element, the
// wait, the turn, and why.
func TestNeverMissNamesElement(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.elements.elements = withoutElement(expenseElements(), "saveButton")
	run.backend.events = nil
	failed := run.execute(`{"ai": "never", "do": [{"act": "Save the invoice"}]}`, nil)
	require.ErrorContains(t, failed.err, `the act cannot run without AI: button "保存" was not found within 0s at turn 1 of 1: it is not there`)
	events := operationEvents(failed.exec.GetAgentSession())
	assert.Equal(t, agentstep.ViaElement, events[0].Via)
	assert.Empty(t, run.backend.inputs(), "nothing is clicked")

	run.elements.elements = expenseElements()
	run.elements.elements = append(run.elements.elements, desktop.Element{Role: desktop.RoleButton, Name: "保存", ID: "thirdSave", Bounds: image.Rect(500, 180, 590, 208), Path: []desktop.PathStep{{Role: desktop.RoleButton, Name: "保存", Index: 5}}})
	ambiguous := run.execute(`{"ai": "never", "do": [{"act": "Save the invoice"}]}`, nil)
	require.NoError(t, ambiguous.err, "the recorded path picks the button where it was")
}

// A turn whose actions landed on no element replays by pixels, as before.
func TestReplayFallsBackToPixelsWithoutElements(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Click the canvas"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(10, 10)), done("Clicked")}}}
	require.NoError(t, run.execute(steps, nil).err)
	entry := lookupRecording(t, run, "Click the canvas")
	assert.Nil(t, entry.Turns[0].Actions[0].Element, "nothing is under the point")

	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	events := operationEvents(replayed.exec.GetAgentSession())
	assert.Equal(t, agentstep.ViaScreen, events[0].Via)

	run.backend.show(pattern(800, 600, 150))
	failed := run.execute(`{"ai": "never", "do": [{"act": "Click the canvas"}]}`, nil)
	require.ErrorContains(t, failed.err, "the screen differs from the recording at turn 1 of 1")
}

// Typing replays into the field that had the focus, which is focused
// again first.
func TestReplayFocusesRecordedField(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Enter the amount"}]}`
	run := elementRun(t)
	run.focusOn("amountBox")
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(290, 152), typing("12,000")), done("Entered")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.elements.shift(37, 11)
	run.backend.events = nil
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"move 327,163", "left down #1", "focus amountBox", "type 12,000"}, run.backend.inputs())
}

// The model is told what a visible placeholder stands for, so it can find or
// choose the value on the screen, as an item in a list; a value that is not
// visible, such as a password, it never sees.
func TestActShowsTheModelVisibleValues(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{done("Chose the item")}}}
	execution := run.execute(`{"idle": "0", "variables": {"item": "Toner T-20", "password": "s3cret"}, "visible": ["item"], "do": [{"act": "Sign in with %password% and choose %item%"}]}`, nil)
	require.NoError(t, execution.err)

	require.Len(t, run.sessionOptions, 1)
	assert.Contains(t, run.sessionOptions[0].System, `%item% is "Toner T-20"`)
	assert.NotContains(t, run.sessionOptions[0].System, "s3cret")

	// A declared secret is never made visible.
	run.secrets = map[string]string{"PORTAL_PASSWORD": "s3cret"}
	refused := run.execute(`{"variables": {"password": "s3cret"}, "visible": ["password"], "do": [{"act": "Sign in with %password%"}]}`, nil)
	require.ErrorContains(t, refused.err, "with.variables.password contains the value of secret PORTAL_PASSWORD")
	assert.Len(t, run.sessionOptions, 1, "no model was asked")
}

// An element named by a variable's value is recorded by the placeholder,
// so the next run, with another value, clicks the element named by it.
func TestReplayRecordsPlaceholderForElementName(t *testing.T) {
	t.Parallel()

	month := func(name string) desktop.Element {
		return desktop.Element{Role: desktop.RoleListItem, Name: name, Bounds: image.Rect(120, 420, 300, 444), Path: []desktop.PathStep{{Role: desktop.RoleListItem, Name: name, Index: 0}}}
	}
	run := elementRun(t)
	run.elements.elements = append(expenseElements(), month("2026-03"))
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(210, 432)), done("Opened")}}}
	require.NoError(t, run.execute(`{"variables": {"month": "2026-03"}, "do": [{"act": "Open the month"}]}`, nil).err)
	entry := lookupRecording(t, run, "Open the month")
	assert.Equal(t, "%month%", entry.Turns[0].Actions[0].Element.Selector.Name)

	run.elements.elements = append(expenseElements(), month("２０２６-04 "))
	run.backend.events = nil
	replayed := run.execute(`{"variables": {"month": "2026-04"}, "do": [{"act": "Open the month"}]}`, nil)
	require.NoError(t, replayed.err, "the item's name is matched as a person reads it")
	assert.Equal(t, []string{"act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()))
	assert.Equal(t, []string{"move 210,432", "left down #1"}, run.backend.inputs())

	padded := elementRun(t)
	padded.elements.elements = append(expenseElements(), month(" 2026-05"))
	padded.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(210, 432)), done("Opened")}}}
	require.NoError(t, padded.execute(`{"variables": {"month": "2026-05"}, "do": [{"act": "Open the month"}]}`, nil).err)
	assert.Equal(t, "%month%", lookupRecording(t, padded, "Open the month").Turns[0].Actions[0].Element.Selector.Name, "a padded name still records the placeholder")
}

// An element replay ends by checking its landmarks: the window in front
// and the elements the act touched last.
func TestElementReplayNeedsLandmarks(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	// The button is there for the click and gone for the final check.
	run.elements.arriveAfter(1, withoutElement(expenseElements(), "saveButton"))
	failed := run.execute(`{"ai": "never", "do": [{"act": "Save the invoice"}]}`, nil)
	require.ErrorContains(t, failed.err, `button "保存" is not there after the last turn`)

	run.elements.elements = expenseElements()
	run.elements.arriveAfter(1, withoutElement(expenseElements(), "saveButton"))
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{done("Already saved")}}}
	healed := run.execute(steps, nil)
	require.NoError(t, healed.err)
	assert.Equal(t, []string{"act:healed"}, eventNames(healed.exec.GetAgentSession()))
}

// Without landmarks, or with a turn that replayed by pixels, a replay ends
// on the recorded final screen.
func TestReplayWithoutLandmarksChecksTheScreen(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	// The button vanishes right after the click, so nothing is a landmark.
	run.elements.arriveAfter(1, withoutElement(expenseElements(), "saveButton"))
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)
	entry := lookupRecording(t, run, "Save the invoice")
	assert.Empty(t, entry.Landmarks)
	assert.Equal(t, "経費精算 - 請求書 1042", entry.Window)

	run.elements.elements = expenseElements()
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()), "the final screen still looks as recorded")

	run.backend.show(pattern(800, 600, 150))
	failed := run.execute(`{"ai": "never", "do": [{"act": "Save the invoice"}]}`, nil)
	require.ErrorContains(t, failed.err, "the screen after the last turn differs from the recording")
}

// A find_within that cannot be read when the act runs fails the act rather
// than waiting some other time.
func TestFindWithinMustResolve(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	failed := run.execute(`{"find_within": "${WAIT}", "do": [{"act": "Save the invoice"}]}`, nil)
	require.ErrorContains(t, failed.err, `find_within "${WAIT}" must be a duration`)
}

// Under input: direct, a recorded click on a button replays by having the
// button invoke itself: no pointer moves, the window need not be where it
// was, and the act still reports a replay by element.
func TestDirectReplayInvokesButton(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.backend.show(pattern(800, 600, 150))
	run.elements.shift(37, 11)
	run.backend.events = nil
	replayed := run.execute(`{"input": "direct", "do": [{"act": "Save the invoice"}]}`, nil)
	require.NoError(t, replayed.err)
	events := operationEvents(replayed.exec.GetAgentSession())
	require.Len(t, events, 1)
	assert.Equal(t, agentstep.StatusCacheHit, events[0].Status)
	assert.Equal(t, agentstep.ViaElement, events[0].Via)
	assert.Equal(t, []string{"invoke saveButton"}, run.backend.inputs(), "the button is invoked, not clicked")
	assert.Contains(t, replayed.stderr.String(), `replay by element: invoke button "保存"`)
}

// A typing whose text became the field's value is recorded as setting it,
// so a direct replay sets the value at once; one whose text did not is
// typed again, after a focus.
func TestDirectReplaySetsValue(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Enter the amount"}]}`
	run := elementRun(t)
	run.focusOn("amountBox")
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(290, 152), typing("12,000")), done("Entered")}}}
	require.NoError(t, run.execute(steps, nil).err)
	entry := lookupRecording(t, run, "Enter the amount")
	require.Len(t, entry.Turns[0].Actions, 2)
	assert.False(t, entry.Turns[0].Actions[0].Element.SetsValue, "a click sets nothing")
	assert.True(t, entry.Turns[0].Actions[1].Element.SetsValue, "the field showed the typed text")

	run.backend.events = nil
	replayed := run.execute(`{"input": "direct", "do": [{"act": "Enter the amount"}]}`, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()))
	assert.Equal(t, []string{"focus amountBox", "set amountBox=12,000"}, run.backend.inputs(), "the click focuses the field and the typing sets it")
	assert.Contains(t, replayed.stderr.String(), `replay by element: set text_field id=amountBox to "12,000"`)

	appended := elementRun(t)
	appended.focusOn("amountBox")
	appended.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(290, 152), typing("000")), done("Entered")}}}
	require.NoError(t, appended.execute(steps, nil).err)
	assert.False(t, lookupRecording(t, appended, "Enter the amount").Turns[0].Actions[1].Element.SetsValue, "the field showed more than the typed text")
	appended.backend.events = nil
	require.NoError(t, appended.execute(`{"input": "direct", "do": [{"act": "Enter the amount"}]}`, nil).err)
	assert.Equal(t, []string{"focus amountBox", "focus amountBox", "type 000"}, appended.backend.inputs(), "the typing is typed again")
}

// A typed placeholder is set with the run's own value.
func TestDirectReplaySetsPlaceholderValue(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.focusOn("amountBox")
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(typing("12,000")), done("Entered")}}}
	require.NoError(t, run.execute(`{"variables": {"amount": "12,000"}, "do": [{"act": "Enter the amount"}]}`, nil).err)
	entry := lookupRecording(t, run, "Enter the amount")
	assert.Equal(t, "%amount%", entry.Turns[0].Actions[0].Action.Text)
	assert.True(t, entry.Turns[0].Actions[0].Element.SetsValue)

	run.backend.events = nil
	replayed := run.execute(`{"input": "direct", "variables": {"amount": "8,500"}, "do": [{"act": "Enter the amount"}]}`, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"set amountBox=8,500"}, run.backend.inputs())
	assert.Contains(t, replayed.stderr.String(), `set text_field id=amountBox to "%amount%"`, "the log keeps the placeholder")
}

// An element that offers no pattern for the action replays it on the
// screen, and the turn is still a replay by element.
func TestDirectReplayFallsBackWithoutPattern(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.elements.withoutPattern("saveButton")
	run.elements.shift(37, 11)
	run.backend.events = nil
	replayed := run.execute(`{"input": "direct", "do": [{"act": "Save the invoice"}]}`, nil)
	require.NoError(t, replayed.err)
	events := operationEvents(replayed.exec.GetAgentSession())
	assert.Equal(t, agentstep.ViaElement, events[0].Via)
	assert.Equal(t, []string{"move 272,205", "left down #1"}, run.backend.inputs(), "the click follows the button")
	assert.Contains(t, replayed.stderr.String(), `button "保存" offers no invoke pattern; using the screen`)
}

// A disabled element is looked at again within find_within, and under
// never the miss names the element and why.
func TestDirectReplayNamesDisabledElement(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.elements.failPattern("saveButton", desktop.ErrDisabled)
	run.backend.events = nil
	failed := run.execute(`{"ai": "never", "input": "direct", "do": [{"act": "Save the invoice"}]}`, nil)
	require.ErrorContains(t, failed.err, `the act cannot run without AI: button "保存" could not be invoked within 0s at turn 1 of 1: it is disabled`)
	assert.Equal(t, agentstep.ViaElement, operationEvents(failed.exec.GetAgentSession())[0].Via)
	assert.Empty(t, run.backend.inputs(), "nothing is clicked in its place")

	run.elements.failPattern("saveButton", errors.New("Invoke: UIA_E_TIMEOUT (0x80131505)"))
	failed = run.execute(`{"ai": "never", "input": "direct", "do": [{"act": "Save the invoice"}]}`, nil)
	require.ErrorContains(t, failed.err, `button "保存" could not be invoked at turn 1 of 1: Invoke: UIA_E_TIMEOUT`)

	// The button enables after a moment.
	run.elements.failPattern("saveButton", desktop.ErrDisabled)
	go func() {
		time.Sleep(300 * time.Millisecond)
		run.elements.mu.Lock()
		delete(run.elements.applyErr, "saveButton")
		run.elements.mu.Unlock()
	}()
	replayed := run.execute(`{"ai": "never", "input": "direct", "find_within": "3s", "do": [{"act": "Save the invoice"}]}`, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"invoke saveButton"}, run.backend.inputs())
}

// The act's input setting wins over the step's.
func TestDirectInputPerAct(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.backend.events = nil
	require.NoError(t, run.execute(`{"input": "direct", "do": [{"act": {"instruction": "Save the invoice", "input": "screen"}}]}`, nil).err)
	assert.Equal(t, []string{"move 235,194", "left down #1"}, run.backend.inputs(), "the act keeps the screen")
	run.backend.events = nil
	require.NoError(t, run.execute(`{"do": [{"act": {"instruction": "Save the invoice", "input": "direct"}}]}`, nil).err)
	assert.Equal(t, []string{"invoke saveButton"}, run.backend.inputs(), "the act goes direct")
}

// The model's own turns always use the screen, whatever input says.
func TestDirectInputLeavesModelTurnsAlone(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(`{"input": "direct", "do": [{"act": "Save the invoice"}]}`, nil).err)
	assert.Equal(t, []string{"move 235,194", "left down #1"}, run.backend.inputs())
}

// A recording of an older format is ignored once and replaced.
func TestOldRecordingIgnoredOnce(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Open the report"}]}`
	run := newTestRun(t)
	cache := openReplayCache(filepath.Join(run.dataDir, computerhost.DataDirName), "invoices", "post")
	old := recording{Width: 400, Height: 200, Turns: []recordedTurn{{Screen: desktop.FingerprintOf(run.backend.screen), Actions: []recordedAction{{Action: clickAt(30, 40)}}}}}
	cache.Stage(replayKey(0, "Open the report"), old)
	require.NoError(t, cache.Commit(t.Context()))

	ignored := run.execute(`{"ai": "never", "do": [{"act": "Open the report"}]}`, nil)
	require.ErrorContains(t, ignored.err, "its recording on this host is from an older version and was ignored")

	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(30, 40)), done("Opened")}}}
	fresh := run.execute(steps, nil)
	require.NoError(t, fresh.err)
	assert.Equal(t, []string{"act:completed"}, eventNames(fresh.exec.GetAgentSession()))
	assert.Equal(t, recordingVersion, lookupRecording(t, run, "Open the report").Version)

	replayed := run.execute(`{"ai": "never", "do": [{"act": "Open the report"}]}`, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()))
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

// A healed act can record nothing, as when the model finds the task already
// done. The recording it healed no longer replays, so it is dropped, and the
// next run asks the model instead of repeating it.
func TestHealedActWithoutActionsDropsRecording(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Open the report"}]}`
	run := newTestRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(30, 40)), done("Opened")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.backend.show(pattern(400, 200, 150))
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{done("Already open")}}}
	healed := run.execute(steps, nil)
	require.NoError(t, healed.err)
	assert.Equal(t, []string{"act:healed"}, eventNames(healed.exec.GetAgentSession()))

	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{done("Already open")}}}
	next := run.execute(steps, nil)
	require.NoError(t, next.err)
	assert.Equal(t, []string{"act:completed"}, eventNames(next.exec.GetAgentSession()))
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
	assert.Equal(t, []string{"act:cache-hit", "expect:failed"}, eventNames(failed.exec.GetAgentSession()))
	// The failed check records what it cost, like any other operation.
	events := operationEvents(failed.exec.GetAgentSession())
	assert.Equal(t, agentstep.ViaModel, events[1].Via)
	assert.Equal(t, int64(6), events[1].Tokens)
	assert.Contains(t, events[1].Content, "expectation not met")

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
	assert.Equal(t, []string{"act:cache-hit", "act:failed"}, eventNames(failed.exec.GetAgentSession()))

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
	session := &scriptedSession{turns: []*computeruse.Turn{actions(typeText("acme")), done("Filled")}}
	run.sessions = []*scriptedSession{session}
	capped := run.execute(steps, nil)
	require.NoError(t, capped.err)
	require.Len(t, session.observations, 2)
	assert.Equal(t, capNote, session.observations[1].Note, "the replay's two actions used the budget")
	assert.Equal(t, []computeruse.Result{{Skipped: true}}, session.observations[1].Results)
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
	assert.Equal(t, []string{waitReasonDesktop}, waitReasons(waited.exec.GetAgentSession()))
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
			assert.Contains(t, strings.Join(lifecycleMessages(session), "\n"), "Waiting until nobody has used the desktop for 100ms")
			assert.Equal(t, []string{waitReasonPerson}, waitReasons(session))
		})
	}
}

// A step never operates the application that runs it: keys while one of
// its windows has the focus, or a click on one, fail and the model is told.
func TestOwnApplicationIsNeverOperated(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.protect = []uint32{99}
	run.backend.focusOn(desktop.WindowID{Handle: 5, Title: "Runbook", PID: 99})
	run.backend.under = desktop.WindowID{Handle: 5, Title: "Runbook", PID: 99}
	session := &scriptedSession{turns: []*computeruse.Turn{
		actions(computeruse.Action{CallID: "k", Kind: computeruse.KindKey, Keys: []string{"alt", "F4"}}),
		actions(clickAt(10, 10)),
		done("Gave up"),
	}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"do": [{"act": "Close the window"}]}`, nil)
	require.NoError(t, execution.err)

	assert.Empty(t, run.backend.inputs(), "nothing reached the desktop")
	require.Len(t, session.observations, 3)
	assert.Contains(t, session.observations[1].Results[0].Error, `"Runbook" belongs to the application that runs this step`)
	assert.Contains(t, session.observations[2].Results[0].Error, "never operates")
}

// The own-application guard covers every window an action reaches: the
// window under the cursor for a button press that moves nothing and for a
// click or scroll that names no position, a later point a drag crosses, and
// the focused window for a click that holds a modifier while the pointer is
// elsewhere.
func TestOwnApplicationGuardCoversEveryReach(t *testing.T) {
	t.Parallel()

	own := desktop.WindowID{Handle: 5, Title: "Runbook", PID: 99}
	other := desktop.WindowID{Handle: 6, Title: "Notepad", PID: 7}

	cases := []struct {
		name  string
		setup func(*fakeBackend)
		turn  *computeruse.Turn
	}{
		{
			name:  "button press at the cursor over an owned window",
			setup: func(b *fakeBackend) { b.under = own },
			turn:  actions(computeruse.Action{CallID: "a", Kind: computeruse.KindMouseDown, Button: "left"}),
		},
		{
			name:  "a click without a position at the cursor over an owned window",
			setup: func(b *fakeBackend) { b.under = own },
			turn:  actions(computeruse.Action{CallID: "a", Kind: computeruse.KindClick, Button: "left"}),
		},
		{
			name:  "a scroll without a position at the cursor over an owned window",
			setup: func(b *fakeBackend) { b.under = own },
			turn:  actions(computeruse.Action{CallID: "a", Kind: computeruse.KindScroll, ScrollY: 3}),
		},
		{
			name: "a drag crosses an owned window after its first point",
			setup: func(b *fakeBackend) {
				b.windowAt = func(x, _ int) desktop.WindowID {
					if x >= 100 {
						return own
					}
					return other
				}
			},
			turn: actions(computeruse.Action{CallID: "a", Kind: computeruse.KindDrag, Path: []computeruse.Point{{X: 10, Y: 10}, {X: 150, Y: 10}}}),
		},
		{
			name:  "a modifier click while an owned window has the focus",
			setup: func(b *fakeBackend) { b.under = other; b.focus = own },
			turn:  actions(computeruse.Action{CallID: "a", Kind: computeruse.KindClick, Button: "left", Modifiers: []string{"alt"}, Point: &computeruse.Point{X: 10, Y: 10}}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := newTestRun(t)
			run.protect = []uint32{99}
			tc.setup(run.backend)
			session := &scriptedSession{turns: []*computeruse.Turn{tc.turn, done("Gave up")}}
			run.sessions = []*scriptedSession{session}
			execution := run.execute(`{"do": [{"act": "Operate"}]}`, nil)
			require.NoError(t, execution.err)
			assert.Empty(t, run.backend.inputs(), "nothing reached the desktop")
			require.GreaterOrEqual(t, len(session.observations), 2)
			assert.Contains(t, session.observations[1].Results[0].Error, "never operates")
		})
	}
}

// An action at the cursor is refused when the cursor's position cannot be
// read, since the window it would reach is unknown.
func TestActionAtAnUnreadableCursorIsRefused(t *testing.T) {
	t.Parallel()

	for _, action := range []computeruse.Action{
		{CallID: "a", Kind: computeruse.KindClick, Button: "left"},
		{CallID: "a", Kind: computeruse.KindScroll, ScrollY: 3},
		{CallID: "a", Kind: computeruse.KindMouseDown, Button: "left"},
	} {
		t.Run(string(action.Kind), func(t *testing.T) {
			t.Parallel()
			run := newTestRun(t)
			run.backend.positionErr = errors.New("no pointer")
			session := &scriptedSession{turns: []*computeruse.Turn{actions(action), done("Gave up")}}
			run.sessions = []*scriptedSession{session}
			require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "Operate"}]}`, nil).err)
			assert.Empty(t, run.backend.inputs(), "nothing reached the desktop")
			require.GreaterOrEqual(t, len(session.observations), 2)
			assert.Contains(t, session.observations[1].Results[0].Error, "the pointer's position cannot be read")
		})
	}
}

// A wait for a person ends with an event, so a view knows the step is at
// work again before its operation finishes.
func TestWaitEndsWithAnEvent(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(1, 1)), done("Clicked")}}}
	run.backend.personKeepsUsing(2)
	execution := run.execute(`{"idle": "100ms", "do": [{"act": "Click"}]}`, nil)
	require.NoError(t, execution.err)
	messages := strings.Join(lifecycleMessages(execution.exec.GetAgentSession()), "\n")
	assert.Contains(t, messages, "Waiting until nobody has used the desktop for 100ms")
	assert.Contains(t, messages, "Nobody has used the desktop for 100ms; continuing")
}

// Waiting for a person is not the task's time: an act whose time limit is
// shorter than the wait still completes, and only a person who never stops
// fails it, after the wait the step allows.
func TestWaitingForPersonIsNotTaskTime(t *testing.T) {
	t.Parallel()

	const with = `{"idle": "100ms", "do": [{"act": {"instruction": "Click", "max_actions": 5}, "timeout": "2s"}]}`
	run := newTestRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(1, 1)), done("Clicked")}}}
	// Each check finds the person at the desktop, and checks are at least a
	// millisecond apart, so the wait outlasts the time limit by itself.
	run.backend.personKeepsUsing(2500)
	execution := run.execute(with, nil)
	require.NoError(t, execution.err)
	assert.Equal(t, []string{"act:completed"}, eventNames(execution.exec.GetAgentSession()))

	// A step that has sent no input of its own takes every input for the
	// person's, and gives up once the wait it allows has passed.
	fresh := newTestRun(t)
	fresh.personWait = 50 * time.Millisecond
	fresh.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(1, 1)), done("Clicked")}}}
	fresh.backend.personKeepsUsing(100000)
	gaveUp := fresh.execute(with, nil)
	require.EqualError(t, gaveUp.err, "computer: do[0] act failed: "+
		"a person kept using the desktop for 50ms, so the step gave up waiting")
	assert.Empty(t, fresh.backend.inputs())
}

// Actions the model chose on a screen a person has since used are not run;
// the model sees the new screen and why.
func TestPersonInputSkipsStaleTurn(t *testing.T) {
	t.Parallel()

	keyS := computeruse.Action{CallID: "c", Kind: computeruse.KindKey, Keys: []string{"ctrl", "s"}}
	for _, tc := range []struct {
		name      string
		changed   bool
		focus     bool
		firstKeys bool
		skip      bool
		inputs    []string
	}{
		// A changed screen sends any turn back to the model.
		{name: "screen changed", changed: true, skip: true, inputs: []string{"move 20,20", "left down #1"}},
		// A person who only touched the mouse leaves the screen as the model
		// saw it, so the actions run without another model turn.
		{name: "screen unchanged", inputs: []string{"move 10,10", "left down #1", "move 20,20", "left down #1"}},
		// A person who moved the keyboard focus to another window would send
		// a turn's keys there, so a turn that types or presses keys goes back
		// to the model.
		{name: "focus moved with keys", focus: true, firstKeys: true, skip: true, inputs: []string{"move 20,20", "left down #1"}},
		// A plain click lands by position, not by focus, and the screen is
		// unchanged, so a moved focus does not hold it back.
		{name: "focus moved, pointer only", focus: true, inputs: []string{"move 10,10", "left down #1", "move 20,20", "left down #1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := newTestRun(t)
			run.backend.focusOn(desktop.WindowID{Handle: 1, Title: "Expense", PID: 7})
			first := clickAt(10, 10)
			if tc.firstKeys {
				first = keyS
			}
			used := false
			session := &scriptedSession{
				turns: []*computeruse.Turn{actions(first), actions(clickAt(20, 20)), done("Clicked")},
				onNext: func() {
					if !used {
						used = true
						run.backend.personUses()
						if tc.changed {
							run.backend.show(pattern(400, 200, 150))
						}
						if tc.focus {
							run.backend.focusOn(desktop.WindowID{Handle: 2, Title: "Mail", PID: 8})
						}
					}
				},
			}
			run.sessions = []*scriptedSession{session}
			execution := run.execute(`{"idle": "100ms", "do": [{"act": "Click the button"}]}`, nil)
			require.NoError(t, execution.err)

			assert.Equal(t, tc.inputs, run.backend.inputs())
			require.Len(t, session.observations, 3)
			if tc.skip {
				assert.Equal(t, []computeruse.Result{{CallID: "c", Skipped: true}}, session.observations[1].Results)
				assert.Equal(t, personNote, session.observations[1].Note)
				assert.Contains(t, execution.stderr.String(), "Clicked (1 actions)")
				return
			}
			assert.Equal(t, []computeruse.Result{{CallID: "c"}}, session.observations[1].Results)
			assert.Empty(t, session.observations[1].Note)
			assert.Contains(t, execution.stderr.String(), "the screen still looks the same")
			assert.Contains(t, execution.stderr.String(), "Clicked (2 actions)")
		})
	}
}

// A person who leaves another window in front while the model works holds
// the act until they stop; the act then brings back the application it was
// working in, so the model's next look is at the task, and it never
// maximizes the window the person left in front, which is theirs, even one
// of the same application.
func TestActReturnsToItsApplicationAfterAPerson(t *testing.T) {
	t.Parallel()

	expense := desktop.WindowID{Handle: 2, Title: "Expense", PID: 7, App: "expense", Bounds: image.Rect(0, 0, 400, 200)}
	for _, tc := range []struct {
		name   string
		person desktop.WindowID
		raised uint64
	}{
		{"another application", desktop.WindowID{Handle: 1, Title: "Mail", PID: 8, App: "mail", Bounds: image.Rect(0, 0, 400, 200)}, expense.Handle},
		{"another window of the same application", desktop.WindowID{Handle: 3, Title: "Expense 2", PID: 7, App: "expense", Bounds: image.Rect(0, 0, 400, 200)}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := newTestRun(t)
			// The person's window is listed first, so a return to the
			// application may settle on it.
			run.backend.windows = []desktop.WindowID{tc.person, expense}
			run.backend.focusOn(expense)
			run.backend.under = expense
			used := false
			session := &scriptedSession{
				turns: []*computeruse.Turn{actions(clickAt(10, 10)), actions(clickAt(20, 20)), done("Clicked")},
				onNext: func() {
					if !used {
						used = true
						run.backend.personUses()
						run.backend.show(pattern(400, 200, 150))
						run.backend.focusOn(tc.person)
						run.backend.under = tc.person
					}
				},
			}
			run.sessions = []*scriptedSession{session}
			execution := run.execute(`{"idle": "100ms", "do": [{"act": "Save the expense"}]}`, nil)
			require.NoError(t, execution.err)

			require.Len(t, session.observations, 3)
			assert.Equal(t, personNote, session.observations[1].Note)
			if tc.raised != 0 {
				require.NotEmpty(t, run.backend.raised)
				assert.Equal(t, tc.raised, run.backend.raised[0].Handle, "the application the act was working in came back")
			}
			for _, w := range run.backend.maximizedWindows() {
				assert.NotEqual(t, tc.person.Handle, w.Handle, "the person's window keeps its size")
			}
			assert.Equal(t, []string{"move 20,20", "left down #1"}, run.backend.inputs(), "only the click chosen after the person stopped lands")
		})
	}
}

// The act works in the application its last action that ran operated: a turn
// that clicks in one application and then another leaves the act in the
// second, which is the one brought back after a person; keys work in the
// window in front as they are sent; a turn stopped by a failed action leaves
// the act where it last worked.
func TestActWorksWhereItsLastActionLanded(t *testing.T) {
	t.Parallel()

	expense := desktop.WindowID{Handle: 2, Title: "Expense", PID: 7, App: "expense", Bounds: image.Rect(0, 0, 200, 200)}
	ledger := desktop.WindowID{Handle: 3, Title: "Ledger", PID: 9, App: "ledger", Bounds: image.Rect(200, 0, 400, 200)}
	mail := desktop.WindowID{Handle: 1, Title: "Mail", PID: 8, App: "mail", Bounds: image.Rect(0, 0, 400, 200)}
	// An element id the screen did not offer fails its action, so the
	// actions after it are skipped.
	missing := computeruse.Action{CallID: "x", Kind: computeruse.KindClick, ElementID: "e99"}
	typeTotal := computeruse.Action{CallID: "t", Kind: computeruse.KindType, Text: "12,000"}
	tests := []struct {
		name  string
		first *computeruse.Turn
		// pressed comes to the front when the turn's click is pressed.
		pressed desktop.WindowID
		raised  desktop.WindowID
	}{
		{name: "the last click", first: actions(clickAt(10, 10), clickAt(300, 10)), raised: ledger},
		{name: "keys after a click that brought another application forward", first: actions(clickAt(10, 10), typeTotal), pressed: ledger, raised: ledger},
		{name: "a failed action", first: actions(clickAt(10, 10), missing, clickAt(300, 10)), raised: expense},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := newTestRun(t)
			run.elements = newFakeElements()
			run.backend.windows = []desktop.WindowID{mail, expense, ledger}
			run.backend.focusOn(expense)
			run.backend.windowAt = func(x, _ int) desktop.WindowID {
				if x < 200 {
					return expense
				}
				return ledger
			}
			if tc.pressed.Known() {
				run.backend.onPress = func() { run.backend.focusOn(tc.pressed) }
			}
			paste := computeruse.Action{CallID: "p", Kind: computeruse.KindKey, Keys: []string{"cmd", "v"}}
			turns := 0
			session := &scriptedSession{
				turns: []*computeruse.Turn{tc.first, actions(paste), actions(paste), done("Copied")},
				onNext: func() {
					turns++
					// Once the first turn ran, a person brings mail forward,
					// where the paste would land. Their input comes after the
					// moment the desktop may still report the step's clicks,
					// so a clock too coarse to order the clicks and the next
					// screenshot still tells it from the step's own.
					if turns == 2 {
						run.backend.personUsesAfterEcho()
						run.backend.focusOn(mail)
					}
				},
			}
			run.sessions = []*scriptedSession{session}
			execution := run.execute(`{"idle": "100ms", "do": [{"act": "Copy the amount into the ledger"}]}`, nil)
			require.NoError(t, execution.err)

			require.NotEmpty(t, run.backend.raised)
			assert.Equal(t, tc.raised.Handle, run.backend.raised[0].Handle, "the application the act last worked in came back")
			for _, w := range run.backend.maximizedWindows() {
				assert.NotEqual(t, mail.Handle, w.Handle, "the person's window keeps its size")
			}
		})
	}
}

// An action that only looks operates no application: a screenshot taken
// while a person's window is in front leaves the act working where it was,
// and that application is the one brought back after the person.
func TestActLooksWithoutChangingItsApplication(t *testing.T) {
	t.Parallel()

	expense := desktop.WindowID{Handle: 2, Title: "Expense", PID: 7, App: "expense", Bounds: image.Rect(0, 0, 400, 200)}
	mail := desktop.WindowID{Handle: 1, Title: "Mail", PID: 8, App: "mail", Bounds: image.Rect(0, 0, 400, 200)}
	run := newTestRun(t)
	run.backend.windows = []desktop.WindowID{mail, expense}
	run.backend.focusOn(expense)
	run.backend.under = expense
	look := computeruse.Action{CallID: "s", Kind: computeruse.KindScreenshot}
	turns := 0
	session := &scriptedSession{
		turns: []*computeruse.Turn{actions(clickAt(10, 10)), actions(look), actions(clickAt(20, 20)), done("Saved")},
		onNext: func() {
			turns++
			switch turns {
			case 2:
				// A person brings mail forward over the same picture, so
				// the screenshot still runs.
				run.backend.personUsesAfterEcho()
				run.backend.focusOn(mail)
				run.backend.under = mail
			case 3:
				// The person changes the screen, so the click is held back.
				run.backend.personUsesAfterEcho()
				run.backend.show(pattern(400, 200, 150))
			}
		},
	}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"idle": "100ms", "do": [{"act": "Save the expense"}]}`, nil)
	require.NoError(t, execution.err)

	require.NotEmpty(t, run.backend.raised)
	assert.Equal(t, expense.Handle, run.backend.raised[0].Handle, "the application the act clicked in came back")
}

// A window that came to the front after the screenshot, with no person
// input at all, still sends the turn back to the model instead of letting
// its actions land on the wrong window. This is the remote-session case,
// where nothing reports a person but the foreground can still change under
// the step.
func TestDivergedScreenSkipsTurnWithoutPerson(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.remote = true
	changed := false
	session := &scriptedSession{
		turns: []*computeruse.Turn{actions(clickAt(10, 10)), actions(clickAt(20, 20)), done("Clicked")},
		onNext: func() {
			if !changed {
				changed = true
				run.backend.show(pattern(400, 200, 150))
			}
		},
	}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"do": [{"act": "Close the window"}]}`, nil)
	require.NoError(t, execution.err)

	assert.Equal(t, []string{"move 20,20", "left down #1"}, run.backend.inputs(), "the stale click never ran")
	require.Len(t, session.observations, 3)
	assert.Equal(t, []computeruse.Result{{CallID: "c", Skipped: true}}, session.observations[1].Results)
	assert.Equal(t, divergedNote, session.observations[1].Note)
	assert.Contains(t, execution.stderr.String(), "the screen changed before the actions ran")
	assert.Contains(t, execution.stderr.String(), "Clicked (1 actions)")
}

// The step that last held the desktop leaves when it sent input, so the next
// step does not take that input for a person's and wait for it.
func TestNextStepIgnoresEarlierInput(t *testing.T) {
	t.Parallel()

	const with = `{"idle": "1h", "cache": false, "do": [{"act": "Click"}]}`
	run := newTestRun(t)
	// A step that waits gives up quickly instead of hanging for the hour.
	run.personWait = time.Second
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

// In a remote desktop session the connection feeds the desktop input of its
// own and nobody at the keyboard shares it, so a step does not wait for a
// person unless idle is set, and says so once.
func TestRemoteSessionDoesNotWaitForAPerson(t *testing.T) {
	t.Parallel()

	t.Run("default idle skips the wait", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.remote = true
		run.backend.personKeepsUsing(50)
		run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(10, 10)), done("Clicked")}}}
		execution := run.execute(`{"do": [{"launch": "app"}, {"act": "Click the button"}]}`, nil)
		require.NoError(t, execution.err)

		messages := strings.Join(lifecycleMessages(execution.exec.GetAgentSession()), "\n")
		assert.NotContains(t, messages, "Waiting until nobody")
		assert.Equal(t, 1, strings.Count(messages, "Running in a remote session, so not waiting for a person"), "said once, not per operation")
		assert.Equal(t, []string{"move 10,10", "left down #1"}, run.backend.inputs())
	})

	t.Run("an explicit idle still waits", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.remote = true
		run.backend.personKeepsUsing(2)
		run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(10, 10)), done("Clicked")}}}
		execution := run.execute(`{"idle": "100ms", "do": [{"act": "Click the button"}]}`, nil)
		require.NoError(t, execution.err)

		messages := strings.Join(lifecycleMessages(execution.exec.GetAgentSession()), "\n")
		assert.Contains(t, messages, "Waiting until nobody has used the desktop for 100ms")
		assert.NotContains(t, messages, "Running in a remote session")
	})
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
			// Told that the task has no actions left, a model that acts again
			// ends the act.
			name:  "max actions",
			with:  `{"max_actions": 2, "cache": false, "do": [{"act": "Click around"}]}`,
			turns: []*computeruse.Turn{actions(clickAt(1, 1), clickAt(2, 2)), actions(clickAt(3, 3)), actions(clickAt(4, 4))},
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

// A task that reaches its action budget asks the model for its report before
// failing, so a task that was in fact done is not thrown away.
func TestCapAsksForReport(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	session := &scriptedSession{turns: []*computeruse.Turn{actions(clickAt(1, 1), clickAt(2, 2)), actions(clickAt(3, 3)), done("Clicked around")}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"max_actions": 2, "cache": false, "do": [{"act": "Click around"}]}`, nil)
	require.NoError(t, execution.err)

	assert.Equal(t, []string{"move 1,1", "left down #1", "move 2,2", "left down #1"}, run.backend.inputs(), "the third click was not run")
	require.Len(t, session.observations, 3)
	assert.Equal(t, capNote, session.observations[2].Note)
	assert.Equal(t, []computeruse.Result{{CallID: "c", Skipped: true}}, session.observations[2].Results)
	assert.Contains(t, execution.stderr.String(), "Clicked around (2 actions)")
}

// A model that repeats its previous round on a screen that did not change is
// told so, before it tries a third time.
func TestRepeatedRoundIsNoted(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	session := &scriptedSession{turns: []*computeruse.Turn{actions(clickAt(5, 5)), actions(clickAt(5, 5)), done("Done")}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"cache": false, "do": [{"act": "Press the button"}]}`, nil)
	require.NoError(t, execution.err)

	require.Len(t, session.observations, 3)
	assert.Empty(t, session.observations[1].Note, "one round is not a repeat")
	assert.Equal(t, repeatNote, session.observations[2].Note)
	assert.Contains(t, execution.stderr.String(), "repeated its previous actions")
}

// A scroll that moved nothing is reported at once, so the model scrolls
// elsewhere instead of trying the same spot again. A caret that blinked
// meanwhile is no movement.
func TestStillScrollIsNoted(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.backend.afterWheels = []*image.RGBA{withCaret(run.backend.screen)}
	session := &scriptedSession{turns: []*computeruse.Turn{actions(scrollDownAt(5, 5)), done("Scrolled")}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"cache": false, "do": [{"act": "Scroll the list"}]}`, nil)
	require.NoError(t, execution.err)

	require.Len(t, session.observations, 2)
	assert.Equal(t, stillScrollNote, session.observations[1].Note)
	assert.Contains(t, execution.stderr.String(), "the scroll moved nothing")
}

// A scroll that moved nothing is reported beside the request to look at the
// result of a turn that also reported the task done.
func TestStillScrollIsNotedWithReport(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	scrolledAndDone := actions(scrollDownAt(5, 5))
	scrolledAndDone.Done = &computeruse.Done{Success: true, Summary: "At the end"}
	session := &scriptedSession{turns: []*computeruse.Turn{scrolledAndDone, done("At the end")}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"cache": false, "do": [{"act": "Scroll to the end"}]}`, nil)
	require.NoError(t, execution.err)

	require.Len(t, session.observations, 2)
	assert.Equal(t, verifyNote+" "+stillScrollNote, session.observations[1].Note)
}

// A turn that scrolls two windows moved something when either window moved.
func TestScrollOfASecondWindowMoved(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	left := desktop.WindowID{Handle: 1, App: "left", Bounds: image.Rect(0, 0, 200, 200)}
	right := desktop.WindowID{Handle: 2, App: "right", Bounds: image.Rect(200, 0, 400, 200)}
	run.backend.windows = []desktop.WindowID{left, right}
	run.backend.windowAt = func(x, _ int) desktop.WindowID {
		if x < 200 {
			return left
		}
		return right
	}
	screen := run.backend.screen
	run.backend.afterWheels = []*image.RGBA{screen, brightened(screen, image.Rect(300, 50, 340, 90), 2)}
	session := &scriptedSession{turns: []*computeruse.Turn{actions(scrollDownAt(50, 50), scrollDownAt(300, 50)), done("Scrolled")}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"cache": false, "do": [{"act": "Scroll both lists"}]}`, nil)
	require.NoError(t, execution.err)

	require.Len(t, session.observations, 2)
	assert.Empty(t, session.observations[1].Note)
}

// Scrolling down a list repeats one scroll, and a small list that moved
// keeps the screen's fingerprint; neither makes the round a repeat that
// changed nothing.
func TestRepeatedScrollThatMovedIsNoRepeat(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	list := run.backend.screen
	moved, movedAgain := nudged(list, 2), nudged(list, 4)
	require.Equal(t, desktop.FingerprintOf(list), desktop.FingerprintOf(movedAgain))
	run.backend.afterWheels = []*image.RGBA{moved, movedAgain}
	session := &scriptedSession{turns: []*computeruse.Turn{actions(scrollDownAt(5, 5)), actions(scrollDownAt(5, 5)), done("Scrolled")}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"cache": false, "do": [{"act": "Scroll the list"}]}`, nil)
	require.NoError(t, execution.err)

	require.Len(t, session.observations, 3)
	assert.Empty(t, session.observations[1].Note)
	assert.Empty(t, session.observations[2].Note)
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
	exec.checkDesktop = func() desktop.Diagnostics { return desktop.Diagnostics{} }
	exec.openDesktop = func() (*desktop.Driver, error) { return nil, errors.New("Screen Recording permission is missing") }
	exec.newProvider = func(context.Context, *ir.LLMConfig) (llmpkg.Provider, error) { return run.vision, nil }
	exec.SetStderr(io.Discard)
	err = exec.Run(run.context())
	require.ErrorContains(t, err, "open the desktop: Screen Recording permission is missing")
}

// screenLocked is what a check of a locked screen reports.
var screenLocked = desktop.Diagnostics{Problems: []desktop.Problem{{
	Code:    "screen_locked",
	Message: "the screen is locked; unlock it and keep it unlocked while computer steps run",
}}}

// lockedFor reports a locked screen for the first checks, then a usable one.
func lockedFor(checks int) func() desktop.Diagnostics {
	return func() desktop.Diagnostics {
		if checks > 0 {
			checks--
			return screenLocked
		}
		return desktop.Diagnostics{}
	}
}

// A step that starts on a locked screen, as a run caught up when a laptop
// wakes does, waits for a person to unlock it and then runs.
func TestWaitsForScreenUnlock(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.checkDesktop = lockedFor(2)
	execution := run.execute(`{"do": [{"wait": "1ms"}]}`, nil)
	require.NoError(t, execution.err)
	session := execution.exec.GetAgentSession()
	assert.Equal(t, []string{"wait:completed"}, eventNames(session))
	assert.Equal(t, []string{waitReasonScreen}, waitReasons(session))
	messages := strings.Join(lifecycleMessages(session), "\n")
	assert.Contains(t, messages, "Waiting for the screen to be unlocked: the screen is locked")
	assert.Contains(t, messages, "The screen is unlocked; continuing")
}

// A step canceled while it waits for the screen stops at once rather than
// at its next check.
func TestScreenWaitStopsWhenCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	run := newTestRun(t)
	run.parent = ctx
	run.screenPoll = time.Hour
	run.checkDesktop = func() desktop.Diagnostics { return screenLocked }
	time.AfterFunc(50*time.Millisecond, cancel)
	execution := run.execute(`{"do": [{"wait": "1ms"}]}`, nil)
	require.EqualError(t, execution.err, "computer: context canceled")
	assert.Equal(t, []string{waitReasonScreen}, waitReasons(execution.exec.GetAgentSession()))
}

// A problem that unlocking the screen does not clear, such as a missing
// permission, fails the step at once.
func TestPermissionProblemDoesNotWaitForScreen(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	// A wrong wait ends with an error of its own rather than outlasting
	// the test.
	run.screenWait = 50 * time.Millisecond
	run.checkDesktop = func() desktop.Diagnostics {
		return desktop.Diagnostics{Problems: []desktop.Problem{{Code: "screen_recording", Message: "Screen Recording permission is missing"}}}
	}
	execution := run.execute(`{"do": [{"wait": "1ms"}]}`, nil)
	require.EqualError(t, execution.err, "computer: open the desktop: Screen Recording permission is missing")
	assert.Empty(t, waitReasons(execution.exec.GetAgentSession()))
}

// A step with no timeout gives up on a screen nobody unlocks once the wait
// it allows has passed, naming the problem, even when the DAG's deadline is
// later.
func TestScreenWaitGivesUp(t *testing.T) {
	t.Parallel()

	for name, deadline := range map[string]time.Duration{"no deadline": 0, "later DAG deadline": time.Hour} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			run := newTestRun(t)
			if deadline > 0 {
				ctx, cancel := context.WithTimeout(t.Context(), deadline)
				t.Cleanup(cancel)
				run.parent = ctx
			}
			run.screenWait = 50 * time.Millisecond
			run.checkDesktop = func() desktop.Diagnostics { return screenLocked }
			execution := run.execute(`{"do": [{"wait": "1ms"}]}`, nil)
			require.EqualError(t, execution.err, "computer: open the desktop after waiting 50ms: "+
				"the screen is locked; unlock it and keep it unlocked while computer steps run")
			assert.Equal(t, []string{waitReasonScreen}, waitReasons(execution.exec.GetAgentSession()))
		})
	}
}

// The model may click an element the observation lists by id. A button is
// invoked through the element itself, which works wherever the window is and
// does not depend on a pixel another window might cover.
func TestModelActsOnElementById(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.elements = newFakeElements()
	session := &scriptedSession{turns: []*computeruse.Turn{
		actions(computeruse.Action{CallID: "x", Kind: computeruse.KindClick, ElementID: "e1"}),
		done("Clicked save"),
	}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"idle": "0", "do": [{"act": "Save the form"}]}`, nil)
	require.NoError(t, execution.err)

	require.NotEmpty(t, session.observations)
	assert.Contains(t, session.observations[0].Elements, computeruse.Element{ID: "e1", Role: desktop.RoleButton, Name: "保存"},
		"the observation offers the button as an element")
	assert.Equal(t, []string{"invoke saveButton"}, run.backend.inputs(), "the button is invoked, not clicked at a pixel")
}

// A click the model aims at a text field element gives it the keyboard focus
// through the element itself, so the typing that follows reaches it even if
// the field moved since the screenshot.
func TestModelFocusesFieldElement(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.elements = newFakeElements()
	// Give the amount field a name so the observation offers it; it is then
	// e1, before the two 保存 buttons.
	run.elements.elements[2].Name = "金額"
	session := &scriptedSession{turns: []*computeruse.Turn{
		actions(computeruse.Action{CallID: "x", Kind: computeruse.KindClick, ElementID: "e1"}),
		done("Focused the field"),
	}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"idle": "0", "do": [{"act": "Click the amount field"}]}`, nil)
	require.NoError(t, execution.err)

	assert.Equal(t, []string{"focus amountBox"}, run.backend.inputs(), "the field takes the focus, no pixel click")
}

// A click the act carried out through an element's pattern replays through
// it under the default screen input too, as the act did, rather than as a
// click at the element's centre.
func TestReplayInvokesAnElementAsTheActDid(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{
		actions(computeruse.Action{CallID: "x", Kind: computeruse.KindClick, ElementID: "e1"}),
		done("Saved"),
	}}}
	require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "Save the form"}]}`, nil).err)
	require.Equal(t, []string{"invoke saveButton"}, run.backend.inputs(), "the act invokes the button")

	run.backend.events = nil
	require.NoError(t, run.execute(`{"ai": "never", "idle": "0", "do": [{"act": "Save the form"}]}`, nil).err)
	assert.Equal(t, []string{"invoke saveButton"}, run.backend.inputs(), "the replay invokes it the same way")
}

// A click the model aims at a combo box lands on it, as a person's click
// would, rather than opening its list: in a combo box that takes typing it
// puts the caret in the field, so the typing that follows goes there.
func TestModelClickOnAComboBoxClicksIt(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.elements.elements[2] = desktop.Element{Role: desktop.RoleComboBox, Name: "品目", ID: "itemBox", Bounds: image.Rect(190, 140, 390, 164), Path: []desktop.PathStep{{Role: desktop.RoleComboBox, Name: "品目", Index: 0}}}
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{
		actions(computeruse.Action{CallID: "x", Kind: computeruse.KindClick, ElementID: "e1"}),
		done("Clicked the item box"),
	}}}
	require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "Click the item box"}]}`, nil).err)

	// e1 is the 品目 box at (190,140)-(390,164); its centre is 290,152.
	assert.Equal(t, []string{"move 290,152", "left down #1"}, run.backend.inputs())
}

// An element that offers no pattern for the click, such as a custom control,
// falls back to a click at its position on the screen.
func TestModelElementFallsBackToClick(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.elements = newFakeElements()
	run.elements.withoutPattern("saveButton")
	session := &scriptedSession{turns: []*computeruse.Turn{
		actions(computeruse.Action{CallID: "x", Kind: computeruse.KindClick, ElementID: "e1"}),
		done("Clicked save"),
	}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"idle": "0", "do": [{"act": "Save the form"}]}`, nil)
	require.NoError(t, execution.err)

	// e1 is the 保存 button at (190,180)-(280,208); its centre is 235,194.
	assert.Equal(t, []string{"move 235,194", "left down #1"}, run.backend.inputs())
}

// An element id the latest observation did not offer fails that action and
// nothing reaches the desktop, so the model can look again.
func TestModelElementIdNotOffered(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.elements = newFakeElements()
	session := &scriptedSession{turns: []*computeruse.Turn{
		actions(computeruse.Action{CallID: "x", Kind: computeruse.KindClick, ElementID: "e99"}),
		done("Gave up"),
	}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"idle": "0", "do": [{"act": "Save the form"}]}`, nil)
	require.NoError(t, execution.err)

	assert.Empty(t, run.backend.inputs(), "nothing reached the desktop")
	require.GreaterOrEqual(t, len(session.observations), 2)
	assert.Contains(t, session.observations[1].Results[0].Error, `no element "e99" is on the screen`)
}

// A checkbox that takes no input at first and reaches its state while the
// step waits for it is left as it is, not toggled away from it.
func TestPinnedCheckLeavesABoxThatReachedItsStateWhileDisabled(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	box := desktop.Element{Role: desktop.RoleCheckbox, Name: "同意する", ID: "agreeBox", Value: "false", Bounds: image.Rect(120, 220, 300, 240),
		Path: []desktop.PathStep{{Role: desktop.RoleCheckbox, Name: "同意する", Index: 0}}}
	run.elements.elements = append(expenseElements(), box)
	run.elements.failPattern("agreeBox", desktop.ErrDisabled)
	// The box is checked before it takes input again, so a retry never
	// meets it enabled and unchecked.
	time.AfterFunc(300*time.Millisecond, func() {
		run.elements.setValue("agreeBox", "true")
		run.elements.mu.Lock()
		delete(run.elements.applyErr, "agreeBox")
		run.elements.mu.Unlock()
	})
	steps := `{"ai": "never", "input": "direct", "find_within": "3s", "do": [{"act": {"instruction": "Agree", "recorded": {"windows": [{"check": {"role": "checkbox", "id": "agreeBox"}}]}}}]}`
	require.NoError(t, run.execute(steps, nil).err)
	assert.Empty(t, run.backend.inputs(), "the box already checked is not toggled")
	found, err := run.elements.Find(desktop.Selector{Role: desktop.RoleCheckbox, ID: "agreeBox"})
	require.NoError(t, err)
	assert.Equal(t, "true", found[0].Value)
}

// A turn that clicks the same checkbox twice by id records each click with
// the state it left the box in, read right before each click rather than when
// the turn listed the box, so the act pins as check, then uncheck.
func TestRecordingReadsACheckboxBeforeEachClick(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	box := desktop.Element{Role: desktop.RoleCheckbox, Name: "同意する", ID: "agreeBox", Value: "false", Bounds: image.Rect(120, 220, 300, 240),
		Path: []desktop.PathStep{{Role: desktop.RoleCheckbox, Name: "同意する", Index: 0}}}
	run.elements.elements = append([]desktop.Element{box}, expenseElements()...)
	click := computeruse.Action{CallID: "x", Kind: computeruse.KindClick, ElementID: "e1"}
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(click, click), done("Agreed, then not")}}}
	require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "Toggle consent"}]}`, nil).err)

	pinned := pinOf(t, lookupRecording(t, run, "Toggle consent"))
	assert.JSONEq(t, `[
		{"check": {"app": "expense", "role": "checkbox", "name": "同意する", "id": "agreeBox"}},
		{"uncheck": {"app": "expense", "role": "checkbox", "name": "同意する", "id": "agreeBox"}}
	]`, marshal(t, pinned.Recorded))
}

// A pinned check or uncheck leaves a checkbox in its state, clicking it only
// when it is in the other one and confirming the state after, so running the
// step again changes nothing. A checkbox that does not change, or does not
// say its state, is a miss.
func TestPinnedCheckboxIsLeftInItsState(t *testing.T) {
	t.Parallel()

	agree := desktop.Element{Role: desktop.RoleCheckbox, Name: "同意する", ID: "agreeBox", Bounds: image.Rect(120, 220, 300, 240),
		Path: []desktop.PathStep{{Role: desktop.RoleCheckbox, Name: "同意する", Index: 0}}}
	for _, tc := range []struct {
		name    string
		verb    string
		state   string
		input   string
		stuck   bool
		inputs  []string
		final   string
		missing string
	}{
		{name: "check an unchecked box", verb: "check", state: "false", inputs: []string{"move 210,230", "left down #1"}, final: "true"},
		{name: "check a checked box", verb: "check", state: "true", final: "true"},
		{name: "uncheck a checked box directly", verb: "uncheck", state: "true", input: "direct", inputs: []string{"toggle agreeBox"}, final: "false"},
		{name: "uncheck an unchecked box", verb: "uncheck", state: "false", final: "false"},
		{name: "a box the click does not change", verb: "check", state: "false", stuck: true, inputs: []string{"move 210,230", "left down #1"}, missing: "is not checked after the click"},
		{name: "a box that does not say its state", verb: "check", missing: "does not say whether it is checked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := elementRun(t)
			box := agree
			box.Value = tc.state
			run.elements.elements = append(expenseElements(), box)
			if !tc.stuck {
				run.backend.onPress = func() {
					run.elements.setValue("agreeBox", map[string]string{"true": "false", "false": "true"}[tc.state])
				}
			}
			steps := fmt.Sprintf(`{"ai": "never", "input": %q, "find_within": "200ms", "do": [{"act": {"instruction": "Agree", "recorded": {"windows": [{%q: {"role": "checkbox", "id": "agreeBox"}}]}}}]}`,
				cmp.Or(tc.input, "screen"), tc.verb)
			execution := run.execute(steps, nil)
			assert.Equal(t, tc.inputs, run.backend.inputs())
			if tc.missing != "" {
				require.ErrorContains(t, execution.err, tc.missing)
				return
			}
			require.NoError(t, execution.err)
			found, err := run.elements.Find(desktop.Selector{Role: desktop.RoleCheckbox, ID: "agreeBox"})
			require.NoError(t, err)
			assert.Equal(t, tc.final, found[0].Value)
		})
	}
}

// A model's click on a checkbox, at its position or on the element by id,
// is recorded with the state it left the box in, so the learned act pins as
// check or uncheck and replaying it never undoes a box already in that state.
// A box the application updates a moment later is waited for; one the click
// leaves as it was records a plain click.
func TestRecordingKeepsTheStateACheckboxWasLeftIn(t *testing.T) {
	t.Parallel()

	box := desktop.Element{Role: desktop.RoleCheckbox, Name: "同意する", ID: "agreeBox", Value: "false", Bounds: image.Rect(120, 220, 300, 240),
		Path: []desktop.PathStep{{Role: desktop.RoleCheckbox, Name: "同意する", Index: 0}}}
	const checked = `[{"check": {"app": "expense", "role": "checkbox", "name": "同意する", "id": "agreeBox"}}]`
	for _, tc := range []struct {
		name   string
		click  computeruse.Action
		update func(run *testRun)
		pinned string
	}{
		{"at its position", clickAt(210, 230), func(run *testRun) { run.elements.setValue("agreeBox", "true") }, checked},
		{"on the element", computeruse.Action{CallID: "x", Kind: computeruse.KindClick, ElementID: "e1"}, nil, checked},
		{"updated a moment later", clickAt(210, 230), func(run *testRun) {
			time.AfterFunc(700*time.Millisecond, func() { run.elements.setValue("agreeBox", "true") })
		}, checked},
		{"left as it was", clickAt(210, 230), func(*testRun) {}, `[{"click": {"app": "expense", "role": "checkbox", "name": "同意する", "id": "agreeBox"}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := elementRun(t)
			run.elements.elements = append([]desktop.Element{box}, expenseElements()...)
			if tc.update != nil {
				run.backend.onPress = func() { tc.update(run) }
			}
			run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(tc.click), done("Agreed")}}}
			require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "Agree"}]}`, nil).err)

			pinned := pinOf(t, lookupRecording(t, run, "Agree"))
			assert.JSONEq(t, tc.pinned, marshal(t, pinned.Recorded))
		})
	}
}
