// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/desktop"
)

// A step that does not hold the desktop works in the background: it reads
// the elements of its application's windows wherever they are, behind
// another application's or covered by one, has elements perform their
// patterns, and waits, but sends no pointer or key input and moves no
// window or the focus, so it runs beside a person and beside steps working
// in other applications, and waits for no person. Its reads go to its own
// window in place of the window in front: the one reach chose last. An
// operation that needs more takes the desktop; see takeDesktop.

// reach makes a window of app the one the step reads, preferring one
// titled title: without the desktop, the window where it is, once the step
// holds app. A window the step cannot read where it is, one minimized, not
// open, or of an application it does not know, takes the desktop, and the
// read goes to the window in front, which the step may then bring forward.
func (r *run) reach(ctx context.Context, app, title string) error {
	if r.lease != nil {
		return nil
	}
	if app == "" {
		return r.takeDesktop(ctx, app)
	}
	// The window is chosen once the step holds app, which another step may
	// have changed meanwhile.
	if err := r.takeApp(ctx, app); err != nil {
		return err
	}
	target := r.recordedWindow(app, title)
	if target == nil || target.Minimized {
		return r.takeDesktop(ctx, app)
	}
	own := r.dialogOver(*target, app, title)
	r.own = &own
	return nil
}

// dialogOver returns the window of target's application in front of its
// others when it is a dialog that holds target, which bringing target
// forward would leave in front of it, or else target itself.
func (r *run) dialogOver(target desktop.WindowID, app, title string) desktop.WindowID {
	for _, w := range r.driver.Windows() {
		if w.PID != target.PID || w.Minimized || r.driver.Owned(w) {
			continue
		}
		if w.Handle == target.Handle {
			return target
		}
		if front, err := r.elements.InWindow(w).FrontWindow(); err == nil && r.dialogHolds(front, &target, app, title) {
			return w
		}
		return target
	}
	return target
}

// ownRead names the window reads go to: the one in front while the step
// holds the desktop, scoped false, and its own window while it does not.
func (r *run) ownRead() (w desktop.WindowID, scoped bool, err error) {
	switch {
	case r.lease != nil:
		return desktop.WindowID{}, false, nil
	case r.own == nil:
		return desktop.WindowID{}, false, desktop.ErrNoWindow
	}
	return *r.own, true, nil
}

// settledWindows lists the windows whose opening and closing the step lets
// settle before it reads elements: every window while it holds the desktop,
// and only its own application's while it reads its own window, so steps
// working in other applications do not hold its reads back.
func (r *run) settledWindows() []desktop.WindowID {
	windows := r.driver.Windows()
	if r.lease != nil || r.own == nil {
		return windows
	}
	return slices.DeleteFunc(windows, func(w desktop.WindowID) bool { return w.PID != r.own.PID })
}

// routedElements reads the window in front while the step holds the
// desktop, and the step's own window while it does not; see ownRead.
type routedElements struct {
	desktop.Elements
	own func() (desktop.WindowID, bool, error)
}

func (e *routedElements) reader() (desktop.Elements, error) {
	w, scoped, err := e.own()
	if err != nil || !scoped {
		return e.Elements, err
	}
	return e.Elements.InWindow(w), nil
}

func (e *routedElements) FrontWindow() (desktop.Element, error) {
	els, err := e.reader()
	if err != nil {
		return desktop.Element{}, err
	}
	return els.FrontWindow()
}

func (e *routedElements) Find(sel desktop.Selector) ([]desktop.Element, error) {
	els, err := e.reader()
	if err != nil {
		return nil, err
	}
	return els.Find(sel)
}

func (e *routedElements) Outline(window desktop.Element, limit int) ([]desktop.Element, error) {
	els, err := e.reader()
	if err != nil {
		return nil, err
	}
	return els.Outline(window, limit)
}

func (e *routedElements) Rows(ctx context.Context, container desktop.Selector, columns []string, maxRows int) ([][]string, error) {
	els, err := e.reader()
	if err != nil {
		return nil, err
	}
	return els.Rows(ctx, container, columns, maxRows)
}

func (e *routedElements) Focus(target desktop.Element) error {
	els, err := e.reader()
	if err != nil {
		return err
	}
	return els.Focus(target)
}

func (e *routedElements) Apply(target desktop.Element, pattern desktop.Pattern, value string) error {
	els, err := e.reader()
	if err != nil {
		return err
	}
	return els.Apply(target, pattern, value)
}

// pauseClock stops the operation's working time while the step waits for
// something that is not its own work, until the returned function.
func (r *run) pauseClock() func() {
	if r.clock == nil {
		return func() {}
	}
	r.clock.pause()
	return r.clock.resume
}
