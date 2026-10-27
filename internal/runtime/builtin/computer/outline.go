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
// can name.
func (r *run) outline(ctx context.Context, index int, name string, timeout time.Duration) error {
	ctx, cancel := r.operationContext(ctx, timeout)
	defer cancel()
	defer r.during(ctx)()
	began := time.Now()
	els, err := r.elementsReader()
	if err != nil {
		return err
	}
	front, err := els.FrontWindow()
	if err != nil {
		return err
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
	r.outputs[name] = r.masker.MaskString(outlineText(front, elements))
	r.report(ctx, agentstep.Report{
		Index: index, Kind: opOutline, Subject: name, Status: agentstep.StatusCompleted, Via: agentstep.ViaExact,
		Detail: fmt.Sprintf("%d elements of %s", min(len(elements), maxOutlineElements), describeWindow(desktop.WindowID{App: front.App, Title: front.Name})), Duration: time.Since(began),
	})
	return nil
}

// outlineText lists a window and its elements, one per line, each element
// indented under the listed element that contains it. An element that shows
// nothing and that no selector can name, such as an unnamed group, is left
// out, and its children move up a level.
func outlineText(window desktop.Element, elements []desktop.Element) string {
	var b strings.Builder
	b.WriteString(outlineLine(window))
	var parents [][]desktop.PathStep
	for i, e := range elements {
		if i == maxOutlineElements {
			fmt.Fprintf(&b, "(the window has more than %d elements; the rest are not listed)\n", maxOutlineElements)
			break
		}
		for len(parents) > 0 && !within(e.Path, parents[len(parents)-1]) {
			parents = parents[:len(parents)-1]
		}
		if e.Name == "" && e.Value == "" && e.Label == "" && e.ID == "" {
			continue
		}
		b.WriteString(strings.Repeat("  ", len(parents)+1))
		b.WriteString(outlineLine(e))
		parents = append(parents, e.Path)
	}
	return b.String()
}

// outlineLine describes one element: its role, accessible name, and the
// app, label, value, id, and state it has beside the name.
func outlineLine(e desktop.Element) string {
	var b strings.Builder
	b.WriteString(e.Role)
	if e.Name != "" {
		fmt.Fprintf(&b, " %q", e.Name)
	}
	if e.App != "" {
		fmt.Fprintf(&b, " app=%q", e.App)
	}
	if e.Label != "" && e.Label != e.Name {
		fmt.Fprintf(&b, " label=%q", e.Label)
	}
	if e.Value != "" && e.Value != e.Name {
		fmt.Fprintf(&b, " value=%q", e.Value)
	}
	if e.ID != "" {
		fmt.Fprintf(&b, " id=%q", e.ID)
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
