// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pinOf converts a recording the way it is read from the cache file.
func pinOf(t *testing.T, entry recording) PinnedOperation {
	t.Helper()
	data, err := json.Marshal(entry)
	require.NoError(t, err)
	pinned, ok := PinRecording(data)
	require.True(t, ok)
	return pinned
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}

func keyPress(keys ...string) computeruse.Action {
	return computeruse.Action{Kind: computeruse.KindKey, Keys: keys}
}

// An act learned on one device, pinned, runs on another device of the same
// system with no recording of its own and no model.
func TestPinnedRecordingRunsOnAnotherDevice(t *testing.T) {
	t.Parallel()

	learned := elementRun(t)
	learned.focusOn("amountBox")
	learned.sessions = []*scriptedSession{{turns: []*computeruse.Turn{
		actions(clickAt(290, 152)), actions(typing("12,000")), actions(keyPress("ctrl", "s")), actions(clickAt(235, 194)), done("Posted"),
	}}}
	before := time.Now().UTC()
	require.NoError(t, learned.execute(`{"variables": {"amount": "12,000"}, "do": [{"act": "Post the invoice"}]}`, nil).err)

	pinned := pinOf(t, lookupRecording(t, learned, "Post the invoice"))
	assert.Equal(t, 1, pinned.Op, "the position counts from 1")
	assert.Equal(t, opAct, pinned.Kind)
	assert.Equal(t, systemWindows, pinned.System)
	assert.False(t, pinned.SavedAt.Before(before), "saved when the run ran")
	assert.Empty(t, pinned.Unpinnable)
	steps := marshal(t, pinned.Recorded)
	assert.JSONEq(t, `[
		{"click": {"app": "expense", "role": "text_field", "id": "amountBox"}},
		{"type": {"into": {"app": "expense", "role": "text_field", "id": "amountBox"}, "text": "%amount%"}},
		{"key": "ctrl+s", "focus": {"app": "expense", "role": "text_field", "id": "amountBox"}},
		{"click": {"app": "expense", "role": "button", "name": "保存", "id": "saveButton"}}
	]`, steps, "typed values keep their placeholder, and keys the element they went to")

	device := elementRun(t)
	replayed := device.execute(`{"ai": "never", "variables": {"amount": "8,500"}, "do": [{"act": {"instruction": "Post the invoice", "recorded": {"windows": `+steps+`}}}]}`, nil)
	require.NoError(t, replayed.err)
	events := operationEvents(replayed.exec.GetAgentSession())
	require.Len(t, events, 1)
	assert.Equal(t, agentstep.StatusCacheHit, events[0].Status)
	assert.Equal(t, agentstep.ViaElement, events[0].Via)
	assert.Equal(t, []string{"move 290,152", "left down #1", "focus amountBox", "type 8,500", "focus amountBox", "key ctrl", "key s", "move 235,194", "left down #1"}, device.backend.inputs())
}

// When a pinned step misses and the model finishes the act, the recording
// begins with the pinned steps that ran, so pinning it again keeps them as
// written instead of dropping the start of the task.
func TestHealedPinnedActRecordsTheStepsThatRan(t *testing.T) {
	t.Parallel()

	const pinned = `[{"type": {"into": {"role": "text_field", "id": "amountBox"}, "text": "%amount%"}}, {"wait": "10ms"}, {"click": {"role": "button", "name": "送信", "id": "submit"}}]`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	healed := run.execute(`{"variables": {"amount": "12,000"}, "do": [{"act": {"instruction": "Save the invoice", "recorded": {"windows": `+pinned+`}}}]}`, nil)
	require.NoError(t, healed.err)
	assert.Equal(t, agentstep.StatusHealed, operationEvents(healed.exec.GetAgentSession())[0].Status)

	steps := marshal(t, pinOf(t, lookupRecording(t, run, "Save the invoice")).Recorded)
	assert.JSONEq(t, `[
		{"type": {"into": {"role": "text_field", "id": "amountBox"}, "text": "%amount%"}},
		{"wait": "10ms"},
		{"click": {"app": "expense", "role": "button", "name": "保存", "id": "saveButton"}}
	]`, steps, "the pinned steps that ran, then what the model did in place of the one that missed")
}

// An element without an id is looked for in its closest named container, so
// a name that repeats in the window still names one element.
func TestPinnedRecordingNamesTheContainer(t *testing.T) {
	t.Parallel()

	elements := expenseElements()
	for i := range elements {
		if elements[i].ID == "paymentSaveButton" {
			elements[i].ID = ""
		}
	}
	learned := elementRun(t)
	learned.elements.elements = elements
	learned.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(255, 384)), done("Saved")}}}
	require.NoError(t, learned.execute(`{"do": [{"act": "Save the payment"}]}`, nil).err)

	steps := marshal(t, pinOf(t, lookupRecording(t, learned, "Save the payment")).Recorded)
	assert.JSONEq(t, `[{"click": {"app": "expense", "role": "button", "name": "保存", "in": {"role": "group", "name": "支払情報"}}}]`, steps)

	device := elementRun(t)
	device.elements.elements = elements
	require.NoError(t, device.execute(`{"ai": "never", "do": [{"act": {"instruction": "Save the payment", "recorded": {"windows": `+steps+`}}}]}`, nil).err)
	assert.Equal(t, []string{"move 255,384", "left down #1"}, device.backend.inputs(), "the button in the group, not the other 保存")
}

// Each recorded action becomes the pinned step that does the same, and an
// act with any action that has none cannot be pinned, naming each such
// action and why.
func TestPinnedRecordingConvertsEachAction(t *testing.T) {
	t.Parallel()

	save := &recordedElement{Selector: desktop.Selector{Role: desktop.RoleButton, Name: "保存", ID: "saveButton"}, App: "expense", FractionX: 0.5, FractionY: 0.5}
	untitled := &recordedElement{Selector: desktop.Selector{Role: desktop.RoleButton, Name: "OK"}, Window: "確認 - %invoice%", FractionX: 0.7, FractionY: 0.3}
	saveEdge := &recordedElement{Selector: save.Selector, App: "expense", FractionX: 0.95, FractionY: 0.5}
	amount := &recordedElement{Selector: desktop.Selector{Role: desktop.RoleTextField, ID: "amountBox"}, App: "expense", FractionX: 0.05, FractionY: 0.5}
	agree := &recordedElement{Selector: desktop.Selector{Role: desktop.RoleCheckbox, ID: "agreeBox"}, App: "expense", FractionX: 0.5, FractionY: 0.5}
	at := &computeruse.Point{X: 235, Y: 194}
	for _, tc := range []struct {
		name       string
		actions    []recordedAction
		steps      string
		unpinnable []UnpinnableAction
	}{
		{
			name: "pinnable",
			actions: []recordedAction{
				{Action: computeruse.Action{Kind: computeruse.KindMove, Point: at}, Element: save},
				{Action: computeruse.Action{Kind: computeruse.KindClick, Point: at}, Element: save},
				{Action: computeruse.Action{Kind: computeruse.KindClick, Button: computeruse.ButtonLeft, Count: 2, Point: at}, Element: save},
				{Action: computeruse.Action{Kind: computeruse.KindClick, Button: computeruse.ButtonRight, Point: at}, Element: untitled},
				{Action: computeruse.Action{Kind: computeruse.KindScroll, ScrollY: -3, Point: at}, Element: save},
				{Action: computeruse.Action{Kind: computeruse.KindKey, Keys: []string{"tab"}, Repeat: 2}},
				{Action: computeruse.Action{Kind: computeruse.KindClick, Point: at}, Element: amount},
				{Action: computeruse.Action{Kind: computeruse.KindKey, Keys: []string{"shift", "tab"}, Repeat: 2}, Element: amount},
				{Action: computeruse.Action{Kind: computeruse.KindWait, Duration: 1500 * time.Millisecond}},
				{Action: computeruse.Action{Kind: computeruse.KindWait, Duration: 5 * time.Minute}},
				{Action: computeruse.Action{Kind: computeruse.KindClick, Point: at}, Element: agree, Checked: new(true)},
				{Action: computeruse.Action{Kind: computeruse.KindClick, Point: at}, Element: agree, Checked: new(false)},
			},
			steps: `[
				{"click": {"app": "expense", "role": "button", "name": "保存", "id": "saveButton"}},
				{"double_click": {"app": "expense", "role": "button", "name": "保存", "id": "saveButton"}},
				{"right_click": {"window": "確認 - %invoice%", "role": "button", "name": "OK"}},
				{"scroll": {"on": {"app": "expense", "role": "button", "name": "保存", "id": "saveButton"}, "dy": -3}},
				{"key": "tab"}, {"key": "tab"},
				{"click": {"app": "expense", "role": "text_field", "id": "amountBox"}},
				{"key": "shift+tab", "focus": {"app": "expense", "role": "text_field", "id": "amountBox"}}, {"key": "shift+tab"},
				{"wait": "1.5s"}, {"wait": "1m0s"},
				{"check": {"app": "expense", "role": "checkbox", "id": "agreeBox"}},
				{"uncheck": {"app": "expense", "role": "checkbox", "id": "agreeBox"}}
			]`,
		},
		{
			name: "unpinnable",
			actions: []recordedAction{
				{Action: computeruse.Action{Kind: computeruse.KindClick, Point: at}, Element: save},
				{Action: computeruse.Action{Kind: computeruse.KindClick, Point: at}},
				{Action: computeruse.Action{Kind: computeruse.KindClick, Point: at, Modifiers: []string{"ctrl"}}, Element: save},
				{Action: computeruse.Action{Kind: computeruse.KindDrag, Path: []computeruse.Point{{X: 1, Y: 2}, {X: 3, Y: 4}}}, Element: save},
				{Action: computeruse.Action{Kind: computeruse.KindMove, Point: at}, Element: save},
				{Action: computeruse.Action{Kind: computeruse.KindType, Text: "%amount%"}},
				{Action: computeruse.Action{Kind: computeruse.KindClick, Point: at}, Element: saveEdge},
				{Action: computeruse.Action{Kind: computeruse.KindScroll, ScrollY: 3, Point: at}, Element: saveEdge},
				{Action: computeruse.Action{Kind: computeruse.KindZoom}},
			},
			unpinnable: []UnpinnableAction{
				{Turn: 2, Action: "click at 235,194", Code: "no_element", Reason: "no element could be read where it landed"},
				{Turn: 2, Action: "click at 235,194 with ctrl", Code: "click_with_keys", Reason: "a click holding keys has no pinned form"},
				{Turn: 2, Action: "drag from 1,2 to 3,4", Code: "drag", Reason: "a drag has no pinned form"},
				{Turn: 2, Action: "move at 235,194", Code: "move", Reason: "moving the pointer alone, such as to open a menu by hovering, has no pinned form"},
				{Turn: 2, Action: `type "%amount%"`, Code: "no_field", Reason: "the field it typed into could not be read"},
				{Turn: 2, Action: "click at 235,194", Code: "edge_click", Reason: "the click landed near the edge of its element, and a pin clicks its centre"},
				{Turn: 2, Action: "scroll 0,3 at 235,194", Code: "edge_scroll", Reason: "the scroll landed near the edge of its element, and a pin scrolls at its centre"},
				{Turn: 2, Action: "zoom", Code: "unsupported", Reason: "zoom has no pinned form"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entry := recording{Op: 0, Version: recordingVersion, System: systemWindows, Turns: []recordedTurn{
				{Actions: tc.actions[:1]}, {Actions: tc.actions[1:]},
			}}
			pinned := pinOf(t, entry)
			assert.Equal(t, tc.unpinnable, pinned.Unpinnable)
			if tc.steps == "" {
				assert.Nil(t, pinned.Recorded, "an act missing an action is not pinned at all")
				return
			}
			steps := marshal(t, pinned.Recorded)
			assert.JSONEq(t, tc.steps, steps)
			var with map[string]any
			require.NoError(t, json.Unmarshal([]byte(`{"ai": "never", "variables": {"invoice": "1042"}, "do": [{"act": {"instruction": "Save", "recorded": {"windows": `+steps+`}}}]}`), &with))
			step := ir.Step{Name: "post", ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: with}}
			require.NoError(t, validateStep(step), "the pin is valid in a step")
		})
	}
}

// An extract's bindings become pinned fields that another device reads with
// no model: a scalar by its element, a table by its container and headers.
func TestPinnedRecordingReadsExtractOnAnotherDevice(t *testing.T) {
	t.Parallel()

	const schema = `{"type": "object", "properties": {"amount": {"type": "number"}, "lines": {"type": "array", "items": {"type": "object", "properties": {"item": {"type": "string"}, "amount": {"type": "number"}}}}}}`
	learned := elementRun(t)
	withTable(learned)
	learned.elements.tableRows = [][]string{{"りんご", "1,200"}}
	learned.vision.extract = `{"amount": {"value": 12000, "element": "e3"}, "lines": {"container": {"role": "list", "name": "明細"}, "columns": {"item": "品目", "amount": "金額"}, "rows": [{"item": "りんご", "amount": 1200}]}}`
	require.NoError(t, learned.execute(`{"do": [{"extract": {"instruction": "The invoice", "schema": `+schema+`}}]}`, nil).err)

	pinned := pinOf(t, lookupRecording(t, learned, "The invoice"))
	assert.Equal(t, opExtract, pinned.Kind)
	fields := marshal(t, pinned.Fields)
	assert.JSONEq(t, `{
		"amount": {"app": "expense", "role": "text_field", "id": "amountBox"},
		"lines": {"container": {"app": "expense", "role": "list", "name": "明細"}, "columns": {"item": "品目", "amount": "金額"}}
	}`, fields)

	device := elementRun(t)
	withTable(device)
	device.elements.tableRows = [][]string{{"みかん", "3,400"}}
	replayed := device.execute(`{"ai": "never", "do": [{"extract": {"instruction": "The invoice", "schema": `+schema+`, "fields": {"windows": `+fields+`}}}]}`, nil)
	require.NoError(t, replayed.err)
	assert.EqualValues(t, 12000, replayed.exec.GetOutputs()["amount"])
	assert.Equal(t, []any{map[string]any{"item": "みかん", "amount": int64(3400)}}, replayed.exec.GetOutputs()["lines"])
	assert.Empty(t, device.vision.requests, "no model request")
}

// Only a recording of a format this engine replays that recorded something
// converts; one that does not name its system was made on this host.
func TestPinRecordingSkipsWhatCannotReplay(t *testing.T) {
	t.Parallel()

	enter := []recordedTurn{{Actions: []recordedAction{{Action: keyPress("enter")}}}}
	for name, entry := range map[string]string{
		"older format": marshal(t, recording{Version: oldestRecordingVersion - 1, Turns: enter}),
		"newer format": marshal(t, recording{Version: recordingVersion + 1, Turns: enter}),
		"nothing":      marshal(t, recording{Version: recordingVersion}),
		"not json":     `{`,
	} {
		_, ok := PinRecording(json.RawMessage(entry))
		assert.False(t, ok, name)
	}

	pinned, ok := PinRecording(json.RawMessage(marshal(t, recording{Op: 2, Version: recordingVersion, Turns: enter})))
	require.True(t, ok)
	assert.Equal(t, hostSystem(), pinned.System)
	assert.Equal(t, 3, pinned.Op)
	assert.True(t, pinned.SavedAt.IsZero())

	_, ok = PinRecording(json.RawMessage(marshal(t, recording{Version: oldestRecordingVersion, Turns: enter})))
	assert.True(t, ok, "a recording from before windows were recorded still converts")
}
