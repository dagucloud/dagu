// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/spf13/cobra"
)

// Computer returns the command group for computer steps.
func Computer() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "computer",
		Short: "Check the desktop and manage state kept by computer steps",
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})

	cmd.AddCommand(computerCheckCommand())
	cmd.AddCommand(computerCacheCommand())
	return cmd
}

func computerCheckCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "check",
		Short: "Check that computer steps can operate this desktop",
		Long: `Check that computer steps can capture the screen and send input on this
host. Run it in the same session and as the same user as the worker that
runs computer steps.

On macOS, the command also asks macOS to show its Screen Recording and
Accessibility prompts for the permissions that are missing. Grant them to the
application that starts Dagu, such as Terminal, or to the dagu binary when it
runs on its own.

On Windows, the worker must run in a logged-in user session, not as a
service, and the screen must stay unlocked.

Computer steps are not supported on other systems.
`,
		Args: cobra.NoArgs,
	}, nil, runComputerCheck)
}

func runComputerCheck(ctx *Context, _ []string) error {
	desktop.RequestPermissions()
	diag := desktop.Check()
	out := ctx.Command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "System:  %s\n", diag.OS)
	if diag.Width > 0 {
		_, _ = fmt.Fprintf(out, "Display: %dx%d pixels\n", diag.Width, diag.Height)
	}
	if len(diag.Problems) == 0 {
		_, err := fmt.Fprintln(out, "Ready:   computer steps can operate this desktop")
		return err
	}
	for _, problem := range diag.Problems {
		_, _ = fmt.Fprintf(out, "Problem: %s\n", problem)
	}
	return errors.New("computer steps cannot operate this desktop")
}

func computerCacheCommand() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "cache",
		Short: "Manage the replay cache of computer steps",
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})

	cmd.AddCommand(NewCommand(&cobra.Command{
		Use:   "clear [flags] <DAG>",
		Short: "Clear the recorded act operations of a DAG's computer steps",
		Long: `Clear the recorded act operations that a DAG's computer steps replay on
later runs. The next run of a cleared step asks the model again and records
the new actions.

Identify the DAG by name or by YAML file path. Without --step, every step of
the DAG is cleared.

The cache is kept on the host that ran the step. In distributed mode, run
this command on the worker.

Examples:
  dagu computer cache clear invoices                # Clear every step
  dagu computer cache clear invoices --step post    # Clear one step
`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{replayCacheStepFlag}, func(ctx *Context, args []string) error {
		return clearReplayCache(ctx, args[0], namedReplayCache{kind: computerhost.AgentProvider, store: computerReplayCache(ctx)})
	}))
	return cmd
}

func computerReplayCache(ctx *Context) *replaycache.Store {
	return replaycache.New(filepath.Join(ctx.Config.Paths.DataDir, computerhost.DataDirName))
}
