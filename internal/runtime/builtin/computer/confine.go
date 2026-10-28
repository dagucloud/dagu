// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/desktop"
)

// A step works in the applications it launches, the one the run's step
// before it worked in, and those an exact check or a replay finds in front.
// Once it works in one, it never operates the windows another application
// had on the screen when the step began, which are the person's: a click or
// a key that would reach one is refused, and the step brings its own
// application back when one of them comes forward with nobody at the
// desktop. A window that opens while the step runs, such as a save panel or
// a page a link opens, is the step's to work in.

// noteStartingWindows remembers the windows on the screen as the step
// begins.
func (r *run) noteStartingWindows() {
	r.present = map[uint64]bool{}
	for _, w := range r.driver.Windows() {
		if w.Known() {
			r.present[w.Handle] = true
		}
	}
}

// trustWorkApp makes app one the step works in.
func (r *run) trustWorkApp(app string) {
	if app != "" && !r.worksIn(app) {
		r.workApps = append(r.workApps, app)
	}
}

func (r *run) worksIn(app string) bool {
	return slices.ContainsFunc(r.workApps, func(a string) bool { return strings.EqualFold(a, app) })
}

// foreign reports a window of an application the step does not work in,
// which was on the screen when the step began. Until the step works in an
// application, no window is, nor is one whose application the host does not
// name.
func (r *run) foreign(w desktop.WindowID) bool {
	return len(r.workApps) > 0 && w.Known() && w.App != "" && r.present[w.Handle] && !r.worksIn(w.App)
}

// offLimits reports a window the step never operates: one of the
// application that runs it, or one of another application that was open
// before the step.
func (r *run) offLimits(w desktop.WindowID) bool {
	return r.driver.Owned(w) || r.foreign(w)
}

// offLimitsMessage says why the step will not operate a window, naming the
// application and never the window's title, which may show the person's
// data.
func (r *run) offLimitsMessage(w desktop.WindowID) string {
	if r.driver.Owned(w) {
		return r.ownedMessage(w)
	}
	return fmt.Sprintf("that is a window of %s, which was open before this step and is not an app it works in; work in %s", w.App, strings.Join(r.workApps, ", "))
}

// homeApp is the application to bring back to the front: the one the step
// last worked in, when it is one the step works in, or else the one it began
// to work in last.
func (r *run) homeApp() string {
	if r.worksIn(r.workApp) {
		return r.workApp
	}
	if len(r.workApps) == 0 {
		return ""
	}
	return r.workApps[len(r.workApps)-1]
}
