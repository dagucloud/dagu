// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	exec1 "github.com/dagucloud/dagu/internal/core/exec"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

// CommandError wraps a command execution error with captured output.
// It preserves the original error for unwrapping (e.g., for exit code extraction).
type CommandError struct {
	Err    error
	Stdout string
	Stderr string
}

func (e *CommandError) Error() string {
	parts := []string{fmt.Sprintf("command failed: %v", e.Err)}
	if e.Stdout != "" {
		parts = append(parts, fmt.Sprintf("stdout: %s", e.Stdout))
	}
	if e.Stderr != "" {
		parts = append(parts, fmt.Sprintf("stderr: %s", e.Stderr))
	}
	return strings.Join(parts, "\n")
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

// cappedBuffer is a buffer that keeps only the last maxSize bytes.
// This prevents memory exhaustion from verbose command output.
type cappedBuffer struct {
	data    []byte
	maxSize int
}

const defaultMaxBufferSize = 64 * 1024 // 64KB

func newCappedBuffer(maxSize int) *cappedBuffer {
	return &cappedBuffer{
		data:    make([]byte, 0, maxSize),
		maxSize: maxSize,
	}
}

func (b *cappedBuffer) Write(p []byte) (n int, err error) {
	n = len(p)
	if len(p) >= b.maxSize {
		// If input is larger than max, keep only the last maxSize bytes
		b.data = append(b.data[:0], p[len(p)-b.maxSize:]...)
		return n, nil
	}
	// Append and trim to maxSize
	b.data = append(b.data, p...)
	if len(b.data) > b.maxSize {
		b.data = b.data[len(b.data)-b.maxSize:]
	}
	return n, nil
}

func (b *cappedBuffer) String() string {
	return string(b.data)
}

// SubCmdBuilder centralizes CLI command argument construction.
type SubCmdBuilder struct {
	executable string
	configFile string
	baseEnv    config.BaseEnv
}

// NewSubCmdBuilder returns a new SubCmdBuilder initialized from cfg.
// It sets Executable to cfg.Paths.Executable, ConfigFile to cfg.Paths.ConfigFileUsed,
// and base environment to cfg.Core.BaseEnv.
func NewSubCmdBuilder(cfg *config.Config) *SubCmdBuilder {
	return &SubCmdBuilder{
		executable: cfg.Paths.Executable,
		configFile: cfg.Paths.ConfigFileUsed,
		baseEnv:    cfg.Core.BaseEnv,
	}
}

func (b *SubCmdBuilder) filteredEnv(extra ...string) []string {
	env := b.baseEnv.AsSlice()
	if len(env) == 0 {
		env = os.Environ()
	}
	env = append(env, extra...)
	return env
}

func (b *SubCmdBuilder) parentEnv(extra ...string) []string {
	env := filteredParentEnv()
	env = append(env, extra...)
	return env
}

func filteredParentEnv() []string {
	env := os.Environ()
	filtered := env[:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, exec1.EnvKeyQueueDispatchRetry+"=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
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

// Start creates a start command spec.
func (b *SubCmdBuilder) Start(dag *core.DAG, opts StartOptions) CmdSpec {
	args := []string{"start"}

	if opts.Params != "" {
		args = append(args, "-p", strconv.Quote(opts.Params))
	}
	if opts.Quiet {
		args = append(args, "-q")
	}

	if opts.DAGRunID != "" {
		args = append(args, fmt.Sprintf("--run-id=%s", opts.DAGRunID))
	}
	if opts.NameOverride != "" {
		args = append(args, fmt.Sprintf("--name=%s", opts.NameOverride))
	}
	if opts.FromRunID != "" {
		args = append(args, fmt.Sprintf("--from-run-id=%s", opts.FromRunID))
	}
	if opts.TriggerType != "" {
		args = append(args, fmt.Sprintf("--trigger-type=%s", opts.TriggerType))
	}
	if labels := effectiveLabels(opts.Labels, opts.Tags); labels != "" {
		args = append(args, fmt.Sprintf("--labels=%s", labels))
	}
	if opts.ScheduleTime != "" {
		args = append(args, fmt.Sprintf("--schedule-time=%s", opts.ScheduleTime))
	}
	if b.configFile != "" {
		args = append(args, "--config", b.configFile)
	}
	target := dag.Location
	if opts.Target != "" {
		target = opts.Target
	}
	args = append(args, target)

	return CmdSpec{
		Executable: b.executable,
		Args:       args,
		Env:        b.parentEnv(),
		BuildEnv:   append([]string{}, dag.Env...),
	}
}

// Enqueue creates an enqueue command spec.
func (b *SubCmdBuilder) Enqueue(dag *core.DAG, opts EnqueueOptions) CmdSpec {
	args := []string{"enqueue"}

	if opts.Params != "" {
		args = append(args, "-p", strconv.Quote(opts.Params))
	}
	if opts.Quiet {
		args = append(args, "-q")
	}
	if opts.DAGRunID != "" {
		args = append(args, fmt.Sprintf("--run-id=%s", opts.DAGRunID))
	}
	if opts.NameOverride != "" {
		args = append(args, fmt.Sprintf("--name=%s", opts.NameOverride))
	}
	if b.configFile != "" {
		args = append(args, "--config", b.configFile)
	}
	if opts.Queue != "" {
		args = append(args, "--queue", opts.Queue)
	}
	if opts.TriggerType != "" {
		args = append(args, fmt.Sprintf("--trigger-type=%s", opts.TriggerType))
	}
	if labels := effectiveLabels(opts.Labels, opts.Tags); labels != "" {
		args = append(args, fmt.Sprintf("--labels=%s", labels))
	}
	if opts.ScheduleTime != "" {
		args = append(args, fmt.Sprintf("--schedule-time=%s", opts.ScheduleTime))
	}
	args = append(args, dag.Location)

	return CmdSpec{
		Executable: b.executable,
		Args:       args,
		Env:        b.filteredEnv(),
		BuildEnv:   append([]string{}, dag.Env...),
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	}
}

// Dequeue creates a dequeue command spec.
func (b *SubCmdBuilder) Dequeue(dag *core.DAG, dagRun exec1.DAGRunRef) CmdSpec {
	queueName := dag.ProcGroup()
	args := []string{"dequeue", queueName, fmt.Sprintf("--dag-run=%s", dagRun.String())}

	if b.configFile != "" {
		args = append(args, "--config", b.configFile)
	}

	return CmdSpec{
		Executable: b.executable,
		Args:       args,
		Env:        b.filteredEnv(),
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	}
}

// Restart creates a restart command spec.
func (b *SubCmdBuilder) Restart(dag *core.DAG, opts RestartOptions) CmdSpec {
	args := []string{"restart"}

	if opts.Quiet {
		args = append(args, "-q")
	}
	if opts.ScheduleTime != "" {
		args = append(args, fmt.Sprintf("--schedule-time=%s", opts.ScheduleTime))
	}
	if b.configFile != "" {
		args = append(args, "--config", b.configFile)
	}
	args = append(args, dag.Location)

	return CmdSpec{
		Executable: b.executable,
		Args:       args,
		Env:        b.parentEnv(),
		BuildEnv:   append([]string{}, dag.Env...),
	}
}

// Retry creates a retry command spec.
func (b *SubCmdBuilder) Retry(dag *core.DAG, dagRunID string, stepName string) CmdSpec {
	args := []string{"retry", fmt.Sprintf("--run-id=%s", dagRunID), "-q"}

	if stepName != "" {
		args = append(args, fmt.Sprintf("--step=%s", stepName))
	}

	if b.configFile != "" {
		args = append(args, "--config", b.configFile)
	}
	args = append(args, dag.Name)

	return CmdSpec{
		Executable: b.executable,
		Args:       args,
		Env:        b.parentEnv(),
		BuildEnv:   append([]string{}, dag.Env...),
	}
}

// QueueDispatchRetry creates a retry command spec for a scheduler-consumed queued run.
func (b *SubCmdBuilder) QueueDispatchRetry(dag *core.DAG, dagRunID string, stepName string) CmdSpec {
	spec := b.Retry(dag, dagRunID, stepName)
	spec.Env = append(spec.Env, exec1.EnvKeyQueueDispatchRetry+"=1")
	return spec
}

// QueueDispatchTaskRetry creates a retry command spec for a worker-consumed queued run.
func (b *SubCmdBuilder) QueueDispatchTaskRetry(task *coordinatorv1.Task, envHints []string, dagName string) CmdSpec {
	spec := b.TaskRetry(task, envHints, dagName)
	spec.Env = append(spec.Env, exec1.EnvKeyQueueDispatchRetry+"=1")
	return spec
}

// TaskStart creates a start command spec for coordinator tasks.
// envHints optionally provides resolved DAG/base-config env entries for
// rebuild-time env values in the child process.
func (b *SubCmdBuilder) TaskStart(task *coordinatorv1.Task, envHints []string, dagName string) CmdSpec {
	args := []string{"start", "-q"}
	env := b.parentEnv()

	// Add hierarchy flags for sub DAGs
	if task.RootDagRunId != "" {
		args = append(args, fmt.Sprintf("--root=%s:%s", task.RootDagRunName, task.RootDagRunId))
	}
	if task.ParentDagRunId != "" {
		args = append(args, fmt.Sprintf("--parent=%s:%s", task.ParentDagRunName, task.ParentDagRunId))
	}

	args = append(args, fmt.Sprintf("--run-id=%s", task.DagRunId))
	if task.AttemptId != "" {
		args = append(args, fmt.Sprintf("--attempt-id=%s", task.AttemptId))
	}

	// Override derived name since temp files lack 'name:' field
	if dagName == "" {
		dagName = task.RootDagRunName
	}
	if dagName == "" {
		dagName = dagNameHint(task.Target)
	}
	if dagName != "" {
		args = append(args, fmt.Sprintf("--name=%s", dagName))
	}

	// Worker ID prevents re-dispatch to coordinator
	if task.WorkerId != "" {
		args = append(args, fmt.Sprintf("--worker-id=%s", task.WorkerId))
	}

	if task.Labels != "" {
		args = append(args, fmt.Sprintf("--labels=%s", task.Labels))
	}
	if task.ScheduleTime != "" {
		args = append(args, fmt.Sprintf("--schedule-time=%s", task.ScheduleTime))
	}
	if task.SourceFile != "" {
		args = append(args, fmt.Sprintf("--source-file=%s", task.SourceFile))
	}

	if b.configFile != "" {
		args = append(args, "--config", b.configFile)
	}
	args = append(args, task.Target)

	if task.Params != "" {
		args = append(args, "--", task.Params)
	}
	if task.ExternalStepRetry {
		env = append(env, exec1.EnvKeyExternalStepRetry+"=1")
	}

	return CmdSpec{
		Executable: b.executable,
		Args:       args,
		Env:        env,
		BuildEnv:   append([]string{}, envHints...),
	}
}

// TaskRetry creates a retry command spec for coordinator tasks.
// envHints optionally provides resolved DAG/base-config env entries for
// rebuild-time env values in the child process.
func (b *SubCmdBuilder) TaskRetry(task *coordinatorv1.Task, envHints []string, dagName string) CmdSpec {
	args := []string{"retry", fmt.Sprintf("--run-id=%s", task.DagRunId), "-q"}
	env := b.parentEnv()
	if task.AttemptId != "" {
		args = append(args, fmt.Sprintf("--attempt-id=%s", task.AttemptId))
	}

	if task.Step != "" {
		args = append(args, fmt.Sprintf("--step=%s", task.Step))
	}

	// Pass worker ID for tracking which worker executes this DAG run
	if task.WorkerId != "" {
		args = append(args, fmt.Sprintf("--worker-id=%s", task.WorkerId))
	}

	if b.configFile != "" {
		args = append(args, "--config", b.configFile)
	}
	if dagName == "" {
		dagName = task.RootDagRunName
	}
	if dagName == "" {
		dagName = dagNameHint(task.Target)
	}
	args = append(args, dagName)
	if task.ExternalStepRetry {
		env = append(env, exec1.EnvKeyExternalStepRetry+"=1")
	}

	return CmdSpec{
		Executable: b.executable,
		Args:       args,
		Env:        env,
		BuildEnv:   append([]string{}, envHints...),
	}
}

// CmdSpec describes a command to be executed with all its configuration.
type CmdSpec struct {
	Executable string
	Args       []string
	Env        []string
	BuildEnv   []string
	Stdout     *os.File
	Stderr     *os.File
}

// StartOptions contains options for initiating a dag-run.
type StartOptions struct {
	Params   string // Parameters to pass to the DAG
	Quiet    bool   // Whether to run in quiet mode
	DAGRunID string // ID for the dag-run

	NameOverride string // Optional DAG name override
	FromRunID    string // Historic dag-run ID to use as a template
	Target       string // Optional CLI argument override (DAG name or file path)
	TriggerType  string // How this DAG run was initiated (scheduler, manual, webhook, subdag)
	Labels       string // Additional labels (comma-separated)
	Tags         string // Deprecated: use Labels.
	ScheduleTime string // RFC 3339 timestamp of when this run was scheduled
}

// EnqueueOptions contains options for enqueuing a dag-run.
type EnqueueOptions struct {
	Params       string // Parameters to pass to the DAG
	Quiet        bool   // Whether to run in quiet mode
	DAGRunID     string // ID for the dag-run
	Queue        string // Queue name to enqueue to
	NameOverride string // Optional DAG name override
	TriggerType  string // How this DAG run was initiated (scheduler, manual, webhook, subdag)
	Labels       string // Additional labels (comma-separated)
	Tags         string // Deprecated: use Labels.
	ScheduleTime string // RFC 3339 timestamp of when this run was scheduled
}

// RestartOptions contains options for restarting a dag-run.
type RestartOptions struct {
	Quiet        bool   // Whether to run in quiet mode
	ScheduleTime string // RFC 3339 timestamp of when this run was scheduled
}

// Run executes the command and waits for it to complete.
// If the command fails and output was captured, it is included in the error for debugging.
// Uses capped buffers to prevent memory exhaustion from verbose command output.
func Run(ctx context.Context, spec CmdSpec) error {
	stdout := newCappedBuffer(defaultMaxBufferSize)
	stderr := newCappedBuffer(defaultMaxBufferSize)

	cmd, cleanup, err := newCommand(ctx, spec, true)
	if err != nil {
		return err
	}
	defer cleanupTransport(cleanup)
	cmd.Stdout = io.MultiWriter(stdout, fileOrDefault(spec.Stdout, os.Stdout))
	cmd.Stderr = io.MultiWriter(stderr, fileOrDefault(spec.Stderr, os.Stderr))

	if err := cmd.Run(); err != nil {
		return buildCommandError(err, stdout, stderr)
	}
	return nil
}

// buildCommandError constructs an error that includes captured output for debugging.
// It preserves the original error for unwrapping (e.g., for exit code extraction via errors.As).
func buildCommandError(err error, stdout, stderr *cappedBuffer) error {
	return &CommandError{
		Err:    err,
		Stdout: strings.TrimSpace(stdout.String()),
		Stderr: strings.TrimSpace(stderr.String()),
	}
}

// fileOrDefault returns the file if non-nil, otherwise returns the default.
func fileOrDefault(file, defaultFile *os.File) *os.File {
	if file != nil {
		return file
	}
	return defaultFile
}

func effectiveLabels(labels, tags string) string {
	if labels != "" {
		return labels
	}
	return tags
}

// Start executes the command without waiting for it to complete.
func Start(ctx context.Context, spec CmdSpec) error {
	cmd, cleanup, err := newCommand(ctx, spec, false)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		cleanupTransport(cleanup)
		return fmt.Errorf("failed to start command: %w", err)
	}

	go execWithRecovery(ctx, func() {
		_ = cmd.Wait()
		cleanupTransport(cleanup)
	})

	return nil
}

// newCommand creates an exec.Cmd from the spec with proper configuration.
// nolint:gosec
func newCommand(ctx context.Context, spec CmdSpec, withContext bool) (*exec.Cmd, func() error, error) {
	var cmd *exec.Cmd
	if withContext {
		cmd = exec.CommandContext(ctx, spec.Executable, spec.Args...)
	} else {
		cmd = exec.Command(spec.Executable, spec.Args...)
	}

	cmdutil.SetupCommand(cmd)
	var env []string
	if spec.Env != nil {
		env = append([]string{}, spec.Env...)
	}
	extraEnv, cleanup, err := buildenv.Prepare(spec.BuildEnv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to prepare presolved build env transport: %w", err)
	}
	if len(extraEnv) > 0 {
		env = append(env, extraEnv...)
	}
	cmd.Env = env
	cmd.Stdout = fileOrDefault(spec.Stdout, os.Stdout)
	cmd.Stderr = fileOrDefault(spec.Stderr, os.Stderr)

	return cmd, cleanup, nil
}

func cleanupTransport(cleanup func() error) {
	if cleanup == nil {
		return
	}
	_ = cleanup()
}
