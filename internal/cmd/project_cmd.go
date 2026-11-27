// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"os"
	"slices"
	"text/tabwriter"

	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	frontendfile "github.com/dagucloud/dagu/v2/internal/service/frontend/file"
	"github.com/spf13/cobra"
)

// projectCommandName names the command group. It stays hidden while
// projects are experimental.
const projectCommandName = "project"

// Project returns the command group that creates, lists, and deletes
// projects.
func Project() *cobra.Command {
	cmd := newProjectCommand(&cobra.Command{
		Use:    projectCommandName,
		Short:  "Create, list, and delete projects",
		Hidden: true,
	}, cobra.NoArgs, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})
	// Cobra shows the help of a hidden command too, so the help of the group
	// and its subcommands answers as an unknown command while projects are
	// off.
	help := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if cfg, _, err := loadCommandConfig(c, nil); err == nil && cfg.ProjectsEnabled {
			help(c, args)
			return
		}
		_ = unknownProjectCommand(c)
		exitProjectHelp(1)
	})
	cmd.AddCommand(projectCreateCommand(), projectListCommand(), projectDeleteCommand())
	return cmd
}

// exitProjectHelp ends the process after the help of a project command
// refuses, which Cobra would end with status 0.
var exitProjectHelp = os.Exit

// unknownProjectCommand prints and returns the error of a command that does
// not exist.
func unknownProjectCommand(cmd *cobra.Command) error {
	root := cmd.Root()
	err := fmt.Errorf("unknown command %q for %q", projectCommandName, root.Name())
	cmd.SilenceErrors = true
	cmd.PrintErrln(cmd.ErrPrefix(), err.Error())
	cmd.PrintErrf("Run '%v --help' for usage.\n", root.CommandPath())
	return err
}

// newProjectCommand returns a command that, while projects are off, answers
// as for a command that does not exist, whatever its arguments.
func newProjectCommand(cmd *cobra.Command, args cobra.PositionalArgs, run func(*Context, []string) error) *cobra.Command {
	cmd.Args = cobra.ArbitraryArgs
	return NewCommand(cmd, nil, func(ctx *Context, values []string) error {
		if !ctx.Config.ProjectsEnabled {
			return unknownProjectCommand(ctx.Command)
		}
		if err := args(ctx.Command, values); err != nil {
			return err
		}
		return run(ctx, values)
	})
}

func projectCreateCommand() *cobra.Command {
	return newProjectCommand(&cobra.Command{
		Use:   "create <name>",
		Short: "Create a project",
		Long: `Create a project. Its DAG directory is <projects_dir>/<name>, which is created
when it does not exist. Every server, scheduler, and coordinator starts using
the project without a restart.`,
	}, cobra.ExactArgs(1), func(ctx *Context, args []string) error {
		registry := projectregistry.New(ctx.Config.Paths.DataDir)
		created, err := file.CreateProject(ctx, ctx.Config, registry, project.CreateInput{Name: args[0]})
		if err != nil {
			return fmt.Errorf("create project %s: %w", args[0], err)
		}
		paths, err := ctx.Config.ProjectPaths(created.Name)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(ctx.Command.OutOrStdout(), "Created project %s. Its DAG directory is %s.\n", created.Name, paths.DAGsDir)
		return err
	})
}

func projectDeleteCommand() *cobra.Command {
	return newProjectCommand(&cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a project",
		Long: `Delete a project once every server, scheduler, and coordinator has released
it. Its DAG directory, state directories, grants, and each API key left with
no role are removed. The delete is refused, and changes nothing, while the
project has a run that is queued, running, or waiting, while a git
checkout tracks files in its DAG directory, while a file runs in place as the
project's, while its in-place wiki folder holds pages, and for the default
project.`,
	}, cobra.ExactArgs(1), func(ctx *Context, args []string) error {
		lifecycle, err := frontendfile.NewProjectLifecycle(ctx.Config, ctx.serverStores)
		if err != nil {
			return err
		}
		deleted, err := lifecycle.Delete(ctx, args[0], nil)
		if err != nil {
			return fmt.Errorf("delete project %s: %w", args[0], err)
		}
		_, err = fmt.Fprintf(ctx.Command.OutOrStdout(), "Deleted project %s.\n", deleted.Name)
		return err
	})
}

func projectListCommand() *cobra.Command {
	return newProjectCommand(&cobra.Command{
		Use:   "list",
		Short: "List the registered projects",
	}, cobra.NoArgs, func(ctx *Context, _ []string) error {
		projects, err := projectregistry.New(ctx.Config.Paths.DataDir).List(ctx)
		if err != nil {
			return err
		}
		// The first server start registers the default project.
		if !slices.ContainsFunc(projects, func(p project.Project) bool { return p.Name == project.DefaultName }) {
			projects = append(projects, project.Project{Name: project.DefaultName, DisplayName: project.DefaultName})
		}
		slices.SortStableFunc(projects, func(a, b project.Project) int {
			return project.CompareNames(a.Name, b.Name)
		})

		w := tabwriter.NewWriter(ctx.Command.OutOrStdout(), 0, 2, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "NAME\tDISPLAY NAME\tSTATE\tDAG DIRECTORY"); err != nil {
			return err
		}
		for _, p := range projects {
			paths, err := ctx.Config.ProjectPaths(p.Name)
			if err != nil {
				return err
			}
			state, err := ctx.serverStores.ProjectState(ctx, p)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Name, p.DisplayName, state, paths.DAGsDir); err != nil {
				return err
			}
		}
		return w.Flush()
	})
}
