// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
)

// CommandEvalOptions returns eval options for command strings.
//
// Shell classification:
//   - Unix-like (sh, bash, zsh, ksh, ash, dash) and nix-shell: these expand
//     ${VAR} natively, so Dagu disables its own env expansion to avoid
//     double-expanding values.
//   - fish: intentionally excluded from IsUnixLikeShell (it lacks -e flag
//     support and uses $VAR but not ${VAR}), so Dagu performs ${VAR} expansion.
//   - Non-Unix (PowerShell, cmd.exe): do not understand ${VAR} syntax at all,
//     so Dagu must expand scoped variables on their behalf (ExpandEnv stays enabled).
//   - direct / empty: no shell is involved; Dagu expands OS variables itself.
func CommandEvalOptions(shell []string) []cmnvalue.Option {
	if len(shell) == 0 || shell[0] == "direct" {
		return []cmnvalue.Option{cmnvalue.WithOSExpansion()}
	}

	opts := []cmnvalue.Option{cmnvalue.WithoutDollarEscape()}

	if cmdutil.IsUnixLikeShell(shell[0]) || cmdutil.IsNixShell(shell[0]) {
		opts = append(opts, cmnvalue.WithoutExpandEnv())
	}

	return opts
}
