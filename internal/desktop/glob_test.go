// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWindowMatches(t *testing.T) {
	t.Parallel()

	assert.True(t, WindowMatches("経費精算 - 請求書 1042", "経費精算"))
	assert.True(t, WindowMatches("経費精算 - 請求書 1042", "請求書 *"))
	assert.True(t, WindowMatches("経費精算 - 請求書 1042", ""))
	assert.False(t, WindowMatches("経費精算 - 請求書 1042", "給与"))
}

func TestGlobMatch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		pattern, s string
		want       bool
	}{
		{"保存", "保存", true},
		{"保存", "保存する", false},
		{"保存*", "保存する", true},
		{"*する", "保存する", true},
		{"請求書 *", "請求書 INV-0001", true},
		{"請求書 %number%*", "請求書 %number% (下書き)", true},
		{"請求書 %number%*", "請求書 INV-0001", false},
		{"*", "", true},
		{"*", "anything", true},
		{"", "", true},
		{"", "a", false},
		{"a*b*c", "axxbyyc", true},
		{"a*b*c", "axxbyy", false},
		{"**", "x", true},
		{"a?", "ab", false},
		{"a?", "a?", true},
	} {
		assert.Equal(t, tc.want, globMatch(tc.pattern, tc.s), "%q against %q", tc.pattern, tc.s)
	}
}
