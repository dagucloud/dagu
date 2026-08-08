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
	Negate    bool   `json:"negate,omitempty"`    // Negate the condition result (run when condition does NOT match)
}

func (c *Condition) Validate() error {
	hasCondition := strings.TrimSpace(c.Condition) != ""
	hasEval := strings.TrimSpace(c.Eval) != ""
	switch {
	case hasCondition && hasEval:
		return fmt.Errorf("only one of condition or eval is allowed")
	case !hasCondition && !hasEval:
		return fmt.Errorf("condition or eval is required")
	case hasEval && strings.TrimSpace(c.Expected) == "":
		return fmt.Errorf("expected is required when eval is set")
	}
	return nil
}
