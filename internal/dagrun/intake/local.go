// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"context"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/history"
)

var (
	ErrLocalExecutionAlreadyExists = history.ErrLocalExecutionAlreadyExists
	ErrProcAcquisitionFailed       = history.ErrProcAcquisitionFailed
)

// LocalAttemptBuilder creates or resolves the attempt that a local execution
// will own.
type LocalAttemptBuilder = history.LocalAttemptBuilder

// LocalProcStore is the proc-store surface needed to claim local execution
// ownership.
type LocalProcStore = history.LocalProcStore

// LocalRequest describes local DAG-run intake before execution starts.
type LocalRequest struct {
	ProcStore LocalProcStore
	DAG       *core.DAG
	DAGRunID  string

	Root        exec.DAGRunRef
	Parent      exec.DAGRunRef
	TriggerType core.TriggerType

	ScheduleTime string
	ProfileName  string

	LogBaseDir      string
	ArtifactBaseDir string

	BuildAttempt LocalAttemptBuilder
}

// LocalPreparation is the successfully prepared local execution ownership.
type LocalPreparation = history.PreparedLocalAttempt

// PrepareLocalExecution preserves the legacy local execution preparation API.
func PrepareLocalExecution(ctx context.Context, req LocalRequest) (*LocalPreparation, error) {
	historySvc := history.New(history.Config{
		ProcStore:       req.ProcStore,
		LogBaseDir:      req.LogBaseDir,
		ArtifactBaseDir: req.ArtifactBaseDir,
	})
	return historySvc.PrepareLocalAttempt(ctx, history.PrepareLocalAttemptCommand{
		DAG:          req.DAG,
		DAGRunID:     req.DAGRunID,
		Root:         req.Root,
		Parent:       req.Parent,
		TriggerType:  req.TriggerType,
		ScheduleTime: req.ScheduleTime,
		ProfileName:  req.ProfileName,
		BuildAttempt: req.BuildAttempt,
	})
}
