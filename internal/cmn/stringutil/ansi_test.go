// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stringutil_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/stretchr/testify/assert"
)

func TestStripANSI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "PlainText", input: "exit status 1\nfatal", want: "exit status 1\nfatal"},
		{name: "Colors", input: "\x1b[90m1:42PM\x1b[0m \x1b[31mfatal\x1b[0m", want: "1:42PM fatal"},
		{name: "ColonSeparatedColor", input: "\x1b[38:2:255:0:0mred\x1b[0m", want: "red"},
		// OSC 8 hyperlinks may end with BEL, ESC \, or C1 ST.
		{name: "HyperlinkBEL", input: "see \x1b]8;;https://example.com/a,b\x07docs\x1b]8;;\x07", want: "see docs"},
		{name: "HyperlinkESC", input: "see \x1b]8;;https://example.com\x1b\\docs\x1b]8;;\x1b\\", want: "see docs"},
		{name: "HyperlinkC1", input: "see \x1b]8;;https://example.com\u009cdocs\x1b]8;;\u009c", want: "see docs"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, stringutil.StripANSI(tt.input))
		})
	}
}
