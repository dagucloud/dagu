// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"fmt"
	"image"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The model is offered what the window's elements say: a field with no name
// of its own by the label beside it, what each holds with the values the
// step types written as their placeholders, and the options of an open menu
// before everything else.
func TestTheModelIsOfferedWhatElementsHold(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.elements = newFakeElements()
	run.elements.elements = append(run.elements.elements,
		desktop.Element{Role: desktop.RoleTextField, Label: "数量", Value: "12", Bounds: image.Rect(190, 220, 390, 244),
			Path: []desktop.PathStep{{Role: desktop.RoleTextField, Index: 1}}},
		desktop.Element{Role: desktop.RoleMenuItem, Name: "明光ラボ", Bounds: image.Rect(190, 250, 390, 270),
			Path: []desktop.PathStep{{Role: desktop.RoleMenuItem, Name: "明光ラボ", Index: 0}}},
	)
	session := &scriptedSession{turns: []*computeruse.Turn{done("Looked")}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"idle": "0", "variables": {"qty": "12"}, "do": [{"act": "数量に %qty% を入れる"}]}`, nil)
	require.NoError(t, execution.err)
	require.NotEmpty(t, session.observations)
	offered := session.observations[0].Elements
	require.NotEmpty(t, offered)
	assert.Equal(t, computeruse.Element{ID: "e1", Role: desktop.RoleMenuItem, Name: "明光ラボ", Takes: []computeruse.Kind{computeruse.KindClick}}, offered[0], "the open menu's option comes first")
	assert.Contains(t, offered, computeruse.Element{ID: "e4", Role: desktop.RoleTextField, Name: "数量", Value: "%qty%", Takes: []computeruse.Kind{computeruse.KindClick, computeruse.KindType}},
		"the unnamed field goes by its label, and the typed value by its placeholder")
}

// A value that holds a declared secret is shown with the whole secret
// masked, even where a typed value is part of the secret.
func TestAnOfferedValueNeverShowsPartOfASecret(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.secrets = map[string]string{"PIN": "4242-9876"}
	run.elements = newFakeElements()
	run.elements.elements = append(run.elements.elements,
		desktop.Element{Role: desktop.RoleTextField, Name: "暗証番号", Value: "4242-9876", Bounds: image.Rect(190, 220, 390, 244),
			Path: []desktop.PathStep{{Role: desktop.RoleTextField, Name: "暗証番号", Index: 1}}})
	session := &scriptedSession{turns: []*computeruse.Turn{done("Looked")}}
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(`{"idle": "0", "variables": {"prefix": "4242"}, "visible": ["prefix"], "do": [{"act": "暗証番号を確かめる"}]}`, nil).err)
	require.NotEmpty(t, session.observations)
	for _, e := range session.observations[0].Elements {
		if e.Name == "暗証番号" {
			assert.NotContains(t, e.Value, "9876")
			assert.NotContains(t, e.Value, "4242")
			return
		}
	}
	t.Fatal("the field was not offered")
}

// An element's name, or the label an unnamed field goes by, is shown with
// declared secrets masked.
func TestAnOfferedNameNeverShowsASecret(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.secrets = map[string]string{"PIN": "4242-9876"}
	run.elements = newFakeElements()
	run.elements.elements = append(run.elements.elements,
		desktop.Element{Role: desktop.RoleTextField, Label: "照合 4242-9876", Bounds: image.Rect(190, 220, 390, 244),
			Path: []desktop.PathStep{{Role: desktop.RoleTextField, Index: 1}}},
		desktop.Element{Role: desktop.RoleButton, Name: "4242-9876 で照合", Bounds: image.Rect(190, 250, 390, 270),
			Path: []desktop.PathStep{{Role: desktop.RoleButton, Name: "4242-9876 で照合", Index: 0}}})
	session := &scriptedSession{turns: []*computeruse.Turn{done("Looked")}}
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "照合欄を確かめる"}]}`, nil).err)
	require.NotEmpty(t, session.observations)
	var names []string
	for _, e := range session.observations[0].Elements {
		names = append(names, e.Name)
	}
	assert.Subset(t, names, []string{"照合 *******", "******* で照合"})
}

// elementTurnRun is the expense form with a session that may be sent the
// element list without a screenshot, answering with turns.
func elementTurnRun(t *testing.T, turns ...*computeruse.Turn) (*testRun, *scriptedSession) {
	t.Helper()
	run := elementRun(t)
	session := &scriptedSession{elementTurns: true, turns: turns}
	run.sessions = []*scriptedSession{session}
	return run, session
}

// A window whose elements are read in full goes to a model that may decide
// from them without a screenshot: the window's text and its unnamed field
// are listed among its controls, each control with what it takes and two
// alike told apart by the group one sits in. The model sees the screen
// once it asks, which does nothing on the screen.
func TestATurnGoesWithTheElementsAlone(t *testing.T) {
	t.Parallel()

	run, session := elementTurnRun(t, actions(computeruse.Action{CallID: "s", Kind: computeruse.KindScreenshot}), done("Looked"))
	require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "金額を確かめる"}]}`, nil).err)

	require.Len(t, session.observations, 2)
	first := session.observations[0]
	assert.Empty(t, first.Screen.Image.Data, "no screenshot goes with the list")
	click := []computeruse.Kind{computeruse.KindClick}
	assert.Equal(t, []computeruse.Element{
		{Role: desktop.RoleText, Name: "保存しました"},
		{Role: desktop.RoleText, Name: "金額"},
		{ID: "e1", Role: desktop.RoleTextField, Value: "12,000", Takes: []computeruse.Kind{computeruse.KindClick, computeruse.KindType}},
		{ID: "e2", Role: desktop.RoleButton, Name: "保存", Takes: click},
		{ID: "e3", Role: desktop.RoleButton, Name: "保存", In: "支払情報", Takes: click},
	}, first.Elements)
	assert.NotEmpty(t, session.observations[1].Screen.Image.Data, "the model sees the screen it asked for")
	assert.Empty(t, run.backend.inputs())
}

// A turn comes with a screenshot when the elements cannot stand for the
// window: the host reads none, the list would leave some out, none of them
// takes an action, or the session cannot take a turn without one.
func TestATurnKeepsItsScreenshotWhenTheElementsCannotStandForTheWindow(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		setup func(*testRun, *scriptedSession)
	}{
		{"the host reads no elements", func(r *testRun, _ *scriptedSession) { r.elements = nil }},
		{"the list would leave some out", func(r *testRun, _ *scriptedSession) {
			for i := range maxModelElements {
				r.elements.elements = append(r.elements.elements, desktop.Element{Role: desktop.RoleText, Name: fmt.Sprintf("行 %d", i+1), Bounds: image.Rect(120, 460, 300, 480)})
			}
		}},
		{"no element takes an action", func(r *testRun, _ *scriptedSession) {
			r.elements.elements = []desktop.Element{
				{Role: desktop.RoleText, Name: "読み込み中", Bounds: image.Rect(120, 110, 300, 130)},
				{Role: desktop.RoleButton, Name: "送信", Disabled: true, Bounds: image.Rect(190, 180, 280, 208)},
			}
		}},
		{"the session needs a screenshot", func(_ *testRun, s *scriptedSession) { s.elementTurns = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run, session := elementTurnRun(t, done("Looked"))
			tc.setup(run, session)
			require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "金額を確かめる"}]}`, nil).err)
			require.Len(t, session.observations, 1)
			assert.NotEmpty(t, session.observations[0].Screen.Image.Data)
		})
	}
}

// A turn the model was sent no screenshot for does not aim at a position on
// the screen, which may show anything by now: nothing reaches the desktop,
// and the model is shown the screen next.
func TestATurnWithoutAScreenshotDoesNotAimAtPositions(t *testing.T) {
	t.Parallel()

	run, session := elementTurnRun(t, actions(clickAt(235, 194)), done("Gave up"))
	require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "保存する"}]}`, nil).err)
	assert.Empty(t, run.backend.inputs())
	require.Len(t, session.observations, 2)
	assert.Equal(t, blindPositionError, session.observations[1].Results[0].Error)
	assert.NotEmpty(t, session.observations[1].Screen.Image.Data)
}

// An element takes only the kinds of action it is offered with: one of
// another kind aimed at it fails before anything reaches the desktop, and
// the model is told what the element takes. A disabled control is listed
// without an id, so no action can name it.
func TestAnElementTakesOnlyWhatItIsOfferedWith(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		action computeruse.Action
		want   string
	}{
		{"typing into a button", computeruse.Action{CallID: "t", Kind: computeruse.KindType, ElementID: "e2", Text: "1200"},
			`element e2 is button "保存", which takes click, not type`},
		{"choosing in a field", computeruse.Action{CallID: "c", Kind: computeruse.KindChoose, ElementID: "e1", Text: "現金"},
			`element e1 is text_field "金額", which takes click and type, not choose`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run, session := elementTurnRun(t, actions(tc.action), done("Gave up"))
			run.elements.elements[2].Name = "金額"
			run.elements.elements = append(run.elements.elements, desktop.Element{Role: desktop.RoleButton, Name: "送信", ID: "submitButton", Disabled: true,
				Bounds: image.Rect(300, 180, 380, 208), Path: []desktop.PathStep{{Role: desktop.RoleButton, Name: "送信", Index: 1}}})
			require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "金額を入れる"}]}`, nil).err)
			assert.Empty(t, run.backend.inputs())
			require.Len(t, session.observations, 2)
			assert.Contains(t, session.observations[0].Elements, computeruse.Element{Role: desktop.RoleButton, Name: "送信", Disabled: true})
			assert.Equal(t, tc.want, session.observations[1].Results[0].Error)
		})
	}
}
