// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reportWindow is the application window the window tests operate.
var reportWindow = desktop.WindowID{Handle: 7, Title: "Report", PID: 42, App: "report", Bounds: image.Rect(100, 100, 500, 400)}

// desk draws a desktop of background stripes with the report window's
// content at bounds; a different background changes everything but the
// window.
func desk(background int, bounds image.Rectangle) *image.RGBA {
	img := stripes(800, 600, background)
	draw.Draw(img, bounds, pattern(bounds.Dx(), bounds.Dy(), 0), image.Point{}, draw.Src)
	return img
}

// windowRun is a desktop that shows the report window at bounds and names
// it as the window in front and under any position inside it.
func windowRun(t *testing.T, bounds image.Rectangle) *testRun {
	t.Helper()
	run := newTestRun(t)
	run.backend = newFakeBackend(800, 600)
	run.placeWindow(bounds)
	return run
}

// placeWindow moves the report window to bounds, on a background of three
// stripes.
func (r *testRun) placeWindow(bounds image.Rectangle) {
	window := reportWindow
	window.Bounds = bounds
	r.backend.mu.Lock()
	defer r.backend.mu.Unlock()
	r.backend.screen = desk(3, bounds)
	r.backend.windows = []desktop.WindowID{window}
	r.backend.focus = window
	r.backend.windowAt = func(x, y int) desktop.WindowID {
		if image.Pt(x, y).In(window.Bounds) {
			return window
		}
		return desktop.WindowID{}
	}
}

// A turn is recorded on the window its click lands on, relative to the
// window's corner, so a replay ignores everything around the window.
func TestReplayComparesOnlyTheWindow(t *testing.T) {
	t.Parallel()

	const steps = `{"maximize": false, "do": [{"act": "Open the report"}]}`
	run := windowRun(t, reportWindow.Bounds)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(150, 160)), done("Opened")}}}
	require.NoError(t, run.execute(steps, nil).err)

	entry := lookupRecording(t, run, "Open the report")
	require.Len(t, entry.Turns, 1)
	assert.Equal(t, &recordedWindow{App: "report", Title: "Report", Width: 400, Height: 300}, entry.Turns[0].Window)
	assert.Equal(t, &computeruse.Point{X: 50, Y: 60}, entry.Turns[0].Actions[0].Action.Point, "relative to the window")
	assert.Equal(t, entry.Turns[0].Window, entry.FinalWindow)

	run.backend.script(desk(9, reportWindow.Bounds))
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err, "no session is left, so a model call would fail")
	assert.Equal(t, []string{"act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()))
	assert.Contains(t, run.backend.inputs(), "move 150,160")
}

// A replay finds a window that moved and clicks where its turn landed on
// the window, not on the display.
func TestReplayFollowsAMovedWindow(t *testing.T) {
	t.Parallel()

	const steps = `{"maximize": false, "do": [{"act": "Open the report"}]}`
	run := windowRun(t, reportWindow.Bounds)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(150, 160)), done("Opened")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.placeWindow(reportWindow.Bounds.Add(image.Pt(120, 80)))
	run.backend.events = nil
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()))
	assert.Contains(t, run.backend.inputs(), "move 270,240")
}

// A window of another size shows a different layout, so the turn misses and
// the model repairs the recording.
func TestReplayMissesAResizedWindow(t *testing.T) {
	t.Parallel()

	const steps = `{"maximize": false, "do": [{"act": "Open the report"}]}`
	run := windowRun(t, reportWindow.Bounds)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(150, 160)), done("Opened")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.placeWindow(image.Rect(100, 100, 500, 350))
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(150, 160)), done("Opened")}}}
	healed := run.execute(steps, nil)
	require.NoError(t, healed.err)
	assert.Equal(t, []string{"act:healed"}, eventNames(healed.exec.GetAgentSession()))
	assert.Contains(t, healed.stderr.String(), "the window of report differs from the recording")
}

// A recording made before windows were recorded keeps replaying on the
// whole display, at display positions.
func TestReplaysARecordingWithoutWindows(t *testing.T) {
	t.Parallel()

	run := windowRun(t, reportWindow.Bounds)
	screen := run.backend.screen
	at := image.Pt(150, 160)
	target := desktop.FingerprintAround(screen, at, targetRadius(screen.Bounds()))
	cache := openReplayCache(run.computerDir(), "invoices", "post")
	cache.Stage(replayKey(0, "Open the report"), recording{
		Version: oldestRecordingVersion, Width: 800, Height: 600,
		Turns: []recordedTurn{{
			Screen:  desktop.FingerprintOf(screen),
			Actions: []recordedAction{{Action: clickAt(at.X, at.Y), Target: &target}},
		}},
		Final: desktop.FingerprintOf(screen),
	})
	require.NoError(t, cache.Commit(t.Context()))

	replayed := run.execute(`{"maximize": false, "do": [{"act": "Open the report"}]}`, nil)
	require.NoError(t, replayed.err, "no session is left, so a model call would fail")
	assert.Equal(t, []string{"act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()))
	assert.Contains(t, run.backend.inputs(), "move 150,160")
}

// A change outside the window between the screenshot and the actions does
// not send the model's actions back to it.
func TestActionsRunDespiteChangesOutsideTheWindow(t *testing.T) {
	t.Parallel()

	run := windowRun(t, reportWindow.Bounds)
	session := &scriptedSession{turns: []*computeruse.Turn{actions(clickAt(150, 160)), done("Opened")}}
	session.onNext = func() { run.backend.show(desk(11, reportWindow.Bounds)) }
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(`{"maximize": false, "do": [{"act": "Open the report"}]}`, nil).err)

	assert.Contains(t, run.backend.inputs(), "move 150,160")
	for _, obs := range session.observations {
		assert.NotEqual(t, divergedNote, obs.Note)
	}
}

// The front window is maximized before the step looks at it, once, and
// only when the step allows it and the window is one the step operates.
// A when on the step's data is decided before the front window is fitted:
// an act it skips leaves the window as it is, and one it runs is fitted as
// any other.
func TestWhenOnDataDecidesBeforeTheWindowIsFitted(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		open      string
		maximized int
	}{
		{"skipped", "no", 0},
		{"run", "yes", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := windowRun(t, reportWindow.Bounds)
			run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(150, 160)), done("Opened")}}}
			steps := `{"variables": {"open": "` + tc.open + `"}, "do": [{"act": "Open the report", "when": {"value": "%open%", "equals": "yes"}}]}`
			require.NoError(t, run.execute(steps, nil).err)
			assert.Len(t, run.backend.maximizedWindows(), tc.maximized)
		})
	}
}

// A launch returns before the application it opens comes to the front, so
// the operation after it waits for that application rather than fit the
// window a person had in front.
func TestFitsTheLaunchedApplication(t *testing.T) {
	t.Parallel()

	terminal := desktop.WindowID{Handle: 3, Title: "zsh", App: "Terminal", Bounds: image.Rect(0, 0, 400, 300)}
	for _, tc := range []struct {
		name, launch string
		// front is the window in front as the launch begins.
		front desktop.WindowID
	}{
		{"an app by name", `{"command": "open", "args": ["-a", "Report"]}`, terminal},
		{"a program", `"C:\\Apps\\report.exe"`, terminal},
		{"an app already in front", `{"command": "open", "args": ["-a", "Report"]}`, reportWindow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := windowRun(t, reportWindow.Bounds)
			run.backend.focusOn(tc.front)
			run.onLaunch = func() {
				go func() {
					time.Sleep(200 * time.Millisecond)
					run.backend.focusOn(reportWindow)
				}()
			}
			execution := run.execute(`{"ai": "never", "do": [{"launch": `+tc.launch+`}, {"screenshot": "opened"}]}`, nil)
			require.NoError(t, execution.err)
			require.Len(t, run.backend.maximizedWindows(), 1)
			assert.Equal(t, reportWindow.Handle, run.backend.maximizedWindows()[0].Handle, "the launched application's window, not the terminal")
			assert.NotContains(t, execution.stderr.String(), "came to the front", "no wait runs out")
		})
	}
}

// The app a launch opened is the one the step works in: when a person
// brings another app forward before the next check, the launched app comes
// back once they stop.
func TestALaunchedAppComesBackAfterAPerson(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	form := expenseWindow()
	expense := desktop.WindowID{Handle: 2, Title: form.Name, App: "expense"}
	notes := desktop.WindowID{Handle: 1, Title: "メモ", App: "notes"}
	run.backend.windows = []desktop.WindowID{notes, expense}
	run.backend.focusOn(notes)
	showNotes := func() {
		run.elements.asApp(notes.App)
		run.elements.retitle(notes.Title)
		run.elements.arriveAfter(0, []desktop.Element{})
	}
	showNotes()
	run.onLaunch = func() {
		// The app comes forward for a moment; then a person brings their
		// notes back in front.
		run.backend.mu.Lock()
		run.backend.focusNext = []desktop.WindowID{expense}
		run.backend.mu.Unlock()
		run.backend.personUses()
	}
	run.backend.onRaise = func(w desktop.WindowID) {
		if w.Handle == expense.Handle {
			run.elements.asApp(expense.App)
			run.elements.retitle(expense.Title)
			run.elements.arriveAfter(0, expenseElements())
		}
	}
	execution := run.execute(`{"ai": "never", "idle": "100ms", "maximize": false, "do": [{"launch": "C:\\Apps\\expense.exe"}, {"expect": {"text": "保存しました"}, "timeout": "2s"}]}`, nil)
	require.NoError(t, execution.err)
	require.Len(t, run.backend.raised, 1)
	assert.Equal(t, expense.Handle, run.backend.raised[0].Handle, "the launched app came back")
}

func TestMaximizesTheFrontWindow(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Open the report"}, {"screenshot": "after"}]}`
	turns := func() []*scriptedSession {
		return []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(150, 160)), done("Opened")}}}
	}

	run := windowRun(t, reportWindow.Bounds)
	run.sessions = turns()
	maximized := run.execute(steps, nil)
	require.NoError(t, maximized.err)
	require.Len(t, run.backend.maximizedWindows(), 1, "each window is maximized once")
	assert.Equal(t, reportWindow.Handle, run.backend.maximizedWindows()[0].Handle)
	assert.Contains(t, maximized.stderr.String(), `maximized report "Report"`)

	for name, configure := range map[string]func(*testRun) string{
		"turned off": func(*testRun) string { return `{"maximize": false, "do": [{"act": "Open the report"}]}` },
		"turned off act": func(*testRun) string {
			return `{"do": [{"act": {"instruction": "Open the report", "maximize": false}}]}`
		},
		"protected": func(r *testRun) string {
			r.protectApps = []string{"Report"}
			return steps
		},
		"fixed size": func(r *testRun) string {
			r.backend.fixedSize = map[uint64]bool{reportWindow.Handle: true}
			return steps
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			run := windowRun(t, reportWindow.Bounds)
			run.sessions = turns()
			execution := run.execute(configure(run), nil)
			require.NoError(t, execution.err)
			assert.Empty(t, run.backend.maximizedWindows())
			assert.NotContains(t, execution.stderr.String(), "maximized")
			if name == "protected" {
				assert.NotContains(t, run.backend.inputs(), "move 150,160", "a protected window is never operated")
			}
		})
	}
}

// computerDir is where the run's steps keep their recordings.
func (r *testRun) computerDir() string {
	return filepath.Join(r.dataDir, computerhost.DataDirName)
}

// A window the replay finds off the display shows nothing to compare, so
// the turn misses rather than matching an empty picture, which hashes like a
// window of one flat colour.
func TestReplayMissesAWindowOffTheDisplay(t *testing.T) {
	t.Parallel()

	const steps = `{"maximize": false, "do": [{"act": "Open the report"}]}`
	run := windowRun(t, reportWindow.Bounds)
	flat := stripes(800, 600, 3)
	draw.Draw(flat, reportWindow.Bounds, image.NewUniform(color.Gray{Y: 128}), image.Point{}, draw.Src)
	run.backend.script(flat)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(150, 160)), done("Opened")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.placeWindow(image.Rect(900, 100, 1300, 400))
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{done("Nothing to do")}}}
	healed := run.execute(steps, nil)
	require.NoError(t, healed.err)
	assert.Equal(t, []string{"act:healed"}, eventNames(healed.exec.GetAgentSession()))
	assert.NotContains(t, run.backend.inputs(), "move 1050,460", "nothing is clicked off the display")
}

// Maximizing a window waits, as input does, until nobody is using the
// desktop.
func TestMaximizeWaitsForAnIdleDesktop(t *testing.T) {
	t.Parallel()

	run := windowRun(t, reportWindow.Bounds)
	run.backend.personKeepsUsing(2)
	execution := run.execute(`{"idle": "20ms", "do": [{"screenshot": "report"}]}`, nil)
	require.NoError(t, execution.err)
	session := execution.exec.GetAgentSession()
	assert.Equal(t, []string{waitReasonPerson}, waitReasons(session))
	require.Len(t, run.backend.maximizedWindows(), 1)
}

// An element the model names is acted on wherever it is now, so an element
// in a window the step never operates is refused as a pointer action there
// is, and the model is told why.
func TestModelElementInAProtectedWindowIsRefused(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.elements = newFakeElements()
	run.backend.app = "Runbook"
	run.protectApps = []string{"runbook"}
	session := &scriptedSession{turns: []*computeruse.Turn{
		actions(computeruse.Action{CallID: "x", Kind: computeruse.KindClick, ElementID: "e1"}),
		done("Could not save"),
	}}
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "Save the form"}]}`, nil).err)

	assert.Empty(t, run.backend.inputs(), "the button is not invoked")
	require.Len(t, session.observations, 2)
	require.Len(t, session.observations[1].Results, 1)
	assert.Contains(t, session.observations[1].Results[0].Error, "belongs to the application that runs this step")
}

// A window whose title is protected, such as the automation's own page in a
// browser a person also uses, is never operated: a click the model aims
// there is refused, and the model is told why.
func TestModelClickOnAProtectedTitleIsRefused(t *testing.T) {
	t.Parallel()

	page := desktop.WindowID{Handle: 4, Title: "Batches · Runbook", PID: 31, App: "Google Chrome", Bounds: image.Rect(0, 0, 400, 200)}
	run := newTestRun(t)
	run.backend.windows = []desktop.WindowID{page}
	run.backend.under = page
	run.protectTitles = []string{"· Runbook"}
	session := &scriptedSession{turns: []*computeruse.Turn{actions(clickAt(10, 10)), done("Could not run")}}
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "Run the sheet"}]}`, nil).err)

	assert.Empty(t, run.backend.inputs(), "the click does not reach the page")
	require.Len(t, session.observations, 2)
	require.Len(t, session.observations[1].Results, 1)
	assert.Contains(t, session.observations[1].Results[0].Error, `Google Chrome "Batches · Runbook" is a page of the application that runs this step`)
}

// A replay never has an element in a window the step never operates act by
// itself, as input: direct would; it misses, naming why.
func TestReplayRefusesAnElementInAProtectedWindow(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.protectApps = []string{"expense"}
	run.backend.events = nil
	replayed := run.execute(`{"ai": "never", "input": "direct", "do": [{"act": "Save the invoice"}]}`, nil)
	require.ErrorContains(t, replayed.err, "which it never operates")
	assert.Empty(t, run.backend.inputs(), "the button is not invoked")
}

// A direct replay that waits for a disabled element checks where the
// element is before each try, so a protected window that came to the front
// meanwhile is never acted on.
func TestDirectReplayRechecksTheWindowBeforeEachTry(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := elementRun(t)
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194)), done("Saved")}}}
	require.NoError(t, run.execute(steps, nil).err)

	run.protectApps = []string{"Runbook"}
	run.elements.failPattern("saveButton", desktop.ErrDisabled)
	// The first try finds the button disabled; by the next, the protected
	// window is in front and the button is enabled.
	run.elements.onRefuse = func(desktop.Element, desktop.Pattern, error) {
		run.backend.focusOn(desktop.WindowID{Handle: 9, Title: "Runbook", PID: 77, App: "Runbook"})
		run.elements.mu.Lock()
		delete(run.elements.applyErr, "saveButton")
		run.elements.mu.Unlock()
	}
	run.backend.events = nil
	failed := run.execute(`{"ai": "never", "input": "direct", "idle": "0", "find_within": "3s", "do": [{"act": "Save the invoice"}]}`, nil)
	require.ErrorContains(t, failed.err, "which it never operates")
	assert.Empty(t, run.backend.inputs(), "the button is not invoked once the protected window is in front")
}
