// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stringutil

import (
	"bytes"
	"regexp"
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
	// Go's regexp treats raw C1 bytes as RuneError, so normalise the single
	// byte forms to their UTF-8 rune encodings before matching.
	b := bytes.ReplaceAll([]byte(s), []byte{0x9b}, []byte("\u009b"))
	b = bytes.ReplaceAll(b, []byte{0x9c}, []byte("\u009c"))
	b = bytes.ReplaceAll(b, []byte{0x9d}, []byte("\u009d"))
	return ansiEscapePattern.ReplaceAllString(string(b), "")
}
