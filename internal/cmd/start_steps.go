// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// selectedSteps is the step selection of a `dagu start --only` run.
type selectedSteps struct {
	steps       []string
	outputsFrom string
	// outputs are outputs of skipped steps, by step and then output name.
	outputs map[string]map[string]string
}

// selectedStepsParams reads and validates --only, --outputs-from, and
// --output.
func selectedStepsParams(ctx *Context) (selectedSteps, error) {
	steps, err := ctx.Command.Flags().GetStringArray(onlyFlag.name)
	if err != nil {
		return selectedSteps{}, fmt.Errorf("failed to get only: %w", err)
	}
	outputsFrom, err := ctx.StringParam(outputsFromFlag.name)
	if err != nil {
		return selectedSteps{}, fmt.Errorf("failed to get outputs-from: %w", err)
	}
	entries, err := ctx.Command.Flags().GetStringArray(outputFlag.name)
	if err != nil {
		return selectedSteps{}, fmt.Errorf("failed to get output: %w", err)
	}
	if len(steps) == 0 {
		if outputsFrom != "" {
			return selectedSteps{}, fmt.Errorf("--outputs-from requires --only")
		}
		if len(entries) > 0 {
			return selectedSteps{}, fmt.Errorf("--output requires --only")
		}
		return selectedSteps{}, nil
	}
	if ctx.Command.Flags().Changed(fromRunIDFlag.name) {
		return selectedSteps{}, fmt.Errorf("--only cannot be combined with --from-run-id")
	}
	if outputsFrom != "" {
		if err := validateRunID(outputsFrom); err != nil {
			return selectedSteps{}, fmt.Errorf("invalid outputs-from: %w", err)
		}
	}
	outputs, err := parseStepOutputs(entries)
	if err != nil {
		return selectedSteps{}, err
	}
	return selectedSteps{steps: steps, outputsFrom: outputsFrom, outputs: outputs}, nil
}

// parseStepOutputs parses --output entries of the form <step>.<name>=<value>.
// Output names never contain a dot, so the step is everything before the last
// one. Errors quote only the part before "=", since values may be secrets.
func parseStepOutputs(entries []string) (map[string]map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	outputs := make(map[string]map[string]string)
	for _, entry := range entries {
		key, value, hasValue := strings.Cut(entry, "=")
		dot := strings.LastIndex(key, ".")
		if !hasValue || dot < 0 {
			return nil, fmt.Errorf("invalid --output %q: expected <step>.<name>=<value>", key)
		}
		step := strings.TrimSpace(key[:dot])
		name := strings.TrimSpace(key[dot+1:])
		if step == "" || name == "" {
			return nil, fmt.Errorf("invalid --output %q: expected <step>.<name>=<value>", key)
		}
		if _, exists := outputs[step][name]; exists {
			return nil, fmt.Errorf("--output sets %q more than once", key)
		}
		if outputs[step] == nil {
			outputs[step] = make(map[string]string)
		}
		outputs[step][name] = value
	}
	return outputs, nil
}

// runSelectedSteps starts a new run of dag in which only the selected steps
// execute. The run is seeded as a queued attempt and executed through the
// retry path, which leaves the seeded skipped steps alone.
func runSelectedSteps(ctx *Context, dag *ir.DAG, dagRunID, params string, opts runOptions, selection selectedSteps) error {
	repo := ctx.Persistence.DAGRunRepository
	var source *ir.DAGRunStatus
	if selection.outputsFrom != "" {
		attempt, err := repo.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, selection.outputsFrom))
		if err != nil {
			return fmt.Errorf("failed to find dag-run %s for DAG %s: %w", selection.outputsFrom, dag.Name, err)
		}
		source, err = attempt.ReadStatus(ctx)
		if err != nil {
			return fmt.Errorf("failed to read status for dag-run %s: %w", selection.outputsFrom, err)
		}
	}
	nodes, err := intake.SelectedStepNodes(dag, selection.steps, source, selection.outputs)
	if err != nil {
		return err
	}
	seed := intake.SeedRequest{
		DAGRunRepository: repo,
		DAG:              dag,
		DAGRunID:         dagRunID,
		Nodes:            nodes,
		Source:           source,
		Params:           params,
		TriggerType:      opts.triggerType,
		TriggerActor:     opts.triggerActor,
		ProfileName:      opts.profileName,
		DefinitionID:     opts.definitionID,
		NoReuse:          opts.noReuse,
		LogBaseDir:       ctx.Config.Paths.LogDir,
		ArtifactBaseDir:  ctx.Config.Paths.ArtifactDir,
	}

	coordinatorCli, err := ctx.NewCoordinatorClient()
	if err != nil {
		return err
	}
	if dispatch.ShouldDispatchToCoordinator(dag, coordinatorCli != nil, ctx.Config.DefaultExecMode) {
		if dag.Type == ir.TypeBuild {
			return dispatch.ErrBuildRequiresLocal
		}
		_, status, err := intake.SeedRun(ctx, seed)
		if err != nil {
			return err
		}
		opts.seed = status
		return dispatchToCoordinatorAndWait(ctx, dag, dagRunID, opts, coordinatorCli)
	}

	var status *ir.DAGRunStatus
	err = withPreparedLocalExecution(
		ctx,
		dag,
		dagRunID,
		opts,
		func(execCtx context.Context) (dagrun.Attempt, error) {
			attempt, seeded, err := intake.SeedRun(execCtx, seed)
			status = seeded
			return attempt, err
		},
		func(preparedAttempt dagrun.Attempt) error {
			run := opts
			run.preparedAttempt = preparedAttempt
			return executeRetry(ctx, dag, status, run)
		},
	)
	if err != nil {
		intake.MarkSeedFailed(ctx, repo, status, err)
	}
	return err
}
