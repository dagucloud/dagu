// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package diagnostic defines operation-scoped diagnostics.
//
// Diagnostics are transient observations produced by explicit inspection operations.
// They are not DAG model state, DAG-run status state, or a persistence format.
package diagnostic

import (
	"sort"
	"strconv"
	"strings"
)

// Severity describes how strongly a diagnostic should be surfaced.
type Severity string

const (
	SeverityNotice  Severity = "notice"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Kind describes the subsystem or concern that produced a diagnostic.
type Kind string

// Code identifies a specific diagnostic condition.
type Code string

// Location identifies where a diagnostic applies.
type Location struct {
	FilePath  string
	FieldPath string
}

// Diagnostic describes a transient operation diagnostic.
type Diagnostic struct {
	Severity   Severity
	Kind       Kind
	Code       Code
	Message    string
	Location   Location
	Attributes map[string]string
}

// Identity returns the diagnostic identity used for deduplication.
func (d Diagnostic) Identity() string {
	var b strings.Builder
	writeIdentitySegment(&b, string(d.Kind))
	writeIdentitySegment(&b, string(d.Code))
	writeIdentitySegment(&b, d.Location.FilePath)
	writeIdentitySegment(&b, d.Location.FieldPath)

	if len(d.Attributes) > 0 {
		keys := make([]string, 0, len(d.Attributes))
		for key := range d.Attributes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			writeIdentitySegment(&b, key)
			writeIdentitySegment(&b, d.Attributes[key])
		}
	}
	return b.String()
}

func writeIdentitySegment(b *strings.Builder, value string) {
	b.WriteString(strconv.Itoa(len(value)))
	b.WriteByte(':')
	b.WriteString(value)
}

// Sink receives diagnostics.
type Sink interface {
	Report(Diagnostic)
}
