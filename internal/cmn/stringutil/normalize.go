// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stringutil

import (
	"strings"

	"golang.org/x/text/width"
)

// NormalizeText returns text as a check compares it: full-width letters,
// digits, and punctuation folded to their half-width forms, half-width
// katakana to full-width, and runs of whitespace, including the ideographic
// space, collapsed to one space with none at the ends. Text an application
// or a page pads, or writes in the other width, still matches what a
// person typed.
func NormalizeText(s string) string {
	return strings.Join(strings.Fields(width.Fold.String(s)), " ")
}
