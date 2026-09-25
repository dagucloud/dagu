// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/spf13/cobra"
)

var browserCacheStepFlag = commandLineFlag{
	name:  "step",
	usage: "Clear only this step (step ID, or step name when the step has no ID)",
}

// Browser returns the command group for browser step state.
func Browser() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "browser",
		Short: "Manage state kept by browser steps",
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})

	cmd.AddCommand(browserCacheCommand())
	return cmd
}

func browserCacheCommand() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "cache",
		Short: "Manage the replay cache of browser steps",
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})

	cmd.AddCommand(browserCacheClearCommand())
	return cmd
}

func browserCacheClearCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "clear [flags] <DAG>",
		Short: "Clear the recorded act operations of a DAG's browser steps",
		Long: `Clear the recorded act operations that a DAG's browser steps replay on
later runs. The next run of a cleared step asks the model again and records
the new actions.

Identify the DAG by name or by YAML file path. Without --step, every step of
the DAG is cleared.

The cache is kept on the host that ran the step. In distributed mode, run
this command on the worker.

Examples:
  dagu browser cache clear billing                 # Clear every step
  dagu browser cache clear billing --step login    # Clear one step
`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{browserCacheStepFlag}, runBrowserCacheClear)
}

func runBrowserCacheClear(ctx *Context, args []string) error {
	dagName, err := extractDAGName(ctx, args[0])
	if err != nil {
		return fmt.Errorf("failed to extract DAG name: %w", err)
	}
	step, err := ctx.StringParam(browserCacheStepFlag.name)
	if err != nil {
		return err
	}

	removed, err := browserReplayCache(ctx).Clear(dagName, step)
	if err != nil {
		return fmt.Errorf("failed to clear browser replay cache for %q: %w", dagName, err)
	}
	if ctx.Quiet {
		return nil
	}

	out := ctx.Command.OutOrStdout()
	if len(removed) == 0 {
		if step != "" {
			_, err = fmt.Fprintf(out, "No browser replay cache for step %q of DAG %q\n", step, dagName)
		} else {
			_, err = fmt.Fprintf(out, "No browser replay cache for DAG %q\n", dagName)
		}
		return err
	}
	for _, s := range removed {
		if _, err := fmt.Fprintf(out, "Removed browser replay cache for step %q of DAG %q\n", s, dagName); err != nil {
			return err
		}
	}
	return nil
}

func browserReplayCache(ctx *Context) *browserhost.ReplayCache {
	return browserhost.NewReplayCache(filepath.Join(ctx.Config.Paths.DataDir, browserhost.DataDirName))
}
