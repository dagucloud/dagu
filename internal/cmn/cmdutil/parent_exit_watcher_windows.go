// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmdutil

import "os/exec"

// StartParentExitWatcher is a no-op on Windows. Windows process cleanup uses
// job/process-tree handling in the platform-specific command utilities.
func StartParentExitWatcher(_ *exec.Cmd) (func(), error) {
	return func() {}, nil
}
