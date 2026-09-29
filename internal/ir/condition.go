// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"fmt"
	"strings"
)

// Condition describes a precondition command check or value match.
type Condition struct {
	Condition string `json:"condition,omitempty"` // Condition to evaluate
	Eval      string `json:"eval,omitempty"`      // Dynamic value to evaluate
	Expected  string `json:"expected,omitempty"`  // Expected value
	// ExpectedAny lists alternative expected values, of which any one matching
	// satisfies the condition. When set, it is used instead of Expected.
	ExpectedAny []string `json:"expectedAny,omitempty"`
	Negate      bool     `json:"negate,omitempty"` // Negate the condition result (run when condition does NOT match)
}

// ExpectedPatterns returns the expected values a value match accepts, or nil
// for a command check.
func (c *Condition) ExpectedPatterns() []string {
	if len(c.ExpectedAny) > 0 {
		return c.ExpectedAny
	}
	if c.Expected != "" {
		return []string{c.Expected}
	}
	return nil
}

func (c *Condition) Validate() error {
	hasCondition := strings.TrimSpace(c.Condition) != ""
	hasEval := strings.TrimSpace(c.Eval) != ""
	switch {
	case hasCondition && hasEval:
		return fmt.Errorf("only one of condition or eval is allowed")
	case !hasCondition && !hasEval:
		return fmt.Errorf("condition or eval is required")
	case hasEval && strings.TrimSpace(c.Expected) == "" && len(c.ExpectedAny) == 0:
		return fmt.Errorf("expected is required when eval is set")
	}
	return nil
}
