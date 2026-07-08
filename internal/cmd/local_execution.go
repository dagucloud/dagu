// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/history"
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
	historySvc := history.New(history.Config{
		ProcStore:       ctx.ProcStore,
		LogBaseDir:      ctx.Config.Paths.LogDir,
		ArtifactBaseDir: ctx.Config.Paths.ArtifactDir,
	})
	prepared, err := historySvc.PrepareLocalAttempt(ctx.Context, history.PrepareLocalAttemptCommand{
		DAG:          dag,
		DAGRunID:     dagRunID,
		Root:         root,
		Parent:       parent,
		TriggerType:  triggerType,
		ScheduleTime: scheduleTime,
		ProfileName:  profileName,
		BuildAttempt: buildAttempt,
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
