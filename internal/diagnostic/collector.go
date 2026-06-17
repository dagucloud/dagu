// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package diagnostic

import (
	"maps"
	"sync"
)

// Collector stores unique diagnostics in insertion order.
type Collector struct {
	mu          sync.Mutex
	diagnostics []Diagnostic
	seen        map[string]struct{}
}

// Report records d unless an equivalent diagnostic has already been recorded.
func (c *Collector) Report(d Diagnostic) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if d.Severity == "" || d.Kind == "" || d.Code == "" || d.Message == "" {
		return
	}
	if c.seen == nil {
		c.seen = make(map[string]struct{})
	}
	key := d.Identity()
	if _, ok := c.seen[key]; ok {
		return
	}
	c.seen[key] = struct{}{}
	c.diagnostics = append(c.diagnostics, cloneDiagnostic(d))
}

// Diagnostics returns the recorded diagnostics in insertion order.
func (c *Collector) Diagnostics() []Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]Diagnostic, 0, len(c.diagnostics))
	for _, d := range c.diagnostics {
		out = append(out, cloneDiagnostic(d))
	}
	return out
}

func cloneDiagnostic(d Diagnostic) Diagnostic {
	if len(d.Attributes) == 0 {
		return d
	}
	attrs := make(map[string]string, len(d.Attributes))
	maps.Copy(attrs, d.Attributes)
	d.Attributes = attrs
	return d
}
