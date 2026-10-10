// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"slices"
	"strings"
)

// Sides a label can be on, relative to the element it names.
const (
	SideRight = "right"
	SideBelow = "below"
	SideLeft  = "left"
	SideAbove = "above"
)

var sides = []string{SideRight, SideBelow, SideLeft, SideAbove}

// nearSlack is how far, in pixels, an element may overlap its label's
// edge and still count as beside it.
const nearSlack = 4

// Selector names an element the way a condition, a pinned step, or an
// extract field does. Role is required, with at least one of Name, ID, or
// Near. Name and Window take * for any run of characters. A %name%
// placeholder in a name is matched as written; whoever runs a workflow
// substitutes it first.
type Selector struct {
	// App is the process image name without its extension, compared
	// ignoring case.
	App string `json:"app,omitempty"`
	// Window is text the window title contains.
	Window string `json:"window,omitempty"`
	Role   string `json:"role"`
	// Name is the accessible name, exact.
	Name string `json:"name,omitempty"`
	// ID is the identifier the application gives the element.
	ID string `json:"id,omitempty"`
	// In narrows the search to the descendants of one container.
	In *Selector `json:"in,omitempty"`
	// Near keeps the element labelled by a label.
	Near *Near `json:"near,omitempty"`
	// Nth picks one of several equal matches, in reading order, from 0.
	Nth *int `json:"nth,omitempty"`
}

// Near names a label an element sits next to.
type Near struct {
	Label string `json:"label"`
	Side  string `json:"side"`
}

// ErrNotFound reports a selector that matches no element.
var ErrNotFound = errors.New("no element matches")

// ErrAmbiguous reports a selector that matches several elements. The error
// returned is an *AmbiguousError that wraps it.
var ErrAmbiguous = errors.New("several elements match")

// AmbiguousError says how many elements a selector matched.
type AmbiguousError struct {
	Selector Selector
	Count    int
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%d elements match %s; add in, near, or nth", e.Count, e.Selector)
}

func (e *AmbiguousError) Is(target error) bool { return target == ErrAmbiguous }

// ParseSelector reads a selector from JSON and validates it. An unknown
// key is an error, so a misspelt field does not silently widen the match.
func ParseSelector(data []byte) (Selector, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var s Selector
	if err := decoder.Decode(&s); err != nil {
		return Selector{}, fmt.Errorf("selector: %w", err)
	}
	if err := s.Validate(); err != nil {
		return Selector{}, err
	}
	return s, nil
}

// Validate checks that the selector names an element.
func (s Selector) Validate() error {
	return s.validate("selector")
}

func (s Selector) validate(what string) error {
	switch {
	case s.Role == "":
		return fmt.Errorf("%s: role is required", what)
	case !slices.Contains(Roles, s.Role):
		return fmt.Errorf("%s: unknown role %q; use one of %s", what, s.Role, strings.Join(Roles, ", "))
	case s.Name == "" && s.ID == "" && s.Near == nil:
		return fmt.Errorf("%s: set name, id, or near", what)
	case s.Near != nil && s.Near.Label == "":
		return fmt.Errorf("%s: near.label is required", what)
	case s.Near != nil && !slices.Contains(sides, s.Near.Side):
		return fmt.Errorf("%s: near.side must be one of %s", what, strings.Join(sides, ", "))
	case s.Nth != nil && *s.Nth < 0:
		return fmt.Errorf("%s: nth must be 0 or more", what)
	case s.In != nil:
		return s.In.validate(what + ".in")
	}
	return nil
}

// String returns the selector as compact JSON, which is how it is written
// and quoted.
func (s Selector) String() string {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Sprintf("%+v", struct{ Selector }{s})
	}
	return string(data)
}

// Match returns the elements sel selects among a window's elements, in
// reading order: one when the selector is good, none on a miss, several
// when it is ambiguous. An error reports a container (in) that misses or
// is ambiguous, naming it.
//
// The search runs in a fixed order: in narrows, an id wins when it is
// unique there, otherwise role and name select the candidates, near keeps
// the one labelled by the label, and nth comes last.
func Match(window Element, elements []Element, sel Selector) ([]Element, error) {
	if sel.App != "" && !strings.EqualFold(sel.App, window.App) {
		return nil, nil
	}
	if sel.Window != "" && !WindowMatches(window.Name, sel.Window) {
		return nil, nil
	}
	scope := elements
	if sel.In != nil {
		containers, err := Match(window, elements, *sel.In)
		if err != nil {
			return nil, err
		}
		container, err := One(containers, *sel.In)
		if err != nil {
			return nil, fmt.Errorf("container %s: %w", sel.In, err)
		}
		scope = within(elements, container)
	}
	candidates := sel.candidates(scope)
	if sel.Near != nil {
		candidates = near(candidates, scope, *sel.Near)
	}
	candidates = readingOrder(candidates)
	if sel.Nth != nil {
		if *sel.Nth >= len(candidates) {
			return nil, nil
		}
		candidates = candidates[*sel.Nth : *sel.Nth+1]
	}
	return candidates, nil
}

// One returns the single match, ErrNotFound, or an *AmbiguousError.
func One(matches []Element, sel Selector) (Element, error) {
	switch len(matches) {
	case 0:
		return Element{}, fmt.Errorf("%w %s", ErrNotFound, sel)
	case 1:
		return matches[0], nil
	default:
		return Element{}, &AmbiguousError{Selector: sel, Count: len(matches)}
	}
}

// candidates selects by id, or by role and name. An id that matches
// nothing is wrong, not a reason to fall back to the name; an id shared by
// several elements is narrowed by the name when one is given.
func (s Selector) candidates(scope []Element) []Element {
	var out []Element
	if s.ID != "" {
		for _, e := range scope {
			if e.Role == s.Role && e.ID == s.ID {
				out = append(out, e)
			}
		}
		if len(out) <= 1 || s.Name == "" {
			return out
		}
		return slices.DeleteFunc(out, func(e Element) bool { return !globMatch(s.Name, e.Name) })
	}
	for _, e := range scope {
		if e.Role == s.Role && (s.Name == "" || globMatch(s.Name, e.Name)) {
			out = append(out, e)
		}
	}
	return out
}

// within returns the elements below container in the tree.
func within(elements []Element, container Element) []Element {
	var out []Element
	for _, e := range elements {
		if len(e.Path) > len(container.Path) && slices.Equal(e.Path[:len(container.Path)], container.Path) {
			out = append(out, e)
		}
	}
	return out
}

// near keeps the candidates labelled by the label. The relation the
// application declares comes first. Without one, for each text element
// with the label's name, the nearest candidate on that side of it that
// overlaps it in the other direction is kept, so two labels keep two.
func near(candidates, scope []Element, n Near) []Element {
	var byRelation []Element
	for _, e := range candidates {
		if e.Label == n.Label {
			byRelation = append(byRelation, e)
		}
	}
	if len(byRelation) > 0 {
		return byRelation
	}
	var kept []Element
	for _, label := range scope {
		if label.Role != RoleText || label.Name != n.Label {
			continue
		}
		var best Element
		found := false
		for _, e := range candidates {
			if !beside(e.Bounds, label.Bounds, n.Side) {
				continue
			}
			if !found || gap(e.Bounds, label.Bounds, n.Side) < gap(best.Bounds, label.Bounds, n.Side) {
				best, found = e, true
			}
		}
		if found && !slices.ContainsFunc(kept, func(k Element) bool { return sameElement(k, best) }) {
			kept = append(kept, best)
		}
	}
	return kept
}

// beside reports whether e is on the given side of label and overlaps it
// in the other direction.
func beside(e, label image.Rectangle, side string) bool {
	overlapsY := e.Min.Y < label.Max.Y && label.Min.Y < e.Max.Y
	overlapsX := e.Min.X < label.Max.X && label.Min.X < e.Max.X
	switch side {
	case SideRight:
		return e.Min.X >= label.Max.X-nearSlack && overlapsY
	case SideLeft:
		return e.Max.X <= label.Min.X+nearSlack && overlapsY
	case SideBelow:
		return e.Min.Y >= label.Max.Y-nearSlack && overlapsX
	case SideAbove:
		return e.Max.Y <= label.Min.Y+nearSlack && overlapsX
	}
	return false
}

// gap is the distance between e and label on the given side.
func gap(e, label image.Rectangle, side string) int {
	switch side {
	case SideRight:
		return e.Min.X - label.Max.X
	case SideLeft:
		return label.Min.X - e.Max.X
	case SideBelow:
		return e.Min.Y - label.Max.Y
	case SideAbove:
		return label.Min.Y - e.Max.Y
	}
	return 0
}

// sameElement reports whether two elements are the same one on screen.
func sameElement(a, b Element) bool {
	return a.Bounds == b.Bounds && slices.Equal(a.Path, b.Path)
}

// readingOrder sorts elements into rows, top to bottom, and left to right
// within a row. An element starts a new row when its top is below the
// vertical middle of the row's first element. Systems list children in
// the order the application created them, which is not what a person
// calls first.
func readingOrder(elements []Element) []Element {
	sorted := slices.Clone(elements)
	slices.SortStableFunc(sorted, func(a, b Element) int { return cmp.Compare(a.Bounds.Min.Y, b.Bounds.Min.Y) })
	out := make([]Element, 0, len(sorted))
	for i := 0; i < len(sorted); {
		first := sorted[i].Bounds
		middle := first.Min.Y + first.Dy()/2
		j := i + 1
		for j < len(sorted) && sorted[j].Bounds.Min.Y < middle {
			j++
		}
		row := sorted[i:j]
		slices.SortStableFunc(row, func(a, b Element) int { return cmp.Compare(a.Bounds.Min.X, b.Bounds.Min.X) })
		out = append(out, row...)
		i = j
	}
	return out
}
