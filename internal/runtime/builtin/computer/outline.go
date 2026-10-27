// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

// maxOutlineElements bounds the elements an outline publishes, so a window
// with a huge tree, such as a spreadsheet grid, keeps the output readable.
const maxOutlineElements = 400

// outline publishes the front window's elements as text under name, read
// from the system's accessibility tree with no model and no screenshot, so
// an author learns what an application shows and what a selector or an act
// can name. It reads only the app the step works in, once known: an app a
// person brought forward makes way for it, and another app's window in
// front is never read.
func (r *run) outline(ctx context.Context, index int, name string, timeout time.Duration) error {
	ctx, cancel := r.operationContext(ctx, timeout)
	defer cancel()
	defer r.during(ctx)()
	began := time.Now()
	els, err := r.elementsReader()
	if err != nil {
		return err
	}
	if _, err := r.resumeAfterPerson(ctx, index); err != nil {
		return err
	}
	front, err := els.FrontWindow()
	if err != nil {
		return err
	}
	if r.workApp != "" && front.App != "" && !strings.EqualFold(front.App, r.workApp) {
		return fmt.Errorf("a window of %s is in front, not of %s, the app this step works in, and an outline reads only that app's windows", front.App, r.workApp)
	}
	for _, w := range []desktop.WindowID{r.driver.FocusedWindow(), {App: front.App, Title: front.Name}} {
		if r.driver.Owned(w) {
			return errors.New(r.ownedMessage(w))
		}
	}
	elements, err := els.Outline(front, maxOutlineElements+1)
	if err != nil {
		return err
	}
	mask := r.masker.MaskString
	r.outputs[name] = outlineText(front, elements, mask)
	// The event goes to the timeline directly: report would capture the
	// screen under screenshots: each, and an outline takes no screenshot.
	r.timeline.Operation(agentstep.Report{
		Index: index, Kind: opOutline, Subject: name, Status: agentstep.StatusCompleted, Via: agentstep.ViaExact,
		Detail: fmt.Sprintf("%d elements of %s", min(len(elements), maxOutlineElements), describeWindow(desktop.WindowID{App: mask(front.App), Title: mask(front.Name)})), Duration: time.Since(began),
	})
	return nil
}

// outlineText lists a window and its elements, one per line, each element
// indented under the listed element that contains it. An element that shows
// nothing and that a person does not act on, such as an unnamed group, is
// left out, and its children move up a level; an unnamed field or button
// stays, since a selector names it by the label near it. mask hides the
// declared secrets any text holds.
func outlineText(window desktop.Element, elements []desktop.Element, mask func(string) string) string {
	var b strings.Builder
	b.WriteString(outlineLine(window, mask))
	var parents [][]desktop.PathStep
	for i, e := range elements {
		if i == maxOutlineElements {
			fmt.Fprintf(&b, "(the window has more than %d elements; the rest are not listed)\n", maxOutlineElements)
			break
		}
		for len(parents) > 0 && !within(e.Path, parents[len(parents)-1]) {
			parents = parents[:len(parents)-1]
		}
		if e.Name == "" && e.Value == "" && e.Label == "" && e.ID == "" && !actionableRole(e.Role) {
			continue
		}
		b.WriteString(strings.Repeat("  ", len(parents)+1))
		b.WriteString(outlineLine(e, mask))
		parents = append(parents, e.Path)
	}
	return b.String()
}

// outlineLine describes one element: its role, accessible name, and the
// app, label, value, id, and state it has beside the name. Each text is
// masked before it is quoted, since quoting changes a secret that holds a
// quote or a backslash.
func outlineLine(e desktop.Element, mask func(string) string) string {
	var b strings.Builder
	b.WriteString(e.Role)
	if e.Name != "" {
		fmt.Fprintf(&b, " %q", mask(e.Name))
	}
	if e.App != "" {
		fmt.Fprintf(&b, " app=%q", mask(e.App))
	}
	if e.Label != "" && e.Label != e.Name {
		fmt.Fprintf(&b, " label=%q", mask(e.Label))
	}
	if e.Value != "" && e.Value != e.Name {
		fmt.Fprintf(&b, " value=%q", mask(e.Value))
	}
	if e.ID != "" {
		fmt.Fprintf(&b, " id=%q", mask(e.ID))
	}
	if e.Disabled {
		b.WriteString(" disabled")
	}
	b.WriteByte('\n')
	return b.String()
}

// within reports whether path leads through parent to an element below it.
func within(path, parent []desktop.PathStep) bool {
	return len(path) > len(parent) && slices.Equal(path[:len(parent)], parent)
}
