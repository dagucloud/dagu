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

// refusedElement reports an element the step never presses: one whose name
// holds a word with.refuse lists, such as 保存 or Save in a check that must
// change nothing. It says why, for the model or the replay that tried.
func (r *run) refusedElement(e desktop.Element) (string, bool) {
	for _, word := range r.cfg.Refuse {
		if namesWord(e.Name, word) {
			return fmt.Sprintf("%s %q is not pressed in this step, which refuses %q; leave it as it is", e.Role, e.Name, word), true
		}
	}
	return "", false
}

// refusedAction reports an action that would press an element the step
// never presses: a click on one, or Enter while one is in the window in
// front, where Enter may press it as the window's default button. An action
// whose element the host cannot read goes on.
func (r *run) refusedAction(action computeruse.Action, at *image.Point) (string, bool) {
	if len(r.cfg.Refuse) == 0 {
		return "", false
	}
	switch action.Kind {
	case computeruse.KindClick, computeruse.KindMouseDown:
		point := at
		if point == nil {
			p, err := r.driver.CursorPosition()
			if err != nil {
				return "", false
			}
			point = &p
		}
		if e, err := r.elementAt(*point); err == nil {
			return r.refusedElement(e)
		}
	case computeruse.KindKey:
		keys, err := desktop.ParseKeys(action.Keys)
		if err != nil || !slices.Contains(keys, desktop.KeyEnter) {
			return "", false
		}
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
