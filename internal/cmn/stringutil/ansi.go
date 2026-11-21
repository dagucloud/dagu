// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stringutil

import (
	"regexp"
	"unicode/utf8"
)

// ansiEscapePattern matches ANSI escape sequences: OSC sequences such as
// hyperlinks, ended by BEL, ESC \ or C1 ST, and CSI sequences such as colors.
// Credit: https://github.com/chalk/ansi-regex/blob/79e112d67d2159cc999aab50398b712d370c2cf9/index.js under MIT license
var ansiEscapePattern = regexp.MustCompile(
	`(?:(?:\x1b\]|\x{9d})[^\x07\x1b\x{9c}\x{9d}]*(?:\x07|\x1b\\|\x{9c}))` +
		`|[\x1b\x{9b}][[\]()#;?]*(?:\d{1,4}(?:[;:]\d{0,4})*)?[\dA-PR-TZcf-nq-uy=><~]`,
)

// StripANSI returns s with ANSI escape sequences, such as colors and
// terminal hyperlinks, removed.
func StripANSI(s string) string {
	// Go's regexp cannot match raw C1 bytes; normalise only standalone ones
	// to their UTF-8 rune form, leaving bytes inside valid UTF-8 intact.
	var b []byte
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 && s[i] >= 0x9b && s[i] <= 0x9d {
			b = append(b, 0xc2, s[i])
			i++
			continue
		}
		b = append(b, s[i:i+size]...)
		i += size
	}
	return ansiEscapePattern.ReplaceAllString(string(b), "")
}
