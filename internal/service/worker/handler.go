// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/proto/convert"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/workspacebundle"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

// TaskHandler defines the interface for executing tasks
type TaskHandler interface {
	Handle(ctx context.Context, task *coordinatorv1.Task) error
}

var _ TaskHandler = (*taskHandler)(nil)

// NewTaskHandler creates a new TaskHandler
func NewTaskHandler(cfg *config.Config, bundleClients ...workspacebundle.Client) TaskHandler {
	var bundleClient workspacebundle.Client
	if len(bundleClients) > 0 {
		bundleClient = bundleClients[0]
	}
	return &taskHandler{
		subCmdBuilder: runtime.NewSubCmdBuilder(cfg),
		baseConfig:    cfg.Paths.BaseConfig,
		dagRunsDir:    cfg.Paths.DAGRunsDir,
		bundleClient:  bundleClient,
	}
}

type taskHandler struct {
	subCmdBuilder *runtime.SubCmdBuilder
	baseConfig    string
	dagRunsDir    string
	bundleClient  workspacebundle.Client
}

// Handle runs the task using the dagrun.Manager.
func (e *taskHandler) Handle(ctx context.Context, task *coordinatorv1.Task) error {
	logger.Info(ctx, "Executing task",
		slog.String("operation", task.Operation.String()),
		tag.Target(task.Target),
		tag.RunID(task.DagRunId),
		slog.String("root-dag-run-id", task.RootDagRunId),
		slog.String("parent-dag-run-id", task.ParentDagRunId),
		slog.String("worker-id", task.WorkerId))

	originalTarget := task.Target
	cleanup, err := e.prepareTaskDAG(ctx, task, originalTarget)
	if err != nil {
		return err
	}
	defer cleanup()

	spec, err := e.buildCommandSpec(ctx, task, originalTarget)
	if err != nil {
		return err
	}

	if err := runtime.Run(ctx, spec); err != nil {
		logger.Error(ctx, "Distributed task execution failed",
			slog.String("operation", task.Operation.String()),
			tag.Target(task.Target),
			tag.RunID(task.DagRunId),
			tag.Error(err))
		return err
	}

	logger.Info(ctx, "Distributed task execution finished",
		slog.String("operation", task.Operation.String()),
		tag.Target(task.Target),
		tag.RunID(task.DagRunId))

	return nil
}

func (e *taskHandler) prepareTaskDAG(ctx context.Context, task *coordinatorv1.Task, originalTarget string) (func(), error) {
	if _, ok, err := taskWorkspaceDescriptor(task); err != nil {
		return nil, err
	} else if ok {
		workDir, err := e.sharedVolumeActionWorkDir(ctx, task)
		if err != nil {
			return nil, err
		}
		workspace, err := materializeTaskWorkspace(ctx, task, e.bundleClient, actionWorkspaceDir(workDir))
		if err != nil {
			return nil, err
		}
		task.Target = workspace.dagFile
		logger.Info(ctx, "Materialized action workspace",
			tag.Target(originalTarget),
			tag.File(workspace.dagFile),
			slog.String("workspace", workspace.dir),
			slog.String("digest", workspace.desc.Digest))
		return func() {}, nil
	}

	logger.Info(ctx, "Creating temporary DAG file from definition",
		tag.DAG(task.Target),
		tag.Size(len(task.Definition)))

	tempFile, err := fileutil.CreateTempDAGFile("worker-dags", task.Target, []byte(task.Definition))
	if err != nil {
		return nil, fmt.Errorf("failed to create temp DAG file: %w", err)
	}
	task.Target = tempFile

	logger.Info(ctx, "Created temporary DAG file",
		tag.File(tempFile))

	return func() {
		if err := os.Remove(tempFile); err != nil && !os.IsNotExist(err) {
			logger.Errorf(ctx, "Failed to remove temp DAG file: %v", err)
		}
	}, nil
}

func (e *taskHandler) sharedVolumeActionWorkDir(ctx context.Context, task *coordinatorv1.Task) (string, error) {
	if e.dagRunsDir == "" {
		return "", fmt.Errorf("dag-run directory is required for action workspace materialization")
	}
	root := exec.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	if root.ID == "" {
		return "", fmt.Errorf("root dag-run is required for action workspace materialization")
	}
	attempt, err := filedagrun.New(e.dagRunsDir).FindSubAttempt(ctx, root, task.DagRunId)
	if err != nil {
		return "", fmt.Errorf("find sub-DAG attempt work directory: %w", err)
	}
	if task.AttemptId != "" && attempt.ID() != task.AttemptId {
		return "", fmt.Errorf("stale action workspace task: attempt %q does not match requested attempt %q", attempt.ID(), task.AttemptId)
	}
	workDir := attempt.WorkDir()
	if workDir == "" {
		return "", fmt.Errorf("sub-DAG attempt does not expose a work directory")
	}
	return workDir, nil
}

func (e *taskHandler) buildCommandSpec(ctx context.Context, task *coordinatorv1.Task, originalTarget string) (runtime.CmdSpec, error) {
	dagName := dagNameHint(originalTarget)

	switch task.Operation {
	case coordinatorv1.Operation_OPERATION_START:
		hints, err := e.subprocessHints(ctx, task, originalTarget)
		if err != nil {
			return runtime.CmdSpec{}, err
		}
		spec := e.subCmdBuilder.TaskStart(task, hints.env, dagName)
		return withDefaultWorkingDir(spec, hints.defaultWorkingDir), nil

	case coordinatorv1.Operation_OPERATION_RETRY:
		hints, err := e.subprocessHints(ctx, task, originalTarget)
		if err != nil {
			return runtime.CmdSpec{}, err
		}
		if isQueueDispatchTask(task) {
			spec := e.subCmdBuilder.QueueDispatchTaskRetry(task, hints.env, dagName)
			return withDefaultWorkingDir(spec, hints.defaultWorkingDir), nil
		}
		spec := e.subCmdBuilder.TaskRetry(task, hints.env, dagName)
		return withDefaultWorkingDir(spec, hints.defaultWorkingDir), nil

	case coordinatorv1.Operation_OPERATION_UNSPECIFIED:
		return runtime.CmdSpec{}, fmt.Errorf("operation not specified")

	default:
		return runtime.CmdSpec{}, fmt.Errorf("unknown operation: %v", task.Operation)
	}
}

type subprocessHintSet struct {
	env               []string
	defaultWorkingDir string
}

func (e *taskHandler) subprocessHints(ctx context.Context, task *coordinatorv1.Task, originalTarget string) (*subprocessHintSet, error) {
	dagName := dagNameHint(originalTarget)

	var loadOpts []spec.LoadOption
	if dagName != "" {
		loadOpts = append(loadOpts, spec.WithName(dagName))
	}
	workspaceDefaultDir := ""
	if _, ok, err := taskWorkspaceDescriptor(task); err != nil {
		return nil, err
	} else if ok {
		workspaceDefaultDir = workspaceDefaultWorkingDir(task)
		loadOpts = append(loadOpts, spec.WithDefaultWorkingDir(workspaceDefaultDir))
	} else {
		if task.BaseConfig != "" {
			loadOpts = append(loadOpts, spec.WithBaseConfigContent([]byte(task.BaseConfig)))
		} else if e.baseConfig != "" {
			loadOpts = append(loadOpts, spec.WithBaseConfig(e.baseConfig))
		}
	}

	dag, err := spec.Load(ctx, task.Target, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load DAG for subprocess hints: %w", err)
	}
	dag.SourceFile = task.SourceFile

	params, err := retryParams(task, dag)
	if err != nil {
		return nil, err
	}
	if task.Operation == coordinatorv1.Operation_OPERATION_START {
		params = task.Params
	}

	resolveOpts := spec.ResolveEnvOptions{}
	if workspaceDefaultDir == "" {
		resolveOpts.BaseConfig = e.baseConfig
	}
	env, err := spec.ResolveEnv(ctx, dag, params, resolveOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve DAG env for subprocess: %w", err)
	}

	return &subprocessHintSet{
		env:               env,
		defaultWorkingDir: workspaceDefaultWorkingDir(task),
	}, nil
}

func workspaceDefaultWorkingDir(task *coordinatorv1.Task) string {
	if _, ok, err := taskWorkspaceDescriptor(task); err != nil || !ok {
		return ""
	}
	dir := filepath.Dir(task.Target)
	relDir := filepath.Dir(filepath.FromSlash(task.WorkspaceBundleDagPath))
	if relDir == "." {
		return dir
	}
	for _, part := range strings.Split(relDir, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		dir = filepath.Dir(dir)
	}
	return dir
}

func withDefaultWorkingDir(spec runtime.CmdSpec, workDir string) runtime.CmdSpec {
	if strings.TrimSpace(workDir) == "" {
		return spec
	}
	targetIndex := len(spec.Args) - 1
	for i, arg := range spec.Args {
		if arg == "--" {
			targetIndex = i - 1
			break
		}
	}
	if targetIndex < 0 || targetIndex > len(spec.Args) {
		return spec
	}
	args := make([]string, 0, len(spec.Args)+1)
	args = append(args, spec.Args[:targetIndex]...)
	args = append(args, fmt.Sprintf("--default-working-dir=%s", workDir))
	args = append(args, spec.Args[targetIndex:]...)
	spec.Args = args
	return spec
}

func dagNameHint(target string) string {
	name := strings.TrimSpace(target)
	if name == "" {
		return ""
	}
	base := filepath.Base(name)
	ext := filepath.Ext(base)
	if ext == ".yaml" || ext == ".yml" {
		return strings.TrimSuffix(base, ext)
	}
	return base
}

func retryParams(task *coordinatorv1.Task, dag *core.DAG) (any, error) {
	if task.Operation != coordinatorv1.Operation_OPERATION_RETRY || task.PreviousStatus == nil {
		return nil, nil
	}

	status, err := convert.ProtoToDAGRunStatus(task.PreviousStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to decode previous task status: %w", err)
	}

	return spec.QuoteRuntimeParams(status.ParamsList, dag.ParamDefs), nil
}

func isQueueDispatchTask(task *coordinatorv1.Task) bool {
	if task == nil || task.Operation != coordinatorv1.Operation_OPERATION_RETRY || task.PreviousStatus == nil {
		return false
	}

	status, err := convert.ProtoToDAGRunStatus(task.PreviousStatus)
	if err != nil {
		return false
	}

	return status != nil && status.Status == core.Queued
}
