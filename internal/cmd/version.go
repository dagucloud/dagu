// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/spf13/cobra"
)

func Version() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Display the Dagu version information",
		Long: `Print the current version number and build details of the Dagu executable.

This command shows the version information that can be useful for troubleshooting,
reporting issues, or verifying that you're running the expected version of Dagu.`,
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(config.Version)
		},
	}
}
