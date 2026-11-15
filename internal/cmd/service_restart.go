// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"runtime"

	"github.com/dagucloud/dagu/v2/internal/service/frontend"
)

// restartExit returns what a server ending with err exits with. After an
// upgrade from the web UI, systemd (Restart=always) and launchd (KeepAlive)
// restart a clean exit, while the Windows service wrapper restarts only a
// failure.
func restartExit(err error) error {
	if errors.Is(err, frontend.ErrRestartRequested) && runtime.GOOS != "windows" {
		return nil
	}
	return err
}
