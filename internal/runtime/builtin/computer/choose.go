// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
)

// chooseSettle bounds how long a way of choosing waits for the control to
// show the option, or an opened control for its options, looked at every
// choosePoll; maxChooseSteps bounds the options Down walks through.
const (
	chooseSettle   = time.Second
	choosePoll     = 50 * time.Millisecond
	maxChooseSteps = 100
)

// choose sets a choice control, a combo box or a pop-up, to the option
// named, through the control itself wherever it can, so neither the model
// nor a replay aims at an option in a list the screen draws apart from the
// window. It tries, until the control shows the option:
//
//  1. nothing, when the control shows it already;
//  2. opening the control and pressing the option among the window's
//     elements, as a macOS pop-up button's menu is, unless the step refuses
//     the option, which then fails the choice;
//  3. typing the option into the control once it holds the keyboard focus,
//     as a person types into a combo box or types to pick from a list, and
//     never Enter, which may press a form's default button;
//  4. Home, then Down one option at a time, until the control shows it or
//     the end of its list.
//
// An option the step refuses is never chosen, nor one with a line break,
// which typing would send as Enter. It returns why the control does not
// show the option, or "".
func (r *run) choose(ctx context.Context, index int, els desktop.Elements, control desktop.Element, option string) (string, error) {
	want := desktop.NormalizeText(option)
	rec := r.recordElement(control, nil, "")
	if want == "" || rec == nil || strings.ContainsAny(option, "\r\n") {
		return fmt.Sprintf("%s cannot be told which option to show", describeElement(control)), nil
	}
	for _, word := range r.cfg.Refuse {
		if namesWord(option, word) {
			return fmt.Sprintf("option %q is not chosen in this step, which refuses %q; leave it as it is", r.shownText(option), word), nil
		}
	}
	shows := func() (string, bool) {
		found, reason, err := r.lookRecorded(els, *rec)
		if err != nil || reason != "" {
			return "", false
		}
		return found.Value, desktop.NormalizeText(found.Value) == want
	}
	awaitShows := func() (bool, error) {
		for deadline := time.Now().Add(chooseSettle); ; {
			if _, ok := shows(); ok {
				return true, nil
			}
			if !time.Now().Before(deadline) {
				return false, nil
			}
			if err := sleep(ctx, choosePoll); err != nil {
				return false, err
			}
		}
	}
	chose := func(how string) {
		logAction(r.timeline, index, fmt.Sprintf("chose %q in %s %s", r.shownText(option), describeElement(control), how))
	}
	if _, ok := shows(); ok {
		chose("(it showed it already)")
		return "", nil
	}
	if opened, refused, err := r.chooseFromOpen(ctx, els, control, option); err != nil || refused != "" {
		return refused, err
	} else if opened {
		if ok, err := awaitShows(); err != nil || ok {
			chose("from its list")
			return "", err
		}
	}
	if r.focusControl(els, control) {
		if err := r.typeOption(ctx, option); err != nil {
			return "", err
		}
		if ok, err := awaitShows(); err != nil || ok {
			chose("by typing it")
			return "", err
		}
		if ok, err := r.walkOptions(ctx, shows); err != nil || ok {
			chose("by stepping through its list")
			return "", err
		}
	}
	value, _ := shows()
	return fmt.Sprintf("%s shows %q, not %q, after every way to choose it", describeElement(control), r.shownText(value), r.shownText(option)), nil
}

// chooseFromOpen opens the control and presses the option among the
// elements its window then holds, and reports whether it pressed one, or
// why the step refuses to. A control that does not open, or whose options
// the window's elements do not hold, as a Windows combo box's list in a
// window of its own, is closed again and left to the keyboard.
func (r *run) chooseFromOpen(ctx context.Context, els desktop.Elements, control desktop.Element, option string) (bool, string, error) {
	if err := r.applyPattern(els, control, desktop.PatternExpand, ""); err != nil {
		return false, "", nil
	}
	for deadline := time.Now().Add(chooseSettle); ; {
		for _, role := range []string{desktop.RoleMenuItem, desktop.RoleListItem} {
			matches, err := els.Find(desktop.Selector{Role: role, Name: option})
			if err != nil || len(matches) != 1 {
				continue
			}
			if why, refused := r.refusedElement(matches[0]); refused {
				return false, why, r.pressKeys(ctx, "escape")
			}
			pattern := desktop.PatternInvoke
			if role == desktop.RoleListItem {
				pattern = desktop.PatternSelect
			}
			if err := r.applyPattern(els, matches[0], pattern, ""); err == nil {
				return true, "", nil
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		if err := sleep(ctx, choosePoll); err != nil {
			return false, "", err
		}
	}
	return false, "", r.pressKeys(ctx, "escape")
}

// focusControl gives the control the keyboard focus and reports whether it
// holds it, so keys meant for it never reach another field.
func (r *run) focusControl(els desktop.Elements, control desktop.Element) bool {
	if err := els.Focus(control); err != nil {
		return false
	}
	focused, err := r.focusedElement()
	return err == nil && slices.Equal(focused.Path, control.Path)
}

// typeOption replaces what the focused control holds with the option, as
// typing over a selection does.
func (r *run) typeOption(ctx context.Context, option string) error {
	selectAll := "ctrl+a"
	if r.system == systemMacOS {
		selectAll = "cmd+a"
	}
	if err := r.pressKeys(ctx, selectAll); err != nil {
		return err
	}
	return r.driver.Type(ctx, option)
}

// walkOptions steps the focused control from its first option down, one
// at a time, and reports whether it came to show the option. It stops at
// the end of the list, where Down leaves the control as it was.
func (r *run) walkOptions(ctx context.Context, shows func() (string, bool)) (bool, error) {
	if err := r.pressKeys(ctx, "home"); err != nil {
		return false, err
	}
	last, seen := "", false
	for range maxChooseSteps {
		if err := sleep(ctx, choosePoll); err != nil {
			return false, err
		}
		value, ok := shows()
		if ok {
			return true, nil
		}
		if seen && value == last {
			return false, nil
		}
		last, seen = value, true
		if err := r.pressKeys(ctx, "down"); err != nil {
			return false, err
		}
	}
	return false, nil
}

// pressKeys presses a chord, such as cmd+a, once.
func (r *run) pressKeys(ctx context.Context, chord string) error {
	keys, err := desktop.ParseKeys(strings.Split(chord, "+"))
	if err != nil {
		return err
	}
	return r.driver.PressKeys(ctx, keys, 1)
}

// shownText is text as the step's log and the model may read it: declared
// secrets masked, then the step's typed values as their placeholders, so a
// typed value within a secret never leaves the rest of it to be seen.
func (r *run) shownText(text string) string {
	return r.placeholderIn(r.masker.MaskString(text))
}
