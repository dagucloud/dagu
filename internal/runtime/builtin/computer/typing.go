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
	case before.Value != "" && !strings.Contains(desktop.NormalizeText(after.Value), desktop.NormalizeText(r.substitute(text))):
		result.Error = fmt.Sprintf("the typing did not reach %s, which still shows %q", name, r.modelValue(after, ""))
	}
}
