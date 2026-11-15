// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/launcher"
)

// runBinariesDir is the directory, in the data directory, that holds the
// copies of the binary that the runs of the Windows service execute.
const runBinariesDir = "run-binaries"

// executableEnvKey sets the executable a Dagu process launches runs with.
const executableEnvKey = "DAGU_EXECUTABLE"

// launchRunsOutsideService readies start-all, when the Dagu Windows service
// runs it, to launch runs that a stop of the service leaves running, from a
// copy of the binary, as launcher.LaunchOutsideService describes. An
// executable set in the configuration is left as it is.
func launchRunsOutsideService(ctx *Context) error {
	cfg := ctx.Config
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find the running binary: %w", err)
	}
	if cfg.Paths.Executable != self {
		return nil
	}
	copied, err := launcher.LaunchOutsideService(self, filepath.Join(cfg.Paths.DataDir, runBinariesDir))
	if err != nil || copied == "" {
		return err
	}
	// Runs take the executable they launch runs with from the environment,
	// so the runs they start execute the copy too.
	cfg.Paths.Executable = copied
	cfg.Core.BaseEnv = config.NewBaseEnv(setEnvEntry(cfg.Core.BaseEnv.AsSlice(), executableEnvKey, copied))
	logger.Info(ctx, "Runs are launched outside the Windows service process tree", tag.File(copied))
	return nil
}

// setEnvEntry returns env with key set to value, in place of any entry for
// key.
func setEnvEntry(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+value)
}
