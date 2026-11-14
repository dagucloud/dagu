// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package desktop

// OnConsoleSession reports whether a person at the keyboard could collide
// with a step's input. Off Windows the system does not distinguish a remote
// session, so the console is assumed, which keeps the guard.
func OnConsoleSession() bool { return true }
