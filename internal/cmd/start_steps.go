// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// selectedSteps is the step selection of a `dagu start --only` run.
type selectedSteps struct {
	steps       []string
	outputsFrom string
	// outputFrom names, by step, the run that the step carries its outputs
	// from instead of outputsFrom.
	outputFrom map[string]string
	// outputs are outputs of skipped steps, by step and then output name.
	outputs map[string]map[string]string
	// reuseFrom is the run whose unchanged steps the run reuses, choosing
	// the steps to run itself.
	reuseFrom string
}

// selectedStepsParams reads and validates --only, --outputs-from,
// --output-from, and the deprecated --output.
func selectedStepsParams(ctx *Context) (selectedSteps, error) {
	steps, err := ctx.Command.Flags().GetStringArray(onlyFlag.name)
	if err != nil {
		return selectedSteps{}, fmt.Errorf("failed to get only: %w", err)
	}
	outputsFrom, err := ctx.StringParam(outputsFromFlag.name)
	if err != nil {
		return selectedSteps{}, fmt.Errorf("failed to get outputs-from: %w", err)
	}
	entries, err := ctx.Command.Flags().GetStringArray(outputFromFlag.name)
	if err != nil {
		return selectedSteps{}, fmt.Errorf("failed to get output-from: %w", err)
	}
	outputEntries, err := ctx.Command.Flags().GetStringArray(outputFlag.name)
	if err != nil {
		return selectedSteps{}, fmt.Errorf("failed to get output: %w", err)
	}
	reuseFrom, err := ctx.StringParam(reuseFromFlag.name)
	if err != nil {
		return selectedSteps{}, fmt.Errorf("failed to get reuse-from: %w", err)
	}
	if reuseFrom = strings.TrimSpace(reuseFrom); reuseFrom == "" && ctx.Command.Flags().Changed(reuseFromFlag.name) {
		return selectedSteps{}, fmt.Errorf("--reuse-from must name a run")
	}
	if reuseFrom != "" {
		if len(steps) > 0 || outputsFrom != "" || len(entries) > 0 || len(outputEntries) > 0 || ctx.Command.Flags().Changed(fromRunIDFlag.name) {
			return selectedSteps{}, fmt.Errorf("--reuse-from chooses the steps to run itself; it cannot be combined with --only, --outputs-from, --output-from, --output, or --from-run-id")
		}
		if err := validateRunID(reuseFrom); err != nil {
			return selectedSteps{}, fmt.Errorf("invalid reuse-from: %w", err)
		}
		return selectedSteps{reuseFrom: reuseFrom}, nil
	}
	if len(steps) == 0 {
		if outputsFrom != "" {
			return selectedSteps{}, fmt.Errorf("--outputs-from requires --only")
		}
		if len(entries) > 0 {
			return selectedSteps{}, fmt.Errorf("--output-from requires --only")
		}
		if len(outputEntries) > 0 {
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
	outputFrom, err := parseOutputFrom(entries)
	if err != nil {
		return selectedSteps{}, err
	}
	outputs, err := parseStepOutputs(outputEntries)
	if err != nil {
		return selectedSteps{}, err
	}
	return selectedSteps{steps: steps, outputsFrom: outputsFrom, outputFrom: outputFrom, outputs: outputs}, nil
}

// reuseUnchangedSteps turns a selection reusing selection.reuseFrom into the
// steps to run, which intake.StepsToRun chooses, each other step carrying its
// outputs from that run, whose work directory the run takes too. When no step
// can be reused, or none changed, every step runs, from nothing.
func reuseUnchangedSteps(ctx context.Context, repo *persis.DAGRunRepository, dag *ir.DAG, profile string, selection selectedSteps) (selectedSteps, error) {
	attempt, err := repo.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, selection.reuseFrom))
	if err != nil {
		return selection, fmt.Errorf("failed to find dag-run %s for DAG %s: %w", selection.reuseFrom, dag.Name, err)
	}
	source, err := attempt.ReadStatus(ctx)
	if err != nil {
		return selection, fmt.Errorf("failed to read status for dag-run %s: %w", selection.reuseFrom, err)
	}
	if source.Status.IsActive() || source.Status == ir.NotStarted {
		return selection, fmt.Errorf("dag-run %s is %s; steps are reused only from a finished run", source.DAGRunID, source.Status)
	}
	sourceDAG, err := attempt.ReadDAG(ctx)
	if err != nil {
		return selection, fmt.Errorf("failed to read the definition of dag-run %s: %w", selection.reuseFrom, err)
	}
	all := make([]string, 0, len(dag.Steps))
	for _, step := range dag.Steps {
		all = append(all, step.Name)
	}
	steps, err := intake.StepsToRun(dag, profile, sourceDAG, source)
	if err != nil || len(steps) == 0 || len(steps) == len(all) {
		return selectedSteps{steps: all}, nil
	}
	reused := make(map[string]string, len(all)-len(steps))
	for _, name := range all {
		if !slices.Contains(steps, name) {
			reused[name] = selection.reuseFrom
		}
	}
	return selectedSteps{steps: steps, outputsFrom: selection.reuseFrom, outputFrom: reused, reuseFrom: selection.reuseFrom}, nil
}

// parseOutputFrom parses --output-from entries of the form <step>=<run-id>.
func parseOutputFrom(entries []string) (map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		step, runID, hasRun := strings.Cut(entry, "=")
		step = strings.TrimSpace(step)
		runID = strings.TrimSpace(runID)
		if !hasRun || step == "" || runID == "" {
			return nil, fmt.Errorf("invalid --output-from %q: expected <step>=<run-id>", entry)
		}
		if err := validateRunID(runID); err != nil {
			return nil, fmt.Errorf("invalid --output-from %q: %w", entry, err)
		}
		if _, exists := result[step]; exists {
			return nil, fmt.Errorf("--output-from names step %q more than once", step)
		}
		result[step] = runID
	}
	return result, nil
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
	if selection.reuseFrom != "" {
		var err error
		if selection, err = reuseUnchangedSteps(ctx, repo, dag, opts.profileName, selection); err != nil {
			return err
		}
	}
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
	fromRuns, err := intake.LoadRunStepSources(ctx, repo, dag.Name, selection.outputFrom, nil)
	if err != nil {
		return err
	}
	nodes, reused, err := intake.SelectedStepNodes(dag, selection.steps, source, fromRuns, selection.outputs, nil)
	if err != nil {
		return err
	}
	skipHandlers, err := intake.SelectionSkipsHandlers(dag, selection.steps)
	if err != nil {
		return err
	}
	seed := intake.SeedRequest{
		DAGRunRepository: repo,
		DAG:              dag,
		DAGRunID:         dagRunID,
		Nodes:            nodes,
		ReusedInputs:     reused,
		SkipHandlers:     skipHandlers,
		Source:           source,
		Params:           params,
		TriggerType:      opts.triggerType,
		TriggerActor:     opts.triggerActor,
		ProfileName:      opts.profileName,
		DefinitionID:     opts.definitionID,
		NoReuse:          opts.noReuse,
		ScheduleTime:     opts.scheduleTime,
		ScheduleWindow:   opts.scheduleWindow,
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
