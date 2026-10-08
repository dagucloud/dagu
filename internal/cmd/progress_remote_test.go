// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestTruncateWorkerDisplayMultibyteBoundary(t *testing.T) {
	t.Parallel()

	// The width cut can land inside a multibyte rune; terminal output must
	// stay valid UTF-8.
	p := &RemoteProgressDisplay{workerID: strings.Repeat("w", 9) + "界界"}
	got := p.truncateWorkerDisplay(11)
	assert.True(t, utf8.ValidString(got))
	assert.True(t, strings.HasSuffix(got, "…"))
}

func TestTruncateWorkerIDMultibyteBoundary(t *testing.T) {
	t.Parallel()

	p := &RemoteProgressDisplay{workerID: strings.Repeat("w", 9) + "界界"}
	got := p.truncateWorkerID(14)
	assert.True(t, utf8.ValidString(got))
	assert.True(t, strings.HasSuffix(got, "…"))
}
