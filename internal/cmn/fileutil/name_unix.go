// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package fileutil

func opensDevice(string) bool { return false }

func unstorableByte(c byte, _ bool) bool { return c == '/' || c == 0 }
