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
	profileName       string
	definitionID      string
	step              string
	includeDownstream bool
	retryPath         dagrun.RetryPath
	preparedAttempt   dagrun.Attempt
	noReuse           bool
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
