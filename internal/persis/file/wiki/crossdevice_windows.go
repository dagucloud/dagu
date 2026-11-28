// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package wiki

import "golang.org/x/sys/windows"

// errCrossDevice is the error of a rename between different volumes.
const errCrossDevice = windows.ERROR_NOT_SAME_DEVICE
