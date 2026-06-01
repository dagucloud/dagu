// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/dagrun/intake"
)

func withPreparedLocalExecution(
	ctx *Context,
	dag *core.DAG,
	dagRunID string,
	root exec.DAGRunRef,
	parent exec.DAGRunRef,
	triggerType core.TriggerType,
	scheduleTime string,
	profileName string,
	buildAttempt func(context.Context) (exec.DAGRunAttempt, error),
	run func(exec.DAGRunAttempt) error,
) error {
	prepared, err := intake.PrepareLocalExecution(ctx.Context, intake.LocalRequest{
		ProcStore:       ctx.ProcStore,
		DAG:             dag,
		DAGRunID:        dagRunID,
		Root:            root,
		Parent:          parent,
		TriggerType:     triggerType,
		ScheduleTime:    scheduleTime,
		ProfileName:     profileName,
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
