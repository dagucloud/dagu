// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Condition contains a precondition expression and optional expected value.
// With Expected empty, Condition is evaluated as a command check.
// With Expected set, Condition is evaluated as a runtime value and compared
// against Expected.
type Condition struct {
	mu sync.RWMutex

	Condition    string // Condition to evaluate
	Expected     string // Expected value
	Negate       bool   // Negate the condition result (run when condition does NOT match)
	errorMessage string // Error message if the condition is not met
}

type conditionJSON struct {
	Condition    string `json:"condition,omitempty"`
	Expected     string `json:"expected,omitempty"`
	Negate       bool   `json:"negate,omitempty"`
	ErrorMessage string `json:"error,omitempty"`
}

func (c *Condition) MarshalJSON() ([]byte, error) { return json.Marshal(c.snapshot()) }

func (c *Condition) UnmarshalJSON(data []byte) error {
	var decoded conditionJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.Condition = decoded.Condition
	c.Expected = decoded.Expected
	c.Negate = decoded.Negate
	c.errorMessage = decoded.ErrorMessage
	return nil
}

func (c *Condition) Validate() error {
	if c.Condition == "" {
		return fmt.Errorf("condition is required")
	}
	return nil
}

func (c *Condition) SetErrorMessage(msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errorMessage = msg
}

func (c *Condition) GetErrorMessage() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.errorMessage
}

func (c *Condition) snapshot() conditionJSON {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return conditionJSON{
		Condition:    c.Condition,
		Expected:     c.Expected,
		Negate:       c.Negate,
		ErrorMessage: c.errorMessage,
	}
}
