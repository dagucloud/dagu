// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"image"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	// expenseApp is the expense window the fake elements show, and the
	// person's terminal is another app open beside it.
	expenseApp = desktop.WindowID{Handle: 2, Title: "経費精算 - 請求書 1042", App: "expense", Bounds: image.Rect(0, 0, 300, 200)}
	terminal   = desktop.WindowID{Handle: 9, Title: "zsh", App: "Terminal", Bounds: image.Rect(300, 0, 400, 200)}
)

// confinedRun is a run whose screen holds the expense window and, right of
// it, the person's terminal, both open before the step; a click at x 300 or
// more lands on under, the terminal unless a test says otherwise.
func confinedRun(t *testing.T, under desktop.WindowID) *testRun {
	t.Helper()
	run := newTestRun(t)
	run.elements = newFakeElements()
	run.backend.windows = []desktop.WindowID{expenseApp, terminal}
	run.backend.windowAt = func(x, _ int) desktop.WindowID {
		if x >= 300 {
			return under
		}
		return expenseApp
	}
	return run
}

// Once the step works in an app, a click on another app's window that was
// open before the step is refused, and the model is told which app to work
// in; the window's title, which may show the person's data, stays out.
func TestAStepNeverOperatesAnotherAppOpenBeforeIt(t *testing.T) {
	t.Parallel()

	run := confinedRun(t, terminal)
	session := &scriptedSession{turns: []*computeruse.Turn{actions(clickAt(350, 50)), done("Stopped")}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"idle": "0", "maximize": false, "do": [{"expect": {"text": "保存しました"}}, {"act": "金額を確かめる"}]}`, nil)
	require.NoError(t, execution.err)
	assert.Empty(t, run.backend.inputs(), "nothing reached the terminal")
	require.Len(t, session.observations, 2)
	refusal := session.observations[1].Results[0].Error
	assert.Contains(t, refusal, "that is a window of Terminal, which was open before this step and is not an app it works in; work in expense")
	assert.NotContains(t, refusal, "zsh")
}

// A window that opens while the step runs, such as a save panel, is the
// step's to work in, whichever app shows it.
func TestAStepWorksInAWindowThatOpensWhileItRuns(t *testing.T) {
	t.Parallel()

	panel := desktop.WindowID{Handle: 20, Title: "保存", App: "OpenAndSavePanelService", Bounds: image.Rect(300, 0, 400, 200)}
	run := confinedRun(t, panel)
	session := &scriptedSession{turns: []*computeruse.Turn{actions(clickAt(350, 50)), done("Saved")}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"idle": "0", "maximize": false, "do": [{"expect": {"text": "保存しました"}}, {"act": "保存先を選ぶ"}]}`, nil)
	require.NoError(t, execution.err)
	assert.NotEmpty(t, run.backend.inputs(), "the click reached the panel")
}

// Until the step works in an app, as when it neither launched one nor saw
// one in front, it keeps no app out.
func TestAStepThatWorksInNoAppYetKeepsNoneOut(t *testing.T) {
	t.Parallel()

	run := confinedRun(t, terminal)
	session := &scriptedSession{turns: []*computeruse.Turn{actions(clickAt(350, 50)), done("Clicked")}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"idle": "0", "maximize": false, "do": [{"act": "金額を確かめる"}]}`, nil)
	require.NoError(t, execution.err)
	assert.NotEmpty(t, run.backend.inputs())
}

// When another app the person had open comes forward with nobody at the
// desktop, as an app that raises itself does, the step brings its own app
// back before the model looks, and offers none of the other app's elements.
func TestAStepBringsItsAppBackWhenAnotherComesForward(t *testing.T) {
	t.Parallel()

	run := confinedRun(t, terminal)
	run.backend.focusOn(terminal)
	session := &scriptedSession{turns: []*computeruse.Turn{done("Checked")}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"idle": "0", "maximize": false, "do": [{"expect": {"text": "保存しました"}}, {"act": "金額を確かめる"}]}`, nil)
	require.NoError(t, execution.err)
	require.NotEmpty(t, run.backend.raised)
	assert.Equal(t, expenseApp.Handle, run.backend.raised[0].Handle, "the expense window came back")
}
