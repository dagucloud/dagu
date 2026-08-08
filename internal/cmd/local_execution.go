// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dagrun/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

type runOptions struct {
	root            dagrun.DAGRunRef
	parent          dagrun.DAGRunRef
	workerID        string
	attemptID       string
	triggerType     ir.TriggerType
	triggerActor    string
	scheduleTime    string
	profileName     string
	step            string
	retryPath       dagrun.RetryPath
	preparedAttempt dagrun.DAGRunAttempt
	noReuse         bool
}

func withPreparedLocalExecution(
	ctx *Context,
	dag *ir.DAG,
	dagRunID string,
	opts runOptions,
	buildAttempt func(context.Context) (dagrun.DAGRunAttempt, error),
	run func(dagrun.DAGRunAttempt) error,
) error {
	prepared, err := intake.PrepareLocalExecution(ctx.Context, intake.LocalRequest{
		ProcStore:       ctx.ProcStore,
		DAG:             dag,
		DAGRunID:        dagRunID,
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
