// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package diagnostic defines operation-scoped diagnostics.
//
// Diagnostics are transient observations produced while loading, inspecting, or
// running a DAG. They are not DAG model state, DAG-run status state, or a
// persistence format.
package diagnostic

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
	Severity    Severity
	Kind        Kind
	Code        Code
	Message     string
	Location    Location
	Attributes  map[string]string
	Fingerprint string
}

// Identity returns the diagnostic identity used for deduplication.
func (d Diagnostic) Identity() string {
	if d.Fingerprint != "" {
		return d.Fingerprint
	}
	return string(d.Kind) + "\x00" + string(d.Code) + "\x00" + d.Location.FilePath + "\x00" + d.Location.FieldPath
}

// Sink receives diagnostics.
type Sink interface {
	Report(Diagnostic)
}
