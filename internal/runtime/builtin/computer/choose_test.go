// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"image"
	"slices"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// methodBox is a pop-up for the payment method, showing 銀行振込, whose
// options its window's elements hold once it opens, as a macOS pop-up
// button's menu is.
var methodBox = desktop.Element{Role: desktop.RoleComboBox, Name: "支払方法", ID: "methodBox", Value: "銀行振込",
	Bounds: image.Rect(190, 220, 390, 244), Path: []desktop.PathStep{{Role: desktop.RoleComboBox, Name: "支払方法", Index: 0}}}

// choiceRun is a run whose window holds methodBox. With opens, opening the
// box shows its options, leaving them as they are once shown, and pressing
// one sets the box to it; without, the box opens to nothing and takes
// typing instead, as an editable combo box.
func choiceRun(t *testing.T, opens bool) *testRun {
	t.Helper()
	run := newTestRun(t)
	run.elements = newFakeElements()
	run.elements.elements = append(run.elements.elements, methodBox)
	setMethod := func(value string) {
		run.elements.mu.Lock()
		defer run.elements.mu.Unlock()
		for i := range run.elements.elements {
			if run.elements.elements[i].ID == "methodBox" {
				run.elements.elements[i].Value = value
			}
		}
	}
	if !opens {
		run.backend.onType = setMethod
		return run
	}
	run.elements.onApply = func(e desktop.Element, pattern desktop.Pattern, _ string) {
		switch {
		case e.ID == "methodBox" && pattern == desktop.PatternExpand:
			openMethodList(run)
		case e.Role == desktop.RoleMenuItem && pattern == desktop.PatternInvoke:
			setMethod(e.Name)
		}
	}
	return run
}

// openMethodList shows methodBox's options among the window's elements, as
// a click on the box does, unless they show already.
func openMethodList(run *testRun) {
	run.elements.mu.Lock()
	defer run.elements.mu.Unlock()
	if slices.ContainsFunc(run.elements.elements, func(e desktop.Element) bool { return e.Role == desktop.RoleMenuItem }) {
		return
	}
	options := slices.Clone(run.elements.elements)
	for i, name := range []string{"銀行振込", "クレジット", "現金"} {
		options = append(options, desktop.Element{Role: desktop.RoleMenuItem, Name: name, Bounds: image.Rect(190, 250+i*20, 390, 268+i*20),
			Path: []desktop.PathStep{{Role: desktop.RoleComboBox, Name: "支払方法", Index: 0}, {Role: desktop.RoleMenuItem, Name: name, Index: i}}})
	}
	run.elements.elements = options
}

// The model chooses an option by name: the box opens and the option presses
// itself, the choice is recorded with the option as its placeholder, and the
// next run replays it with no model.
func TestTheModelChoosesAnOptionByName(t *testing.T) {
	t.Parallel()

	const steps = `{"idle": "0", "variables": {"method": "クレジット"}, "do": [{"act": "支払方法を %method% にする"}]}`
	run := choiceRun(t, true)
	session := &scriptedSession{turns: []*computeruse.Turn{
		actions(computeruse.Action{CallID: "c", Kind: computeruse.KindChoose, ElementID: "e3", Text: "%method%"}),
		done("Chosen"),
	}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(steps, nil)
	require.NoError(t, execution.err)
	require.Len(t, session.observations, 2)
	assert.False(t, session.observations[1].Results[0].Failed(), session.observations[1].Results[0].Error)
	assert.Contains(t, execution.stderr.String(), `chose "%method%" in combo_box "支払方法" from its list`)
	recorded := lookupRecording(t, run, "支払方法を %method% にする").Turns[0].Actions[0]
	assert.Equal(t, computeruse.KindChoose, recorded.Action.Kind)
	assert.Equal(t, "%method%", recorded.Action.Text, "the option is recorded by its placeholder")

	replay := choiceRun(t, true)
	replay.dataDir = run.dataDir
	replay.sessions = nil
	replayed := replay.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"act:" + agentstep.StatusCacheHit}, eventNames(replayed.exec.GetAgentSession()), "the choice replays with no model")
}

// A box that does not open to its options takes the option by typing, never
// followed by Enter, and the choice is checked by what the box shows.
func TestAChoiceIsTypedIntoABoxThatTakesTyping(t *testing.T) {
	t.Parallel()

	run := choiceRun(t, false)
	session := &scriptedSession{turns: []*computeruse.Turn{
		actions(computeruse.Action{CallID: "c", Kind: computeruse.KindChoose, ElementID: "e3", Text: "現金"}),
		done("Chosen"),
	}}
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "支払方法を現金にする"}]}`, nil).err)
	assert.False(t, session.observations[1].Results[0].Failed(), session.observations[1].Results[0].Error)
	inputs := run.backend.inputs()
	assert.Contains(t, inputs, "type 現金")
	assert.NotContains(t, inputs, "key enter", "a choice never presses Enter")
}

// A box whose list is open already, as a click on the box leaves it, is
// chosen in as a closed one is: from its list, or, where the list lies in
// a window of its own, as a Windows combo box's does, by typing, with no
// Escape that could reach the window. An option the step refuses is still
// not chosen.
func TestAChoiceIsMadeInAListAlreadyOpen(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, refuse, shows, says string
		opens                     bool
	}{
		{name: "a pop-up whose menu is open", opens: true, shows: "現金", says: `chose "現金" in combo_box "支払方法" from its list`},
		{name: "a combo box whose list is a window of its own", shows: "現金", says: `chose "現金" in combo_box "支払方法" by typing it`},
		{name: "an option it refuses", refuse: "現金", opens: true, shows: "銀行振込", says: `which refuses "現金"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := choiceRun(t, tc.opens)
			run.llm = nil
			if tc.opens {
				openMethodList(run)
			} else {
				run.elements.withoutPattern("methodBox")
			}
			refuse := "[]"
			if tc.refuse != "" {
				refuse = `["` + tc.refuse + `"]`
			}
			execution := run.execute(`{"ai": "never", "idle": "0", "refuse": `+refuse+`, "do": [
				{"act": {"instruction": "支払方法を現金にする", "recorded": {"windows": [{"choose": {"from": {"role": "combo_box", "id": "methodBox"}, "option": "現金"}}]}}}
			]}`, nil)
			if tc.refuse == "" {
				require.NoError(t, execution.err)
				assert.Contains(t, execution.stderr.String(), tc.says)
			} else {
				require.Error(t, execution.err)
				assert.Contains(t, execution.err.Error(), tc.says)
			}
			box, err := run.elements.Find(desktop.Selector{Role: desktop.RoleComboBox, ID: "methodBox"})
			require.NoError(t, err)
			require.Len(t, box, 1)
			assert.Equal(t, tc.shows, box[0].Value)
			assert.NotContains(t, run.backend.inputs(), "key escape")
		})
	}
}

// A box that shows the option after no way of choosing it fails the action
// and says what it shows, so the model can try another way.
func TestAChoiceThatDoesNotTakeSaysWhatTheBoxShows(t *testing.T) {
	t.Parallel()

	run := choiceRun(t, false)
	run.backend.onType = nil
	session := &scriptedSession{turns: []*computeruse.Turn{
		actions(computeruse.Action{CallID: "c", Kind: computeruse.KindChoose, ElementID: "e3", Text: "現金"}),
		done("Gave up"),
	}}
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "支払方法を現金にする"}]}`, nil).err)
	assert.Equal(t, `combo_box "支払方法" shows "銀行振込", not "現金", after every way to choose it`, session.observations[1].Results[0].Error)
}

// A choice of an option the step refuses, or in a control it refuses, fails
// with the reason and leaves the box as it was.
func TestARefusedChoiceIsNotMade(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, refuse string
		opens        bool
	}{
		{"an option it refuses", "クレジット", true},
		{"an option it refuses in a box that takes typing", "クレジット", false},
		{"a control it refuses", "支払", false},
	} {
		refuse := tc.refuse
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := choiceRun(t, tc.opens)
			run.backend.onType = func(string) { t.Error("a refused choice is never typed") }
			session := &scriptedSession{turns: []*computeruse.Turn{
				actions(computeruse.Action{CallID: "c", Kind: computeruse.KindChoose, ElementID: "e3", Text: "クレジット"}),
				done("Left it"),
			}}
			run.sessions = []*scriptedSession{session}
			require.NoError(t, run.execute(`{"idle": "0", "refuse": ["`+refuse+`"], "do": [{"act": "支払方法をクレジットにする"}]}`, nil).err)
			assert.Contains(t, session.observations[1].Results[0].Error, `which refuses "`+refuse+`"`)
			box, err := run.elements.Find(desktop.Selector{Role: desktop.RoleComboBox, ID: "methodBox"})
			require.NoError(t, err)
			require.Len(t, box, 1)
			assert.Equal(t, "銀行振込", box[0].Value)
		})
	}
}

// An option the step refuses is not chosen even where the box shows it
// already, and an option with a line break, which typing would send as
// Enter, is not chosen at all.
func TestAChoiceNeverTakesARefusedOrMultilineOption(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, with, option, reason string
	}{
		{"a refused option the box shows", `"refuse": ["銀行"]`, "銀行振込", `which refuses "銀行"`},
		{"an option with a line break", `"refuse": []`, "現金\n", "cannot be told which option to show"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := choiceRun(t, false)
			run.backend.onType = func(string) { t.Error("the option is never typed") }
			session := &scriptedSession{turns: []*computeruse.Turn{
				actions(computeruse.Action{CallID: "c", Kind: computeruse.KindChoose, ElementID: "e3", Text: tc.option}),
				done("Left it"),
			}}
			run.sessions = []*scriptedSession{session}
			require.NoError(t, run.execute(`{"idle": "0", `+tc.with+`, "do": [{"act": "支払方法を確かめる"}]}`, nil).err)
			assert.Contains(t, session.observations[1].Results[0].Error, tc.reason)
			assert.Empty(t, run.backend.inputs(), "nothing reached the box")
		})
	}
}

// A pinned choose step sets the box with no model, and a recorded choice
// pins as one.
func TestAPinnedChoiceRunsWithNoModel(t *testing.T) {
	t.Parallel()

	run := choiceRun(t, true)
	run.llm = nil
	execution := run.execute(`{"ai": "never", "idle": "0", "variables": {"method": "クレジット"}, "do": [
		{"act": {"instruction": "支払方法を選ぶ", "recorded": {"windows": [{"choose": {"from": {"role": "combo_box", "id": "methodBox"}, "option": "%method%"}}]}}},
		{"expect": {"text": "クレジット"}}
	]}`, nil)
	require.NoError(t, execution.err)

	steps, reason := pinAction(recordedAction{Action: computeruse.Action{Kind: computeruse.KindChoose, Text: "%method%"}, Element: new(pinnedElement(desktop.Selector{Role: desktop.RoleComboBox, ID: "methodBox"}))})
	require.Empty(t, reason)
	require.Len(t, steps, 1)
	require.NotNil(t, steps[0].Choose)
	assert.Equal(t, "%method%", steps[0].Choose.Option)
	assert.Equal(t, "methodBox", steps[0].Choose.From.ID)
}
