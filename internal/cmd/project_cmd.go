// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/spf13/cobra"
)

// projectCommandName names the command group. It stays hidden while
// projects are experimental.
const projectCommandName = "project"

// Project states that dagu project list shows.
const (
	projectStateActive   = "active"
	projectStateMissing  = "missing"
	projectStateDeleting = "deleting"
)

// Project returns the command group that creates and lists projects.
func Project() *cobra.Command {
	cmd := newProjectCommand(&cobra.Command{
		Use:    projectCommandName,
		Short:  "Create and list projects",
		Hidden: true,
	}, cobra.NoArgs, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})
	cmd.AddCommand(projectCreateCommand(), projectListCommand())
	return cmd
}

// newProjectCommand returns a command that, while projects are off, answers
// as for a command that does not exist, whatever its arguments.
func newProjectCommand(cmd *cobra.Command, args cobra.PositionalArgs, run func(*Context, []string) error) *cobra.Command {
	cmd.Args = cobra.ArbitraryArgs
	return NewCommand(cmd, nil, func(ctx *Context, values []string) error {
		if !ctx.Config.ProjectsEnabled {
			root := ctx.Command.Root()
			err := fmt.Errorf("unknown command %q for %q", projectCommandName, root.Name())
			ctx.Command.SilenceErrors = true
			ctx.Command.PrintErrln(ctx.Command.ErrPrefix(), err.Error())
			ctx.Command.PrintErrf("Run '%v --help' for usage.\n", root.CommandPath())
			return err
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
			return compareProjects(a.Name, b.Name)
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
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Name, p.DisplayName, projectState(p, paths.DAGsDir), paths.DAGsDir); err != nil {
				return err
			}
		}
		return w.Flush()
	})
}

// compareProjects orders the default project first, then by name.
func compareProjects(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == project.DefaultName:
		return -1
	case b == project.DefaultName:
		return 1
	}
	return strings.Compare(a, b)
}

// projectState tells whether p is being deleted, or is missing its DAG
// directory.
func projectState(p project.Project, dagsDir string) string {
	switch {
	case p.Deleting:
		return projectStateDeleting
	case !fileutil.FileExists(dagsDir):
		return projectStateMissing
	default:
		return projectStateActive
	}
}
