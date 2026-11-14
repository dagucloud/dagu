// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin || windows

package desktop

// pick returns yes when cond holds and no otherwise.
func pick[T any](cond bool, yes, no T) T {
	if cond {
		return yes
	}
	return no
}
