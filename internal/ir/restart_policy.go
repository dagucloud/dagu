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
