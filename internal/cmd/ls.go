// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/spf13/cobra"
)

// Ls creates and returns a cobra command for listing DAG definitions.
func Ls() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "ls [flags] [pattern]",
			Short: "List DAG definitions",
			Long: `List DAG definitions with optional enrichment and sorting.

Optional pattern filters by DAG name or file name (substring match).
This command only lists DAGs from the local Dagu installation.

Flags:
  -n, --next         Show next scheduled run time
  -l, --last         Show last run status and time
  -H, --history      Show a compact recent-history summary
  -t, --sort-last    Sort by last run time, newest first
  -r, --reverse      Reverse sort order

Examples:
  dagu ls
  dagu ls my-
  dagu ls -n -l
  dagu ls -t -r
  dagu ls -H my-workflow
`,
			Args: cobra.MaximumNArgs(1),
		},
		lsFlags,
		runLs,
	)
}

var lsFlags = []commandLineFlag{
	lsNextFlag,
	lsLastFlag,
	lsHistoryFlag,
	lsSortLastFlag,
	lsReverseFlag,
}

type lsRow struct {
	dag         *ir.DAG
	nextRun     time.Time
	lastStatus  string
	lastStarted string
	lastTime    time.Time
	history     string
}

func runLs(ctx *Context, args []string) error {
	showNext, _ := ctx.Command.Flags().GetBool("next")
	showLast, _ := ctx.Command.Flags().GetBool("last")
	showHistory, _ := ctx.Command.Flags().GetBool("history")
	sortLast, _ := ctx.Command.Flags().GetBool("sort-last")
	reverse, _ := ctx.Command.Flags().GetBool("reverse")

	pattern := ""
	if len(args) > 0 {
		pattern = args[0]
	}

	if ctx.DAGStore == nil {
		return fmt.Errorf("DAG store is not available")
	}

	now := time.Now()
	pg := pagination.NewPaginator(1, math.MaxInt)
	listOpts := dagstore.ListDAGsOptions{
		Paginator: &pg,
		Name:      pattern,
		Sort:      "name",
		Order:     "asc",
		Time:      &now,
	}
	var nextRunProjection func(*ir.DAG, time.Time) time.Time
	if showNext {
		nextRunProjection = lsNextRunProjection(ctx)
		listOpts.NextRunProjection = nextRunProjection
	}
	if showNext && !sortLast {
		listOpts.Sort = "nextRun"
		if reverse {
			listOpts.Order = "desc"
		}
	} else if reverse && !sortLast {
		listOpts.Order = "desc"
	}

	result, errs, err := ctx.DAGStore.List(ctx, listOpts)
	if err != nil {
		return fmt.Errorf("failed to list DAGs: %w", err)
	}
	for _, e := range errs {
		_, _ = fmt.Fprintf(ctx.Command.ErrOrStderr(), "warning: %s\n", e)
	}

	needEnrich := showLast || showHistory || sortLast
	rows := make([]lsRow, 0, len(result.Items))
	for _, dag := range result.Items {
		row := lsRow{dag: dag}
		if showNext {
			row.nextRun = nextRunProjection(dag, now)
		}
		if needEnrich {
			enrichLsRow(ctx, &row, showLast || sortLast, showHistory)
		}
		rows = append(rows, row)
	}

	if sortLast {
		sortLsRowsByLastRun(rows, reverse)
	}

	return renderLsTable(ctx.Command.OutOrStdout(), rows, showNext, showLast, showHistory)
}

func lsNextRunProjection(ctx *Context) func(*ir.DAG, time.Time) time.Time {
	stateStore := scheduler.NewWatermarkStore(
		file.NewCollection(filepath.Join(ctx.Config.Paths.DataDir, "scheduler"), file.WithIndentedJSON()),
	)
	state, err := stateStore.Load(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(ctx.Command.ErrOrStderr(), "warning: failed to load scheduler state: %s\n", err)
		state = nil
	}

	projectNextRun := scheduler.NewNextRunProjection(ctx.Config.Core.Location, state)
	return func(dag *ir.DAG, now time.Time) time.Time {
		if ctx.DAGStore.IsSuspended(ctx, dag.FileName()) {
			return time.Time{}
		}
		return projectNextRun(dag, now)
	}
}

func sortLsRowsByLastRun(rows []lsRow, reverse bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		ti, tj := rows[i].lastTime, rows[j].lastTime
		if ti.Equal(tj) {
			nameI := strings.ToLower(rows[i].dag.Name)
			nameJ := strings.ToLower(rows[j].dag.Name)
			if reverse {
				return nameI > nameJ
			}
			return nameI < nameJ
		}
		if ti.IsZero() {
			return false
		}
		if tj.IsZero() {
			return true
		}
		if reverse {
			return ti.Before(tj)
		}
		return ti.After(tj)
	})
}

func enrichLsRow(ctx *Context, row *lsRow, wantLast, wantHistory bool) {
	if wantLast {
		st, err := ctx.DAGRunMgr.GetLatestStatus(ctx, row.dag)
		if err == nil && st.Status != ir.NotStarted {
			row.lastStatus = formatStatusText(st.Status)
			row.lastStarted = formatTimestamp(st.StartedAt)
			if t := parseTimeString(st.StartedAt); !t.IsZero() {
				row.lastTime = t
			}
		} else {
			row.lastStatus = "-"
			row.lastStarted = "-"
		}
	}

	if wantHistory {
		statuses := ctx.DAGRunMgr.ListRecentStatus(ctx, row.dag.Name, 5)
		if len(statuses) == 0 {
			row.history = "-"
			return
		}
		parts := make([]string, 0, len(statuses))
		for _, st := range statuses {
			parts = append(parts, formatStatusText(st.Status))
		}
		row.history = strings.Join(parts, ",")
	}
}

func renderLsTable(out io.Writer, rows []lsRow, showNext, showLast, showHistory bool) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(out, "No DAGs found")
		return err
	}

	headers := []string{"NAME"}
	if showNext {
		headers = append(headers, "NEXT_RUN")
	}
	if showLast {
		headers = append(headers, "LAST_STATUS", "LAST_STARTED")
	}
	if showHistory {
		headers = append(headers, "HISTORY")
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, strings.Join(headers, "\t")); err != nil {
		return err
	}

	for _, row := range rows {
		cols := []string{row.dag.Name}
		if showNext {
			if row.nextRun.IsZero() {
				cols = append(cols, "-")
			} else {
				cols = append(cols, row.nextRun.UTC().Format(time.RFC3339))
			}
		}
		if showLast {
			cols = append(cols, row.lastStatus, row.lastStarted)
		}
		if showHistory {
			cols = append(cols, row.history)
		}
		if _, err := fmt.Fprintln(w, strings.Join(cols, "\t")); err != nil {
			return err
		}
	}

	return w.Flush()
}
