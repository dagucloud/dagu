// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
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

With --values, print each setting instead, with the source that supplied it:
default, file, or env:<NAME>. Values are shown as read from the file, the
environment or the defaults, before Dagu's own resolution, so a setting Dagu
fills in later can show as empty. auth.mode shows the mode Dagu runs in.
Secret values are masked. Use --format json for a list a script can read.

Example:
  dagu config
  dagu config --dagu-home /custom/path
  dagu config --values
  dagu config --values --format json
`,
		}, []commandLineFlag{configValuesFlag, configFormatFlag}, runConfig,
	)
}

var (
	configValuesFlag = commandLineFlag{
		name:   "values",
		usage:  "Print each setting with its value and source",
		isBool: true,
	}

	configFormatFlag = commandLineFlag{
		name:         "format",
		shorthand:    "f",
		defaultValue: "table",
		usage:        "Output format for --values: table or json (default: table)",
	}
)

func runConfig(ctx *Context, _ []string) error {
	values, err := ctx.Command.Flags().GetBool("values")
	if err != nil {
		return fmt.Errorf("failed to get values flag: %w", err)
	}
	format, err := ctx.StringParam("format")
	if err != nil {
		return fmt.Errorf("failed to get format: %w", err)
	}
	if !values {
		if ctx.Command.Flags().Changed("format") {
			return fmt.Errorf("--format requires --values")
		}
		return renderConfigPaths(ctx)
	}
	switch format {
	case "table", "json":
	default:
		return fmt.Errorf("invalid format '%s'. Valid formats: table, json", format)
	}

	settings := ctx.configLoader.Settings(ctx.Config)

	out := ctx.Command.OutOrStdout()
	if format == "json" {
		return renderSettingsJSON(out, settings)
	}
	return renderSettingsTable(out, settings)
}

func renderConfigPaths(ctx *Context) error {
	paths := ctx.Config.Paths

	w := tabwriter.NewWriter(ctx.Command.OutOrStdout(), 0, 0, 3, ' ', 0)

	rows := []struct {
		label string
		value string
	}{
		{"Config file", paths.ConfigFileUsed},
		{"Base config", paths.BaseConfig},
		{"DAGs directory", paths.DAGsDir},
		{"Wiki directory", paths.WikiDir},
		{"DAG runs", paths.DAGRunsDir},
		{"DAG-run work directory", paths.DAGRunWorkDir},
		{"Data directory", paths.DataDir},
		{"Tools directory", paths.ToolsDir},
		{"Log directory", paths.LogDir},
		{"Admin logs", paths.AdminLogsDir},
		{"Suspend flags", paths.SuspendFlagsDir},
		{"Queue", paths.QueueDir},
		{"Processes", paths.ProcDir},
		{"Service registry", paths.ServiceRegistryDir},
		{"Executable", paths.Executable},
	}

	for _, r := range rows {
		if r.value != "" {
			fmt.Fprintf(w, "%s:\t%s\n", r.label, r.value)
		}
	}

	return w.Flush()
}

func renderSettingsTable(out io.Writer, settings []config.Setting) error {
	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "KEY\tVALUE\tSOURCE")
	for _, s := range settings {
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.Key, s.Value, s.Source)
	}
	return w.Flush()
}

func renderSettingsJSON(out io.Writer, settings []config.Setting) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(settings)
}
