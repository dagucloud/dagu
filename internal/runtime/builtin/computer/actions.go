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

// perform runs a turn's actions in order on the screen the model saw,
// stopping at the first failure. It returns one result per action and the
// actions that completed, in display pixels, for the replay cache.
func (r *run) perform(ctx context.Context, index int, actions []computeruse.Action, seen screen, limit computeruse.ImageLimit) ([]computeruse.Result, []recordedAction) {
	results := make([]computeruse.Result, 0, len(actions))
	var recorded []recordedAction
	failed := false
	for _, action := range actions {
		if failed {
			results = append(results, computeruse.Result{CallID: action.CallID, Skipped: true})
			continue
		}
		logAction(r.timeline, index, describeAction(action))
		result := r.runAction(ctx, action, seen.toFull, &seen, limit)
		results = append(results, result)
		if result.Failed() {
			failed = true
			logAction(r.timeline, index, "  failed: "+result.Error)
			continue
		}
		if display, ok := toDisplay(action, seen); ok {
			recorded = append(recorded, recordAction(display, seen.full))
		}
	}
	return results, recorded
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
	var err error
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
// actions that only read the screen and need no replay.
func toDisplay(action computeruse.Action, seen screen) (computeruse.Action, bool) {
	switch action.Kind {
	case computeruse.KindScreenshot, computeruse.KindZoom, computeruse.KindCursorPosition:
		return computeruse.Action{}, false
	}
	display := action
	display.CallID = ""
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
