// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"math"
	"testing"
)

// A huge length in a step reference must not overflow start+length into a
// negative slice bound.
func TestApplyStepSliceHugeLength(t *testing.T) {
	t.Parallel()

	got := applyStepSlice("hello", stepSliceSpec{
		hasStart:  true,
		start:     1,
		hasLength: true,
		length:    math.MaxInt,
	})
	if got != "ello" {
		t.Fatalf("applyStepSlice = %q, want %q", got, "ello")
	}
}
