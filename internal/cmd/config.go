// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func Config() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "config",
			Short: "Display the resolved configuration paths",
			Long: `Show all resolved file system paths used by Dagu.

This is useful for debugging workflows, inspecting stored data,
or verifying which directories Dagu is using in the current environment.

Example:
  dagu config
  dagu config --dagu-home /custom/path
`,
		}, nil, runConfig,
	)
}

func runConfig(ctx *Context, _ []string) error {
	paths := ctx.Config.Paths

	w := tabwriter.NewWriter(ctx.Command.OutOrStdout(), 0, 0, 3, ' ', 0)

	rows := []struct {
		label string
		value string
	}{
		{"Config file", paths.ConfigFileUsed},
		{"Base config", paths.BaseConfig},
		{"DAGs directory", paths.DAGsDir},
		{"Docs directory", paths.DocsDir},
		{"DAG runs", paths.DAGRunsDir},
		{"Data directory", paths.DataDir},
		{"Tools directory", paths.ToolsDir},
		{"Log directory", paths.LogDir},
		{"Admin logs", paths.AdminLogsDir},
		{"Suspend flags", paths.SuspendFlagsDir},
		{"Queue", paths.QueueDir},
		{"Processes", paths.ProcDir},
		{"Service registry", paths.ServiceRegistryDir},
		{"Sessions", paths.SessionsDir},
		{"Executable", paths.Executable},
	}

	for _, r := range rows {
		if r.value != "" {
			fmt.Fprintf(w, "%s:\t%s\n", r.label, r.value)
		}
	}

	return w.Flush()
}
