// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"path/filepath"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
	"github.com/spf13/cobra"
)

var (
	writesStepFlag = commandLineFlag{
		name:  "step",
		usage: "Only this step (step ID, or step name when the step has no ID)",
	}
	writesSignatureFlag = commandLineFlag{
		name:          "signature",
		isStringArray: true,
		usage:         "Only the commit points of this write signature; repeat for more",
	}
	writesFromDAGFlag = commandLineFlag{
		name:  "from-dag",
		usage: "Copy the commit points of this DAG, such as the one a rehearsal ran as, and approve them",
	}
)

func browserWritesCommand() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "writes",
		Short: "Manage the commit points that let browser steps send writes",
		Long: `A commit point is an operation of a browser step, an act or an upload, and
the kind of write it sends, named by its signature. A rehearsal, or a write
the gate blocked, keeps the operation as a commit point to approve. A step
whose write gate records sends only the writes its approved commit points
name, each from its own operation, and replays that act's recorded actions
without asking a model.

Commit points are kept on the host that ran the step. Identify the DAG by
name or by YAML file path.
`,
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})
	cmd.AddCommand(browserWritesListCommand(), browserWritesApproveCommand(), browserWritesRevokeCommand())
	return cmd
}

func browserWritesListCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "list [flags] <DAG>",
		Short: "List the commit points of a DAG's browser steps",
		Args:  cobra.ExactArgs(1),
	}, []commandLineFlag{writesStepFlag}, runBrowserWritesList)
}

func browserWritesApproveCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "approve [flags] <DAG>",
		Short: "Approve the commit points of a DAG's browser steps",
		Long: `Approve the commit points of the DAG's steps, or of --step, or only those of
the --signature given. With --from-dag, the points of that DAG's steps, such
as those of a rehearsal run under another name, are copied into the DAG's
steps of the same name and approved.

Examples:
  dagu browser writes approve books --step enter
  dagu browser writes approve books --from-dag books-trial
`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{writesStepFlag, writesSignatureFlag, writesFromDAGFlag}, runBrowserWritesApprove)
}

func browserWritesRevokeCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "revoke [flags] <DAG>",
		Short: "Withdraw the approval of a DAG's commit points",
		Args:  cobra.ExactArgs(1),
	}, []commandLineFlag{writesStepFlag, writesSignatureFlag}, runBrowserWritesRevoke)
}

// commitChange is a commit point an approval or a revocation changed.
type commitChange struct {
	Step      string `json:"step"`
	Op        int    `json:"op"`
	Operation string `json:"operation"`
	Signature string `json:"signature"`
}

func commitStore(ctx *Context) *writegate.CommitStore {
	return writegate.NewCommitStore(filepath.Join(ctx.Config.Paths.DataDir, browserhost.DataDirName))
}

// commitSteps returns --step, or every step of dag with commit points.
func commitSteps(ctx *Context, store *writegate.CommitStore, dag string) ([]string, error) {
	step, err := ctx.StringParam(writesStepFlag.name)
	if err != nil || step != "" {
		return []string{step}, err
	}
	return store.Steps(dag)
}

func runBrowserWritesList(ctx *Context, args []string) error {
	dag, err := extractDAGName(ctx, args[0])
	if err != nil {
		return err
	}
	store := commitStore(ctx)
	steps, err := commitSteps(ctx, store, dag)
	if err != nil {
		return err
	}
	listed := map[string][]writegate.CommitPoint{}
	for _, step := range steps {
		points, err := store.Load(dag, step)
		if err != nil {
			return err
		}
		listed[step] = nonNilPoints(points)
	}
	return writeIndentedJSON(ctx.Command.OutOrStdout(), map[string]any{"dag": dag, "steps": listed})
}

func runBrowserWritesApprove(ctx *Context, args []string) error {
	dag, err := extractDAGName(ctx, args[0])
	if err != nil {
		return err
	}
	from := dag
	if source, _ := ctx.Command.Flags().GetString(writesFromDAGFlag.name); source != "" {
		if from, err = extractDAGName(ctx, source); err != nil {
			return err
		}
	}
	signatures, _ := ctx.Command.Flags().GetStringArray(writesSignatureFlag.name)
	store := commitStore(ctx)
	steps, err := commitSteps(ctx, store, from)
	if err != nil {
		return err
	}
	changed := []commitChange{}
	for _, step := range steps {
		var copied []writegate.CommitPoint
		match := func(point writegate.CommitPoint) bool { return chosen(point, signatures) }
		if from != dag {
			points, err := store.Load(from, step)
			if err != nil {
				return err
			}
			for _, point := range points {
				if chosen(point, signatures) {
					point.Approved, point.ApprovedAt = false, nil
					copied = append(copied, point)
				}
			}
			// Only what was copied is approved, not the DAG's other points.
			match = func(point writegate.CommitPoint) bool {
				return slices.ContainsFunc(copied, func(c writegate.CommitPoint) bool { return c.Key == point.Key && c.Signature == point.Signature })
			}
		}
		approved, err := store.Approve(ctx, dag, step, copied, match, time.Now())
		if err != nil {
			return err
		}
		changed = append(changed, changes(step, approved)...)
	}
	return writeIndentedJSON(ctx.Command.OutOrStdout(), map[string]any{"dag": dag, "approved": changed})
}

func runBrowserWritesRevoke(ctx *Context, args []string) error {
	dag, err := extractDAGName(ctx, args[0])
	if err != nil {
		return err
	}
	signatures, _ := ctx.Command.Flags().GetStringArray(writesSignatureFlag.name)
	store := commitStore(ctx)
	steps, err := commitSteps(ctx, store, dag)
	if err != nil {
		return err
	}
	changed := []commitChange{}
	for _, step := range steps {
		revoked, err := store.Revoke(ctx, dag, step, func(point writegate.CommitPoint) bool { return chosen(point, signatures) })
		if err != nil {
			return err
		}
		changed = append(changed, changes(step, revoked)...)
	}
	return writeIndentedJSON(ctx.Command.OutOrStdout(), map[string]any{"dag": dag, "revoked": changed})
}

// chosen reports whether a point is one of signatures, or any point when
// none is given.
func chosen(point writegate.CommitPoint, signatures []string) bool {
	return len(signatures) == 0 || slices.Contains(signatures, point.Signature)
}

func changes(step string, points []writegate.CommitPoint) []commitChange {
	out := make([]commitChange, 0, len(points))
	for _, point := range points {
		out = append(out, commitChange{Step: step, Op: point.Op, Operation: point.Operation, Signature: point.Signature})
	}
	return out
}

func nonNilPoints(points []writegate.CommitPoint) []writegate.CommitPoint {
	if points == nil {
		return []writegate.CommitPoint{}
	}
	return points
}
