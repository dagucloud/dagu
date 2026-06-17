// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"fmt"
	"strings"
)

// DiagnosticKindValueResolution identifies diagnostics produced by value resolution.
const DiagnosticKindValueResolution = "value_resolution"

// CodeValueReferenceUnresolved identifies a supported reference left unresolved.
const CodeValueReferenceUnresolved = "value_reference_unresolved"

// Diagnostic describes a passive value-resolution diagnostic.
type Diagnostic struct {
	Message   string
	FieldPath string
	Token     string
}

// DiagnosticSink receives passive value-resolution diagnostics.
type DiagnosticSink interface {
	Report(Diagnostic)
}

// DiagnosticCollector stores unique diagnostics in insertion order.
type DiagnosticCollector struct {
	diagnostics []Diagnostic
	seen        map[diagnosticKey]struct{}
}

type diagnosticKey struct {
	fieldPath string
	token     string
}

// Report records d unless the same field has already reported the same token.
func (c *DiagnosticCollector) Report(d Diagnostic) {
	if c.seen == nil {
		c.seen = make(map[diagnosticKey]struct{})
	}
	key := diagnosticKey{fieldPath: d.FieldPath, token: d.Token}
	if _, ok := c.seen[key]; ok {
		return
	}
	c.seen[key] = struct{}{}
	c.diagnostics = append(c.diagnostics, d)
}

// Diagnostics returns recorded diagnostics in insertion order.
func (c *DiagnosticCollector) Diagnostics() []Diagnostic {
	out := make([]Diagnostic, len(c.diagnostics))
	copy(out, c.diagnostics)
	return out
}

func addUnresolvedReferenceDiagnostic(sink DiagnosticSink, field, token string, err error) {
	if sink == nil {
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
	sink.Report(Diagnostic{
		Message:   message,
		FieldPath: field,
		Token:     token,
	})
}
