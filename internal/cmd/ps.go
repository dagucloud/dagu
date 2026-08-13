// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/spf13/cobra"
)

// Ps creates and returns a cobra command for listing live DAG processes.
func Ps() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "ps [flags]",
			Short: "List running DAG processes",
			Long: `List currently alive DAG processes from the local process store.

Flags:
  -d, --dag string      Filter by DAG name
  -r, --run-id string   Filter by run ID (partial match supported)

Columns: DAG, RUN_ID, ATTEMPT, STARTED, GROUP, FRESH

Examples:
  dagu ps
  dagu ps -d my-workflow
  dagu ps -d my-workflow -r abc123
`,
			Args: cobra.NoArgs,
		},
		psFlags,
		runPs,
	)
}

var psFlags = []commandLineFlag{
	psDAGFlag,
	psRunIDFlag,
}

func runPs(ctx *Context, args []string) error {
	_ = args

	dagFilter, err := ctx.StringParam("dag")
	if err != nil {
		return fmt.Errorf("failed to get dag filter: %w", err)
	}
	runIDFilter, err := ctx.StringParam("run-id")
	if err != nil {
		return fmt.Errorf("failed to get run-id filter: %w", err)
	}

	if ctx.ProcRepository == nil {
		return fmt.Errorf("process store is not available")
	}

	entries, err := ctx.ProcRepository.ListAllEntries(ctx)
	if err != nil {
		return fmt.Errorf("failed to list processes: %w", err)
	}

	var matched []proc.ProcEntry
	for _, entry := range entries {
		if !entry.Fresh {
			continue
		}
		if dagFilter != "" && entry.Meta.Name != dagFilter {
			continue
		}
		if runIDFilter != "" && !strings.Contains(entry.Meta.DAGRunID, runIDFilter) {
			continue
		}
		matched = append(matched, entry)
	}

	if len(matched) == 0 {
		if !ctx.Quiet {
			if _, err := fmt.Fprintln(ctx.Command.OutOrStdout(), "No running processes"); err != nil {
				return err
			}
		}
		return nil
	}

	return renderPsTable(ctx.Command.OutOrStdout(), matched)
}

func renderPsTable(out io.Writer, entries []proc.ProcEntry) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "DAG\tRUN_ID\tATTEMPT\tSTARTED\tGROUP\tFRESH"); err != nil {
		return err
	}

	for _, entry := range entries {
		started := "-"
		if entry.Meta.StartedAt > 0 {
			started = time.Unix(entry.Meta.StartedAt, 0).UTC().Format(time.RFC3339)
		}
		fresh := "yes"
		if !entry.Fresh {
			fresh = "no"
		}
		attempt := entry.Meta.AttemptID
		if attempt == "" {
			attempt = "-"
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			entry.Meta.Name,
			entry.Meta.DAGRunID,
			attempt,
			started,
			entry.GroupName,
			fresh,
		); err != nil {
			return err
		}
	}

	return w.Flush()
}
