// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package desktop

import "os"

// ownProcessFamily returns this process and the one that started it.
func ownProcessFamily() map[uint32]bool {
	return map[uint32]bool{uint32(os.Getpid()): true, uint32(os.Getppid()): true} //nolint:gosec // process ids fit
}
