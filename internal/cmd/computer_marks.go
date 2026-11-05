// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"os"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/spf13/cobra"
)

// computerMarksCommand draws, on macOS, the marks that show a person at
// the desktop where a computer step acts: only a process's main thread may
// draw a window there, so a step starts this program as its helper. The
// step sends the marks on stdin and reads one line on stdout for each, so
// it loads no Dagu configuration and prints nothing else.
func computerMarksCommand() *cobra.Command {
	desktop.RegisterMarksHelper()
	return &cobra.Command{
		Use:           "marks",
		Short:         "Draw where a computer step acts",
		Hidden:        true,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(*cobra.Command, []string) error {
			return desktop.ServeMarks(os.Stdin, os.Stdout)
		},
	}
}
