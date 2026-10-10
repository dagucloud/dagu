// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"strings"

	"golang.org/x/text/width"
)

// NormalizeText returns text as it is compared: full-width letters, digits,
// and punctuation folded to their half-width forms, half-width katakana to
// full-width, and runs of whitespace, including the ideographic space,
// collapsed to one space with none at the ends. A name an application
// pads, or writes in the other width, still matches what a person typed.
func NormalizeText(s string) string {
	return strings.Join(strings.Fields(width.Fold.String(s)), " ")
}

// normalizeLabel normalizes a label, dropping the colon a form puts after
// it, which a person naming the label leaves out.
func normalizeLabel(s string) string {
	return strings.TrimSuffix(NormalizeText(s), ":")
}

// nameMatches reports whether a name matches a pattern with * wildcards,
// comparing both as NormalizeText does.
func nameMatches(pattern, name string) bool {
	return globMatch(NormalizeText(pattern), NormalizeText(name))
}
