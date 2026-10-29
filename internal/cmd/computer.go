// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/computer"
	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
)

// Computer returns the command group for computer steps.
func Computer() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "computer",
		Short: "Check the desktop, read its elements, and manage state kept by computer steps",
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})

	cmd.AddCommand(computerCheckCommand())
	cmd.AddCommand(computerElementsCommand())
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
service, and the screen must stay unlocked. A Remote Desktop or child
session must stay connected: once its client is gone the session stops
drawing, which the check reports as session_disconnected.

Computer steps are not supported on other systems.

With --format json, the result is one JSON object: os, width, height, ready,
and problems, each with a code and a message.
`,
		Args: cobra.NoArgs,
	}, []commandLineFlag{computerFormatFlag}, runComputerCheck)
}

var computerFormatFlag = commandLineFlag{
	name:         "format",
	shorthand:    "f",
	defaultValue: "text",
	usage:        "Output format: text or json (default: text)",
}

// computerCheckResult is the JSON output of dagu computer check.
type computerCheckResult struct {
	OS       string            `json:"os"`
	Width    int               `json:"width"`
	Height   int               `json:"height"`
	Ready    bool              `json:"ready"`
	Problems []desktop.Problem `json:"problems"`
}

func runComputerCheck(ctx *Context, _ []string) error {
	format, err := ctx.StringParam("format")
	if err != nil {
		return fmt.Errorf("failed to get format: %w", err)
	}
	if format != "text" && format != "json" {
		return fmt.Errorf("invalid format %q: use text or json", format)
	}
	desktop.RequestPermissions()
	diag := desktop.Check()
	out := ctx.Command.OutOrStdout()
	if format == "json" {
		result := computerCheckResult{OS: diag.OS, Width: diag.Width, Height: diag.Height, Ready: len(diag.Problems) == 0, Problems: diag.Problems}
		if result.Problems == nil {
			result.Problems = []desktop.Problem{}
		}
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return err
		}
		if result.Ready {
			return nil
		}
		return errors.New("computer steps cannot operate this desktop")
	}
	_, _ = fmt.Fprintf(out, "System:  %s\n", diag.OS)
	if diag.Width > 0 {
		_, _ = fmt.Fprintf(out, "Display: %dx%d pixels\n", diag.Width, diag.Height)
	}
	if len(diag.Problems) == 0 {
		_, err := fmt.Fprintln(out, "Ready:   computer steps can operate this desktop")
		return err
	}
	for _, problem := range diag.Problems {
		_, _ = fmt.Fprintf(out, "Problem: %s\n", problem.Message)
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
  dagu computer cache clear invoices                   # Clear every step
  dagu computer cache clear invoices --step post       # Clear one step
  dagu computer cache clear invoices --step post --op 3  # Forget one task of the step
`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{replayCacheStepFlag, replayCacheOpFlag}, func(ctx *Context, args []string) error {
		cache := namedReplayCache{kind: computerhost.AgentProvider, store: computerReplayCache(ctx)}
		if op, err := ctx.StringParam(replayCacheOpFlag.name); err != nil {
			return err
		} else if op != "" {
			return clearReplayCacheOp(ctx, args[0], cache, op)
		}
		return clearReplayCache(ctx, args[0], cache)
	}))
	cmd.AddCommand(NewCommand(&cobra.Command{
		Use:   "show [flags] <DAG>",
		Short: "Show the recorded operations of a DAG's computer steps as pinned steps",
		Long: `Show what a DAG's computer steps recorded on this host, written the way a
step pins it: an act's recorded steps and an extract's fields, keyed by the
system the recording was made on. Written into the step, they run on every
device of that system with no model and no recording of its own.

An act with an action that has no pinned form, such as a drag or a click
where no element could be read, cannot be pinned; each such action is
listed with the reason instead. An act whose actions never used a variable
its task names, as one recorded where a field already showed the value, says
so in a comment above its steps: pinned, they leave that field as each run
finds it.

Identify the DAG by name or by YAML file path. Without --step, every step
of the DAG is shown. A task whose instruction changed, or that ran with
other parameters, has a recording for each; the newest comes first.

With --format json, the result is one JSON object holding operations, each
with step, op, kind, system, saved_at, assumes, which names those
variables, and either recorded, fields, or unpinnable, whose items carry
turn, action, code, and reason.

Examples:
  dagu computer cache show invoices                        # Every step
  dagu computer cache show invoices --step post --op 2     # One task of a step
  dagu computer cache show invoices --step post -f json    # For a program
`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{replayCacheStepFlag, computerCacheShowOpFlag, computerFormatFlag}, runComputerCacheShow))
	return cmd
}

// computerCacheShowOpFlag names one task of a step by its position in
// with.do, counted from 1 as the run log does.
var computerCacheShowOpFlag = commandLineFlag{
	name:  "op",
	usage: "With --step: show only the task at this position in with.do, counted from 1",
}

// computerCacheShowResult is the JSON output of dagu computer cache show.
type computerCacheShowResult struct {
	Operations []computerCacheOperation `json:"operations"`
}

// computerCacheOperation is one recording of a step's task in its pinned
// form.
type computerCacheOperation struct {
	Step string `json:"step"`
	computer.PinnedOperation
}

func runComputerCacheShow(ctx *Context, args []string) error {
	format, err := ctx.StringParam(computerFormatFlag.name)
	if err != nil {
		return fmt.Errorf("failed to get format: %w", err)
	}
	if format != "text" && format != "json" {
		return fmt.Errorf("invalid format %q: use text or json", format)
	}
	dagName, err := extractDAGName(ctx, args[0])
	if err != nil {
		return fmt.Errorf("failed to extract DAG name: %w", err)
	}
	step, err := ctx.StringParam(replayCacheStepFlag.name)
	if err != nil {
		return err
	}
	position := 0
	if opArg, err := ctx.StringParam(computerCacheShowOpFlag.name); err != nil {
		return err
	} else if opArg != "" {
		if step == "" {
			return errors.New("--op needs --step")
		}
		if position, err = strconv.Atoi(opArg); err != nil || position < 1 {
			return fmt.Errorf("--op must be a position in with.do from 1, not %q", opArg)
		}
	}
	ops, err := pinnedOperations(computerReplayCache(ctx), dagName, step, position)
	if err != nil {
		return err
	}
	out := ctx.Command.OutOrStdout()
	if format == "json" {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(computerCacheShowResult{Operations: ops})
	}
	return printPinnedOperations(out, dagName, step, ops)
}

// pinnedOperations reads the recordings of a DAG's steps, or of one step,
// in their pinned form: by step, then by task, newest first. A position
// above 0 keeps only that task.
func pinnedOperations(store *replaycache.Store, dagName, step string, position int) ([]computerCacheOperation, error) {
	steps := []string{step}
	if step == "" {
		var err error
		if steps, err = store.Steps(dagName); err != nil {
			return nil, fmt.Errorf("failed to list the computer replay cache of %q: %w", dagName, err)
		}
	}
	ops := []computerCacheOperation{}
	for _, s := range steps {
		entries, err := store.Entries(dagName, s)
		if err != nil {
			return nil, fmt.Errorf("failed to read the computer replay cache of step %q of %q: %w", s, dagName, err)
		}
		var found []computerCacheOperation
		for _, key := range slices.Sorted(maps.Keys(entries)) {
			pinned, ok := computer.PinRecording(entries[key])
			if ok && (position == 0 || pinned.Op == position) {
				found = append(found, computerCacheOperation{Step: s, PinnedOperation: pinned})
			}
		}
		slices.SortStableFunc(found, func(a, b computerCacheOperation) int {
			return cmp.Or(cmp.Compare(a.Op, b.Op), b.SavedAt.Compare(a.SavedAt))
		})
		ops = append(ops, found...)
	}
	return ops, nil
}

// printPinnedOperations writes each recording as the YAML to put in its
// step, or the actions that keep it from being pinned.
func printPinnedOperations(out io.Writer, dagName, step string, ops []computerCacheOperation) error {
	if len(ops) == 0 {
		if step != "" {
			_, err := fmt.Fprintf(out, "No computer recordings for step %q of DAG %q\n", step, dagName)
			return err
		}
		_, err := fmt.Fprintf(out, "No computer recordings for DAG %q\n", dagName)
		return err
	}
	for i, op := range ops {
		if i > 0 {
			_, _ = fmt.Fprintln(out)
		}
		header := fmt.Sprintf("Step %q, task %d (%s)", op.Step, op.Op, op.Kind)
		if !op.SavedAt.IsZero() {
			header += ", recorded " + op.SavedAt.Format(time.RFC3339)
		}
		if len(op.Unpinnable) > 0 {
			_, _ = fmt.Fprintf(out, "%s, cannot be pinned:\n", header)
			for _, action := range op.Unpinnable {
				_, _ = fmt.Fprintf(out, "  turn %d, %s: %s\n", action.Turn, action.Action, action.Reason)
			}
			continue
		}
		pin := map[string]any{"recorded": map[string]any{op.System: op.Recorded}}
		if op.Fields != nil {
			pin = map[string]any{"fields": map[string]any{op.System: op.Fields}}
		}
		data, err := json.Marshal(pin)
		if err != nil {
			return err
		}
		if data, err = yaml.JSONToYAML(data); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "%s:\n%s%s", header, assumesNote(op.Assumes), data); err != nil {
			return err
		}
	}
	return nil
}

// assumesNote is a YAML comment naming the variables a recording never used,
// which its pinned steps leave as each run finds them, or "" for none.
func assumesNote(names []string) string {
	if len(names) == 0 {
		return ""
	}
	placeholders := make([]string, len(names))
	for i, name := range names {
		placeholders[i] = "%" + name + "%"
	}
	return fmt.Sprintf("# The steps never use %s, which the task names; pinned, they leave each as a run finds it, whatever its value.\n", strings.Join(placeholders, ", "))
}

func computerReplayCache(ctx *Context) *replaycache.Store {
	return replaycache.New(filepath.Join(ctx.Config.Paths.DataDir, computerhost.DataDirName))
}
