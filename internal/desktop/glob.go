// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

// WindowMatches reports whether a window title contains pattern, where *
// matches any run of characters.
func WindowMatches(title, pattern string) bool {
	return globMatch("*"+pattern+"*", title)
}

// globMatch reports whether s matches pattern in full, where * matches any
// run of characters, including none. Every other character matches itself.
func globMatch(pattern, s string) bool {
	p, str := []rune(pattern), []rune(s)
	pi, si := 0, 0
	// starP and starS remember the last * and where it started matching,
	// so a failed match after it can give the * one more character.
	starP, starS := -1, 0
	for si < len(str) {
		switch {
		case pi < len(p) && p[pi] == '*':
			starP, starS = pi, si
			pi++
		case pi < len(p) && p[pi] == str[si]:
			pi++
			si++
		case starP >= 0:
			pi = starP + 1
			starS++
			si = starS
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}
