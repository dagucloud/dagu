// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestLimitControllerObservationPreservesValidUTF8(t *testing.T) {
	t.Parallel()

	got := limitControllerObservation("status: succeeded\n"+strings.Repeat("界", 100), 64)
	assert.LessOrEqual(t, len(got), 64)
	assert.True(t, utf8.ValidString(got))
	assert.Contains(t, got, "[observation truncated]")
}

func TestLimitControllerObservationCanBeDisabled(t *testing.T) {
	t.Parallel()

	content := "status: succeeded\n" + strings.Repeat("x", 1000)
	assert.Equal(t, content, limitControllerObservation(content, 0))
}
