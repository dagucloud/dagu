// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin || windows

package desktop

// awakeReason names the power requests that keep the display on while a
// backend is open, as the operating system lists them.
const awakeReason = "Dagu computer step is operating the desktop"
