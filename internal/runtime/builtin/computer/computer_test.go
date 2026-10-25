// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"errors"
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

	t.Run("text fails with the reason", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		execution := run.execute(`{"do": [{"expect": {"text": "エラー"}, "timeout": "300ms"}]}`, nil)
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
	window := desktop.Element{Role: desktop.RoleWindow, Name: "確認", App: "expense", Window: "確認", Bounds: image.Rect(250, 200, 550, 350)}
	return window, []desktop.Element{
		{Role: desktop.RoleText, Name: "すでに入力中のものがあります。クリアしていいですか？", Window: "確認", Bounds: image.Rect(270, 220, 530, 250), Path: []desktop.PathStep{{Role: desktop.RoleText, Index: 0}}},
		{Role: desktop.RoleButton, Name: "はい", Window: "確認", Bounds: image.Rect(300, 300, 380, 330), Path: []desktop.PathStep{{Role: desktop.RoleButton, Name: "はい", Index: 0}}},
	}
}

// A handler clears a screen that comes up only sometimes, whenever the step
// meets it: after an action, while a check waits, or before an action, even
// a modal drawn over the page, which leaves the elements behind it readable.
// A run that never meets it pays nothing for it.
func TestHandlerClearsAScreenThatComesUpSometimes(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "input": "direct", "find_within": "300ms",
		"handle": [{"name": "入力中クリアの確認", "when": {"text": "クリアしていいですか"}, "recorded": {"windows": [{"click": {"role": "button", "name": "はい"}}]}}],
		"do": [
			{"act": {"instruction": "Reset", "recorded": {"windows": [{"click": {"role": "button", "id": "saveButton"}}]}}},
			{"expect": {"element": {"role": "text_field", "id": "amountBox"}, "within": "2s"}}
		]}`
	for _, tc := range []struct {
		name    string
		before  bool
		after   bool
		handled bool
		inputs  []string
	}{
		{name: "after the click", after: true, handled: true, inputs: []string{"invoke saveButton", "invoke はい"}},
		{name: "over the page before the click", before: true, handled: true, inputs: []string{"invoke はい", "invoke saveButton"}},
		{name: "never", inputs: []string{"invoke saveButton"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := elementRun(t)
			form := expenseWindow()
			dialog, dialogElements := confirmDialog()
			show := func(window desktop.Element, elements []desktop.Element) {
				run.elements.mu.Lock()
				run.elements.window = window
				run.elements.mu.Unlock()
				run.elements.arriveAfter(0, elements)
			}
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
// of tries instead of coming back forever.
func TestHandlerThatDoesNotClearItsScreenFails(t *testing.T) {
	t.Parallel()

	const steps = `{"ai": "never", "input": "direct",
		"handle": [{"name": "入力中クリアの確認", "when": {"text": "クリアしていいですか"}, "recorded": {"windows": [{"click": {"role": "button", "name": "はい"}}]}}],
		"do": [{"expect": {"element": {"role": "text_field", "id": "amountBox"}, "within": "5s"}}]}`
	run := elementRun(t)
	dialog, dialogElements := confirmDialog()
	run.elements.window = dialog
	run.elements.elements = dialogElements
	execution := run.execute(steps, nil)
	require.ErrorContains(t, execution.err, `handle "入力中クリアの確認": the screen was still there after 20 times`)
	assert.Len(t, run.backend.inputs(), maxHandled)
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
	run.backend.focusOn(desktop.WindowID{Handle: 5, Title: "Kitewell", PID: 99})
	run.backend.under = desktop.WindowID{Handle: 5, Title: "Kitewell", PID: 99}
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
	assert.Contains(t, session.observations[1].Results[0].Error, `"Kitewell" belongs to the application that runs this step`)
	assert.Contains(t, session.observations[2].Results[0].Error, "never operates")
}

// The own-application guard covers every window an action reaches: the
// window under the cursor for a button press that moves nothing, a later
// point a drag crosses, and the focused window for a click that holds a
// modifier while the pointer is elsewhere.
func TestOwnApplicationGuardCoversEveryReach(t *testing.T) {
	t.Parallel()

	own := desktop.WindowID{Handle: 5, Title: "Kitewell", PID: 99}
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
