// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

// maxActionPause caps the waits and key holds a model asks for, so one
// action cannot stall the step.
const maxActionPause = time.Minute

// positionMapper converts a position an action names into display pixels.
type positionMapper func(computeruse.Point) image.Point

// performOne runs one action on the screen the model saw and records it for
// the replay cache when it completed and can be replayed. The recorded
// action is nil when the action only read the screen or failed.
func (r *run) performOne(ctx context.Context, index int, action computeruse.Action, seen screen, limit computeruse.ImageLimit) (computeruse.Result, *recordedAction) {
	logAction(r.timeline, index, describeAction(action))
	// The element the action lands on is read before the action changes the
	// screen; the pointer has not moved yet either.
	display, replayable := r.toDisplay(action, seen)
	var element *recordedElement
	var before *bool
	if replayable {
		element, before = r.elementOf(display)
	}
	result := r.runAction(ctx, action, seen.toFull, &seen, limit)
	if result.Failed() {
		logAction(r.timeline, index, "  failed: "+result.Error)
		return result, nil
	}
	if !replayable {
		return result, nil
	}
	if element != nil && action.Kind == computeruse.KindType {
		r.proveValue(element, action.Text)
	}
	rec := recordAction(display, element)
	rec.Checked = r.checkedAfter(ctx, display, element, before)
	return result, &rec
}

// A recorded click on a checkbox waits up to checkedSettle, looking every
// checkedPoll, for the box to show the state the click left it in.
const (
	checkedSettle = time.Second
	checkedPoll   = 50 * time.Millisecond
)

// checkedAfter reads the state a plain click left a checkbox in, so the
// recording replays the click as that state rather than as a toggle that
// would undo a box already in it. An application may update the box a
// moment after the click, so it waits for a state other than before, the
// one the box showed before the click, when that is known. It is nil for
// any other action or element, and when the box shows no state or still
// shows the one it had.
func (r *run) checkedAfter(ctx context.Context, display computeruse.Action, element *recordedElement, before *bool) *bool {
	if element == nil || element.Selector.Role != desktop.RoleCheckbox || !plainClick(display) {
		return nil
	}
	els, err := r.elementsIfAvailable()
	if err != nil {
		return nil
	}
	for deadline := time.Now().Add(checkedSettle); ; {
		found, reason, err := r.lookRecorded(els, *element)
		if err == nil && reason == "" {
			if checked, known := checkedState(found); known && (before == nil || checked != *before) {
				return &checked
			}
		}
		if !time.Now().Before(deadline) || sleep(ctx, checkedPoll) != nil {
			return nil
		}
	}
}

// elementOf reads the element a replayable action lands on, in display
// pixels, or types into, when the host can read elements, and the state a
// checkbox shows before the action, when it is one.
func (r *run) elementOf(display computeruse.Action) (*recordedElement, *bool) {
	if at, pointer := target(display); pointer {
		if e, err := r.elementAt(at); err == nil {
			// The application comes from the window the action lands on, so
			// an action in a second visible window records its own window,
			// not the one the turn started in.
			return r.recordElement(e, &at, r.driver.WindowAt(at).App), checkedBefore(e)
		}
		return nil, nil
	}
	switch display.Kind {
	case computeruse.KindType, computeruse.KindKey, computeruse.KindHoldKey:
		if e, err := r.focusedElement(); err == nil {
			return r.recordElement(e, nil, r.driver.FocusedWindow().App), nil
		}
	}
	return nil, nil
}

// checkedNow reads the state a checkbox shows now, where the screen has it,
// rather than trusting e, which the step read earlier; it falls back to e
// when the box cannot be found again, and is nil for any other element.
func (r *run) checkedNow(els desktop.Elements, e desktop.Element) *bool {
	if e.Role != desktop.RoleCheckbox {
		return nil
	}
	centre := elementCentre(e)
	if rec := r.recordElement(e, &centre, r.driver.WindowAt(centre).App); rec != nil {
		if found, reason, err := r.lookRecorded(els, *rec); err == nil && reason == "" {
			return checkedBefore(found)
		}
	}
	return checkedBefore(e)
}

// checkedBefore is the state a checkbox shows, or nil for an element that
// shows none.
func checkedBefore(e desktop.Element) *bool {
	if checked, known := checkedState(e); known && e.Role == desktop.RoleCheckbox {
		return &checked
	}
	return nil
}

// runAction performs one action. seen is the screen positions refer to; it
// is nil when replaying, where positions are already display pixels.
func (r *run) runAction(ctx context.Context, action computeruse.Action, toFull positionMapper, seen *screen, limit computeruse.ImageLimit) computeruse.Result {
	result := computeruse.Result{CallID: action.CallID}
	var at *image.Point
	if action.Point != nil {
		p := toFull(*action.Point)
		at = &p
	}
	// The step never operates the application that runs it: a keystroke
	// to a window of its own a person just focused could close it.
	own, ok, err := r.ownWindow(action, at, toFull)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if ok {
		result.Error = r.ownedMessage(own)
		return result
	}
	if why, refused := r.refusedAction(action, at, toFull); refused {
		result.Error = why
		return result
	}
	switch action.Kind {
	case computeruse.KindClick:
		err = r.click(ctx, action, at)
	case computeruse.KindMove:
		if at == nil {
			err = errors.New("move needs a position")
			break
		}
		var modifiers []desktop.Key
		if modifiers, err = desktop.ParseKeys(action.Modifiers); err == nil {
			err = r.driver.MoveHolding(ctx, *at, modifiers)
		}
	case computeruse.KindDrag:
		path := make([]image.Point, 0, len(action.Path))
		for _, p := range action.Path {
			path = append(path, toFull(p))
		}
		var modifiers []desktop.Key
		if modifiers, err = desktop.ParseKeys(action.Modifiers); err == nil {
			err = r.driver.Drag(ctx, path, modifiers)
		}
	case computeruse.KindMouseDown, computeruse.KindMouseUp:
		var button desktop.Button
		if button, err = desktop.ParseButton(action.Button); err == nil {
			if action.Kind == computeruse.KindMouseDown {
				err = r.driver.PressButton(button)
			} else {
				err = r.driver.ReleaseButton(button)
			}
		}
	case computeruse.KindScroll:
		var modifiers []desktop.Key
		if modifiers, err = desktop.ParseKeys(action.Modifiers); err == nil {
			err = r.driver.Scroll(ctx, at, action.ScrollX, action.ScrollY, modifiers)
		}
	case computeruse.KindType:
		err = r.driver.Type(ctx, r.substitute(action.Text))
	case computeruse.KindKey:
		var keys []desktop.Key
		if keys, err = desktop.ParseKeys(action.Keys); err == nil {
			err = r.driver.PressKeys(ctx, keys, action.Repeat)
		}
	case computeruse.KindHoldKey:
		var keys []desktop.Key
		if keys, err = desktop.ParseKeys(action.Keys); err == nil {
			err = r.driver.HoldKeys(ctx, keys, min(action.Duration, maxActionPause))
		}
	case computeruse.KindWait:
		err = sleep(ctx, min(action.Duration, maxActionPause))
	case computeruse.KindScreenshot:
		result.Image, err = r.snapshot(ctx, limit, nil)
	case computeruse.KindZoom:
		if action.Region == nil || seen == nil {
			err = errors.New("zoom needs a region")
			break
		}
		region := image.Rectangle{Min: toFull(action.Region.Min), Max: toFull(action.Region.Max)}
		result.Image, err = r.snapshot(ctx, limit, &region)
	case computeruse.KindCursorPosition:
		var p image.Point
		if p, err = r.driver.CursorPosition(); err == nil && seen != nil {
			model := seen.toModel(p)
			result.Output = fmt.Sprintf("X=%d, Y=%d", model.X, model.Y)
		}
	default:
		err = fmt.Errorf("unsupported action %q", action.Kind)
	}
	if err != nil {
		result.Error = r.masker.MaskString(err.Error())
	}
	return result
}

func (r *run) click(ctx context.Context, action computeruse.Action, at *image.Point) error {
	button, err := desktop.ParseButton(action.Button)
	if err != nil {
		return err
	}
	modifiers, err := desktop.ParseKeys(action.Modifiers)
	if err != nil {
		return err
	}
	return r.driver.Click(ctx, at, button, max(action.Count, 1), modifiers)
}

// ownWindow reports a window an action would operate that belongs to the
// application that runs the step. It gathers every window the action
// reaches: the focused window for keys and typing, the window under the
// position for pointer actions, the window under the cursor for a click or
// scroll that names no position and for a button press, which moves
// nothing, every point a drag crosses, and the focused
// window for any pointer action that holds modifiers, whose key-down
// reaches the focus wherever the pointer is. It reports the first of those
// the step owns, so a keystroke or click never reaches the application. An
// action at the cursor is refused with an error when the cursor's position
// cannot be read, since the window it would reach is unknown.
func (r *run) ownWindow(action computeruse.Action, at *image.Point, toFull positionMapper) (desktop.WindowID, bool, error) {
	var windows []desktop.WindowID
	underCursor := func() error {
		p, err := r.driver.CursorPosition()
		if err != nil {
			return fmt.Errorf("the pointer's position cannot be read to check the window it is over: %w", err)
		}
		windows = append(windows, r.driver.WindowAt(p))
		return nil
	}
	switch action.Kind {
	case computeruse.KindType, computeruse.KindKey, computeruse.KindHoldKey:
		windows = append(windows, r.driver.FocusedWindow())
	case computeruse.KindClick, computeruse.KindScroll:
		// One that names no position acts at the cursor.
		if at != nil {
			windows = append(windows, r.driver.WindowAt(*at))
		} else if err := underCursor(); err != nil {
			return desktop.WindowID{}, false, err
		}
	case computeruse.KindMove:
		if at != nil {
			windows = append(windows, r.driver.WindowAt(*at))
		}
	case computeruse.KindMouseDown, computeruse.KindMouseUp:
		// A button press acts at the cursor, which the action does not move.
		if err := underCursor(); err != nil {
			return desktop.WindowID{}, false, err
		}
	case computeruse.KindDrag:
		for _, p := range action.Path {
			windows = append(windows, r.driver.WindowAt(toFull(p)))
		}
	default:
		return desktop.WindowID{}, false, nil
	}
	// A pointer action that holds modifiers sends key-downs to the focused
	// window wherever the pointer is; a button press sends none.
	if len(action.Modifiers) > 0 && action.Kind != computeruse.KindMouseDown && action.Kind != computeruse.KindMouseUp {
		windows = append(windows, r.driver.FocusedWindow())
	}
	for _, window := range windows {
		if r.driver.Owned(window) {
			return window, true, nil
		}
	}
	return desktop.WindowID{}, false, nil
}

// snapshot captures the screen, or a region of it, scaled to the model's
// limit.
func (r *run) snapshot(ctx context.Context, limit computeruse.ImageLimit, region *image.Rectangle) (*llmpkg.Image, error) {
	full, err := r.settle(ctx)
	if err != nil {
		return nil, err
	}
	if region != nil {
		full = desktop.Crop(full, *region)
		if full.Bounds().Empty() {
			return nil, errors.New("the zoom region is outside the screen")
		}
	}
	shot, err := newScreen(full, limit)
	if err != nil {
		return nil, err
	}
	img := shot.image()
	return &img, nil
}

// substitute replaces %name% placeholders with variable values. Unknown
// names stay as written.
func (r *run) substitute(text string) string {
	return agentstep.SubstituteVariables(text, r.variables)
}

// toDisplay returns a completed action in display pixels, or false for
// actions that only read the screen and need no replay. Typed text equal
// to a variable's value is kept as its placeholder.
func (r *run) toDisplay(action computeruse.Action, seen screen) (computeruse.Action, bool) {
	switch action.Kind {
	case computeruse.KindScreenshot, computeruse.KindZoom, computeruse.KindCursorPosition:
		return computeruse.Action{}, false
	}
	display := action
	display.CallID = ""
	if action.Kind == computeruse.KindType {
		display.Text = r.placeholder(action.Text)
	}
	if action.Point != nil {
		p := seen.toFull(*action.Point)
		display.Point = &computeruse.Point{X: p.X, Y: p.Y}
	}
	if len(action.Path) > 0 {
		display.Path = make([]computeruse.Point, 0, len(action.Path))
		for _, point := range action.Path {
			p := seen.toFull(point)
			display.Path = append(display.Path, computeruse.Point{X: p.X, Y: p.Y})
		}
	}
	return display, true
}

// target returns where a pointer action lands, in display pixels.
func target(action computeruse.Action) (image.Point, bool) {
	switch {
	case action.Point != nil:
		return image.Pt(action.Point.X, action.Point.Y), true
	case len(action.Path) > 0:
		return image.Pt(action.Path[0].X, action.Path[0].Y), true
	default:
		return image.Point{}, false
	}
}

func identity(p computeruse.Point) image.Point {
	return image.Pt(p.X, p.Y)
}

// describeAction summarizes an action for the step log. Typed text keeps
// its %name% placeholders.
func describeAction(action computeruse.Action) string {
	var b strings.Builder
	b.WriteString(string(action.Kind))
	switch action.Kind {
	case computeruse.KindClick:
		if action.Button != "" && action.Button != computeruse.ButtonLeft {
			b.WriteString(" " + action.Button)
		}
		if action.Count > 1 {
			fmt.Fprintf(&b, " x%d", action.Count)
		}
	case computeruse.KindScroll:
		fmt.Fprintf(&b, " %d,%d", action.ScrollX, action.ScrollY)
	case computeruse.KindType:
		fmt.Fprintf(&b, " %q", action.Text)
	case computeruse.KindKey, computeruse.KindHoldKey:
		b.WriteString(" " + strings.Join(action.Keys, "+"))
		if action.Repeat > 1 {
			fmt.Fprintf(&b, " x%d", action.Repeat)
		}
	case computeruse.KindWait:
		b.WriteString(" " + action.Duration.String())
	case computeruse.KindZoom:
		if region := action.Region; region != nil {
			fmt.Fprintf(&b, " %d,%d-%d,%d", region.Min.X, region.Min.Y, region.Max.X, region.Max.Y)
		}
	}
	if action.Point != nil {
		fmt.Fprintf(&b, " at %d,%d", action.Point.X, action.Point.Y)
	}
	for i, p := range action.Path {
		if i == 0 {
			fmt.Fprintf(&b, " from %d,%d", p.X, p.Y)
		} else {
			fmt.Fprintf(&b, " to %d,%d", p.X, p.Y)
		}
	}
	if len(action.Modifiers) > 0 {
		b.WriteString(" with " + strings.Join(action.Modifiers, "+"))
	}
	return b.String()
}
