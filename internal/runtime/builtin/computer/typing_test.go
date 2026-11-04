// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"fmt"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The model is told what its typing did to the field: what the field now
// shows, or, when the field shows what it did before without the typed
// text, that the typing did not reach it, which stops the turn before the
// save after it. A field that shows the typed value as it formats it, such
// as 12,000 for 12000, has it.
func TestTheModelIsToldWhatTypingDid(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		typed string
		lands bool
		check func(t *testing.T, results []computeruse.Result, inputs []string)
	}{
		{"it lands", "1200", true, func(t *testing.T, results []computeruse.Result, inputs []string) {
			assert.Equal(t, `the field now shows "1200"`, results[0].Output)
			assert.Contains(t, inputs, "invoke saveButton", "the save after it runs")
		}},
		{"it does not land", "1200", false, func(t *testing.T, results []computeruse.Result, inputs []string) {
			assert.Equal(t, `the typing did not reach the field, which still shows "12,000"`, results[0].Error)
			assert.True(t, results[1].Skipped, "the save after it does not run")
			assert.NotContains(t, inputs, "invoke saveButton")
		}},
		{"the field shows it formatted", "12000", false, func(t *testing.T, results []computeruse.Result, inputs []string) {
			assert.False(t, results[0].Failed(), results[0].Error)
			assert.Contains(t, inputs, "invoke saveButton", "the save after it runs")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := elementRun(t)
			run.focusOn("amountBox")
			if tc.lands {
				run.backend.onType = func(text string) {
					run.elements.mu.Lock()
					run.elements.focused.Value = text
					run.elements.mu.Unlock()
				}
			}
			session := &scriptedSession{turns: []*computeruse.Turn{
				actions(typing(tc.typed), computeruse.Action{CallID: "s", Kind: computeruse.KindClick, ElementID: "e1"}),
				done("Entered"),
			}}
			run.sessions = []*scriptedSession{session}
			execution := run.execute(`{"idle": "0", "do": [{"act": "金額に 1200 を入れて保存する"}]}`, nil)
			require.NoError(t, execution.err)
			require.Len(t, session.observations, 2)
			tc.check(t, session.observations[1].Results, run.backend.inputs())
		})
	}
}

// A field that shows nothing before and after the typing, as a password
// field always does, says nothing of it.
func TestTypingIntoAFieldThatShowsNothingSaysNothing(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.elements.elements = append(run.elements.elements, desktop.Element{Role: desktop.RoleTextField, ID: "pinBox",
		Path: []desktop.PathStep{{Role: desktop.RoleTextField, Index: 1}}})
	run.focusOn("pinBox")
	session := &scriptedSession{turns: []*computeruse.Turn{actions(typing("4321")), done("Entered")}}
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "暗証番号を入れる"}]}`, nil).err)
	require.Len(t, session.observations, 2)
	result := session.observations[1].Results[0]
	assert.False(t, result.Failed())
	assert.Empty(t, result.Output)
}

// Typing into a field the model names by id sets the field's value through
// the element when the field reads back the text, so no key is sent. The
// act records it so, and its replay sets the value the same way.
func TestTypingIntoAFieldByIdSetsItsValue(t *testing.T) {
	t.Parallel()

	const instruction = "金額に %amount% を入れて保存する"
	run, session := elementTurnRun(t,
		actions(computeruse.Action{CallID: "t", Kind: computeruse.KindType, ElementID: "e1", Text: "%amount%"},
			computeruse.Action{CallID: "s", Kind: computeruse.KindClick, ElementID: "e2"}),
		done("Saved"))
	run.elements.elements[2].Name = "金額"
	steps := `{"idle": "0", "variables": {"amount": "1200"}, "do": [{"act": "` + instruction + `"}]}`
	require.NoError(t, run.execute(steps, nil).err)
	assert.Equal(t, []string{"set amountBox=1200", "invoke saveButton"}, run.backend.inputs(), "no key reaches the window")
	require.Len(t, session.observations, 2)
	assert.Equal(t, `金額 now shows "%amount%"`, session.observations[1].Results[0].Output)

	pinned := pinOf(t, lookupRecording(t, run, instruction))
	assert.JSONEq(t, `[
		{"type": {"into": {"app": "expense", "role": "text_field", "name": "金額", "id": "amountBox"}, "text": "%amount%"}},
		{"click": {"app": "expense", "role": "button", "name": "保存", "id": "saveButton"}}
	]`, marshal(t, pinned.Recorded))

	run.backend.events = nil
	run.elements.setValue("amountBox", "12,000")
	require.NoError(t, run.execute(`{"ai": "never", "idle": "0", "variables": {"amount": "1200"}, "do": [{"act": "`+instruction+`"}]}`, nil).err)
	assert.Equal(t, []string{"set amountBox=1200", "invoke saveButton"}, run.backend.inputs(), "the replay sets the value as the act did")
}

// Typing into a field by id types keys over the field's text, which the
// field takes the focus with and selects, when the field takes no value
// through the element, when it reads back something else, as one that
// reformats what it is given does, which is emptied for the keys, or when
// the text holds a line break. The act records the selection, so a replay
// replaces the field's text too.
func TestTypingIntoAFieldByIdFallsBackToKeys(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		text   string
		setup  func(*testRun)
		inputs []string
	}{
		{"the field takes no value", "1200", func(r *testRun) { r.elements.withoutPattern("amountBox") },
			[]string{"focus amountBox", "key ctrl", "key a", "type 1200"}},
		{"the field reformats the value", "1200", func(r *testRun) {
			r.elements.onApply = func(e desktop.Element, _ desktop.Pattern, value string) {
				r.backend.recordAccessibility(fmt.Sprintf("set %s=%s", focusedID(e), value))
				if value != "" {
					r.elements.setValue("amountBox", "1,200")
				}
			}
		}, []string{"set amountBox=1200", "set amountBox=", "focus amountBox", "key ctrl", "key a", "type 1200"}},
		{"the text holds a line break", "1200\n", nil, []string{"focus amountBox", "key ctrl", "key a", "type 1200", "key enter"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run, session := elementTurnRun(t, actions(computeruse.Action{CallID: "t", Kind: computeruse.KindType, ElementID: "e1", Text: tc.text}), done("Entered"))
			run.backend.onType = func(text string) {
				run.elements.mu.Lock()
				run.elements.focused.Value = text
				run.elements.mu.Unlock()
			}
			if tc.setup != nil {
				tc.setup(run)
			}
			require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "金額を入れる"}]}`, nil).err)
			assert.Equal(t, tc.inputs, run.backend.inputs())
			require.Len(t, session.observations, 2)
			assert.False(t, session.observations[1].Results[0].Failed(), session.observations[1].Results[0].Error)
			pinned := pinOf(t, lookupRecording(t, run, "金額を入れる"))
			require.NotEmpty(t, pinned.Recorded)
			assert.JSONEq(t, `{"key": "ctrl+a", "focus": {"app": "expense", "role": "text_field", "id": "amountBox"}}`, marshal(t, pinned.Recorded[0]),
				"the replay selects the field's text before it types")
		})
	}
}
