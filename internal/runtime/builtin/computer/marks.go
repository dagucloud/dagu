// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"cmp"
	"fmt"
	"image"

	"github.com/dagucloud/dagu/v2/internal/desktop"
)

// patternMarks name, on a mark, what an element performing a pattern does.
var patternMarks = map[desktop.Pattern]string{
	desktop.PatternInvoke: "press", desktop.PatternToggle: "toggle", desktop.PatternSelect: "select",
	desktop.PatternExpand: "open", desktop.PatternSetValue: "set value",
}

// showMarks has the desktop show a person where each of the step's actions
// lands, labelled with the step's name, unless with.marks is false. A
// remote session shows none unless with.marks is true: nobody at the
// console watches it. A host that cannot draw marks runs the step without
// them.
func (r *run) showMarks() {
	show := r.exec.onConsole == nil || r.exec.onConsole()
	if r.cfg.Marks != nil {
		show = *r.cfg.Marks
	}
	if !show {
		return
	}
	marks, err := r.exec.openMarks()
	if err != nil {
		_, _ = fmt.Fprintf(r.timeline.Log, "warning: show where the step acts: %s\n", r.masker.MaskString(err.Error()))
		return
	}
	r.driver.ShowMarks(marks, r.stepName)
}

// markedElements marks each element the step acts on through the reader,
// just before it acts: one taking the keyboard focus, and one performing a
// pattern itself, which moves no pointer a person could follow.
type markedElements struct {
	desktop.Elements
	mark func(target image.Rectangle, action string)
}

func (m markedElements) Focus(target desktop.Element) error {
	m.mark(target.Bounds, "focus")
	return m.Elements.Focus(target)
}

func (m markedElements) Apply(target desktop.Element, pattern desktop.Pattern, value string) error {
	m.mark(target.Bounds, cmp.Or(patternMarks[pattern], string(pattern)))
	return m.Elements.Apply(target, pattern, value)
}
