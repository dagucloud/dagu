// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A step that refuses a word never presses an element named with it: not a
// click the model aims at it by position or by id, nor Enter while it is in
// the window in front, where Enter may press it as the default button. The
// model is told why, and nothing reaches the application.
func TestARefusedWordIsNeverPressed(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		turn *computeruse.Turn
	}{
		{"a click at it", actions(clickAt(235, 194))},
		{"a click on its id", actions(computeruse.Action{CallID: "x", Kind: computeruse.KindClick, ElementID: "e1"})},
		{"Enter in its window", actions(keyPress("Return"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := newTestRun(t)
			run.elements = newFakeElements()
			session := &scriptedSession{turns: []*computeruse.Turn{tc.turn, done("Left it")}}
			run.sessions = []*scriptedSession{session}
			execution := run.execute(`{"idle": "0", "refuse": ["保存"], "do": [{"act": "金額を確かめる"}]}`, nil)
			require.NoError(t, execution.err)
			assert.Empty(t, run.backend.inputs(), "nothing reached the application")
			require.Len(t, session.observations, 2)
			assert.Contains(t, session.observations[1].Results[0].Error, `button "保存" is not pressed in this step, which refuses "保存"`)
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
