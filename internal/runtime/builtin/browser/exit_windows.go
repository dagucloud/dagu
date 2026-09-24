// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package browser

import "github.com/dagucloud/dagu/v2/internal/cmn/procutil"

// browserExited reports whether the browser started as process pid has
// exited.
func browserExited(pid int) bool {
	return !procutil.IsAlive(pid)
}
