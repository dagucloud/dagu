// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// AgentError is the error of a failed attempt of an agent.run step. It carries
// the failure kind and the exit code that the kind gives.
type AgentError struct {
	Kind ir.AgentFailureKind
	// Code is the exit code of the step: 75 for rate_limited, 77 for
	// auth_required, otherwise the process exit code or 1.
	Code int
	// Reason is a short key, for example no_result or max_tool_iterations.
	Reason string
	Err    error
}

// Error implements error.
func (e *AgentError) Error() string {
	text := fmt.Sprintf("agent attempt ended %s", e.Kind)
	if e.Reason != "" {
		text += " (" + e.Reason + ")"
	}
	if e.Err != nil {
		text += ": " + e.Err.Error()
	}
	return text
}

// Unwrap returns the cause.
func (e *AgentError) Unwrap() error { return e.Err }

// ExitCode returns the exit code of the step.
func (e *AgentError) ExitCode() int { return e.Code }

// NewAgentError builds the error of a failed attempt. The exit code follows the
// kind: 77 for auth_required and 75 for rate_limited. For error it is exitCode
// when above zero, otherwise 1.
func NewAgentError(kind ir.AgentFailureKind, exitCode int, reason string, cause error) *AgentError {
	code := exitCode
	switch kind {
	case ir.AgentKindAuthRequired:
		code = ir.AgentExitAuthRequired
	case ir.AgentKindRateLimited:
		code = ir.AgentExitRateLimited
	case ir.AgentKindOK:
		code = 0
	default:
		if code <= 0 {
			code = 1
		}
	}
	return &AgentError{Kind: kind, Code: code, Reason: reason, Err: cause}
}

// AsAgentError returns the AgentError in the chain of err.
func AsAgentError(err error) (*AgentError, bool) {
	return errors.AsType[*AgentError](err)
}
