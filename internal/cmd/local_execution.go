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
	// seed is the queued status of a selected-steps run, which is dispatched
	// as a retry of that attempt.
	seed *ir.DAGRunStatus
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
		LogBaseDir:      ctx.Config.Paths.LogDir,
		ArtifactBaseDir: ctx.Config.Paths.ArtifactDir,
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
		_ = prepared.Proc.Stop(ctx)
	}()

	return run(prepared.Attempt)
}
