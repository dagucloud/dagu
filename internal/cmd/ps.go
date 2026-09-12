// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
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
  -f, --format string   Output format: table or json (default: table)

Columns: DAG, RUN_ID, ATTEMPT, STARTED, GROUP, FRESH

JSON output is a list, empty when nothing is running, so a script can read
the result without parsing the table.

Examples:
  dagu ps
  dagu ps -d my-workflow
  dagu ps -d my-workflow -r abc123
  dagu ps --format json
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
	psFormatFlag,
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
	format, err := ctx.StringParam("format")
	if err != nil {
		return fmt.Errorf("failed to get format: %w", err)
	}
	if err := validatePsFormat(format); err != nil {
		return err
	}

	if ctx.Persistence.ProcRepository == nil {
		return fmt.Errorf("process persistence is not available")
	}

	entries, err := ctx.Persistence.ProcRepository.ListAllEntries(ctx)
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

	if format == "json" {
		return renderPsJSON(ctx.Command.OutOrStdout(), matched)
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

// validatePsFormat checks if the output format is valid.
func validatePsFormat(format string) error {
	switch format {
	case "", "table", "json":
		return nil
	default:
		return fmt.Errorf("invalid format '%s'. Valid formats: table, json", format)
	}
}

// renderPsJSON writes live DAG processes as a list, which stays empty rather
// than becoming a message when nothing is running.
func renderPsJSON(out io.Writer, entries []proc.ProcEntry) error {
	type psEntry struct {
		Name      string `json:"name"`
		DAGRunID  string `json:"dagRunId"`
		AttemptID string `json:"attemptId,omitempty"`
		StartedAt string `json:"startedAt,omitempty"`
		Group     string `json:"group"`
	}

	listed := make([]psEntry, 0, len(entries))
	for _, entry := range entries {
		item := psEntry{
			Name:      entry.Meta.Name,
			DAGRunID:  entry.Meta.DAGRunID,
			AttemptID: entry.Meta.AttemptID,
			Group:     entry.GroupName,
		}
		if entry.Meta.StartedAt > 0 {
			item.StartedAt = time.Unix(entry.Meta.StartedAt, 0).UTC().Format(time.RFC3339)
		}
		listed = append(listed, item)
	}

	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(listed)
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
