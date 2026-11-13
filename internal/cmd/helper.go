// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/rundates"
	"github.com/dagucloud/dagu/v2/internal/runtimeenv"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// parseTriggerTypeParam parses and validates the trigger-type flag from the command context.
// Returns TriggerTypeUnknown (zero value) if the flag is empty, otherwise validates
// that the provided value is a known trigger type.
func parseTriggerTypeParam(ctx *Context) (ir.TriggerType, error) {
	triggerTypeStr, err := ctx.StringParam("trigger-type")
	if err != nil {
		logger.Debug(ctx, "Failed to read trigger-type flag", tag.Error(err))
	}
	if triggerTypeStr == "" {
		return ir.TriggerTypeUnknown, nil
	}

	triggerType := ir.ParseTriggerType(triggerTypeStr)
	if triggerType == ir.TriggerTypeUnknown {
		return ir.TriggerTypeUnknown, fmt.Errorf(
			"invalid trigger-type %q: must be one of scheduler, manual, webhook, subdag, retry, catchup, restart",
			triggerTypeStr,
		)
	}

	return triggerType, nil
}

func labelsParam(ctx *Context) (string, error) {
	labels, err := ctx.StringParam("labels")
	if err != nil {
		return "", fmt.Errorf("failed to get labels: %w", err)
	}
	tags, err := ctx.StringParam("tags")
	if err != nil {
		return "", fmt.Errorf("failed to get deprecated tags: %w", err)
	}

	labelsChanged := ctx.Command.Flags().Changed("labels")
	tagsChanged := ctx.Command.Flags().Changed("tags")
	if labelsChanged && tagsChanged {
		return "", fmt.Errorf("labels and deprecated tags cannot both be set")
	}
	if labelsChanged {
		return labels, nil
	}
	return tags, nil
}

// parseScheduleTimeParam reads and validates the --schedule-time flag.
// Returns the validated RFC 3339 string or empty if not set.
func parseScheduleTimeParam(ctx *Context) (string, error) {
	scheduleTime, err := ctx.StringParam("schedule-time")
	if err != nil {
		return "", fmt.Errorf("failed to get schedule-time: %w", err)
	}
	if scheduleTime != "" {
		if _, parseErr := time.Parse(time.RFC3339, scheduleTime); parseErr != nil {
			return "", fmt.Errorf("invalid --schedule-time value %q: must be RFC 3339 format: %w", scheduleTime, parseErr)
		}
	}
	return scheduleTime, nil
}

// runSlot is the schedule slot a run is started for.
type runSlot struct {
	scheduleTime string
	window       ir.ScheduleWindow
	// operatorChosen reports a slot named with --scheduled-at, not by the
	// scheduler.
	operatorChosen bool
	// isDefault reports the default slot of a run that named none.
	isDefault bool
}

// withDefaultSlot gives a run that names no slot the default slot, which a
// DAG with dates gets so that its dates follow its schedule.
func withDefaultSlot(ctx *Context, dag *ir.DAG, profile string, slot runSlot) runSlot {
	if slot.scheduleTime != "" {
		return slot
	}
	def, ok := rundates.DefaultSlot(dag, profile, ctx.Config.Core.Location, spec.NewCalendarSource(ctx.Config.Paths.CalendarsDir), time.Now())
	if !ok {
		return slot
	}
	return runSlot{scheduleTime: def.ScheduleTime, window: def.Window, isDefault: true}
}

// runSlotParams reads the slot a run is started for: --schedule-time, which
// the scheduler sets, or --scheduled-at, which an operator sets. The window
// is computed among the DAG's start schedules for the profile. A slot later
// than now is rejected unless allowFuture. A warning about a time no
// schedule fires is printed to stderr.
func runSlotParams(ctx *Context, dag *ir.DAG, profile string, allowFuture bool) (runSlot, error) {
	var scheduleTime string
	if ctx.Command.Flags().Lookup(scheduleTimeFlag.name) != nil {
		var err error
		if scheduleTime, err = parseScheduleTimeParam(ctx); err != nil {
			return runSlot{}, err
		}
	}
	scheduledAt, err := ctx.StringParam("scheduled-at")
	if err != nil {
		return runSlot{}, fmt.Errorf("failed to get scheduled-at: %w", err)
	}
	switch {
	case scheduledAt != "" && scheduleTime != "":
		return runSlot{}, fmt.Errorf("--scheduled-at cannot be combined with --schedule-time")
	case scheduleTime != "":
		at, _ := time.Parse(time.RFC3339, scheduleTime)
		window := ir.ScheduleWindowAt(boundStartSchedules(ctx, dag), profile, ctx.Config.Core.Location, at)
		return runSlot{scheduleTime: scheduleTime, window: window}, nil
	case scheduledAt == "":
		return runSlot{}, nil
	}

	slot, err := ir.ResolveScheduleSlot(ir.ScheduleSlotRequest{
		Value:       scheduledAt,
		Schedules:   boundStartSchedules(ctx, dag),
		Profile:     profile,
		Location:    ctx.Config.Core.Location,
		Now:         time.Now(),
		AllowFuture: allowFuture,
	})
	if err != nil {
		return runSlot{}, fmt.Errorf("invalid --scheduled-at: %w", err)
	}
	if slot.Warning != "" {
		_, _ = fmt.Fprintf(ctx.Command.ErrOrStderr(), "Warning: %s\n", slot.Warning)
	}
	return runSlot{
		scheduleTime:   stringutil.FormatTime(slot.At.In(ctx.Config.Core.Location)),
		window:         slot.Window,
		operatorChosen: true,
	}, nil
}

// slotDates resolves the dates of a run being created for slot. An error
// refuses the run before it exists.
func slotDates(ctx *Context, dag *ir.DAG, profile string, slot runSlot) ([]ir.DateValue, error) {
	return rundates.Resolve(rundates.Request{
		DAG:          dag,
		Profile:      profile,
		ScheduleTime: slot.scheduleTime,
		Window:       slot.window,
		CreatedAt:    time.Now(),
		Location:     ctx.Config.Core.Location,
		Calendars:    spec.NewCalendarSource(ctx.Config.Paths.CalendarsDir),
	})
}

// boundStartSchedules returns a copy of the DAG's start schedules with their
// calendars bound, so they fire as the scheduler fires them.
func boundStartSchedules(ctx *Context, dag *ir.DAG) []ir.Schedule {
	schedules := slices.Clone(dag.Schedule)
	spec.BindScheduleCalendars(schedules, spec.NewCalendarSource(ctx.Config.Paths.CalendarsDir))
	return schedules
}

// restoreDAGFromStatus restores a DAG from a previous run's status and YAML.
// It restores params from the status, loads dotenv, and rebuilds fields excluded
// from JSON serialization (env, params JSON, registryAuths, etc.).
func restoreDAGFromStatus(ctx context.Context, dag *ir.DAG, status *ir.DAGRunStatus, repository *persis.DAGRunRepository) (*ir.DAG, error) {
	cfg := config.GetConfig(ctx)
	opts := []spec.LoadOption{spec.WithBaseConfig(cfg.Paths.BaseConfig)}
	if cfg.Paths.DAGsDir != "" {
		opts = append(opts, spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(cfg.Paths.DAGsDir)))
	}
	dag, err := refreshRunBaseSMTP(ctx, dag, status, repository, opts)
	if err != nil {
		return nil, err
	}
	runtimeParams := append([]string(nil), status.ParamsList...)
	dag.Params = runtimeParams
	resolvedEnv, err := runtimeenv.Resolve(ctx, dag)
	dag.Env = resolvedEnv.Env
	dag.RuntimeResolved = true
	for _, warning := range resolvedEnv.Warnings {
		logger.Warn(ctx, warning)
	}
	if err != nil {
		return nil, err
	}
	restored, err := spec.RebuildFromYAML(ctx, dag, spec.QuoteRuntimeParams(runtimeParams, dag.ParamDefs))
	if err != nil {
		return nil, err
	}
	if status.ParallelItem != "" {
		restored.Env = append(restored.Env,
			ir.ParallelItemVariable+"="+status.ParallelItem,
			runenv.EnvKeyParallelItem+"="+status.ParallelItem,
		)
	}
	applyPersistedRunWorkingDir(restored, status)
	return restored, nil
}

func refreshRunBaseSMTP(ctx context.Context, dag *ir.DAG, status *ir.DAGRunStatus, repository *persis.DAGRunRepository, opts []spec.LoadOption) (*ir.DAG, error) {
	if dag.BaseConfigWorkspace == nil && repository != nil && !status.Parent.Zero() {
		attempt, err := findRetryAttempt(ctx, repository, status.Parent, status.Root)
		if err != nil {
			return nil, err
		}
		parent, err := attempt.ReadDAG(ctx)
		if err != nil {
			return nil, err
		}
		parentStatus, err := attempt.ReadStatus(ctx)
		if err != nil {
			return nil, err
		}
		// Legacy child snapshots may omit the workspace inherited from their file.
		parentCopy := *parent
		parentCopy.LocalDAGs = map[string]*ir.DAG{dag.Name: dag}
		parent, err = refreshRunBaseSMTP(ctx, &parentCopy, parentStatus, repository, opts)
		if err != nil {
			return nil, err
		}
		return parent.LocalDAGs[dag.Name], nil
	}
	return spec.RefreshBaseSMTP(dag, opts...)
}

func applyPersistedRunWorkingDir(dag *ir.DAG, status *ir.DAGRunStatus) {
	if dag == nil || status == nil || status.WorkingDir == "" {
		return
	}
	dag.WorkingDir = status.WorkingDir
	dag.WorkingDirExplicit = true
}

// extractDAGName extracts the DAG name from a file path or name.
// If the input is a file path (.yaml or .yml), it loads the DAG metadata
// to extract the name. Otherwise, it returns the input as-is.
func extractDAGName(ctx *Context, name string) (string, error) {
	if !fileutil.IsYAMLFile(name) {
		return name, nil
	}

	absolutePath, err := filepath.Abs(name)
	if err != nil {
		return "", fmt.Errorf("failed to resolve DAG file path %s: %w", name, err)
	}

	dagRepository, err := ctx.dagRepository(dagRepositoryConfig{
		SearchPaths: []string{filepath.Dir(absolutePath)},
	})
	if err != nil {
		return "", fmt.Errorf("failed to initialize DAG store: %w", err)
	}

	dag, err := dagRepository.GetMetadata(ctx, absolutePath)
	if err != nil {
		return "", fmt.Errorf("failed to read DAG metadata from file %s: %w", name, err)
	}

	return dag.Name, nil
}

// localOSSubject returns the local OS account running the command as an actor
// name and an "os:<uid>" ID. Both are empty when the account is unknown.
func localOSSubject(currentUser func() (*user.User, error)) (name, id string) {
	if currentUser == nil {
		return "", ""
	}
	current, err := currentUser()
	if err != nil || current == nil {
		return "", ""
	}
	name = strings.TrimSpace(current.Username)
	if uid := strings.TrimSpace(current.Uid); uid != "" {
		id = "os:" + uid
	}
	return name, id
}
