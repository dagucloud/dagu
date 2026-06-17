// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Level describes how strongly a value-resolution diagnostic should be surfaced.
type Level string

const (
	LevelNotice Level = "notice"
)

const CodeValueReferenceUnresolved = "value_reference_unresolved"

// Diagnostic describes a transient value-resolution diagnostic.
type Diagnostic struct {
	Level   Level  `json:"level"`
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Token   string `json:"token,omitempty"`
	Message string `json:"message"`
}

// Sink receives value-resolution diagnostics.
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
	key := diagnosticKey(d)
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

func diagnosticKey(d Diagnostic) string {
	return string(d.Level) + "\x00" + d.Code + "\x00" + d.Field + "\x00" + d.Token
}

type diagnosticContextKey struct{}

type diagnosticContext struct {
	sink Sink
}

// WithDiagnosticSink returns a context that records value-resolution diagnostics.
func WithDiagnosticSink(ctx context.Context, sink Sink) context.Context {
	if sink == nil {
		return ctx
	}
	current, _ := ctx.Value(diagnosticContextKey{}).(diagnosticContext)
	current.sink = sink
	return context.WithValue(ctx, diagnosticContextKey{}, current)
}

func addUnresolvedReferenceDiagnostic(ctx context.Context, field, token string, err error) {
	diagCtx, ok := ctx.Value(diagnosticContextKey{}).(diagnosticContext)
	if !ok || diagCtx.sink == nil {
		return
	}
	refName := token
	if strings.HasPrefix(token, "${") && strings.HasSuffix(token, "}") {
		refName = token[2 : len(token)-1]
	}
	message := fmt.Sprintf("%s was left unchanged because %s had no value when %s was evaluated.", token, refName, field)
	if field == "" {
		message = fmt.Sprintf("%s was left unchanged because %s had no value when the field was evaluated.", token, refName)
	}
	if err != nil {
		message += " " + err.Error() + "."
	}
	diagCtx.sink.AddDiagnostic(Diagnostic{
		Level:   LevelNotice,
		Code:    CodeValueReferenceUnresolved,
		Field:   field,
		Token:   token,
		Message: message,
	})
}
