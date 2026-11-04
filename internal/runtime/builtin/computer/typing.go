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

// showsReplaced reports whether a field's value is the text that replaced
// what it held, as typed or with formatting the field inserts: every typed
// character kept in order, and nothing added but separators and symbols,
// such as the comma of 1,200 or the parentheses and hyphen of a phone
// number. A letter, a digit, a decimal point, or a hyphen anywhere but
// right after a digit, where it could be a sign, is never taken as
// formatting, since each changes the value; nor is old text the selection
// did not cover.
func showsReplaced(value, typed string) bool {
	want := []rune(hyphenateMinus(desktop.NormalizeText(typed)))
	afterDigit := false
	for _, r := range hyphenateMinus(desktop.NormalizeText(value)) {
		switch {
		case len(want) > 0 && r == want[0]:
			want = want[1:]
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' && !afterDigit:
			return false
		}
		afterDigit = unicode.IsDigit(r)
	}
	return len(want) == 0
}

// hyphenateMinus writes the minus sign some fields show as a hyphen.
func hyphenateMinus(s string) string {
	return strings.ReplaceAll(s, "−", "-")
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
// reach the window. Otherwise the field takes the keyboard focus with all
// its text selected and gets the text as keys over it, or, for empty text,
// a deletion of it. recs are the actions a replay repeats, in order.
func (l *actLoop) typeElement(ctx context.Context, els desktop.Elements, action computeruse.Action, e desktop.Element) (recs []recordedAction, result computeruse.Result) {
	result = computeruse.Result{CallID: action.CallID}
	if els == nil {
		result.Error = "this host cannot read the field to type into; click it, then type"
		return nil, result
	}
	if own, owned := l.r.elementOffLimits(e); owned {
		result.Error = l.r.offLimitsMessage(own)
		return nil, result
	}
	text := l.r.substitute(action.Text)
	field := l.r.recordElement(e, nil, l.r.driver.WindowAt(elementCentre(e)).App)
	// A field the step cannot find again cannot be read back, and a line
	// break or a tab is a key the window acts on, so either is typed.
	if field != nil && !strings.ContainsAny(text, "\r\n\t") {
		set, err := l.setField(ctx, els, e, *field, text)
		if err != nil {
			result.Error = l.r.masker.MaskString(err.Error())
			return nil, result
		}
		if set != nil {
			l.touched = true
			logAction(l.r.timeline, l.index, fmt.Sprintf("%s %s to %q", desktop.PatternSetValue, describeElement(e), action.Text))
			result.Output = fmt.Sprintf("%s now shows %q", l.r.masker.MaskString(cmp.Or(set.Name, set.Label, "the field")), l.r.modelValue(*set, ""))
			field.SetsValue = true
			recorded := recordAction(computeruse.Action{Kind: computeruse.KindType, Text: l.r.placeholder(action.Text)}, field)
			recorded.Invoked = true
			return []recordedAction{recorded}, result
		}
	}
	if err := els.Focus(e); err != nil {
		result.Error = l.r.masker.MaskString(err.Error())
		return nil, result
	}
	l.touched = true
	logAction(l.r.timeline, l.index, "focus "+describeElement(e))
	// The focus is recorded as a click on the field, as a click the model
	// aims at a field is, so a replay by pixels, which reads no elements,
	// puts the caret in the field before its keys too.
	recs = []recordedAction{l.recordInvoked(ctx, computeruse.Action{Kind: computeruse.KindClick}, e, nil)}
	chords := []string{l.r.selectAllChord()}
	if text == "" {
		// Typing nothing over the selection would leave it in the field.
		chords = append(chords, string(desktop.KeyBackspace))
	}
	for _, chord := range chords {
		keys := computeruse.Action{CallID: action.CallID, Kind: computeruse.KindKey, Keys: computeruse.SplitKeys(chord)}
		pressed, rec := l.r.performOne(ctx, l.index, keys, l.seen, l.limit)
		if pressed.Failed() {
			return nil, pressed
		}
		recs = appendRecorded(recs, rec)
	}
	if text == "" {
		return recs, l.reportEmptied(ctx, action.CallID, e)
	}
	typing := action
	typing.ElementID = ""
	typed, rec := l.r.performOne(ctx, l.index, typing, l.seen, l.limit)
	if typed.Failed() {
		return nil, typed
	}
	l.confirmReplaced(ctx, &typed, e, text)
	return appendRecorded(recs, rec), typed
}

// reportEmptied tells the model whether the field the step deleted the
// text of shows nothing now, giving an app that updates the value a moment
// after the keys typedSettle. A field the step cannot read says nothing, and
// so does one that showed nothing before, as a password field always does,
// since nothing it shows would tell its text was deleted.
func (l *actLoop) reportEmptied(ctx context.Context, callID string, e desktop.Element) computeruse.Result {
	result := computeruse.Result{CallID: callID}
	if e.Value == "" {
		return result
	}
	name := l.r.masker.MaskString(cmp.Or(e.Name, e.Label, "the field"))
	for deadline := time.Now().Add(typedSettle); ; {
		field, err := l.r.focusedElement()
		if err != nil || !slices.Equal(field.Path, e.Path) {
			return result
		}
		if field.Value == "" {
			result.Output = name + " is now empty"
			return result
		}
		if !time.Now().Before(deadline) || sleep(ctx, typedPoll) != nil {
			result.Error = fmt.Sprintf("the text of %s was not deleted; it still shows %q", name, l.r.modelValue(field, ""))
			return result
		}
	}
}

// confirmReplaced fails a typing whose field, its text selected first, does
// not come to show just the typed text, so the rest of the turn, such as a
// save, does not run on the wrong value.
func (l *actLoop) confirmReplaced(ctx context.Context, result *computeruse.Result, e desktop.Element, text string) {
	if field, wrong := l.r.notReplaced(ctx, e, text); wrong {
		name := l.r.masker.MaskString(cmp.Or(e.Name, e.Label, "the field"))
		result.Output = ""
		result.Error = fmt.Sprintf("%s shows %q, not the text typed into it", name, l.r.modelValue(field, ""))
	}
}

// notReplaced waits up to typedSettle for the field e, its text selected
// and typed over, to show just the typed text, as typed or as the field
// formats it, and reports the field as it shows otherwise, as when the
// field cuts the text short or keeps old text. A field the step cannot
// read, one that shows nothing before and after, as a password field does,
// and text holding a key the window acts on are taken as replaced.
func (r *run) notReplaced(ctx context.Context, e desktop.Element, text string) (desktop.Element, bool) {
	if strings.ContainsAny(text, "\r\n\t") {
		return desktop.Element{}, false
	}
	for deadline := time.Now().Add(typedSettle); ; {
		field, err := r.focusedElement()
		if err != nil || !slices.Equal(field.Path, e.Path) || field.Value == "" && e.Value == "" || showsReplaced(field.Value, text) {
			return desktop.Element{}, false
		}
		if !time.Now().Before(deadline) || sleep(ctx, typedPoll) != nil {
			return field, true
		}
	}
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
