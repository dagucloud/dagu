// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmdutil

import (
	"os"
	"syscall"

	"github.com/dagucloud/dagu/internal/cmn/signal"
)

// TerminationMode describes the lifecycle intent behind a process stop request.
type TerminationMode string

const (
	// TerminationModeGraceful asks the platform adapter to stop the process tree
	// using the least forceful supported mechanism for the requested signal.
	TerminationModeGraceful TerminationMode = "graceful"
	// TerminationModeForce asks the platform adapter to terminate the process tree.
	TerminationModeForce TerminationMode = "force"
)

// TerminationIntent is the platform-neutral stop request used by process
// adapters. Signal is retained for POSIX adapters and for user-facing logs, but
// callers should make decisions from Mode instead of assuming Unix semantics.
type TerminationIntent struct {
	Mode   TerminationMode
	Signal os.Signal
}

// StopReason describes why a process stop was requested.
type StopReason string

const (
	StopReasonUnknown    StopReason = ""
	StopReasonCancel     StopReason = "cancel"
	StopReasonTimeout    StopReason = "timeout"
	StopReasonShutdown   StopReason = "shutdown"
	StopReasonParentExit StopReason = "parent-exit"
)

// StopMechanism describes how a platform adapter requested process termination.
type StopMechanism string

const (
	StopMechanismNone         StopMechanism = "none"
	StopMechanismProcessGroup StopMechanism = "process-group"
	StopMechanismProcessTree  StopMechanism = "process-tree"
	StopMechanismJobObject    StopMechanism = "job-object"
)

// StopRequest is the platform-neutral lifecycle request for a local process.
type StopRequest struct {
	Intent TerminationIntent
	Reason StopReason
}

// StopOutcome reports how a platform adapter applied a stop request.
type StopOutcome struct {
	RequestedMode TerminationMode
	AppliedMode   TerminationMode
	Mechanism     StopMechanism
	Contained     bool
	Partial       bool
	Reason        StopReason
}

// TerminationFromSignal preserves the legacy signal-based call sites while
// moving the implementation behind an intent-based seam.
func TerminationFromSignal(sig os.Signal) TerminationIntent {
	if isForceSignal(sig) {
		return ForceTermination()
	}
	return GracefulTermination(sig)
}

// GracefulTermination creates a graceful stop request for the given signal.
// Force-class signals are normalized to a forceful intent.
func GracefulTermination(sig os.Signal) TerminationIntent {
	if isForceSignal(sig) {
		return TerminationIntent{
			Mode:   TerminationModeForce,
			Signal: sig,
		}
	}
	return TerminationIntent{
		Mode:   TerminationModeGraceful,
		Signal: sig,
	}
}

// ForceTermination creates a forceful process-tree termination request.
func ForceTermination() TerminationIntent {
	return TerminationIntent{
		Mode:   TerminationModeForce,
		Signal: os.Kill,
	}
}

// WithSignal returns a copy of the intent with a different signal. Force-class
// signals always force the mode because they cannot be graceful in practice.
func (i TerminationIntent) WithSignal(sig os.Signal) TerminationIntent {
	if i.IsForce() {
		return ForceTermination()
	}
	if isForceSignal(sig) {
		return ForceTermination()
	}
	i.Signal = sig
	if i.Mode == "" {
		i.Mode = TerminationModeGraceful
	}
	return i
}

// IsForce reports whether this request is a forceful stop.
func (i TerminationIntent) IsForce() bool {
	return i.Mode == TerminationModeForce
}

// IsTermination reports whether this intent should mark a running node aborted.
func (i TerminationIntent) IsTermination() bool {
	return i.IsForce() || signal.IsTerminationSignalOS(i.Signal)
}

// SignalName returns a stable string for logging.
func (i TerminationIntent) SignalName() string {
	if i.Signal == nil {
		return ""
	}
	return i.Signal.String()
}

func isForceSignal(sig os.Signal) bool {
	sysSig, ok := sig.(syscall.Signal)
	return ok && sysSig == syscall.SIGKILL
}

func (r StopRequest) normalize() StopRequest {
	if r.Intent.Mode == "" {
		r.Intent = GracefulTermination(r.Intent.Signal)
	}
	return r
}

func noopStopOutcome(req StopRequest) StopOutcome {
	req = req.normalize()
	return StopOutcome{
		RequestedMode: req.Intent.Mode,
		AppliedMode:   req.Intent.Mode,
		Mechanism:     StopMechanismNone,
		Contained:     true,
		Reason:        req.Reason,
	}
}
