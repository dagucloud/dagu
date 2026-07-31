// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/dagrun/intake"
)

type runOptions struct {
	root            exec.DAGRunRef
	parent          exec.DAGRunRef
	workerID        string
	attemptID       string
	triggerType     core.TriggerType
	triggerActor    string
	scheduleTime    string
	profileName     string
	step            string
	retryPath       exec.RetryPath
	preparedAttempt exec.DAGRunAttempt
}

func withPreparedLocalExecution(
	ctx *Context,
	dag *core.DAG,
	dagRunID string,
	opts runOptions,
	buildAttempt func(context.Context) (exec.DAGRunAttempt, error),
	run func(exec.DAGRunAttempt) error,
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
