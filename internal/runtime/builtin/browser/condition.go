// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// condition is the value of expect or when: a statement the model judges,
// or a fixed page check that gives the same answer on every run.
type condition struct {
	// Statement is judged by the model.
	Statement string `json:"-"`
	// Text holds when the page's visible text contains it.
	Text string `json:"text,omitempty"`
	// Selector holds when a CSS selector matches a visible element.
	Selector string `json:"selector,omitempty"`
	// URL holds when the current page URL contains it.
	URL string `json:"url,omitempty"`
	// Within is how long a fixed check keeps reading the page before it
	// gives up, such as 10s.
	Within string `json:"within,omitempty"`
}

// UnmarshalJSON accepts a statement string or a fixed check object.
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
	for _, value := range []string{c.Statement, c.Text, c.Selector, c.URL} {
		if value != "" {
			set++
		}
	}
	if set != 1 {
		return errors.New("a condition is a statement, or an object with exactly one of text, selector, or url")
	}
	return validateDuration("within", c.Within)
}

// window returns how long a fixed check keeps reading the page, or fallback
// when within is unset.
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
	case c.Selector != "":
		return fmt.Sprintf("selector %q", c.Selector)
	case c.URL != "":
		return fmt.Sprintf("url %q", c.URL)
	default:
		return c.Statement
	}
}
