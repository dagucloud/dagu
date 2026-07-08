// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"os"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
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
	mode history.PrepareAttemptMode,
	attemptID string,
	attemptOptions exec.NewDAGRunAttemptOptions,
	run func(*history.ExecutionContext) error,
) error {
	historySvc := history.New(history.Config{
		DAGRunStore:     ctx.DAGRunStore,
		ProcStore:       ctx.ProcStore,
		LogBaseDir:      ctx.Config.Paths.LogDir,
		ArtifactBaseDir: ctx.Config.Paths.ArtifactDir,
	})
	prepared, err := historySvc.PrepareLocalAttempt(ctx.Context, history.PrepareLocalAttemptCommand{
		DAG:            dag,
		DAGRunID:       dagRunID,
		Root:           root,
		Parent:         parent,
		TriggerType:    triggerType,
		ScheduleTime:   scheduleTime,
		ProfileName:    profileName,
		Mode:           mode,
		AttemptID:      attemptID,
		AttemptOptions: attemptOptions,
	})
	if err != nil {
		logger.Debug(ctx, "Failed to prepare local execution", tag.Error(err))
		return err
	}

	prevProc := ctx.Proc
	ctx.Proc = prepared.Execution.ProcHandle()
	defer func() {
		ctx.Proc = prevProc
		_ = prepared.Execution.Release(ctx)
	}()

	return run(prepared.Execution)
}

func openExecutionLogFile(ctx *Context, prepared *history.ExecutionContext, dag *core.DAG, dagRunID string) (*os.File, error) {
	if prepared != nil && prepared.LogFile() != "" {
		return fileutil.OpenOrCreateFile(prepared.LogFile())
	}
	return ctx.OpenLogFile(dag, dagRunID)
}

func executionArtifactDir(ctx *Context, prepared *history.ExecutionContext, dag *core.DAG, dagRunID string) (string, error) {
	if prepared != nil {
		return prepared.ArtifactDir(), nil
	}
	return ctx.GenArtifactDir(dag, dagRunID)
}
