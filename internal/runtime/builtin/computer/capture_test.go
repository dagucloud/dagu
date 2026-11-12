// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// launchAndAct launches the report app, which makes it the app the step
// works in, then acts in it.
const launchAndAct = `{"maximize": false, "do": [{"launch": "report.exe"}, {"act": "Open the report"}]}`

// appRun is a desktop that shows the report window in front at its usual
// place, with a terminal the person had open right of it.
func appRun(t *testing.T) *testRun {
	t.Helper()
	run := windowRun(t, reportWindow.Bounds)
	run.backend.windows = append(run.backend.windows, terminalBeside)
	return run
}

// terminalBeside is a window of another app, open before the step, right of
// the report window.
var terminalBeside = desktop.WindowID{Handle: 9, Title: "zsh", App: "Terminal", Bounds: image.Rect(520, 100, 780, 400)}

// decoded is the picture a model was sent.
func decoded(t *testing.T, obs computeruse.Observation) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(obs.Screen.Image.Data))
	require.NoError(t, err)
	return img
}

// Once the step works in an app, the model sees that app's windows alone,
// and a position it gives on them maps back to the display.
func TestALookShowsOnlyTheWindowsOfTheApp(t *testing.T) {
	t.Parallel()

	run := appRun(t)
	session := &scriptedSession{turns: []*computeruse.Turn{actions(clickAt(50, 60)), done("Opened")}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(launchAndAct, nil)
	require.NoError(t, execution.err)

	first := session.observations[0]
	assert.Equal(t, [2]int{400, 300}, [2]int{first.Screen.Width, first.Screen.Height}, "the report window's size")
	want := desktop.Crop(run.backend.screen, reportWindow.Bounds)
	got := image.NewRGBA(want.Bounds())
	draw.Draw(got, got.Bounds(), decoded(t, first), image.Point{}, draw.Src)
	assert.Equal(t, want.Pix, got.Pix, "the window as the screen shows it, nothing around it")
	assert.Contains(t, run.backend.inputs(), "move 150,160", "the click lands relative to the window's corner")
	assert.Contains(t, lifecycleMessages(execution.exec.GetAgentSession()), "Capturing only the windows of report")
}

// A look captures the whole screen until the step works in an app, and
// when the app's windows cannot be captured; the timeline says which and
// why.
func TestALookFallsBackToTheWholeScreen(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		steps   string
		failing error
		note    string
	}{
		{
			name:  "no app yet",
			steps: `{"maximize": false, "do": [{"act": "Open the report"}]}`,
			note:  "Capturing the whole screen until the step works in an app",
		},
		{
			name:    "windows cannot be captured",
			steps:   launchAndAct,
			failing: errors.New("the window server refused"),
			note:    "Capturing the whole screen, since the windows of report cannot be captured: the window server refused",
		},
		{
			name:    "no window on the display",
			steps:   launchAndAct,
			failing: desktop.ErrNoWindows,
			note:    "Capturing the whole screen, since no window of report shows on the main display",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := appRun(t)
			run.backend.captureErr = tc.failing
			session := &scriptedSession{turns: []*computeruse.Turn{done("Opened")}}
			run.sessions = []*scriptedSession{session}
			execution := run.execute(tc.steps, nil)
			require.NoError(t, execution.err)
			assert.Equal(t, [2]int{800, 600}, [2]int{session.observations[0].Screen.Width, session.observations[0].Screen.Height})
			assert.Contains(t, lifecycleMessages(execution.exec.GetAgentSession()), tc.note)
		})
	}
}

// A click on a part of the app's window that another app's window covers
// on the screen brings the app's window forward first, so the click lands
// where the model saw it rather than on the cover.
func TestAClickBringsACoveredWindowForward(t *testing.T) {
	t.Parallel()

	run := appRun(t)
	cover := image.Rect(300, 100, 520, 400)
	uncovered := false
	run.backend.windowAt = func(x, y int) desktop.WindowID {
		switch at := image.Pt(x, y); {
		case at.In(cover) && !uncovered:
			return terminalBeside
		case at.In(reportWindow.Bounds):
			return reportWindow
		}
		return desktop.WindowID{}
	}
	run.backend.onRaise = func(w desktop.WindowID) { uncovered = w.Handle == reportWindow.Handle }
	session := &scriptedSession{turns: []*computeruse.Turn{actions(clickAt(300, 100)), done("Opened")}}
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(launchAndAct, nil).err)

	require.NotEmpty(t, run.backend.raised)
	assert.Equal(t, reportWindow.Handle, run.backend.raised[0].Handle)
	assert.Equal(t, []string{"move 400,200", "left down #1"}, run.backend.inputs())
	assert.Empty(t, session.observations[1].Results[0].Error)
}

// A covered spot whose window does not come forward is not clicked, and
// the model is told what covers it.
func TestAClickOnAWindowThatStaysCoveredFails(t *testing.T) {
	t.Parallel()

	run := appRun(t)
	run.backend.windowAt = func(int, int) desktop.WindowID { return terminalBeside }
	session := &scriptedSession{turns: []*computeruse.Turn{actions(clickAt(300, 100)), done("Stopped")}}
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(launchAndAct, nil).err)

	assert.Empty(t, run.backend.inputs(), "nothing reached the cover")
	assert.Contains(t, session.observations[1].Results[0].Error, "a window of Terminal lies over that spot")
}

// A window of the app that opened since the capture, such as a menu an
// earlier action of the round opened, takes a click the capture does not
// cover.
func TestAClickReachesAWindowTheAppOpenedSince(t *testing.T) {
	t.Parallel()

	run := appRun(t)
	menu := desktop.WindowID{Handle: 12, App: "report", Bounds: image.Rect(150, 400, 300, 500)}
	run.backend.windowAt = func(x, y int) desktop.WindowID {
		if image.Pt(x, y).In(menu.Bounds) {
			return menu
		}
		return reportWindow
	}
	// 100,350 on the window's picture is 200,450 on the display, below the
	// window, where the menu the first click opened lies.
	session := &scriptedSession{turns: []*computeruse.Turn{actions(clickAt(50, 60), clickAt(100, 350)), done("Opened")}}
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(launchAndAct, nil).err)

	assert.Equal(t, []string{"move 150,160", "left down #1", "move 200,450", "left down #1"}, run.backend.inputs())
}

// A click outside the app's windows aims at nothing the model saw, so it
// is refused and reaches no other window.
func TestAClickOutsideTheCapturedWindowsIsRefused(t *testing.T) {
	t.Parallel()

	run := appRun(t)
	run.backend.windowAt = func(x, y int) desktop.WindowID {
		if image.Pt(x, y).In(terminalBeside.Bounds) {
			return terminalBeside
		}
		return reportWindow
	}
	// 500,60 on the window's picture is 600,160 on the display, over the
	// terminal: the picture is 400 wide, so a model that misreads it may
	// still aim past the window.
	session := &scriptedSession{turns: []*computeruse.Turn{actions(clickAt(500, 60)), done("Stopped")}}
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(launchAndAct, nil).err)

	assert.Empty(t, run.backend.inputs(), "nothing reached the terminal")
	assert.Contains(t, session.observations[1].Results[0].Error, "outside the windows the screenshot shows")
}

// A recording made on the whole screen is recorded again, not compared,
// once the step captures the app's windows; the new recording replays on
// them.
func TestAWholeScreenRecordingIsRecordedAgain(t *testing.T) {
	t.Parallel()

	run := appRun(t)
	screen := run.backend.screen
	cache := openReplayCache(filepath.Join(run.dataDir, computerhost.DataDirName), "invoices", "post")
	key := replayKey(1, "Open the report")
	cache.Stage(key, recording{
		Op: 1, Version: recordingVersion, Width: 800, Height: 600,
		Turns: []recordedTurn{{Screen: desktop.FingerprintOf(screen), Actions: []recordedAction{{Action: clickAt(150, 160)}}}},
		Final: desktop.FingerprintOf(screen),
	})
	require.NoError(t, cache.Commit(t.Context()))

	never := `{"ai": "never", "maximize": false, "do": [{"launch": "report.exe"}, {"act": "Open the report"}]}`
	missed := run.execute(never, nil)
	require.Error(t, missed.err)
	assert.Contains(t, missed.err.Error(), "turn 1 of 1 was recorded on the whole screen, and this run captures the windows of the apps it works in")
	assert.Empty(t, run.backend.inputs())

	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(50, 60)), done("Opened")}}}
	healed := run.execute(launchAndAct, nil)
	require.NoError(t, healed.err)
	assert.Equal(t, []string{"launch:completed", "act:healed"}, eventNames(healed.exec.GetAgentSession()))
	entry, ok := openReplayCache(filepath.Join(run.dataDir, computerhost.DataDirName), "invoices", "post").Lookup(key)
	require.True(t, ok)
	require.Len(t, entry.Turns, 1)
	assert.Equal(t, captureWindows, entry.Turns[0].Capture)
	assert.Equal(t, captureWindows, entry.FinalCapture)

	run.runID = "run-2"
	run.backend.events = nil
	replayed := run.execute(launchAndAct, nil)
	require.NoError(t, replayed.err, "no session is left, so a model call would fail")
	assert.Equal(t, []string{"launch:completed", "act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()))
	assert.Contains(t, run.backend.inputs(), "move 150,160")
}
