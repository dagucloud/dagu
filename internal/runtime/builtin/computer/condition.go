// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

// condition is the value of expect or when: a statement the model judges
// against the screen, an exact check that reads the front window's
// elements without a model and gives the same answer on every run, or a
// check on the step's data that reads no screen at all.
type condition struct {
	// Statement is judged by the model.
	Statement string `json:"statement,omitempty"`
	// Text holds when the name or value of a visible element of the front
	// window contains it.
	Text string `json:"text,omitempty"`
	// Element holds when the selector matches exactly one visible element
	// of the front window.
	Element *desktop.Selector `json:"element,omitempty"`
	// Window holds when the front window's title contains it. It takes *
	// for any run of characters.
	Window string `json:"window,omitempty"`
	// Value is data the step was given, such as a parameter or a
	// variable: the check holds when it equals Equals, or, with NotEquals
	// instead, when it differs from that. It reads no screen and is decided
	// at once.
	Value     *string `json:"value,omitempty"`
	Equals    *string `json:"equals,omitempty"`
	NotEquals *string `json:"not_equals,omitempty"`
	// Within is how long the check keeps looking before it is taken as
	// false, such as 30s.
	Within string `json:"within,omitempty"`
}

// UnmarshalJSON accepts a statement string or an object.
func (c *condition) UnmarshalJSON(data []byte) error {
	var statement string
	if err := json.Unmarshal(data, &statement); err == nil {
		c.Statement = statement
		return nil
	}
	type plain condition
	return json.Unmarshal(data, (*plain)(c))
}

// judged reports whether the model evaluates the condition.
func (c condition) judged() bool {
	return c.Statement != ""
}

// onValue reports a check on the step's data rather than the screen.
func (c condition) onValue() bool {
	return c.Value != nil
}

func (c condition) validate() error {
	set := 0
	for _, value := range []string{c.Statement, c.Text, c.Window} {
		if value != "" {
			set++
		}
	}
	if c.Element != nil {
		set++
	}
	if c.Value != nil {
		set++
	}
	if set != 1 {
		return errors.New("a condition is a statement, or an object with exactly one of statement, text, element, window, or value")
	}
	if c.onValue() {
		if (c.Equals == nil) == (c.NotEquals == nil) {
			return errors.New("value takes exactly one of equals or not_equals")
		}
		if c.Within != "" {
			return errors.New("value is decided at once and takes no within")
		}
		return nil
	}
	if c.Equals != nil || c.NotEquals != nil {
		return errors.New("equals and not_equals go with value")
	}
	if c.Element != nil {
		if err := c.Element.Validate(); err != nil {
			return fmt.Errorf("element: %w", err)
		}
	}
	return agentstep.ValidateDuration("within", c.Within)
}

// window returns how long the check keeps looking, or fallback when within
// is unset.
func (c condition) window(fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(c.Within); err == nil && d > 0 {
		return d
	}
	return fallback
}

// String describes the condition for logs and the timeline.
func (c condition) String() string {
	switch {
	case c.onValue() && c.Equals != nil:
		return fmt.Sprintf("value %q equals %q", *c.Value, *c.Equals)
	case c.onValue():
		return fmt.Sprintf("value %q differs from %q", *c.Value, *c.NotEquals)
	case c.Text != "":
		return fmt.Sprintf("text %q", c.Text)
	case c.Element != nil:
		return "element " + c.Element.String()
	case c.Window != "":
		return fmt.Sprintf("window %q", c.Window)
	default:
		return c.Statement
	}
}

// via is how the condition is decided.
func (c condition) via() string {
	if c.judged() {
		return agentstep.ViaModel
	}
	return agentstep.ViaExact
}

// references returns the %name% placeholders an exact check uses. A
// statement's placeholders are the model's to read.
func (c condition) references() []string {
	if c.judged() {
		return nil
	}
	texts := []string{c.Text, c.Window}
	if c.Element != nil {
		texts = append(texts, selectorTexts(*c.Element)...)
	}
	for _, text := range []*string{c.Value, c.Equals, c.NotEquals} {
		if text != nil {
			texts = append(texts, *text)
		}
	}
	var names []string
	for _, text := range texts {
		names = append(names, agentstep.VariableReferences(text)...)
	}
	return names
}

// substituted returns the condition with its placeholders replaced. A
// statement is left for the model to read as written.
func (c condition) substituted(values map[string]string) condition {
	if c.judged() {
		return c
	}
	out := c
	out.Text = agentstep.SubstituteVariables(c.Text, values)
	out.Window = agentstep.SubstituteVariables(c.Window, values)
	if c.Element != nil {
		sel := substituteSelector(*c.Element, values)
		out.Element = &sel
	}
	out.Value = substitutedText(c.Value, values)
	out.Equals = substitutedText(c.Equals, values)
	out.NotEquals = substitutedText(c.NotEquals, values)
	return out
}

// substitutedText is text with its placeholders replaced, or nil for none.
func substitutedText(text *string, values map[string]string) *string {
	if text == nil {
		return nil
	}
	return new(agentstep.SubstituteVariables(*text, values))
}

// valueHolds decides a check on the step's data, comparing the texts as an
// element's name is compared, and says why.
func (c condition) valueHolds() (bool, string) {
	value := desktop.NormalizeText(*c.Value)
	if c.Equals != nil {
		if value == desktop.NormalizeText(*c.Equals) {
			return true, fmt.Sprintf("%q is %q", *c.Value, *c.Equals)
		}
		return false, fmt.Sprintf("%q is not %q", *c.Value, *c.Equals)
	}
	if value != desktop.NormalizeText(*c.NotEquals) {
		return true, fmt.Sprintf("%q is not %q", *c.Value, *c.NotEquals)
	}
	return false, fmt.Sprintf("%q is %q", *c.Value, *c.NotEquals)
}

// selectorTexts returns the texts of a selector that may hold placeholders:
// the name, the window, the label, and those of its container.
func selectorTexts(sel desktop.Selector) []string {
	texts := []string{sel.Name, sel.Window}
	if sel.Near != nil {
		texts = append(texts, sel.Near.Label)
	}
	if sel.In != nil {
		texts = append(texts, selectorTexts(*sel.In)...)
	}
	return texts
}

// substituteSelector returns a copy of the selector with its placeholders
// replaced. The id and the app are identifiers, not text, and stay as
// written.
func substituteSelector(sel desktop.Selector, values map[string]string) desktop.Selector {
	out := sel
	out.Name = agentstep.SubstituteVariables(sel.Name, values)
	out.Window = agentstep.SubstituteVariables(sel.Window, values)
	if sel.Near != nil {
		near := *sel.Near
		near.Label = agentstep.SubstituteVariables(near.Label, values)
		out.Near = &near
	}
	if sel.In != nil {
		in := substituteSelector(*sel.In, values)
		out.In = &in
	}
	if sel.Nth != nil {
		nth := *sel.Nth
		out.Nth = &nth
	}
	return out
}
