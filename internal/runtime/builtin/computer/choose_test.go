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
// box shows its options and pressing one sets the box to it; without, the
// box opens to nothing and takes typing instead, as an editable combo box.
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
	options := []desktop.Element{}
	for i, name := range []string{"銀行振込", "クレジット", "現金"} {
		options = append(options, desktop.Element{Role: desktop.RoleMenuItem, Name: name, Bounds: image.Rect(190, 250+i*20, 390, 268+i*20),
			Path: []desktop.PathStep{{Role: desktop.RoleComboBox, Name: "支払方法", Index: 0}, {Role: desktop.RoleMenuItem, Name: name, Index: i}}})
	}
	run.elements.onApply = func(e desktop.Element, pattern desktop.Pattern, _ string) {
		switch {
		case e.ID == "methodBox" && pattern == desktop.PatternExpand:
			run.elements.mu.Lock()
			run.elements.elements = append(slices.Clone(run.elements.elements), options...)
			run.elements.mu.Unlock()
		case e.Role == desktop.RoleMenuItem && pattern == desktop.PatternInvoke:
			setMethod(e.Name)
		}
	}
	return run
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
