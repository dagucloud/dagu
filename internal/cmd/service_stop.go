// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ServiceStop returns the command a service wrapper runs to stop the Dagu
// process it supervises.
func ServiceStop() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:    "service-stop",
			Hidden: true,
			Short:  "Ask the Dagu process serving this data directory to shut down",
			Long: `Ask the dagu start-all process serving this data directory to shut down
as it does on SIGTERM.

A Windows service wrapper whose stop arguments name this command runs it
to stop Dagu, so that stopping or restarting the service leaves runs in
progress running. The wrapper appends the service's own arguments, which
this command ignores.`,
			Args: cobra.ArbitraryArgs,
		},
		nil,
		func(ctx *Context, _ []string) error {
			// Dagu first waits out the drain for the runs it started.
			timeout := ctx.Config.SignalHandling.DrainTimeout + serviceStopTimeout
			if err := requestServiceStop(ctx.Config.Paths.DataDir, timeout); err != nil {
				return fmt.Errorf("failed to stop Dagu: %w", err)
			}
			return nil
		},
	)
}

// serviceStopTimeout bounds how long service-stop waits, beyond the drain,
// for Dagu to shut down before it ends Dagu, leaving runs in progress
// running. It matches the stoptimeout the installer gives the service
// wrapper, after which a wrapper that stops Dagu itself ends the whole
// process tree.
const serviceStopTimeout = 30 * time.Second

// serviceStopEventName names the Windows event through which service-stop
// asks the start-all process serving dataDir to shut down. Windows paths are
// case-insensitive, so the name is too.
func serviceStopEventName(dataDir string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(dataDir))))
	return `Local\dagu-service-stop-` + hex.EncodeToString(sum[:8])
}
