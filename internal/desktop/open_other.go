// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !darwin && !windows

package desktop

import "runtime"

// Open returns a driver for the desktop of the current session.
func Open() (*Driver, error) {
	return nil, ErrUnsupported
}

// Check reports whether the desktop of the current session can be automated.
func Check() Diagnostics {
	return Diagnostics{OS: runtime.GOOS, Problems: []Problem{{Code: problemUnsupported, Message: ErrUnsupported.Error()}}}
}

// RequestPermissions does nothing where desktop automation is unsupported.
func RequestPermissions() {}
