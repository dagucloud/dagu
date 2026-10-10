// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && !(amd64 || arm64)

package desktop

// Window identity needs the 64-bit calling convention; elsewhere the system
// does not say, and a step treats the focus as unknown.
func (windowsBackend) FocusedWindow() WindowID    { return WindowID{} }
func (windowsBackend) WindowAt(int, int) WindowID { return WindowID{} }
