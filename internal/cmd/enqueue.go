// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"log/slog"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/dagrun/intake"
	"github.com/spf13/cobra"
)

// Enqueue returns the cobra command for queueing a DAG-run.
func Enqueue() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "enqueue [flags] <DAG definition> [-- param1 param2 ...]",
			Short: "Enqueue a DAG-run to the queue.",
			Long: `Enqueue a DAG-run to the queue.

Examples:
	dagu enqueue --run-id=run_id my_dag -- P1=foo P2=bar
	dagu enqueue --name my_custom_name my_dag.yaml -- P1=foo P2=bar
`,
			Args: cobra.MinimumNArgs(1),
		}, enqueueFlags, runEnqueue,
	)
}

var enqueueFlags = []commandLineFlag{paramsFlag, nameFlag, dagRunIDFlag, queueFlag, labelsFlag, tagsFlag, defaultWorkingDirFlag, profileFlag, triggerTypeFlag, scheduleTimeFlag}

func runEnqueue(ctx *Context, args []string) error {
	if ctx.IsRemote() {
		return remoteRunEnqueue(ctx, args)
	}
	runID, err := ctx.StringParam("run-id")
	if err != nil {
		return fmt.Errorf("failed to get Run ID: %w", err)
	}

	if runID == "" {
		runID, err = genRunID()
		if err != nil {
			return fmt.Errorf("failed to generate Run ID: %w", err)
		}
	} else if err := validateRunID(runID); err != nil {
		return fmt.Errorf("invalid Run ID: %w", err)
	}

	queueOverride, err := ctx.StringParam("queue")
	if err != nil {
		return fmt.Errorf("failed to get queue override: %w", err)
	}

	dag, _, err := loadDAGWithParams(ctx, args, false)
	if err != nil {
		return err
	}

	if queueOverride != "" {
		dag.Queue = queueOverride
	}

	if err := parseAndAppendLabels(ctx, dag); err != nil {
		return err
	}

	triggerType, err := parseTriggerTypeParam(ctx)
	if err != nil {
		return err
	}

	scheduleTime, err := parseScheduleTimeParam(ctx)
	if err != nil {
		return err
	}
	profileName, err := runtimeProfileNameParam(ctx)
	if err != nil {
		return err
	}

	return enqueueDAGRun(ctx, dag, runID, triggerType, scheduleTime, profileName)
}

// enqueueDAGRun enqueues a dag-run to the queue.
// The DAG location is cleared to allow concurrent queued runs (location is used
// for unix pipe generation which would prevent parallel execution).
func enqueueDAGRun(ctx *Context, dag *core.DAG, dagRunID string, triggerType core.TriggerType, scheduleTime, profileName string) error {
	dag.Location = ""

	if !ctx.Config.Queues.Enabled {
		return fmt.Errorf("queues are disabled in configuration")
	}

	dagRun := exec.NewDAGRunRef(dag.Name, dagRunID)

	if _, err := ctx.DAGRunStore.FindAttempt(ctx, dagRun); err == nil {
		return fmt.Errorf("DAG %q with ID %q already exists", dag.Name, dagRunID)
	}

	queued, err := intake.EnqueueRun(ctx.Context, intake.QueueRequest{
		DAGRunStore:             ctx.DAGRunStore,
		QueueStore:              ctx.QueueStore,
		DAG:                     dag,
		DAGRunID:                dagRunID,
		LogBaseDir:              ctx.Config.Paths.LogDir,
		ArtifactBaseDir:         ctx.Config.Paths.ArtifactDir,
		TriggerType:             triggerType,
		ScheduleTime:            scheduleTime,
		ProfileName:             profileName,
		ProceedOnStatusCloseErr: true,
	})
	if err != nil {
		return err
	}
	if queued.StatusCloseErr != nil {
		logger.Warn(ctx.Context, "Failed to close queued status before enqueue",
			tag.Error(queued.StatusCloseErr))
	}

	logger.Info(ctx.Context, "Enqueued dag-run",
		tag.DAG(dag.Name),
		tag.RunID(dagRunID),
		slog.Any("params", dag.Params),
	)

	return nil
}
