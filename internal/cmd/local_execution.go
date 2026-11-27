// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"os"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
)

type runOptions struct {
	root              ir.DAGRunRef
	parent            ir.DAGRunRef
	workerID          string
	attemptID         string
	triggerType       ir.TriggerType
	triggerActor      string
	parallelItem      string
	scheduleTime      string
	scheduleWindow    ir.ScheduleWindow
	dates             []ir.DateValue
	defaultSlot       bool
	profileName       string
	definitionID      string
	step              string
	includeDownstream bool
	retryPath         dagrun.RetryPath

	bypassPreconditions bool
	preparedAttempt     dagrun.Attempt
	noReuse             bool
	recursiveOutput     bool
	// targetNodes narrows the run's steps with nodes (RFC 013).
	targetNodes []string
	// seed is the queued status of a selected-steps run, which is dispatched
	// as a retry of that attempt.
	seed *ir.DAGRunStatus
	// retried is the status of a run that a retry dispatches to its worker.
	retried *ir.DAGRunStatus
	// boundWorker is the worker that runs a run bound to a worker node.
	boundWorker string
	// secretValues, when non-nil, holds the value of each DAG secret in place
	// of its provider.
	secretValues map[string]string
	// redirectRoot moves the step redirect files of a test case into its case
	// directory.
	redirectRoot runtime.RedirectRoot
	// reportNoTrigger leaves the trigger out of a run's job-run reports, for
	// a run dagu start begins as a retry, such as a selected-steps run,
	// without --trigger-type.
	reportNoTrigger bool
	// testSideEffects, when non-nil, runs a test case without the run-level
	// side effects of a real run, among them a job-run report to Dagu
	// Console with a run key.
	testSideEffects *agent.TestSideEffects
	// executorOverride replaces the actions of the mocked steps of a test
	// case.
	executorOverride runtime.ExecutorOverride
	// conditionOverride scripts the conditions of a test case that would run
	// a command, and bounds its steps that repeat without a limit.
	conditionOverride runtime.ConditionOverride
	// skipWaits runs a test case without waiting for retry and repeat
	// intervals or the start delay, except for the steps in keepWaits.
	skipWaits bool
	keepWaits []string
}

// prepareTools prepares the tools of dag for one attempt of the run. A
// workflow-test case installs them only when it has a real step.
func (o runOptions) prepareTools(ctx *Context, dag *ir.DAG) ([]string, error) {
	if o.testSideEffects != nil {
		return prepareTestDAGTools(ctx, dag, o.testSideEffects.HasRealSteps)
	}
	return prepareDAGTools(ctx, dag)
}

// withTestOptions returns agentOpts with the options a workflow-test case
// gives every attempt of its run. Outside a test case, they are all empty.
func (o runOptions) withTestOptions(agentOpts agent.Options) agent.Options {
	agentOpts.SecretValues = o.secretValues
	agentOpts.RedirectRoot = o.redirectRoot
	agentOpts.TestSideEffects = o.testSideEffects
	agentOpts.ExecutorOverride = o.executorOverride
	agentOpts.ConditionOverride = o.conditionOverride
	agentOpts.SkipWaits = o.skipWaits
	agentOpts.KeepWaits = o.keepWaits
	return agentOpts
}

func dagDefinitionIDFromEnv() string {
	return os.Getenv(runenv.EnvKeyDAGDefinitionID)
}

func parallelItemFromEnv(env []string) string {
	prefix := runenv.EnvKeyParallelItem + "="
	for _, e := range slices.Backward(env) {
		if after, ok := strings.CutPrefix(e, prefix); ok {
			return after
		}
	}
	return ""
}

func withPreparedLocalExecution(
	ctx *Context,
	dag *ir.DAG,
	dagRunID string,
	opts runOptions,
	buildAttempt func(context.Context) (dagrun.Attempt, error),
	run func(dagrun.Attempt) error,
) error {
	prepared, err := intake.PrepareLocalExecution(ctx.Context, intake.LocalRequest{
		ProcRepository:  ctx.Persistence.ProcRepository,
		DAG:             dag,
		DAGRunID:        dagRunID,
		DefinitionID:    opts.definitionID,
		Root:            opts.root,
		Parent:          opts.parent,
		TriggerType:     opts.triggerType,
		TriggerActor:    opts.triggerActor,
		ScheduleTime:    opts.scheduleTime,
		ScheduleWindow:  opts.scheduleWindow,
		ProfileName:     opts.profileName,
		LogBaseDir:      ctx.projectPaths().LogDir,
		ArtifactBaseDir: ctx.projectPaths().ArtifactDir,
		BuildAttempt:    buildAttempt,
	})
	if err != nil {
		logger.Debug(ctx, "Failed to prepare local execution", tag.Error(err))
		return err
	}

	prevProc := ctx.Proc
	ctx.Proc = prepared.Proc
	defer func() {
		ctx.Proc = prevProc
		// A record left behind makes the run look alive until it goes stale,
		// so the failure is logged. It does not change the run's result.
		if err := prepared.Proc.Stop(ctx); err != nil {
			logger.Warn(ctx, "Failed to release the process record of the dag-run",
				tag.DAG(dag.Name),
				tag.RunID(dagRunID),
				tag.Error(err),
			)
		}
	}()

	return run(prepared.Attempt)
}
