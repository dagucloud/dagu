// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package wiki

import "syscall"

// errCrossDevice is the error of a rename between different devices or
// mounts.
const errCrossDevice = syscall.EXDEV
