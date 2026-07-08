// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/collections"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/logpath"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/transform"
)

func (s *Service) seedEditRetryRun(ctx context.Context, cmd SeedEditRetryRunRequest) (*SeededEditRetryRun, error) {
	if err := s.validateSeedEditRetryRun(cmd); err != nil {
		return nil, err
	}

	now := s.now()
	dagRun := exec.NewDAGRunRef(cmd.DAG.Name, cmd.DAGRunID)
	nodes := editRetrySeedNodes(cmd.DAG, cmd.SourceStatus, cmd.SkippedSteps)
	attempt, err := s.cfg.DAGRunStore.CreateAttempt(ctx, cmd.DAG, now, cmd.DAGRunID, exec.NewDAGRunAttemptOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create edit retry attempt: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rmErr := s.cfg.DAGRunStore.RemoveDAGRun(ctx, dagRun); rmErr != nil {
			logger.Error(ctx, "Failed to rollback edit retry attempt",
				tag.DAG(cmd.DAG.Name),
				tag.RunID(cmd.DAGRunID),
				tag.Error(rmErr),
			)
		}
	}()

	logFile, err := logpath.Generate(ctx, s.cfg.LogBaseDir, cmd.DAG.LogDir, cmd.DAG.Name, cmd.DAGRunID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate edit retry log file: %w", err)
	}
	artifactDir, err := s.localArtifactDir(ctx, cmd.DAG, cmd.DAGRunID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate edit retry artifact directory: %w", err)
	}

	status := editRetrySeedStatus(cmd, dagRun, attempt.ID(), logFile, artifactDir, now, nodes)

	if err := attempt.Open(ctx); err != nil {
		return nil, fmt.Errorf("failed to open edit retry attempt: %w", err)
	}
	if hasSkippedEditRetryNode(nodes) && cmd.SourceWorkDir != "" {
		newWorkDir := attempt.WorkDir()
		if err := copyEditRetryWorkDir(cmd.SourceWorkDir, newWorkDir); err != nil {
			_ = attempt.Close(ctx)
			return nil, fmt.Errorf("failed to copy edit retry work directory: %w", err)
		}
		remapEditRetryWorkDirOutputs(status.Nodes, cmd.SourceWorkDir, newWorkDir)
	}

	if err := attempt.Write(ctx, status); err != nil {
		_ = attempt.Close(ctx)
		return nil, fmt.Errorf("failed to save edit retry status: %w", err)
	}
	if err := attempt.Close(ctx); err != nil {
		return nil, fmt.Errorf("failed to close edit retry attempt: %w", err)
	}
	committed = true

	return &SeededEditRetryRun{DAGRun: dagRun, Status: &status}, nil
}

func (s *Service) markEditRetrySeedFailed(ctx context.Context, cmd MarkEditRetrySeedFailedRequest) error {
	if cmd.Status == nil || cmd.Cause == nil {
		return nil
	}
	if s.cfg.DAGRunStore == nil {
		return fmt.Errorf("dag-run store is required")
	}
	_, _, err := s.cfg.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		cmd.Status.DAGRun(),
		cmd.Status.AttemptID,
		core.Queued,
		func(latest *exec.DAGRunStatus) error {
			latest.Status = core.Failed
			latest.FinishedAt = stringutil.FormatTime(s.now())
			latest.Error = cmd.Cause.Error()
			return nil
		},
	)
	if err != nil {
		logger.Warn(ctx, "Failed to mark edit retry seed as failed",
			tag.DAG(cmd.Status.Name),
			tag.RunID(cmd.Status.DAGRunID),
			tag.Error(err),
		)
	}
	return nil
}

func (s *Service) validateSeedEditRetryRun(cmd SeedEditRetryRunRequest) error {
	if s.cfg.DAGRunStore == nil {
		return fmt.Errorf("dag-run store is required")
	}
	if cmd.DAG == nil {
		return fmt.Errorf("dag is required")
	}
	if cmd.DAGRunID == "" {
		return fmt.Errorf("dag-run ID is required")
	}
	if cmd.SourceStatus == nil {
		return fmt.Errorf("source status is required")
	}
	return nil
}

func editRetrySeedStatus(
	cmd SeedEditRetryRunRequest,
	dagRun exec.DAGRunRef,
	attemptID string,
	logFile string,
	artifactDir string,
	now time.Time,
	nodes []runtime.NodeData,
) exec.DAGRunStatus {
	opts := []transform.StatusOption{
		transform.WithNodes(nodes),
		transform.WithLogFilePath(logFile),
		transform.WithArchiveDir(artifactDir),
		transform.WithAttemptID(attemptID),
		transform.WithQueuedAt(stringutil.FormatTime(now)),
		transform.WithPreconditions(cmd.DAG.Preconditions),
		transform.WithHierarchyRefs(dagRun, exec.DAGRunRef{}),
		transform.WithTriggerType(core.TriggerTypeRetry),
		transform.WithRuntimeProfile(cmd.ProfileName, "", nil),
	}
	status := transform.NewStatusBuilder(cmd.DAG).Create(cmd.DAGRunID, core.Queued, 0, time.Time{}, opts...)
	status.Params = cmd.Params
	status.ParamsList = cmd.DAG.Params
	return status
}

func editRetrySeedNodes(dag *core.DAG, sourceStatus *exec.DAGRunStatus, skippedSteps []string) []runtime.NodeData {
	sourceNodes := make(map[string]*exec.Node, len(sourceStatus.Nodes))
	for _, node := range sourceStatus.Nodes {
		if node != nil {
			sourceNodes[node.Step.Name] = node
		}
	}
	skipSet := make(map[string]struct{}, len(skippedSteps))
	for _, stepName := range skippedSteps {
		skipSet[stepName] = struct{}{}
	}

	nodes := make([]runtime.NodeData, 0, len(dag.Steps))
	for _, step := range dag.Steps {
		data := runtime.NodeData{
			Step: step,
			State: runtime.NodeState{
				Status: core.NodeNotStarted,
			},
		}
		if _, ok := skipSet[step.Name]; ok {
			data.State = skippedEditRetryNodeState(sourceNodes[step.Name])
		}
		nodes = append(nodes, data)
	}
	return nodes
}

func skippedEditRetryNodeState(source *exec.Node) runtime.NodeState {
	state := runtime.NodeState{
		Status:         core.NodeSkipped,
		SkippedByRetry: true,
	}
	if source == nil {
		return state
	}

	startedAt, _ := stringutil.ParseTime(source.StartedAt)
	finishedAt, _ := stringutil.ParseTime(source.FinishedAt)
	retriedAt, _ := stringutil.ParseTime(source.RetriedAt)
	state.Stdout = source.Stdout
	state.Stderr = source.Stderr
	state.StartedAt = startedAt
	state.FinishedAt = finishedAt
	state.RetriedAt = retriedAt
	state.RetryCount = source.RetryCount
	state.DoneCount = source.DoneCount
	state.Repeated = source.Repeated
	state.OutputVariables = cloneSyncMap(source.OutputVariables)
	state.ChatMessages = append([]exec.LLMMessage(nil), source.ChatMessages...)
	state.ToolDefinitions = append([]exec.ToolDefinition(nil), source.ToolDefinitions...)
	state.ApprovalInputs = cloneStringMap(source.ApprovalInputs)
	state.ApprovedAt = source.ApprovedAt
	state.ApprovedBy = source.ApprovedBy
	state.RejectedAt = source.RejectedAt
	state.RejectedBy = source.RejectedBy
	state.RejectionReason = source.RejectionReason
	state.ApprovalIteration = source.ApprovalIteration
	state.PushBackInputs = cloneStringMap(source.PushBackInputs)
	state.PushBackHistory = clonePushBackHistory(source.PushBackHistory)
	return state
}

func cloneSyncMap(src *collections.SyncMap) *collections.SyncMap {
	if src == nil {
		return nil
	}
	dst := &collections.SyncMap{}
	src.Range(func(key, value any) bool {
		dst.Store(key, value)
		return true
	})
	return dst
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}

func clonePushBackHistory(src []exec.PushBackEntry) []exec.PushBackEntry {
	if len(src) == 0 {
		return nil
	}
	dst := make([]exec.PushBackEntry, len(src))
	for i, entry := range src {
		dst[i] = exec.PushBackEntry{
			Iteration: entry.Iteration,
			By:        entry.By,
			At:        entry.At,
			Inputs:    cloneStringMap(entry.Inputs),
		}
	}
	return dst
}

func hasSkippedEditRetryNode(nodes []runtime.NodeData) bool {
	for _, node := range nodes {
		if node.State.SkippedByRetry {
			return true
		}
	}
	return false
}

func copyEditRetryWorkDir(sourceWorkDir, targetWorkDir string) error {
	sourceWorkDir = cleanEditRetryWorkDir(sourceWorkDir)
	targetWorkDir = cleanEditRetryWorkDir(targetWorkDir)
	if sourceWorkDir == "" || targetWorkDir == "" || sourceWorkDir == targetWorkDir {
		return nil
	}

	info, err := os.Stat(sourceWorkDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", sourceWorkDir)
	}
	if err := os.MkdirAll(targetWorkDir, 0o750); err != nil {
		return err
	}

	return filepath.WalkDir(sourceWorkDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceWorkDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		targetPath := filepath.Join(targetWorkDir, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		switch {
		case entry.IsDir():
			return os.MkdirAll(targetPath, mode.Perm())
		case mode.Type()&os.ModeSymlink != 0:
			return copyEditRetrySymlink(sourceWorkDir, targetWorkDir, path, targetPath)
		case mode.IsRegular():
			return copyEditRetryFile(path, targetPath, mode)
		default:
			return nil
		}
	})
}

func copyEditRetrySymlink(sourceWorkDir, targetWorkDir, sourcePath, targetPath string) error {
	linkTarget, err := os.Readlink(sourcePath)
	if err != nil {
		return err
	}

	resolvedSourceTarget := linkTarget
	if !filepath.IsAbs(resolvedSourceTarget) {
		resolvedSourceTarget = filepath.Join(filepath.Dir(sourcePath), resolvedSourceTarget)
	}
	resolvedSourceTarget = filepath.Clean(resolvedSourceTarget)
	if err := ensureEditRetryPathWithin(sourceWorkDir, resolvedSourceTarget); err != nil {
		return fmt.Errorf("unsafe symlink target %s: %w", sourcePath, err)
	}
	if evaluatedSourceTarget, err := filepath.EvalSymlinks(resolvedSourceTarget); err == nil {
		if err := ensureEditRetryResolvedPathWithin(sourceWorkDir, evaluatedSourceTarget); err != nil {
			return fmt.Errorf("unsafe symlink target %s: %w", sourcePath, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	sourceTargetRel, err := filepath.Rel(sourceWorkDir, resolvedSourceTarget)
	if err != nil {
		return err
	}
	targetLinkTarget := filepath.Join(targetWorkDir, sourceTargetRel)
	relativeTargetLink, err := filepath.Rel(filepath.Dir(targetPath), targetLinkTarget)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return err
	}
	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(relativeTargetLink, targetPath) //nolint:gosec // symlink target is constrained to the copied work directory.
}

func ensureEditRetryPathWithin(baseDir, targetPath string) error {
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	relToBase, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return err
	}
	if relToBase == ".." || strings.HasPrefix(relToBase, ".."+string(filepath.Separator)) || filepath.IsAbs(relToBase) {
		return fmt.Errorf("path escapes source work directory")
	}
	return nil
}

func ensureEditRetryResolvedPathWithin(baseDir, targetPath string) error {
	resolvedBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		return err
	}
	return ensureEditRetryPathWithin(resolvedBase, targetPath)
}

func copyEditRetryFile(sourcePath, targetPath string, mode fs.FileMode) error {
	source, err := os.Open(sourcePath) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() {
		_ = source.Close()
	}()

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return err
	}
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm()) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() {
		_ = target.Close()
	}()

	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return target.Chmod(mode.Perm())
}

func remapEditRetryWorkDirOutputs(nodes []*exec.Node, sourceWorkDir, targetWorkDir string) {
	sourceWorkDir = cleanEditRetryWorkDir(sourceWorkDir)
	targetWorkDir = cleanEditRetryWorkDir(targetWorkDir)
	if sourceWorkDir == "" || targetWorkDir == "" || sourceWorkDir == targetWorkDir {
		return
	}

	replacements := [][2]string{{sourceWorkDir, targetWorkDir}}
	sourceSlash := filepath.ToSlash(sourceWorkDir)
	targetSlash := filepath.ToSlash(targetWorkDir)
	if sourceSlash != sourceWorkDir || targetSlash != targetWorkDir {
		replacements = append(replacements, [2]string{sourceSlash, targetSlash})
	}

	for _, node := range nodes {
		if node == nil || !node.SkippedByRetry || node.OutputVariables == nil {
			continue
		}
		node.OutputVariables.Range(func(key, value any) bool {
			text, ok := value.(string)
			if !ok {
				return true
			}
			rewritten := text
			for _, replacement := range replacements {
				rewritten = strings.ReplaceAll(rewritten, replacement[0], replacement[1])
			}
			if rewritten != text {
				node.OutputVariables.Store(key, rewritten)
			}
			return true
		})
	}
}

func cleanEditRetryWorkDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(dir)
}
