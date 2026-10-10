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
// against the screen, or an exact check that reads the front window's
// elements without a model and gives the same answer on every run.
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
	if set != 1 {
		return errors.New("a condition is a statement, or an object with exactly one of statement, text, element, or window")
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
	return out
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
