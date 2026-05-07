// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/spf13/cobra"

	_ "github.com/dagucloud/dagu/internal/runtime/builtin" // Register built-in executors
)

var rootCmd = &cobra.Command{
	Use:   config.AppSlug,
	Short: "Dagu is a compact, portable workflow engine",
	Long: `Dagu is a compact, portable workflow engine.

It provides a declarative model for orchestrating command execution across
diverse environments, including shell scripts, Python commands, containerized
operations, or remote commands.
`,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().String("context", "", "Context name to use for command execution (default: current context or local)")
	rootCmd.AddCommand(cmd.Start())
	rootCmd.AddCommand(cmd.Exec())
	rootCmd.AddCommand(cmd.Enqueue())
	rootCmd.AddCommand(cmd.Dequeue())
	rootCmd.AddCommand(cmd.Stop())
	rootCmd.AddCommand(cmd.Restart())
	rootCmd.AddCommand(cmd.Dry())
	rootCmd.AddCommand(cmd.Validate())
	rootCmd.AddCommand(cmd.Status())
	rootCmd.AddCommand(cmd.History())
	rootCmd.AddCommand(cmd.Version())
	rootCmd.AddCommand(cmd.Server())
	rootCmd.AddCommand(cmd.Scheduler())
	rootCmd.AddCommand(cmd.CmdCoordinator())
	rootCmd.AddCommand(cmd.CmdWorker())
	rootCmd.AddCommand(cmd.Retry())
	rootCmd.AddCommand(cmd.StartAll())
	rootCmd.AddCommand(cmd.Migrate())
	rootCmd.AddCommand(cmd.Cleanup())
	rootCmd.AddCommand(cmd.Sync())
	rootCmd.AddCommand(cmd.Upgrade())
	rootCmd.AddCommand(cmd.License())
	rootCmd.AddCommand(cmd.Schema())
	rootCmd.AddCommand(cmd.Example())
	rootCmd.AddCommand(cmd.Config())
	rootCmd.AddCommand(cmd.ContextCommand())
	rootCmd.AddCommand(cmd.Agent())

	config.Version = version
}

var version = "0.0.0"
