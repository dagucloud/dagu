// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"
)

// fileOnlyBlocks lists the blocks that only the configuration file sets: ai
// (Spec 093), untrusted (Spec 092) and agent_tools (Spec 090). Their keys are
// closed objects, so a variable of the environment cannot set one of them.
var fileOnlyBlocks = []struct{ block, variable string }{
	{AIBlock, "DAGU_AI"},
	{"untrusted", "DAGU_UNTRUSTED"},
	{"agent_tools", "DAGU_AGENT_TOOLS"},
}

// checkFileOnlyBlocks stops Dagu at start when the environment has a variable
// that names a whole file-only block. The viper environment binding would
// replace the block with the value of the variable. The error names the
// variable and the key, and never shows the value: it can hold a secret.
func checkFileOnlyBlocks() error {
	for _, b := range fileOnlyBlocks {
		if _, set := os.LookupEnv(b.variable); set {
			return fmt.Errorf("config: the environment variable %s is not read: set the block %s in the configuration file",
				b.variable, b.block)
		}
	}
	return nil
}
