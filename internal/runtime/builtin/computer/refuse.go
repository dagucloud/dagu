// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"fmt"
	"image"
	"regexp"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
)

// refusedElement reports an element the step never presses: one whose name,
// or the name of the button, link, custom control, or other element a
// person presses that holds it, has a word with.refuse lists, such as 保存
// or Save. It says why, for the model or the replay that tried.
func (r *run) refusedElement(e desktop.Element) (string, bool) {
	n, word, refused := r.refusedName(e)
	if !refused {
		return "", false
	}
	return fmt.Sprintf("%s %q is not pressed in this step, which refuses %q; leave it as it is", n.Role, n.Name, word), true
}

// refusedName returns the name, of e or of the element a person presses
// that holds it, that has a word with.refuse lists, and that word.
func (r *run) refusedName(e desktop.Element) (desktop.PathStep, string, bool) {
	names := []desktop.PathStep{{Role: e.Role, Name: e.Name}}
	for i := len(e.Path) - 2; i >= 0; i-- {
		// A custom control, other to the system, is pressed as a button
		// is, though the model is not offered it by id.
		if actionableRole(e.Path[i].Role) || e.Path[i].Role == desktop.RoleOther {
			names = append(names, e.Path[i])
		}
	}
	for _, word := range r.cfg.Refuse {
		for _, n := range names {
			if namesWord(n.Name, word) {
				return n, word, true
			}
		}
	}
	return desktop.PathStep{}, "", false
}

// refusedAction reports an action that would press an element the step
// never presses: a click on one, a button press or a drag that starts over
// one, or Enter, pressed, held, typed as a line break, or held as a
// modifier, while one is in the window in front, where Enter may press it
// as the window's default button. An action whose element the host cannot
// read goes on.
func (r *run) refusedAction(action computeruse.Action, at *image.Point, toFull positionMapper) (string, bool) {
	if len(r.cfg.Refuse) == 0 {
		return "", false
	}
	if modifiers, err := desktop.ParseKeys(action.Modifiers); err == nil && slices.Contains(modifiers, desktop.KeyEnter) {
		if why, refused := r.refusedDefault(); refused {
			return why, true
		}
	}
	switch action.Kind {
	case computeruse.KindType:
		if strings.ContainsAny(r.substitute(action.Text), "\r\n") {
			return r.refusedDefault()
		}
	case computeruse.KindClick:
		return r.refusedAt(at)
	case computeruse.KindMouseDown:
		// A button press acts at the cursor, wherever the action points.
		return r.refusedAt(nil)
	case computeruse.KindDrag:
		if len(action.Path) > 0 {
			start := toFull(action.Path[0])
			return r.refusedAt(&start)
		}
	case computeruse.KindKey, computeruse.KindHoldKey:
		keys, err := desktop.ParseKeys(action.Keys)
		if err != nil || !slices.Contains(keys, desktop.KeyEnter) {
			return "", false
		}
		return r.refusedDefault()
	}
	return "", false
}

// refusedAt reports a refused element at a display point, or at the cursor
// when at is nil.
func (r *run) refusedAt(at *image.Point) (string, bool) {
	point := at
	if point == nil {
		p, err := r.driver.CursorPosition()
		if err != nil {
			return "", false
		}
		point = &p
	}
	e, err := r.elementAt(*point)
	if err != nil {
		return "", false
	}
	return r.refusedElement(e)
}

// refusedDefault reports a refused button in the window in front, which
// Enter may press as the window's default button. It says what sets a
// field or an option instead.
func (r *run) refusedDefault() (string, bool) {
	els, err := r.elementsIfAvailable()
	if err != nil {
		return "", false
	}
	front, err := els.FrontWindow()
	if err != nil {
		return "", false
	}
	elements, err := els.Outline(front, 0)
	if err != nil {
		return "", false
	}
	for _, e := range elements {
		if e.Role != desktop.RoleButton {
			continue
		}
		if n, _, refused := r.refusedName(e); refused {
			return fmt.Sprintf("Return is not pressed: it may press the default button %q, which this step never presses. To set a combo box or pop-up, call choose_option, or open it and click the option; to fill a text field, type without Return; to move on, press Tab.", n.Name), true
		}
	}
	return "", false
}

// latinWord is a word of Latin letters and digits, which a name holds only
// as a whole word, so Save is not found in Unsaved.
var latinWord = regexp.MustCompile(`^[A-Za-z0-9 ]+$`)

// namesWord reports whether name holds word, ignoring case and the width of
// characters: a Latin word as a whole word, any other as written.
func namesWord(name, word string) bool {
	name, word = strings.ToLower(desktop.NormalizeText(name)), strings.ToLower(desktop.NormalizeText(word))
	if word == "" {
		return false
	}
	if !latinWord.MatchString(word) {
		return strings.Contains(name, word)
	}
	return regexp.MustCompile(`(^|[^a-z0-9])` + regexp.QuoteMeta(word) + `($|[^a-z0-9])`).MatchString(name)
}
