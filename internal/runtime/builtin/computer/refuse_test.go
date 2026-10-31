// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"image"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A step that refuses a word never presses an element named with it: not a
// click the model aims at it by position, by id, or at its label, nor a drag
// that starts on it, nor a button press while the pointer rests on it, nor
// Enter, pressed, held, typed as a line break, or held with a click, while
// it is in the window in front, where Enter may press it as the default
// button. Nothing reaches the application, and the model is told why: a
// press names the element, and Enter names the default button and what
// sets a field or an option instead.
func TestARefusedWordIsNeverPressed(t *testing.T) {
	t.Parallel()

	const (
		pressed       = `button "保存" is not pressed in this step, which refuses "保存"; leave it as it is`
		returnPressed = `Return is not pressed: it may press the default button "保存", which this step never presses. To set a combo box or pop-up, call choose_option, or open it and click the option; to fill a text field, type without Return; to move on, press Tab.`
	)
	// The button's label, a child the system reads at the button's centre.
	label := func(r *testRun) {
		r.elements.elements = append(r.elements.elements, desktop.Element{Role: desktop.RoleText, Bounds: image.Rect(215, 186, 255, 202),
			Path: []desktop.PathStep{{Role: desktop.RoleButton, Name: "保存", Index: 0}, {Role: desktop.RoleText, Index: 0}}})
	}
	for _, tc := range []struct {
		name  string
		setup func(*testRun)
		turn  *computeruse.Turn
		want  string
	}{
		{"a click at it", nil, actions(clickAt(235, 194)), pressed},
		{"a click on its id", nil, actions(computeruse.Action{CallID: "x", Kind: computeruse.KindClick, ElementID: "e1"}), pressed},
		{"a click on its label", label, actions(clickAt(235, 194)), pressed},
		{"a click inside a custom control named with it", func(r *testRun) {
			control := desktop.PathStep{Role: desktop.RoleOther, Name: "保存する", Index: 0}
			r.elements.elements = append(r.elements.elements,
				desktop.Element{Role: desktop.RoleOther, Name: "保存する", Bounds: image.Rect(300, 140, 380, 170), Path: []desktop.PathStep{control}},
				desktop.Element{Role: desktop.RoleText, Bounds: image.Rect(310, 145, 370, 165), Path: []desktop.PathStep{control, {Role: desktop.RoleText, Index: 0}}})
		}, actions(clickAt(340, 155)), `other "保存する" is not pressed in this step, which refuses "保存"; leave it as it is`},
		{"a drag that starts on it", nil, actions(computeruse.Action{CallID: "d", Kind: computeruse.KindDrag, Path: []computeruse.Point{{X: 235, Y: 194}, {X: 300, Y: 194}}}), pressed},
		{"a button press while the pointer rests on it", func(r *testRun) { r.backend.pointer = image.Pt(235, 194) },
			actions(computeruse.Action{CallID: "p", Kind: computeruse.KindMouseDown, Button: "left", Point: &computeruse.Point{X: 10, Y: 10}}), pressed},
		{"Enter in its window", nil, actions(keyPress("Return")), returnPressed},
		{"Enter held in its window", nil, actions(computeruse.Action{CallID: "h", Kind: computeruse.KindHoldKey, Keys: []string{"Return"}, Duration: time.Millisecond}), returnPressed},
		{"a line break typed in its window", nil, actions(computeruse.Action{CallID: "t", Kind: computeruse.KindType, Text: "12000\n"}), returnPressed},
		{"a click elsewhere holding Enter", nil, actions(computeruse.Action{CallID: "m", Kind: computeruse.KindClick, Point: &computeruse.Point{X: 10, Y: 10}, Modifiers: []string{"return"}}), returnPressed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := newTestRun(t)
			run.elements = newFakeElements()
			if tc.setup != nil {
				tc.setup(run)
			}
			session := &scriptedSession{turns: []*computeruse.Turn{tc.turn, done("Left it")}}
			run.sessions = []*scriptedSession{session}
			execution := run.execute(`{"idle": "0", "refuse": ["保存"], "do": [{"act": "金額を確かめる"}]}`, nil)
			require.NoError(t, execution.err)
			assert.Empty(t, run.backend.inputs(), "nothing reached the application")
			require.Len(t, session.observations, 2)
			assert.Equal(t, tc.want, session.observations[1].Results[0].Error)
		})
	}
}

// A pinned step that would press a refused element is a miss, so a run under
// never fails rather than press it.
func TestARefusedWordStopsAPinnedPress(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.llm = nil
	execution := run.execute(`{"ai": "never", "idle": "0", "refuse": ["保存"], "do": [{"act": {"instruction": "Save the form", "recorded": {"windows": [{"click": {"role": "button", "id": "saveButton"}}]}}}]}`, nil)
	require.ErrorContains(t, execution.err, `which refuses "保存"`)
	assert.Empty(t, run.backend.inputs(), "nothing reached the application")
}

// A Latin word is refused as a whole word, ignoring case and the width of
// characters; any other word wherever a name holds it.
func TestRefusedWordsMatchNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, word string
		refused    bool
	}{
		{"Save", "save", true},
		{"Save draft", "Save", true},
		{"Ｓａｖｅ", "save", true},
		{"Unsaved changes", "save", false},
		{"Place order", "place order", true},
		{"受注を保存", "保存", true},
		{"保存先", "保存", true},
		{"キャンセル", "保存", false},
	} {
		assert.Equal(t, tc.refused, namesWord(tc.name, tc.word), "%q refusing %q", tc.name, tc.word)
	}
}
