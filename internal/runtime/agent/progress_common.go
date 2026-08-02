// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"fmt"
	"io"
	"os"

	"github.com/dagucloud/dagu/v2/internal/core"
	"golang.org/x/term"
)

// progressWriter is the output half shared by the progress displays: where
// lines are written, whether the destination is an interactive terminal, and
// the run header format.
type progressWriter struct {
	out io.Writer
	tty bool
}

// newProgressWriter writes to stderr, with terminal features enabled when
// stderr is an interactive terminal.
func newProgressWriter() progressWriter {
	return progressWriter{
		out: os.Stderr,
		tty: term.IsTerminal(int(os.Stderr.Fd())),
	}
}

// statusIcon maps a run's final status to the mark shown on the closing line.
// Success is explicit rather than the default, so a status outside the known
// terminal set is never dressed up as one.
func statusIcon(status core.Status) string {
	switch status {
	case core.Succeeded, core.PartiallySucceeded:
		return "✓"
	case core.Failed, core.Aborted:
		return "✗"
	case core.Waiting:
		return "⏸"
	default:
		return "●"
	}
}

// gray returns text in gray color when writing to a terminal.
func (w *progressWriter) gray(s string) string {
	if !w.tty {
		return s
	}
	return "\033[38;5;245m" + s + "\033[0m"
}

// printHeader prints the run header line naming the DAG, run ID, and params.
func (w *progressWriter) printHeader(dag *core.DAG, dagRunID, params string) {
	dagName := "unknown"
	if dag != nil {
		dagName = dag.Name
	}
	runID := dagRunID
	if runID == "" {
		runID = "..."
	}
	if params != "" {
		fmt.Fprintf(w.out, "▶ %s %s %s\n", dagName, w.gray("("+runID+")"), w.gray("["+params+"]"))
	} else {
		fmt.Fprintf(w.out, "▶ %s %s\n", dagName, w.gray("("+runID+")"))
	}
}
