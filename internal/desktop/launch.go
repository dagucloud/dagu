// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import "fmt"

// Launch starts an application in a working directory without waiting for
// it. The application keeps running after the calling process exits.
func Launch(dir, command string, args []string) error {
	cmd, err := startDetached(dir, command, args)
	if err != nil {
		return fmt.Errorf("launch %s: %w", command, err)
	}
	// Reap the process when it exits so it does not linger as a zombie.
	go func() { _ = cmd.Wait() }()
	return nil
}
