// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"fmt"
	"strings"
)

// OverlapPolicy controls behavior when a new run is triggered while a previous run is still active.
type OverlapPolicy string

const (
	// OverlapPolicySkip skips a new run if the previous is still running.
	OverlapPolicySkip OverlapPolicy = "skip"

	// OverlapPolicyAll queues all runs and executes them sequentially in chronological order.
	OverlapPolicyAll OverlapPolicy = "all"

	// OverlapPolicyLatest discards all but the most recent missed interval.
	OverlapPolicyLatest OverlapPolicy = "latest"
)

// ParseOverlapPolicy parses a string into an OverlapPolicy.
// Empty string defaults to OverlapPolicySkip.
func ParseOverlapPolicy(s string) (OverlapPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "skip":
		return OverlapPolicySkip, nil
	case "all":
		return OverlapPolicyAll, nil
	case "latest":
		return OverlapPolicyLatest, nil
	default:
		return "", fmt.Errorf("invalid overlapPolicy %q: must be \"skip\", \"all\", or \"latest\"", s)
	}
}
