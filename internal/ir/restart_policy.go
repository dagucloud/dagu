// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"fmt"
	"strings"
)

// RestartPolicy controls whether the scheduler keeps a run of a DAG alive by
// starting a new run when the previous one ends. The zero value is
// RestartPolicyNo.
type RestartPolicy string

const (
	// RestartPolicyNo never starts a run because of the policy.
	RestartPolicyNo RestartPolicy = ""

	// RestartPolicyAlways starts a new run whenever the DAG has no active run.
	RestartPolicyAlways RestartPolicy = "always"

	// RestartPolicyUnlessStopped behaves like RestartPolicyAlways, except that
	// no run is started after a run that an operator stopped.
	RestartPolicyUnlessStopped RestartPolicy = "unless-stopped"
)

// ParseRestartPolicy parses a string into a RestartPolicy.
// The empty string and "no" both mean RestartPolicyNo.
func ParseRestartPolicy(s string) (RestartPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "no":
		return RestartPolicyNo, nil
	case "always":
		return RestartPolicyAlways, nil
	case "unless-stopped":
		return RestartPolicyUnlessStopped, nil
	default:
		return "", fmt.Errorf("invalid restart_policy %q: must be \"no\", \"always\", or \"unless-stopped\"", s)
	}
}

// Enabled reports whether the scheduler restarts runs of a DAG with this policy.
func (p RestartPolicy) Enabled() bool {
	return p == RestartPolicyAlways || p == RestartPolicyUnlessStopped
}

// LoopOnFailure says what a run that loops does when a cycle fails. The zero
// value is LoopOnFailureRestart.
type LoopOnFailure string

const (
	// LoopOnFailureRestart ends the run with the status of the failed cycle, so
	// the restart policy starts a new run after restart_wait_sec and counts it.
	LoopOnFailureRestart LoopOnFailure = ""

	// LoopOnFailureContinue goes on with the next cycle after loop_wait_sec, as
	// a while-true loop does.
	LoopOnFailureContinue LoopOnFailure = "continue"
)

// ParseLoopOnFailure parses a string into a LoopOnFailure. The empty string
// and "restart" both mean LoopOnFailureRestart.
func ParseLoopOnFailure(s string) (LoopOnFailure, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "restart":
		return LoopOnFailureRestart, nil
	case "continue":
		return LoopOnFailureContinue, nil
	default:
		return "", fmt.Errorf("invalid loop_on_failure %q: must be \"restart\" or \"continue\"", s)
	}
}

// String returns the name that the field takes in a DAG file.
func (l LoopOnFailure) String() string {
	if l == LoopOnFailureContinue {
		return "continue"
	}
	return "restart"
}
