// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"errors"
	"fmt"
	"image"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
)

const (
	// minPlaceholderValue is the shortest variable value recorded as its
	// placeholder; a shorter one, such as a digit, names too much.
	minPlaceholderValue = 2
	// maxLandmarks is how many elements a recording keeps to recognise the
	// screen an act ends on.
	maxLandmarks = 3
)

// recordedElement is the element an action landed on or typed into, kept
// beside the action's pixels so a later run finds it again on a screen
// that moved or changed.
type recordedElement struct {
	// Selector names the element by role and its name or id. A name equal
	// to a variable's value holds the placeholder instead.
	Selector desktop.Selector `json:"selector"`
	// Path is where the element sat in its window, to break a tie between
	// equal matches.
	Path []desktop.PathStep `json:"path"`
	// App is the application of the element's window when the action ran:
	// the process image name, which a replay matches the window in front
	// by, since a title often shows the document's contents and changes
	// every run. It is read when the action is recorded, not at the end of
	// the act, so an act that closes its window still records the window it
	// acted on, not whatever was behind it. Window is that window's title,
	// kept to tell apart several windows of one application and as a
	// fallback when the application cannot be read.
	App    string `json:"app,omitempty"`
	Window string `json:"window"`
	Label  string `json:"label,omitempty"`
	// FractionX and FractionY are where a pointer action landed inside the
	// element's bounds, from 0 to 1. They are zero for typing and keys.
	FractionX float64 `json:"fx"`
	FractionY float64 `json:"fy"`
}

// recordElement describes an element for a recording, or returns nil for
// one a later run could not find again: a window, an element without
// bounds, or one with neither a name nor an id.
func (r *run) recordElement(e desktop.Element, at *image.Point, app string) *recordedElement {
	if e.Role == desktop.RoleWindow || e.Bounds.Empty() || (e.Name == "" && e.ID == "") {
		return nil
	}
	rec := &recordedElement{
		Selector: desktop.Selector{Role: e.Role, Name: r.placeholder(e.Name), ID: e.ID},
		Path:     e.Path,
		App:      app,
		Window:   r.placeholderIn(e.Window),
		Label:    e.Label,
	}
	if at != nil {
		rec.FractionX = fraction(at.X-e.Bounds.Min.X, e.Bounds.Dx())
		rec.FractionY = fraction(at.Y-e.Bounds.Min.Y, e.Bounds.Dy())
	}
	return rec
}

func fraction(offset, size int) float64 {
	if size <= 0 {
		return 0
	}
	return min(max(float64(offset)/float64(size), 0), 1)
}

// placeholder returns the %name% of the variable whose value is text, so
// a recording made on one record replays on the next, or text as it is.
// Values compare as names match, so a padded name still finds its
// variable.
func (r *run) placeholder(text string) string {
	if len(text) < minPlaceholderValue {
		return text
	}
	wanted := desktop.NormalizeText(text)
	for _, name := range slices.Sorted(maps.Keys(r.variables)) {
		if desktop.NormalizeText(r.variables[name]) == wanted {
			return "%" + name + "%"
		}
	}
	return text
}

// placeholderIn replaces every variable value text contains with its
// placeholder, for a window title that carries a record's number.
func (r *run) placeholderIn(text string) string {
	for _, name := range slices.Sorted(maps.Keys(r.variables)) {
		if value := r.variables[name]; len(value) >= minPlaceholderValue {
			text = strings.ReplaceAll(text, value, "%"+name+"%")
		}
	}
	return text
}

// point is where a pointer action lands on the element as found now.
func (rec recordedElement) point(found desktop.Element) image.Point {
	return image.Pt(
		found.Bounds.Min.X+int(rec.FractionX*float64(found.Bounds.Dx())+0.5),
		found.Bounds.Min.Y+int(rec.FractionY*float64(found.Bounds.Dy())+0.5),
	)
}

// describe names the element for logs and reasons: button "保存", or
// text_field id=amountBox for one without a name.
func (rec recordedElement) describe() string {
	if rec.Selector.Name != "" {
		return fmt.Sprintf("%s %q", rec.Selector.Role, rec.Selector.Name)
	}
	return fmt.Sprintf("%s id=%s", rec.Selector.Role, rec.Selector.ID)
}

// elementTurn reports whether a turn replays by elements: at least one
// action has one, and every pointer action does.
func elementTurn(turn recordedTurn) bool {
	any := false
	for _, rec := range turn.Actions {
		if rec.Element != nil {
			any = true
			continue
		}
		if _, pointer := target(rec.Action); pointer {
			return false
		}
	}
	return any
}

// describeRecorded summarizes a recorded action replayed on its element.
func describeRecorded(rec recordedAction, at image.Point) string {
	switch rec.Action.Kind {
	case computeruse.KindType:
		return fmt.Sprintf("type %q into %s", rec.Action.Text, rec.Element.describe())
	case computeruse.KindKey, computeruse.KindHoldKey:
		return fmt.Sprintf("%s %s in %s", rec.Action.Kind, strings.Join(rec.Action.Keys, "+"), rec.Element.describe())
	default:
		return fmt.Sprintf("%s %s at %d,%d", rec.Action.Kind, rec.Element.describe(), at.X, at.Y)
	}
}

// elementsIfAvailable opens the elements reader for recording and replay.
// A host that cannot read elements is remembered, so a run pays for that
// once and records pixels alone.
func (r *run) elementsIfAvailable() (desktop.Elements, error) {
	if r.elementsErr != nil {
		return nil, r.elementsErr
	}
	els, err := r.elementsReader()
	if err != nil {
		r.elementsErr = err
	}
	return els, err
}

// elementAt reads the element under a display point.
func (r *run) elementAt(at image.Point) (desktop.Element, error) {
	els, err := r.elementsIfAvailable()
	if err != nil {
		return desktop.Element{}, err
	}
	return els.At(at.X, at.Y)
}

// focusedElement reads the element with the keyboard focus.
func (r *run) focusedElement() (desktop.Element, error) {
	els, err := r.elementsIfAvailable()
	if err != nil {
		return desktop.Element{}, err
	}
	return els.Focused()
}

// landmarks completes a recording with the screen the act ended on: the
// front window's title and up to three of the elements the act touched
// last that are still there.
func (r *run) landmarks(rec *recording, touched []recordedElement) {
	els, err := r.elementsIfAvailable()
	if err != nil {
		return
	}
	front, err := els.FrontWindow()
	if err != nil {
		return
	}
	rec.App = front.App
	rec.Window = r.placeholderIn(front.Name)
	seen := map[string]bool{}
	for i := len(touched) - 1; i >= 0 && len(rec.Landmarks) < maxLandmarks; i-- {
		key := touched[i].Selector.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, reason, err := r.lookRecorded(els, touched[i]); err == nil && reason == "" {
			rec.Landmarks = append(rec.Landmarks, recordedElement{Selector: touched[i].Selector, Path: touched[i].Path, App: touched[i].App, Window: touched[i].Window})
		}
	}
}

// frontMatches reports whether the window in front is the recorded one. It
// matches by application when both apps are known, since a title often
// shows the document's contents and changes every run; it falls back to the
// title when either app is unknown. It returns why it did not match.
func (r *run) frontMatches(front desktop.Element, app, window string) (bool, string) {
	if app != "" && front.App != "" {
		if strings.EqualFold(front.App, app) {
			return true, ""
		}
		return false, fmt.Sprintf("the app in front is %q, not %q", front.App, app)
	}
	title := r.substitute(window)
	if desktop.WindowMatches(front.Name, title) {
		return true, ""
	}
	return false, fmt.Sprintf("the window in front is %q, not %q", front.Name, title)
}

// windowMatchesRecorded reports whether a top-level window is the recorded
// one, by application when known, else by title. title is already
// substituted. It is used to find a window to raise and to tell whether the
// right one is already in front.
func windowMatchesRecorded(w desktop.WindowID, app, title string) bool {
	if app != "" && w.App != "" {
		return strings.EqualFold(w.App, app)
	}
	return title != "" && desktop.WindowMatches(w.Title, title)
}

// lookRecorded finds a recorded element on the screen now, or says why it
// is not there. The window in front must be the application the element was
// recorded in.
func (r *run) lookRecorded(els desktop.Elements, rec recordedElement) (desktop.Element, string, error) {
	sel := substituteSelector(rec.Selector, r.variables)
	front, err := els.FrontWindow()
	if err != nil {
		return desktop.Element{}, "", err
	}
	if ok, reason := r.frontMatches(front, rec.App, rec.Window); !ok {
		return desktop.Element{}, reason, nil
	}
	matches, err := els.Find(sel)
	switch {
	case errors.Is(err, desktop.ErrNoElements):
		return desktop.Element{}, fmt.Sprintf("%q exposes no elements", front.Name), nil
	case errors.Is(err, desktop.ErrNotFound) || errors.Is(err, desktop.ErrAmbiguous):
		return desktop.Element{}, err.Error(), nil
	case err != nil:
		return desktop.Element{}, "", err
	}
	found, err := desktop.One(desktop.MatchPath(matches, rec.Path), sel)
	if err == nil {
		return found, "", nil
	}
	if ambiguous, ok := errors.AsType[*desktop.AmbiguousError](err); ok {
		return desktop.Element{}, fmt.Sprintf("%d elements match", ambiguous.Count), nil
	}
	return desktop.Element{}, "it is not there", nil
}

// resolveElement finds a recorded element, looking again until it is
// there or findWithin passes.
func (r *run) resolveElement(ctx context.Context, index int, els desktop.Elements, rec recordedElement, findWithin time.Duration) (desktop.Element, string, error) {
	deadline := time.Now().Add(findWithin)
	title := r.substitute(rec.Window)
	for {
		// A window that slipped behind another is brought forward rather
		// than counted a miss, so a replay survives a focus change.
		r.bringForward(ctx, index, rec.App, title)
		found, reason, err := r.lookRecorded(els, rec)
		if err != nil || reason == "" {
			return found, reason, err
		}
		if !time.Now().Before(deadline) {
			return desktop.Element{}, reason, nil
		}
		if err := sleep(ctx, min(exactPollInterval, time.Until(deadline))); err != nil {
			return desktop.Element{}, "", err
		}
	}
}

// foregroundSettle gives a raised window a moment to reach the front before
// the step looks at it again.
const foregroundSettle = 200 * time.Millisecond

// bringForward raises the recorded window when it is not already in front,
// so a replay acts on it even after it lost the focus. The window is matched
// by its application; among several windows of that application the one whose
// title still matches is preferred. It never raises the step's own
// application, and is a best effort: the look that follows still checks the
// front window. title is already substituted.
func (r *run) bringForward(ctx context.Context, index int, app, title string) {
	if app == "" && title == "" {
		return
	}
	// Choose the best window before looking at the focused one: a window of
	// the recorded app whose title still matches is preferred, so a second
	// document of the same application does not win just by holding the
	// focus; any window of the app is the fallback.
	windows := r.driver.Windows()
	var best, any *desktop.WindowID
	for i := range windows {
		w := windows[i]
		if r.driver.Owned(w) || !windowMatchesRecorded(w, app, title) {
			continue
		}
		if any == nil {
			any = &windows[i]
		}
		if title != "" && desktop.WindowMatches(w.Title, title) {
			best = &windows[i]
			break
		}
	}
	target := best
	if target == nil {
		target = any
	}
	if target == nil {
		return
	}
	// Already acting on the best window? Leave it; only raise when a better
	// window than the one in front is open.
	if front := r.driver.FocusedWindow(); front.Known() && front.Handle == target.Handle {
		return
	}
	if err := r.driver.Raise(*target); err != nil {
		return
	}
	logAction(r.timeline, index, fmt.Sprintf("bringing %q to the front", target.Title))
	_ = sleep(ctx, foregroundSettle)
}

// replayByElement replays one turn on the elements it recorded. It returns
// the reason the turn missed, or "" when every action ran.
func (r *run) replayByElement(ctx context.Context, index, position, total int, turn recordedTurn, els desktop.Elements, findWithin time.Duration, outcome *replayOutcome) (string, error) {
	for _, rec := range turn.Actions {
		action := rec.Action
		if rec.Element == nil {
			logAction(r.timeline, index, "replay "+describeAction(action))
		} else {
			found, reason, err := r.resolveElement(ctx, index, els, *rec.Element, findWithin)
			if err != nil {
				return "", err
			}
			if reason != "" {
				return fmt.Sprintf("%s was not found within %s at turn %d of %d: %s", rec.Element.describe(), findWithin, position+1, total, reason), nil
			}
			var at image.Point
			switch action.Kind {
			case computeruse.KindType, computeruse.KindKey, computeruse.KindHoldKey:
				if err := els.Focus(found); err != nil {
					return fmt.Sprintf("%s could not take the focus at turn %d of %d: %v", rec.Element.describe(), position+1, total, err), nil
				}
			default:
				at = rec.Element.point(found)
				if action.Point != nil {
					action.Point = &computeruse.Point{X: at.X, Y: at.Y}
				}
				if len(action.Path) > 0 {
					shift := at.Sub(image.Pt(action.Path[0].X, action.Path[0].Y))
					action.Path = slices.Clone(action.Path)
					for i := range action.Path {
						action.Path[i] = computeruse.Point{X: action.Path[i].X + shift.X, Y: action.Path[i].Y + shift.Y}
					}
				}
			}
			logAction(r.timeline, index, "replay by element: "+describeRecorded(rec, at))
		}
		outcome.actions++
		if result := r.runAction(ctx, action, identity, nil, computeruse.ImageLimit{}); result.Failed() {
			return fmt.Sprintf("%s failed at turn %d of %d: %s", describeAction(action), position+1, total, result.Error), ctx.Err()
		}
	}
	return "", nil
}

// awaitLandmarks checks the screen an element replay ends on: the window
// in front and the landmarks, looking again until they are there or
// findWithin passes. It returns the reason they are not.
func (r *run) awaitLandmarks(ctx context.Context, els desktop.Elements, entry recording, findWithin time.Duration) (string, error) {
	deadline := time.Now().Add(findWithin)
	for {
		reason, err := r.lookLandmarks(els, entry)
		if err != nil || reason == "" {
			return reason, err
		}
		if !time.Now().Before(deadline) {
			return reason, nil
		}
		if err := sleep(ctx, min(exactPollInterval, time.Until(deadline))); err != nil {
			return "", err
		}
	}
}

func (r *run) lookLandmarks(els desktop.Elements, entry recording) (string, error) {
	front, err := els.FrontWindow()
	if err != nil {
		return "", err
	}
	if ok, _ := r.frontMatches(front, entry.App, entry.Window); !ok {
		if entry.App != "" && front.App != "" {
			return fmt.Sprintf("the app after the last turn is %q, not %q", front.App, entry.App), nil
		}
		return fmt.Sprintf("the window after the last turn is %q, not %q", front.Name, r.substitute(entry.Window)), nil
	}
	for _, landmark := range entry.Landmarks {
		if _, reason, err := r.lookRecorded(els, landmark); err != nil {
			return "", err
		} else if reason != "" {
			return fmt.Sprintf("%s is not there after the last turn: %s", landmark.describe(), reason), nil
		}
	}
	return "", nil
}
