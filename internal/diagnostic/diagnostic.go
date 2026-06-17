// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package diagnostic

import "sync"

// Level describes how strongly a diagnostic should be surfaced.
type Level string

const (
	LevelError   Level = "error"
	LevelWarning Level = "warning"
	LevelNotice  Level = "notice"
)

const CodeValueReferenceUnresolved = "value_reference_unresolved"

// Diagnostic is a structured, user-visible diagnostic.
type Diagnostic struct {
	Level   Level  `json:"level"`
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Token   string `json:"token,omitempty"`
	Message string `json:"message"`
}

// Sink receives diagnostics.
type Sink interface {
	AddDiagnostic(Diagnostic)
}

// Collector stores unique diagnostics in insertion order.
type Collector struct {
	mu          sync.Mutex
	diagnostics []Diagnostic
	seen        map[string]struct{}
}

func (c *Collector) AddDiagnostic(d Diagnostic) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if d.Level == "" || d.Code == "" || d.Message == "" {
		return
	}
	if c.seen == nil {
		c.seen = make(map[string]struct{})
	}
	key := Key(d)
	if _, ok := c.seen[key]; ok {
		return
	}
	c.seen[key] = struct{}{}
	c.diagnostics = append(c.diagnostics, d)
}

func (c *Collector) Diagnostics() []Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]Diagnostic(nil), c.diagnostics...)
}

// AppendUnique appends diagnostics while preserving the first occurrence.
func AppendUnique(existing []Diagnostic, next ...Diagnostic) []Diagnostic {
	if len(next) == 0 {
		return existing
	}
	out := append([]Diagnostic(nil), existing...)
	seen := make(map[string]struct{}, len(out)+len(next))
	for _, d := range out {
		seen[Key(d)] = struct{}{}
	}
	for _, d := range next {
		if d.Level == "" || d.Code == "" || d.Message == "" {
			continue
		}
		key := Key(d)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, d)
	}
	return out
}

// Key returns the diagnostic identity used for deduplication.
func Key(d Diagnostic) string {
	return string(d.Level) + "\x00" + d.Code + "\x00" + d.Field + "\x00" + d.Token
}
