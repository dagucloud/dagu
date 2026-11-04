// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
)

// typedSettle bounds how long the step waits for typing to show in a
// field, looked at every typedPoll: an app may update a field's value a
// moment after the keys.
const (
	typedSettle = 500 * time.Millisecond
	typedPoll   = 50 * time.Millisecond
)

// typedField reads the single-line field a typing goes into, before the
// keys, so what they did to it can be told after. It is false for any
// other action, text that moves the focus or makes lines, and a host that
// cannot read the field.
func (r *run) typedField(action computeruse.Action) (desktop.Element, bool) {
	if action.Kind != computeruse.KindType || strings.TrimSpace(action.Text) == "" || strings.ContainsAny(action.Text, "\r\n\t") {
		return desktop.Element{}, false
	}
	e, err := r.focusedElement()
	if err != nil || e.Role != desktop.RoleTextField {
		return desktop.Element{}, false
	}
	return e, true
}

// reportTyped tells the model what typing did to the field it went into:
// the value the field now shows, or, when the field showed something and
// shows it still without the typed text, that the typing did not reach it,
// which fails the action so the rest of the turn, such as a save, does not
// run on a form left as it was. A field that shows nothing before and
// after, as a password field always does, says nothing.
func (r *run) reportTyped(ctx context.Context, result *computeruse.Result, before desktop.Element, text string) {
	var after desktop.Element
	for deadline := time.Now().Add(typedSettle); ; {
		e, err := r.focusedElement()
		if err != nil || !slices.Equal(e.Path, before.Path) {
			// The focus moved, so the field is no longer the one typed in.
			return
		}
		after = e
		if e.Value != before.Value || !time.Now().Before(deadline) || sleep(ctx, typedPoll) != nil {
			break
		}
	}
	name := cmp.Or(after.Name, after.Label, "the field")
	switch {
	case after.Value != before.Value:
		result.Output = fmt.Sprintf("%s now shows %q", name, r.modelValue(after, ""))
	case before.Value != "" && !showsTyped(after.Value, r.substitute(text)):
		result.Error = fmt.Sprintf("the typing did not reach %s, which still shows %q", name, r.modelValue(after, ""))
	}
}

// showsTyped reports whether a field's value holds the typed text, as typed
// or as the field formats it: the same letters and digits with separators
// or symbols between, such as 12,000 or ¥12,000 for 12000.
func showsTyped(value, typed string) bool {
	value, typed = desktop.NormalizeText(value), desktop.NormalizeText(typed)
	return strings.Contains(value, typed) || lettersAndDigits(value) == lettersAndDigits(typed)
}

func lettersAndDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}

// typeElement puts the text the model typed into the field it named by id,
// in place of what the field holds: through the element itself when the
// field takes the value and reads back the same text, so no key has to
// reach the window. Otherwise the field takes the keyboard focus and done
// is false, so the caller types the text as keys.
func (l *actLoop) typeElement(ctx context.Context, els desktop.Elements, action computeruse.Action, e desktop.Element) (done bool, rec *recordedAction, result computeruse.Result) {
	result = computeruse.Result{CallID: action.CallID}
	if els == nil {
		result.Error = "this host cannot read the field to type into; click it, then type"
		return true, nil, result
	}
	if own, owned := l.r.elementOffLimits(e); owned {
		result.Error = l.r.offLimitsMessage(own)
		return true, nil, result
	}
	text := l.r.substitute(action.Text)
	field := l.r.recordElement(e, nil, l.r.driver.WindowAt(elementCentre(e)).App)
	// A field the step cannot find again cannot be read back, and a line
	// break or a tab is a key the window acts on, so either is typed.
	if field != nil && !strings.ContainsAny(text, "\r\n\t") {
		set, err := l.setField(ctx, els, e, *field, text)
		if err != nil {
			result.Error = l.r.masker.MaskString(err.Error())
			return true, nil, result
		}
		if set != nil {
			l.touched = true
			logAction(l.r.timeline, l.index, fmt.Sprintf("%s %s to %q", desktop.PatternSetValue, describeElement(e), action.Text))
			result.Output = fmt.Sprintf("%s now shows %q", l.r.masker.MaskString(cmp.Or(set.Name, set.Label, "the field")), l.r.modelValue(*set, ""))
			field.SetsValue = true
			recorded := recordAction(computeruse.Action{Kind: computeruse.KindType, Text: l.r.placeholder(action.Text)}, field)
			recorded.Invoked = true
			return true, &recorded, result
		}
	}
	if err := els.Focus(e); err != nil {
		result.Error = l.r.masker.MaskString(err.Error())
		return true, nil, result
	}
	l.touched = true
	logAction(l.r.timeline, l.index, "focus "+describeElement(e))
	return false, nil, computeruse.Result{}
}

// setField sets a field's value through the element and returns the field
// as it then reads, or nil when it does not read back the text. A field
// that refuses the value is left as it was; one that takes it but reads
// back something else, as a field that hides or reformats what it is
// given does, is emptied, so the text typed after replaces what it held.
func (l *actLoop) setField(ctx context.Context, els desktop.Elements, e desktop.Element, field recordedElement, text string) (*desktop.Element, error) {
	switch err := l.r.applyPattern(els, e, desktop.PatternSetValue, text); {
	case desktop.Refused(err):
		return nil, nil
	case err != nil:
		return nil, err
	}
	want := desktop.NormalizeText(text)
	for deadline := time.Now().Add(typedSettle); ; {
		found, reason, err := l.r.lookRecorded(els, field)
		if err != nil {
			return nil, err
		}
		if reason == "" && desktop.NormalizeText(found.Value) == want {
			return &found, nil
		}
		if !time.Now().Before(deadline) {
			break
		}
		if err := sleep(ctx, typedPoll); err != nil {
			return nil, err
		}
	}
	logAction(l.r.timeline, l.index, describeElement(e)+" does not read back the value set; typing it instead")
	return nil, l.r.applyPattern(els, e, desktop.PatternSetValue, "")
}
