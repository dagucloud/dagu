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
)

// maxHandled bounds how often one handler runs in a step, so a screen its
// steps do not clear fails the step instead of coming back forever.
const maxHandled = 20

// handler clears a screen that may come up at any point of a step, such as
// a confirmation an application shows only sometimes: when its exact check
// holds in the front window, its pinned steps for this system run, and the
// step carries on.
type handler struct {
	Name     string                  `json:"name,omitempty"`
	When     condition               `json:"when"`
	Recorded map[string][]pinnedStep `json:"recorded"`
}

// title names the handler in the timeline and in errors.
func (h handler) title() string {
	if h.Name != "" {
		return h.Name
	}
	return h.When.String()
}

// validate checks a handler's check and pinned steps. The step's schema
// admits only an exact check without within for when, and so does this,
// for a handler that reaches the step any other way: a statement would ask
// the model at every look, and within would go unused.
func (h handler) validate() error {
	if err := h.When.validate(); err != nil {
		return fmt.Errorf("when: %w", err)
	}
	if h.When.judged() || h.When.Within != "" {
		return errors.New("when must be an exact check (text, element, or window) without within")
	}
	if len(h.Recorded) == 0 {
		return errors.New("recorded must list the steps for at least one system")
	}
	return validateRecorded(h.Recorded)
}

// references lists the variables the handler's check and steps use.
func (h handler) references() []string {
	names := h.When.references()
	for _, system := range slices.Sorted(maps.Keys(h.Recorded)) {
		for _, step := range h.Recorded[system] {
			names = append(names, step.references()...)
		}
	}
	return names
}

// handleScreens runs the first handler whose check holds in the front
// window now, and reports whether one ran. A step calls it before each
// replayed action and whenever what it waits for is not there yet. Steps a
// handler runs look for no handler themselves.
func (r *run) handleScreens(ctx context.Context, index int) (bool, error) {
	if r.handling || len(r.cfg.Handle) == 0 {
		return false, nil
	}
	for i, h := range r.cfg.Handle {
		steps := h.Recorded[r.system]
		if len(steps) == 0 {
			continue
		}
		c, err := r.resolve(h.When)
		if err != nil {
			return false, fmt.Errorf("handle %q: %w", h.title(), err)
		}
		// A check the window cannot answer now, such as one read while no
		// window is in front, is not a screen to clear.
		holds, _, err := r.evaluate(ctx, c)
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if err != nil || !holds {
			continue
		}
		if r.handled == nil {
			r.handled = map[int]int{}
		}
		if r.handled[i] >= maxHandled {
			return false, fmt.Errorf("handle %q: the screen was still there after %d times", h.title(), maxHandled)
		}
		r.handled[i]++
		logAction(r.timeline, index, fmt.Sprintf("handling %s", h.title()))
		if err := r.runHandler(ctx, index, h, steps); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// clearScreens runs handlers until none finds its screen, so what comes
// next, such as the model's look at the screen, meets none, and reports
// whether any ran. A screen its handler does not clear ends the step
// through the handler's limit.
func (r *run) clearScreens(ctx context.Context, index int) (bool, error) {
	var ran bool
	for {
		handled, err := r.handleScreens(ctx, index)
		if err != nil || !handled {
			return ran, err
		}
		ran = true
	}
}

// settleClear waits for a still screen that shows no screen a handler
// clears: one that comes up while the screen settles is cleared, and the
// wait starts again.
func (r *run) settleClear(ctx context.Context, index int) (*image.RGBA, error) {
	for {
		if _, err := r.clearScreens(ctx, index); err != nil {
			return nil, err
		}
		full, err := r.settle(ctx)
		if err != nil {
			return nil, err
		}
		handled, err := r.handleScreens(ctx, index)
		if err != nil || !handled {
			return full, err
		}
	}
}

// runHandler runs a handler's pinned steps, as an act's run, with the
// step's input and find_within.
func (r *run) runHandler(ctx context.Context, index int, h handler, steps []pinnedStep) error {
	findWithin, err := r.cfg.findWithin(actSpec{}, r.exec.findWithin)
	if err != nil {
		return err
	}
	direct, err := r.cfg.direct(actSpec{})
	if err != nil {
		return err
	}
	r.handling = true
	defer func() { r.handling = false }()
	outcome, err := r.replayPinned(ctx, index, steps, direct, findWithin)
	if err != nil {
		return err
	}
	if !outcome.done {
		return fmt.Errorf("handle %q: %s", h.title(), outcome.reason)
	}
	return nil
}
