// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint

import (
	"errors"
	"fmt"
)

// Errors of the store and of the service.
var (
	// ErrInvalid marks a value that breaks a rule of Spec 093. The message names the field.
	ErrInvalid = errors.New("invalid AI endpoint")
	// ErrAlreadyExists means that the name is in use in the scope.
	ErrAlreadyExists = errors.New("AI endpoint already exists")
	// ErrNotFound means that the endpoint does not exist in the scope.
	ErrNotFound = errors.New("AI endpoint not found")
)

// Reason is a name of the table of failure kinds of Spec 094, "Failure kinds".
// This package gives the reasons that concern an endpoint.
type Reason string

// The reasons that concern an endpoint.
const (
	ReasonUnknown          Reason = "endpoint_unknown"
	ReasonForbidden        Reason = "endpoint_forbidden"
	ReasonProtocolMismatch Reason = "endpoint_protocol_mismatch"
	ReasonKeyMissing       Reason = "endpoint_key_missing"
	ReasonUnavailable      Reason = "endpoint_unavailable"
	// ReasonModelUnknown is the reason of a step whose model is not in the models
	// list of its endpoint.
	ReasonModelUnknown Reason = "model_unknown"
	// ReasonMCPUnknown and ReasonMCPForbidden are the reasons of a MCP server entry.
	ReasonMCPUnknown   Reason = "mcp_server_unknown"
	ReasonMCPForbidden Reason = "mcp_server_forbidden"
)

// Error is a failure of the use of an endpoint or of a MCP server entry. It has
// the reason of the table of failure kinds and the name of the target. The text
// never has a key.
type Error struct {
	Reason Reason
	// Name is the name of the target that the step names.
	Name string
	// Kind names the kind of the target in the text: "MCP server" for a MCP server
	// entry. It is "endpoint" when empty.
	Kind string
	// Detail tells the cause in a short sentence.
	Detail string
	// Err is the cause that a caller can inspect, or nil.
	Err error
}

func (e *Error) Error() string {
	kind := e.Kind
	if kind == "" {
		kind = "endpoint"
	}
	text := fmt.Sprintf("%s %q: %s", kind, e.Name, e.Reason)
	if e.Detail != "" {
		text += ": " + e.Detail
	}
	return text
}

func (e *Error) Unwrap() error { return e.Err }

// Is matches an error with the same reason, so that
// errors.Is(err, &Error{Reason: ReasonForbidden}) works.
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && other.Reason == e.Reason
}

// ReasonOf returns the reason of err, or an empty reason when err is not an *Error.
func ReasonOf(err error) Reason {
	if target, ok := errors.AsType[*Error](err); ok {
		return target.Reason
	}
	return ""
}
