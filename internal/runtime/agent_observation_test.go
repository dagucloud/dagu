// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestLimitAgentObservationPreservesValidUTF8(t *testing.T) {
	t.Parallel()

	got := limitAgentObservation("status: succeeded\n"+strings.Repeat("界", 100), 64)
	assert.LessOrEqual(t, len(got), 64)
	assert.True(t, utf8.ValidString(got))
	assert.Contains(t, got, "[observation truncated]")
}

func TestLimitAgentObservationCanBeDisabled(t *testing.T) {
	t.Parallel()

	content := "status: succeeded\n" + strings.Repeat("x", 1000)
	assert.Equal(t, content, limitAgentObservation(content, 0))
}

func TestLimitAgentObservationUsesExactFitMarker(t *testing.T) {
	t.Parallel()

	const marker = "\n[observation truncated]"
	assert.Equal(t, marker, limitAgentObservation(strings.Repeat("x", 100), len(marker)))
}
