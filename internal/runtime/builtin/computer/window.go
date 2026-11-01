// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"fmt"
	"image"
	"os"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
)

// protectedAppsEnv lists applications, by executable name and separated by
// commas, whose windows a step never operates or maximizes, such as the one
// a person starts the automation from.
const protectedAppsEnv = "DAGU_DESKTOP_PROTECTED_APPS"

// windowSizeTolerance is how many pixels a window's size may differ from the
// recorded one and still be the window a turn was recorded on.
const windowSizeTolerance = 2

// A view still shows the same picture when no more than one in stillShare
// of its pixels changed, or stillPixels in a small view: enough for a
// blinking caret or a clock, too few for a list that scrolled.
const (
	stillShare  = 10000
	stillPixels = 256
)

// protectedTitlesEnv lists window titles, separated by commas and matched
// as a window title is anywhere else, whose windows a step never operates or
// maximizes, such as the automation's own pages in a browser.
const protectedTitlesEnv = "DAGU_DESKTOP_PROTECTED_TITLES"

// protectedApps reads the applications a step never operates.
func protectedApps() []string {
	return strings.Split(os.Getenv(protectedAppsEnv), ",")
}

// protectedTitles reads the window titles a step never operates.
func protectedTitles() []string {
	return strings.Split(os.Getenv(protectedTitlesEnv), ",")
}

// recordedWindow is the application window a pixel turn was recorded on.
// The turn fingerprints only this window, and its actions are relative to
// the window's top left, so a replay finds the window wherever it is now and
// whatever surrounds it. A replay knows the window by its application and
// size; the title only prefers one of several such windows.
type recordedWindow struct {
	App    string `json:"app"`
	Title  string `json:"title,omitempty"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// view is the part of the display a pixel turn is compared on: the window it
// was recorded on, or the whole display where the system names no window.
type view struct {
	rect   image.Rectangle
	window *recordedWindow
}

// displayView compares the whole display, with actions in display pixels.
func displayView(full *image.RGBA) view {
	return view{rect: full.Bounds()}
}

// region is the part of an image the view compares. It shares the image's
// pixels and coordinates; the sub-image of an RGBA is always an RGBA.
func (v view) region(img *image.RGBA) *image.RGBA {
	sub, _ := img.SubImage(v.rect.Intersect(img.Bounds())).(*image.RGBA)
	return sub
}

// fingerprint hashes what the view shows of an image.
func (v view) fingerprint(img *image.RGBA) desktop.Fingerprint {
	return desktop.FingerprintOf(v.region(img))
}

// still reports whether the view shows the same picture in two captures of
// the display, but for a few pixels.
func (v view) still(before, after *image.RGBA) bool {
	if before.Bounds() != after.Bounds() {
		return false
	}
	region := v.region(before)
	size := region.Bounds().Size()
	return desktop.ChangedPixels(region, v.region(after)) <= max(stillPixels, size.X*size.Y/stillShare)
}

// targetPrint hashes the area around a display position, inside the view.
func (v view) targetPrint(img *image.RGBA, at image.Point) desktop.Fingerprint {
	return desktop.FingerprintAround(v.region(img), at, targetRadius(v.rect))
}

// relative moves an action from display pixels to the view's own.
func (v view) relative(action computeruse.Action) computeruse.Action {
	if v.window == nil {
		return action
	}
	return shifted(action, image.Point{}.Sub(v.rect.Min))
}

// absolute moves an action from the view's pixels to the display's.
func (v view) absolute(action computeruse.Action) computeruse.Action {
	if v.window == nil {
		return action
	}
	return shifted(action, v.rect.Min)
}

// place records an action the model ran in display pixels within the view:
// relative to the view, with the area it lands on fingerprinted on the
// screen the model chose it on.
func (v view) place(rec recordedAction, seen *image.RGBA) recordedAction {
	if at, ok := target(rec.Action); ok {
		fingerprint := v.targetPrint(seen, at)
		rec.Target = &fingerprint
	}
	rec.Action = v.relative(rec.Action)
	return rec
}

// shifted moves an action's positions by offset.
func shifted(action computeruse.Action, offset image.Point) computeruse.Action {
	if action.Point != nil {
		action.Point = &computeruse.Point{X: action.Point.X + offset.X, Y: action.Point.Y + offset.Y}
	}
	if len(action.Path) > 0 {
		path := make([]computeruse.Point, len(action.Path))
		for i, p := range action.Path {
			path[i] = computeruse.Point{X: p.X + offset.X, Y: p.Y + offset.Y}
		}
		action.Path = path
	}
	return action
}

// windowView is the view of the application window w is part of: w itself
// when it is a top-level window, or the front window of its application
// when w is a menu or another window the system does not list. It is the
// whole display when the system does not name the window.
func (r *run) windowView(w desktop.WindowID, display view) view {
	w, ok := r.appWindow(w)
	if !ok || w.Bounds.Intersect(display.rect).Empty() {
		return display
	}
	return view{rect: w.Bounds, window: &recordedWindow{App: w.App, Title: w.Title, Width: w.Bounds.Dx(), Height: w.Bounds.Dy()}}
}

// appWindow returns the listed top-level window w belongs to.
func (r *run) appWindow(w desktop.WindowID) (desktop.WindowID, bool) {
	if !w.Known() || w.App == "" || w.Bounds.Empty() {
		return desktop.WindowID{}, false
	}
	windows := r.driver.Windows()
	for _, c := range windows {
		if c.Handle == w.Handle {
			return c, !c.Bounds.Empty()
		}
	}
	for _, c := range windows {
		if c.PID == w.PID && c.App != "" && !c.Bounds.Empty() {
			return c, true
		}
	}
	return desktop.WindowID{}, false
}

// findWindow finds the window a turn was recorded on: the front window of
// its application with its size, preferring one with its title.
func (r *run) findWindow(rec recordedWindow) (image.Rectangle, bool) {
	var bySize image.Rectangle
	for _, w := range r.driver.Windows() {
		if !strings.EqualFold(w.App, rec.App) || r.driver.Owned(w) ||
			abs(w.Bounds.Dx()-rec.Width) > windowSizeTolerance || abs(w.Bounds.Dy()-rec.Height) > windowSizeTolerance {
			continue
		}
		if w.Title == rec.Title {
			return w.Bounds, true
		}
		if bySize.Empty() {
			bySize = w.Bounds
		}
	}
	return bySize, !bySize.Empty()
}

// launchFront bounds how long a launch waits for what it opened to come to
// the front, unless its timeout is shorter, looked at every launchFrontPoll.
const (
	launchFront     = 5 * time.Second
	launchFrontPoll = 100 * time.Millisecond
)

// awaitLaunched waits, up to wait, until what a launch opened is in front:
// a window of the application it names, or any window other than the one
// in front before it. A launch returns before the application comes to the
// front, and the next operation fits and reads the window in front, which
// until then is the one a person or another application had there. The
// application the launch names, or the one a launch naming none brought
// forward, is then the one the step works in; another application that
// took the front first is not. A host that did not say which window was in
// front before, or a launch whose window never comes to the front, goes on
// as it is. A launch naming none that brings forward a window the step
// never operates, such as another application's a person had open, does
// not make that application one the step works in.
func (r *run) awaitLaunched(ctx context.Context, index int, spec launchSpec, before desktop.WindowID, wait time.Duration) error {
	if !before.Known() {
		return nil
	}
	app := spec.app()
	deadline := time.Now().Add(wait)
	for {
		w := r.driver.FocusedWindow()
		named := app != "" && strings.EqualFold(w.App, app)
		if w.Known() && (named || w.Handle != before.Handle) {
			if (named && !r.driver.Owned(w)) || (app == "" && !r.offLimits(w)) {
				r.trustWorkApp(w.App)
			}
			if named || app == "" {
				r.noteWorkApp(w)
			}
			return nil
		}
		if !time.Now().Before(deadline) {
			logAction(r.timeline, index, fmt.Sprintf("nothing it opened came to the front within %s", wait))
			return nil
		}
		if err := sleep(ctx, launchFrontPoll); err != nil {
			return err
		}
	}
}

// fit maximizes the front window when the operation running asks for it.
// Each window is maximized at most once in a step attempt, so one a person
// or its application resizes afterwards is left as it is. A window the step
// never operates, one that cannot be maximized, and one already maximized
// are left alone. Resizing a window moves what a person may be working on,
// so it waits for an idle desktop as input does.
func (r *run) fit(ctx context.Context, index int) error {
	if !r.maximize || r.driver == nil {
		return nil
	}
	if w := r.driver.FocusedWindow(); !r.fits(w) {
		return nil
	}
	if err := r.awaitPerson(ctx); err != nil {
		return err
	}
	// The window in front may have changed while the step waited.
	w := r.driver.FocusedWindow()
	if !r.fits(w) {
		return nil
	}
	r.fitted[w.Handle] = true
	done, err := r.driver.Maximize(w)
	switch {
	case err != nil:
		logAction(r.timeline, index, fmt.Sprintf("could not maximize %s: %s", describeWindow(w), r.masker.MaskString(err.Error())))
	case done:
		logAction(r.timeline, index, "maximized "+describeWindow(w))
	}
	return nil
}

// fits reports a window fit has yet to try: one the system names, that the
// step may operate, and that this attempt has not tried.
func (r *run) fits(w desktop.WindowID) bool {
	return w.Known() && !r.offLimits(w) && !r.fitted[w.Handle]
}

// elementOffLimits reports a window the step never operates that an action
// on an element would reach: the window under the element's centre, where
// the elements reader finds the element first, or the window in front,
// where it looks for the element next.
func (r *run) elementOffLimits(e desktop.Element) (desktop.WindowID, bool) {
	if !e.Bounds.Empty() {
		if w := r.driver.WindowAt(elementCentre(e)); r.offLimits(w) {
			return w, true
		}
	}
	if w := r.driver.FocusedWindow(); r.offLimits(w) {
		return w, true
	}
	return desktop.WindowID{}, false
}

// ownedMessage says why the step will not operate a window: it belongs to
// the application that runs the step, or shows one of its pages.
func (r *run) ownedMessage(w desktop.WindowID) string {
	if r.driver.OwnedPage(w) {
		return fmt.Sprintf("%s is a page of the application that runs this step, which it never operates", describeWindow(w))
	}
	return fmt.Sprintf("%q belongs to the application that runs this step, which it never operates", w.Title)
}

// describeWindow names a window for the step log.
func describeWindow(w desktop.WindowID) string {
	if w.Title == "" {
		return w.App
	}
	return fmt.Sprintf("%s %q", w.App, w.Title)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
