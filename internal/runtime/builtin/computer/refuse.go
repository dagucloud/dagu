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
// or the name of the button, link, or other element a person presses that
// holds it, has a word with.refuse lists, such as 保存 or Save in a check
// that must change nothing. It says why, for the model or the replay that
// tried.
func (r *run) refusedElement(e desktop.Element) (string, bool) {
	names := []desktop.PathStep{{Role: e.Role, Name: e.Name}}
	for i := len(e.Path) - 2; i >= 0; i-- {
		if actionableRole(e.Path[i].Role) {
			names = append(names, e.Path[i])
		}
	}
	for _, word := range r.cfg.Refuse {
		for _, n := range names {
			if namesWord(n.Name, word) {
				return fmt.Sprintf("%s %q is not pressed in this step, which refuses %q; leave it as it is", n.Role, n.Name, word), true
			}
		}
	}
	return "", false
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
// Enter may press as the window's default button.
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
		if why, refused := r.refusedElement(e); refused {
			return "Enter may press " + why, true
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
