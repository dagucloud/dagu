// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	dockerexec "github.com/dagucloud/dagu/internal/runtime/builtin/docker"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/runtime/resourcelimit"
	"github.com/goccy/go-yaml"
	dockerclient "github.com/moby/moby/client"
)

var _ executor.Executor = (*harnessExecutor)(nil)
var _ executor.Stopper = (*harnessExecutor)(nil)
var _ executor.ExitCoder = (*harnessExecutor)(nil)
var _ executor.ChatMessageHandler = (*harnessExecutor)(nil)
var _ executor.PushBackAware = (*harnessExecutor)(nil)
var _ executor.PushBackPreviousStdoutAware = (*harnessExecutor)(nil)

const failedStdoutTailLimit = 1024

type providerConfig struct {
	name       string
	builtin    bool
	provider   Provider
	definition *core.HarnessDefinition
	flags      map[string]any
}

type defaultConfigProvider interface {
	DefaultConfig() map[string]any
}

type harnessExecutor struct {
	mu                     sync.Mutex
	process                *cmdutil.ManagedProcess
	stdout                 io.Writer
	stderr                 io.Writer
	exitCode               int
	stderrTail             *executor.TailWriter
	step                   core.Step
	configs                []providerConfig
	prompt                 string
	script                 string // piped to stdin if present
	workDir                string
	cancelBuiltin          context.CancelFunc
	builtinStopped         bool
	contextMessages        []coreexec.LLMMessage
	savedMessages          []coreexec.LLMMessage
	pushBackInputs         map[string]string
	pushBackIteration      int
	pushBackPreviousStdout string

	// container-run state (SDK path); set under mu while a containerized step runs
	containerClient *dockerexec.Client
	containerCancel context.CancelFunc

	// shared-container exec state; set while running in the DAG-level container
	sharedContainerCancel context.CancelFunc
}

func (e *harnessExecutor) ExitCode() int {
	return e.exitCode
}

func (e *harnessExecutor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *harnessExecutor) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *harnessExecutor) SetContext(msgs []coreexec.LLMMessage) {
	e.contextMessages = append([]coreexec.LLMMessage(nil), msgs...)
}

func (e *harnessExecutor) GetMessages() []coreexec.LLMMessage {
	if e.savedMessages == nil {
		return append([]coreexec.LLMMessage(nil), e.contextMessages...)
	}
	return append([]coreexec.LLMMessage(nil), e.savedMessages...)
}

func (e *harnessExecutor) Kill(sig os.Signal) error {
	return e.Stop(cmdutil.TerminationFromSignal(sig))
}

func (e *harnessExecutor) Stop(intent cmdutil.TerminationIntent) error {
	return e.stop(cmdutil.StopRequest{Intent: intent})
}

func (e *harnessExecutor) stop(req cmdutil.StopRequest) error {
	e.mu.Lock()
	if e.process == nil {
		if e.cancelBuiltin != nil {
			e.builtinStopped = true
			e.cancelBuiltin()
			e.cancelBuiltin = nil
		}
		// Containerized run: cancel the run context and stop the container via
		// the SDK so a cancelled step leaves no running orphan (Close auto-removes
		// when AutoRemove is set, i.e. keep_container is false).
		if e.containerClient != nil || e.containerCancel != nil {
			cli := e.containerClient
			cancel := e.containerCancel
			e.containerCancel = nil
			e.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			if cli != nil {
				return cli.Stop(req.Intent.Signal)
			}
			return nil
		}
		if e.sharedContainerCancel != nil {
			cancel := e.sharedContainerCancel
			e.sharedContainerCancel = nil
			e.mu.Unlock()
			cancel()
			return nil
		}
		e.mu.Unlock()
		return nil
	}
	defer e.mu.Unlock()
	_, err := e.process.Stop(req)
	return err
}

func (e *harnessExecutor) SetPushBackContext(inputs map[string]string, iteration int) {
	e.pushBackInputs = maps.Clone(inputs)
	e.pushBackIteration = iteration
}

func (e *harnessExecutor) SetPushBackPreviousStdout(path string) {
	e.pushBackPreviousStdout = path
}

func (e *harnessExecutor) effectivePrompt() string {
	if e.pushBackIteration == 0 {
		return e.prompt
	}

	var sb strings.Builder
	sb.WriteString(e.prompt)
	sb.WriteString("\n\n## Push-back Context\n\n")
	fmt.Fprintf(&sb, "Push-back iteration: %d\n", e.pushBackIteration)
	if e.pushBackPreviousStdout != "" {
		fmt.Fprintf(&sb, "Previous stdout log: %s\n", e.pushBackPreviousStdout)
	}

	keys := make([]string, 0, len(e.pushBackInputs))
	for key := range e.pushBackInputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		sb.WriteString("\nReviewer feedback:\n")
		for _, key := range keys {
			fmt.Fprintf(&sb, "- %s: %s\n", key, e.pushBackInputs[key])
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

func (e *harnessExecutor) Run(ctx context.Context) error {
	var lastErr error

	for i, cfg := range e.configs {
		stdout, err := e.runOnce(ctx, cfg)
		if err == nil {
			e.exitCode = 0
			if writeErr := e.writeStdout(stdout); writeErr != nil {
				e.exitCode = 1
				return fmt.Errorf("harness: failed to write stdout: %w", writeErr)
			}
			return nil
		}

		lastErr = err
		if ctx.Err() != nil {
			return err
		}
		if i+1 < len(e.configs) {
			next := e.configs[i+1]
			_, _ = fmt.Fprintf(
				e.stderrWriter(),
				"harness: attempt %d/%d with %s failed; trying fallback %d/%d with %s\n",
				i+1,
				len(e.configs),
				cfg.name,
				i+2,
				len(e.configs),
				next.name,
			)
		}
	}

	if lastErr != nil {
		return lastErr
	}
	return nil
}

func (e *harnessExecutor) runOnce(ctx context.Context, cfg providerConfig) (*os.File, error) {
	hasRootContainer := rootContainerConfigured(ctx)

	if cfg.builtin {
		if e.step.Container != nil || hasRootContainer {
			e.exitCode = 1
			return nil, fmt.Errorf("harness: builtin provider does not support container execution")
		}
		return e.runBuiltinOnce(ctx, cfg)
	}

	if e.step.Container != nil {
		return e.runContainerOnce(ctx, cfg)
	}

	if hasRootContainer {
		return e.runSharedContainerOnce(ctx, cfg)
	}

	return e.runHostSubprocessOnce(ctx, cfg)
}

func rootContainerConfigured(ctx context.Context) bool {
	env, ok := runtime.LookupEnv(ctx)
	return ok && env.DAG != nil && env.DAG.Container != nil
}

// runHostSubprocessOnce runs the agent CLI as a host subprocess (the
// non-container path). Unchanged from the original runOnce behavior.
func (e *harnessExecutor) runHostSubprocessOnce(ctx context.Context, cfg providerConfig) (*os.File, error) {
	e.mu.Lock()

	env := runtime.GetEnv(ctx)
	tw := executor.NewTailWriterWithEncoding(e.stderrWriter(), 0, env.LogEncodingCharset)
	e.stderrTail = tw
	args, stdin, err := cfg.buildInvocation(e.effectivePrompt(), e.script)
	if err != nil {
		e.exitCode = 1
		e.mu.Unlock()
		return nil, err
	}

	stdout, err := newStdoutSpool()
	if err != nil {
		e.exitCode = 1
		e.mu.Unlock()
		return nil, fmt.Errorf("harness: failed to create stdout spool: %w", err)
	}

	cmd, err := e.invocationCommand(ctx, cfg, args, stdin, stdout, tw)
	if err != nil {
		e.exitCode = exitCodeFromError(err)
		_ = cleanupStdoutSpool(stdout)
		e.mu.Unlock()
		return nil, err
	}

	if cmd.Dir != "" {
		if err := os.MkdirAll(cmd.Dir, 0o750); err != nil {
			e.exitCode = 1
			_ = cleanupStdoutSpool(stdout)
			e.mu.Unlock()
			return nil, fmt.Errorf("harness: failed to create working directory: %w", err)
		}
	}

	return e.startAndWaitLocked(ctx, cmd, stdout, tw, env.LogEncodingCharset)
}

func (e *harnessExecutor) invocationCommand(ctx context.Context, cfg providerConfig, args []string, stdin io.Reader, stdout *os.File, stderr io.Writer) (*exec.Cmd, error) {
	binaryPath, err := resolveBinaryPath(cfg.binaryName(), e.workDir, runtime.AllEnvsMap(ctx))
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("harness: %q CLI not found in PATH; install it first: %w", cfg.binaryName(), err)
		}
		return nil, fmt.Errorf("harness: failed to resolve binary %q: %w", cfg.binaryName(), err)
	}

	cmd := exec.CommandContext(ctx, binaryPath, args...)
	if len(cmd.Args) > 0 {
		cmd.Args[0] = cfg.binaryName()
	}
	cmd.Env = append(cmd.Env, runtime.AllEnvs(ctx)...)
	cmd.Dir = e.workDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if stdin != nil {
		cmd.Stdin = stdin
	}
	return cmd, nil
}

// mergedContainerEnv merges inherited engine env and explicit container.env into
// full KEY=value entries (explicit wins by key). Used as the SDK container's
// Config.Env, so secret values are passed as container env, never in argv.
func mergedContainerEnv(inherited, explicit []string) []string {
	merged := append([]string(nil), inherited...)
	indexByKey := make(map[string]int, len(merged))
	for i, entry := range merged {
		key, _, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			indexByKey[key] = i
		}
	}
	for _, entry := range explicit {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if idx, exists := indexByKey[key]; exists {
			merged[idx] = entry
			continue
		}
		indexByKey[key] = len(merged)
		merged = append(merged, entry)
	}
	return merged
}

// buildHarnessContainerRunConfig builds the Moby SDK container config for running
// the agent CLI inside a container, plus the command slice to hand to the client.
// Pure (no daemon/IO) so it is unit-testable. The agent binary becomes the
// container entrypoint (image mode) so an image ENTRYPOINT does not double it.
func buildHarnessContainerRunConfig(
	workDir string,
	ct core.Container,
	registryAuths map[string]*core.AuthConfig,
	binaryName string,
	args []string,
	inheritedEnv []string,
	envs map[string]string,
) (*dockerexec.Config, []string, error) {
	if strings.TrimSpace(binaryName) == "" {
		return nil, nil, fmt.Errorf("harness: empty container command")
	}

	host, err := dockerexec.ResolveDaemonHost(envs)
	if err != nil {
		return nil, nil, err
	}

	mergedEnv := mergedContainerEnv(inheritedEnv, ct.Env)

	cfg, err := dockerexec.LoadConfig(workDir, ct, registryAuths)
	if err != nil {
		return nil, nil, fmt.Errorf("harness: failed to build container config: %w", err)
	}
	cfg.DaemonHost = host
	cfg.ShouldStart = true

	var runCmd []string
	if ct.IsExecMode() {
		// exec into an existing container: no image entrypoint to collide with,
		// so the command is the full [binary, args...]. Env goes on ExecOptions.
		if cfg.ExecOptions == nil {
			cfg.ExecOptions = &dockerclient.ExecCreateOptions{}
		}
		cfg.ExecOptions.Env = mergedEnv
		runCmd = append([]string{binaryName}, args...)
		return cfg, runCmd, nil
	}

	// image mode: agent binary is the entrypoint, args are the command. Setting
	// Entrypoint=[binary] avoids doubling an image-level ENTRYPOINT (e.g. the
	// reviewer-claude image has ENTRYPOINT ["claude"]).
	//
	// That entrypoint+args shaping is correct only on the container-CREATE path.
	// A named container (container.name) can resolve to an already-running
	// container, in which case Client.Run execs the args INTO it; docker exec
	// ignores the image/Config entrypoint, so the agent binary would be dropped
	// and the wrong command run. The harness container model is a fresh ephemeral
	// container, so reject container.name in image mode; targeting an existing
	// container is the job of exec mode (container.exec).
	if strings.TrimSpace(ct.Name) != "" {
		return nil, nil, fmt.Errorf("harness: container.name is not supported for an image-mode container step (it would exec into an existing container without the agent entrypoint); use exec mode (container.exec) to run inside an existing container")
	}
	if cfg.Container == nil {
		return nil, nil, fmt.Errorf("harness: container config missing for image %q", ct.Image)
	}
	cfg.Container.Entrypoint = []string{binaryName}
	cfg.Container.Cmd = []string{}
	cfg.Container.Env = mergedEnv
	runCmd = append([]string(nil), args...)
	return cfg, runCmd, nil
}

// runContainerOnce runs the agent CLI inside a container by driving Dagu's Moby
// SDK Client (create + start + stream + auto-remove), against the daemon socket
// chosen by the DAGU_CONTAINER_RUNTIME service setting. No docker/podman CLI subprocess is used.
func (e *harnessExecutor) runContainerOnce(ctx context.Context, cfg providerConfig) (*os.File, error) {
	e.mu.Lock()

	env := runtime.GetEnv(ctx)
	tw := executor.NewTailWriterWithEncoding(e.stderrWriter(), 0, env.LogEncodingCharset)
	e.stderrTail = tw

	args, stdin, err := cfg.buildInvocation(e.effectivePrompt(), e.script)
	if err != nil {
		e.exitCode = 1
		e.mu.Unlock()
		return nil, err
	}
	if stdin != nil {
		// Client.Run has no stdin. The script + container combination is rejected
		// earlier at validation (validateHarnessStep); this guards the remaining
		// stdin source — a custom harness with prompt_mode: stdin — which can only
		// be known after provider resolution. The CLI providers we containerize
		// (claude/codex/copilot) pass the prompt as argv, not stdin.
		e.exitCode = 1
		e.mu.Unlock()
		return nil, fmt.Errorf("harness: containerized harness does not support stdin input (prompt_mode: stdin); use a provider that passes the prompt as arguments")
	}

	expanded, err := dockerexec.EvalContainerFields(ctx, *e.step.Container)
	if err != nil {
		e.exitCode = 1
		e.mu.Unlock()
		return nil, fmt.Errorf("harness: failed to evaluate container config: %w", err)
	}

	// Inject only USER-declared env (DAG env, step env, outputs, secrets, params)
	// into the container, NOT the engine's OS env. The container has its own
	// HOME/PATH/USER from its image; injecting the engine container's os.Environ
	// would clobber them (e.g. HOME=/home/iand.guest, which is unwritable as the
	// image's uid and breaks the agent CLI). This mirrors the docker executor,
	// which injects only ct.Env.
	userEnv := env.UserEnvsMap()
	inheritedEnv := make([]string, 0, len(userEnv))
	for k, v := range userEnv {
		inheritedEnv = append(inheritedEnv, k+"="+v)
	}

	dcfg, runCmd, err := buildHarnessContainerRunConfig(
		e.workDir,
		expanded,
		dockerexec.RegistryAuthFromContext(ctx),
		cfg.binaryName(),
		args,
		inheritedEnv,
		// Runtime/socket selection comes from the engine PROCESS env only, so a
		// DAG/step env: cannot override the service-level DAGU_CONTAINER_RUNTIME
		// or DAGU_PODMAN_HOST and redirect the daemon socket.
		dockerexec.ServiceRuntimeEnv(),
	)
	if err != nil {
		e.exitCode = 1
		e.mu.Unlock()
		return nil, err
	}

	// Apply DAG resource limits to the harness container, mirroring the native
	// docker executor (newDocker) and the DAG-level container path (agent.go).
	// The host-subprocess harness path is constrained via the resourcelimit guard
	// on the child PID, but a daemon-created container is only constrained through
	// its HostConfig, so without this a containerized harness.run step would run
	// unbounded by the DAG's configured CPU/memory limits.
	if env.DAG != nil && env.DAG.Resources.HasLimits() &&
		!dockerexec.ApplyResourceLimitsToConfig(dcfg, env.DAG.Resources.Limits) {
		logger.Warn(ctx, "Resource limits requested but cannot be applied to the harness container")
	}

	stdout, err := newStdoutSpool()
	if err != nil {
		e.exitCode = 1
		e.mu.Unlock()
		return nil, fmt.Errorf("harness: failed to create stdout spool: %w", err)
	}

	// Publish the cancellable run context BEFORE the (potentially slow)
	// InitializeClient and release e.mu across init. Exec-mode readiness can wait
	// up to ~120s inside InitializeClient; Node.Stop dispatches to this executor's
	// Stop (it does not cancel the run ctx once an executor exists), and Stop needs
	// e.mu. Holding e.mu across init would block Stop until init returned. Mirrors
	// the native docker executor, which sets its cancel before InitializeClient.
	// The client itself is published only after init succeeds.
	runCtx, cancel := context.WithCancel(ctx)
	e.containerCancel = cancel
	e.mu.Unlock()

	cli, err := dockerexec.InitializeClient(runCtx, dcfg)
	if err != nil {
		e.mu.Lock()
		e.containerCancel = nil
		e.exitCode = 1
		e.mu.Unlock()
		cancel()
		_ = cleanupStdoutSpool(stdout)
		return nil, fmt.Errorf("harness: failed to initialize container client: %w", err)
	}

	e.mu.Lock()
	e.containerClient = cli
	e.mu.Unlock()

	defer func() {
		// Clear the client references under the lock BEFORE closing the client, so
		// a concurrent Stop()/Kill() cannot observe a non-nil containerClient and
		// then call Stop() on a client whose Close() has nil'd the underlying SDK
		// handle.
		e.mu.Lock()
		e.containerClient = nil
		e.containerCancel = nil
		e.mu.Unlock()
		cli.Close(ctx)
		cancel()
	}()

	// Run the container in a goroutine and watch ctx so a cancelled step (e.g.
	// timeout_sec, which arrives only as ctx cancellation, not as a Stop() call)
	// stops the container instead of blocking forever in Client.Run's post-wait
	// loop. Mirrors the host subprocess path in startAndWaitLocked.
	type containerRunResult struct {
		exitCode int
		err      error
	}
	runDone := make(chan containerRunResult, 1)
	go func() {
		ec, re := cli.Run(runCtx, runCmd, stdout, tw)
		runDone <- containerRunResult{exitCode: ec, err: re}
	}()

	var exitCode int
	var runErr error
	select {
	case <-ctx.Done():
		// Stop the container via the SDK, then wait for Run to unwind.
		_ = e.stop(cmdutil.StopRequest{Intent: cmdutil.ForceTermination(), Reason: cmdutil.StopReasonTimeout})
		<-runDone
		e.exitCode = 124
		_ = cleanupStdoutSpool(stdout)
		return nil, ctx.Err()
	case res := <-runDone:
		exitCode, runErr = res.exitCode, res.err
	}

	e.exitCode = exitCode
	if runErr != nil {
		if exitCode == 0 {
			e.exitCode = exitCodeFromError(runErr)
		}
		stdoutTail, tailErr := readSpoolTail(stdout, failedStdoutTailLimit, env.LogEncodingCharset)
		_ = cleanupStdoutSpool(stdout)
		if tailErr != nil {
			return nil, fmt.Errorf("harness: failed to read stdout tail: %w", tailErr)
		}
		if stdoutTail != "" {
			_, _ = fmt.Fprintf(e.stderrWriter(), "recent stdout (tail):\n%s\n", stdoutTail)
		}
		return nil, formatProcessFailure(runErr, tw.Tail(), stdoutTail)
	}

	if _, err := stdout.Seek(0, io.SeekStart); err != nil {
		e.exitCode = 1
		_ = cleanupStdoutSpool(stdout)
		return nil, fmt.Errorf("harness: failed to rewind stdout spool: %w", err)
	}
	return stdout, nil
}

func (e *harnessExecutor) runSharedContainerOnce(ctx context.Context, cfg providerConfig) (*os.File, error) {
	e.mu.Lock()

	env := runtime.GetEnv(ctx)
	tw := executor.NewTailWriterWithEncoding(e.stderrWriter(), 0, env.LogEncodingCharset)
	e.stderrTail = tw

	args, stdin, err := cfg.buildInvocation(e.effectivePrompt(), e.script)
	if err != nil {
		e.exitCode = 1
		e.mu.Unlock()
		return nil, err
	}
	if stdin != nil {
		e.exitCode = 1
		e.mu.Unlock()
		return nil, fmt.Errorf("harness: containerized harness does not support stdin input (prompt_mode: stdin); use a provider that passes the prompt as arguments")
	}

	cli := dockerexec.GetContainerClient(ctx)
	if cli == nil {
		e.exitCode = 1
		e.mu.Unlock()
		return nil, fmt.Errorf("harness: root-level container is configured but no shared container client is available")
	}

	stdout, err := newStdoutSpool()
	if err != nil {
		e.exitCode = 1
		e.mu.Unlock()
		return nil, fmt.Errorf("harness: failed to create stdout spool: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	e.sharedContainerCancel = cancel
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		e.sharedContainerCancel = nil
		e.mu.Unlock()
		cancel()
	}()

	runCmd := append([]string{cfg.binaryName()}, args...)
	exitCode, runErr := cli.Exec(runCtx, runCmd, stdout, tw, dockerexec.ExecOptions{
		Env:               sharedContainerHarnessEnv(env.UserEnvsMap()),
		Direct:            true,
		PIDFile:           sharedContainerHarnessPIDFile(e.step.Name),
		TerminateOnCancel: true,
	})
	e.exitCode = exitCode
	if runErr != nil {
		if ctx.Err() != nil && (errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded)) {
			e.exitCode = 124
		} else if exitCode == 0 {
			e.exitCode = exitCodeFromError(runErr)
		}
		stdoutTail, tailErr := readSpoolTail(stdout, failedStdoutTailLimit, env.LogEncodingCharset)
		_ = cleanupStdoutSpool(stdout)
		if tailErr != nil {
			return nil, fmt.Errorf("harness: failed to read stdout tail: %w", tailErr)
		}
		if stdoutTail != "" {
			_, _ = fmt.Fprintf(e.stderrWriter(), "recent stdout (tail):\n%s\n", stdoutTail)
		}
		return nil, formatProcessFailure(runErr, tw.Tail(), stdoutTail)
	}

	if _, err := stdout.Seek(0, io.SeekStart); err != nil {
		e.exitCode = 1
		_ = cleanupStdoutSpool(stdout)
		return nil, fmt.Errorf("harness: failed to rewind stdout spool: %w", err)
	}
	return stdout, nil
}

var sharedContainerHostPathEnvKeys = map[string]struct{}{
	"PWD":                                        {},
	coreexec.EnvKeyDAGDocsDir:                    {},
	coreexec.EnvKeyDAGRunArtifactsDir:            {},
	coreexec.EnvKeyDAGRunLogFile:                 {},
	coreexec.EnvKeyDAGRunStepStderrFile:          {},
	coreexec.EnvKeyDAGRunStepStdoutFile:          {},
	coreexec.EnvKeyDAGRunWorkDir:                 {},
	coreexec.EnvKeyDAGPushBackPreviousStdoutFile: {},
}

func sharedContainerHarnessEnv(userEnv map[string]string) []string {
	keys := make([]string, 0, len(userEnv))
	for key := range userEnv {
		if _, hostPath := sharedContainerHostPathEnvKeys[key]; hostPath {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	envs := make([]string, 0, len(keys))
	for _, key := range keys {
		envs = append(envs, key+"="+userEnv[key])
	}
	return envs
}

func sharedContainerHarnessPIDFile(stepName string) string {
	safeStepName := fileutil.SafeName(stepName)
	if safeStepName == "" {
		safeStepName = "step"
	}
	return fmt.Sprintf("/tmp/dagu-harness-%s-%d.pid", safeStepName, time.Now().UnixNano())
}

func (e *harnessExecutor) startAndWaitLocked(ctx context.Context, cmd *exec.Cmd, stdout *os.File, tw *executor.TailWriter, logEncoding string) (*os.File, error) {
	process, err := cmdutil.StartManagedProcess(cmd)
	if err != nil {
		e.exitCode = exitCodeFromError(err)
		_ = cleanupStdoutSpool(stdout)
		e.mu.Unlock()
		return nil, formatProcessFailure(err, tw.Tail(), "")
	}
	e.process = process
	if guard := resourcelimit.FromContext(ctx); guard != nil {
		if err := guard.AssignProcess(process.PID()); err != nil {
			logger.Warn(ctx, "Resource limits requested but process assignment failed", tag.Error(err))
		}
	}
	e.mu.Unlock()
	defer func() { _ = process.Release() }()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- process.Wait()
	}()

	select {
	case <-ctx.Done():
		_ = e.stop(cmdutil.StopRequest{Intent: cmdutil.ForceTermination(), Reason: cmdutil.StopReasonTimeout})
		<-waitDone
		e.exitCode = 124
		_ = cleanupStdoutSpool(stdout)
		return nil, ctx.Err()
	case err := <-waitDone:
		if err != nil {
			e.exitCode = exitCodeFromError(err)
			stdoutTail, tailErr := readSpoolTail(stdout, failedStdoutTailLimit, logEncoding)
			_ = cleanupStdoutSpool(stdout)
			if tailErr != nil {
				return nil, fmt.Errorf("harness: failed to read stdout tail: %w", tailErr)
			}
			if stdoutTail != "" {
				_, _ = fmt.Fprintf(e.stderrWriter(), "recent stdout (tail):\n%s\n", stdoutTail)
			}
			return nil, formatProcessFailure(err, tw.Tail(), stdoutTail)
		}
		if _, err := stdout.Seek(0, io.SeekStart); err != nil {
			e.exitCode = 1
			_ = cleanupStdoutSpool(stdout)
			return nil, fmt.Errorf("harness: failed to rewind stdout spool: %w", err)
		}
		return stdout, nil
	}
}

func (e *harnessExecutor) writeStdout(stdout *os.File) error {
	if stdout == nil {
		return nil
	}
	defer func() {
		_ = cleanupStdoutSpool(stdout)
	}()

	_, err := io.Copy(e.stdoutWriter(), stdout)
	return err
}

func (e *harnessExecutor) stdoutWriter() io.Writer {
	if e.stdout == nil {
		return io.Discard
	}
	return e.stdout
}

func (e *harnessExecutor) stderrWriter() io.Writer {
	if e.stderr == nil {
		return io.Discard
	}
	return e.stderr
}

// reservedKeys are config keys consumed by the harness executor itself, not passed as CLI flags.
var reservedKeys = map[string]bool{
	"provider": true,
	"fallback": true,
}

// configToFlags converts config map entries into CLI flags.
// Keys become --key, values are type-dependent:
//   - string → --key value
//   - bool true → --key (false is omitted)
//   - number → --key N
//   - []any → --key v1 --key v2 (repeated)
//
// Reserved keys are skipped. Built-in providers normalize snake_case keys to
// kebab-case. Keys are sorted for deterministic output.
func configToFlags(cfg map[string]any, definition *core.HarnessDefinition) []string {
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		if reservedKeys[k] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var args []string
	for _, key := range keys {
		flag := flagTokenForKey(key, definition)
		switch v := cfg[key].(type) {
		case bool:
			if v {
				args = append(args, flag)
			}
		case string:
			if v != "" {
				args = append(args, flag, v)
			}
		case int:
			args = append(args, flag, strconv.Itoa(v))
		case int8:
			args = append(args, flag, strconv.FormatInt(int64(v), 10))
		case int16:
			args = append(args, flag, strconv.FormatInt(int64(v), 10))
		case int32:
			args = append(args, flag, strconv.FormatInt(int64(v), 10))
		case int64:
			args = append(args, flag, strconv.FormatInt(v, 10))
		case uint:
			args = append(args, flag, strconv.FormatUint(uint64(v), 10))
		case uint8:
			args = append(args, flag, strconv.FormatUint(uint64(v), 10))
		case uint16:
			args = append(args, flag, strconv.FormatUint(uint64(v), 10))
		case uint32:
			args = append(args, flag, strconv.FormatUint(uint64(v), 10))
		case uint64:
			args = append(args, flag, strconv.FormatUint(v, 10))
		case float32:
			args = append(args, flag, strconv.FormatFloat(float64(v), 'f', -1, 32))
		case float64:
			if v == float64(int(v)) {
				args = append(args, flag, strconv.Itoa(int(v)))
			} else {
				args = append(args, flag, strconv.FormatFloat(v, 'f', -1, 64))
			}
		case []string:
			for _, item := range v {
				args = append(args, flag, item)
			}
		case []any:
			for _, item := range v {
				args = append(args, flag, fmt.Sprint(item))
			}
		}
	}
	return args
}

func newHarness(ctx context.Context, step core.Step) (executor.Executor, error) {
	if err := validatePromptCommand(step); err != nil {
		return nil, err
	}

	cfg := normalizeConfigMap(step.ExecutorConfig.Config)
	var defs core.HarnessDefinitions
	env := runtime.GetEnv(ctx)
	if env.DAG != nil {
		defs = env.DAG.Harnesses
	}
	configs, err := buildProviderConfigs(cfg, defs)
	if err != nil {
		return nil, err
	}

	prompt := extractPrompt(step)

	return &harnessExecutor{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		step:    step,
		configs: configs,
		prompt:  prompt,
		script:  step.Script,
		workDir: env.WorkingDir,
	}, nil
}

func buildProviderConfigs(cfg map[string]any, defs core.HarnessDefinitions) ([]providerConfig, error) {
	if err := validateProviderConfigs(cfg); err != nil {
		return nil, err
	}

	primary, fallbacks, err := extractFallbackConfigs(cfg)
	if err != nil {
		return nil, err
	}

	attempts := make([]map[string]any, 0, 1+len(fallbacks))
	attempts = append(attempts, primary)
	attempts = append(attempts, fallbacks...)

	configs := make([]providerConfig, 0, len(attempts))
	for i := range attempts {
		resolved, err := resolveProvider(attempts[i], defs)
		if err != nil {
			if i == 0 {
				return nil, err
			}
			return nil, fmt.Errorf("harness: invalid fallback[%d]: %w", i-1, err)
		}
		resolved.flags = mergeProviderDefaultConfig(resolved.provider, attempts[i])
		configs = append(configs, resolved)
	}

	return configs, nil
}

func extractFallbackConfigs(cfg map[string]any) (map[string]any, []map[string]any, error) {
	primary := cloneConfigMap(cfg)
	raw, ok := primary["fallback"]
	if !ok {
		return primary, nil, nil
	}
	delete(primary, "fallback")

	fallbacks, err := fallbackConfigsFromValue(raw)
	if err != nil {
		return nil, nil, err
	}
	return primary, fallbacks, nil
}

func fallbackConfigsFromValue(raw any) ([]map[string]any, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case []map[string]any:
		return cloneFallbackConfigs(v), nil
	case []any:
		fallbacks := make([]map[string]any, len(v))
		for i := range v {
			item, ok := v[i].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("harness: fallback[%d] must be an object", i)
			}
			fallbacks[i] = cloneConfigMap(item)
		}
		return fallbacks, nil
	default:
		return nil, fmt.Errorf("harness: fallback must be an array of objects")
	}
}

func resolveProvider(cfg map[string]any, defs core.HarnessDefinitions) (providerConfig, error) {
	providerName, _ := cfg["provider"].(string)
	if providerName == "" {
		return providerConfig{}, fmt.Errorf("harness: config.provider is required")
	}
	if isTemplatedValue(providerName) {
		return providerConfig{}, fmt.Errorf("harness: unresolved provider template %q", providerName)
	}
	if core.IsBuiltinAgentHarnessProvider(providerName) {
		return providerConfig{name: providerName, builtin: true}, nil
	}
	if core.IsBuiltinCLIHarnessProvider(providerName) {
		provider, err := getProvider(providerName)
		if err != nil {
			return providerConfig{}, err
		}
		return providerConfig{
			name:     provider.Name(),
			provider: provider,
		}, nil
	}
	if defs != nil {
		if def, ok := defs[providerName]; ok && def != nil {
			return providerConfig{
				name:       providerName,
				definition: cloneDefinition(def),
			}, nil
		}
	}
	return providerConfig{}, fmt.Errorf("harness: unknown provider %q; registered: %v", providerName, knownProviders(defs))
}

func mergeProviderDefaultConfig(provider Provider, cfg map[string]any) map[string]any {
	merged := cloneConfigMap(cfg)
	if provider == nil {
		return merged
	}
	defaultProvider, ok := provider.(defaultConfigProvider)
	if !ok {
		return merged
	}
	defaults := defaultProvider.DefaultConfig()
	if len(defaults) == 0 {
		return merged
	}
	defaults = core.NormalizeBuiltinHarnessFlagKeys(defaults)
	merged = core.NormalizeBuiltinHarnessFlagKeys(merged)
	withDefaults := cloneConfigMap(defaults)
	maps.Copy(withDefaults, merged)
	return withDefaults
}

func normalizeConfigMap(cfg map[string]any) map[string]any {
	if cfg == nil {
		return nil
	}

	normalized := make(map[string]any, len(cfg))
	for key, value := range cfg {
		normalized[key] = normalizeConfigValue(value)
	}
	return normalized
}

func normalizeConfigValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return normalizeConfigMap(v)
	case []any:
		normalized := make([]any, len(v))
		for i := range v {
			normalized[i] = normalizeConfigValue(v[i])
		}
		return normalized
	case []string:
		normalized := make([]string, len(v))
		for i := range v {
			normalized[i] = fmt.Sprint(coerceScalarString(v[i]))
		}
		return normalized
	case []map[string]any:
		normalized := make([]map[string]any, len(v))
		for i := range v {
			normalized[i] = normalizeConfigMap(v[i])
		}
		return normalized
	case string:
		return coerceScalarString(v)
	default:
		return value
	}
}

func coerceScalarString(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return value
	}

	var parsed any
	if err := yaml.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return value
	}

	switch parsed.(type) {
	case bool, int, int64, uint64, float32, float64:
		return parsed
	default:
		return value
	}
}

func cloneConfigMap(cfg map[string]any) map[string]any {
	if cfg == nil {
		return nil
	}

	cloned := make(map[string]any, len(cfg))
	for key, value := range cfg {
		cloned[key] = cloneConfigValue(value)
	}
	return cloned
}

func cloneFallbackConfigs(cfgs []map[string]any) []map[string]any {
	if cfgs == nil {
		return nil
	}

	cloned := make([]map[string]any, len(cfgs))
	for i := range cfgs {
		cloned[i] = cloneConfigMap(cfgs[i])
	}
	return cloned
}

func cloneConfigValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneConfigMap(v)
	case []any:
		cloned := make([]any, len(v))
		for i := range v {
			cloned[i] = cloneConfigValue(v[i])
		}
		return cloned
	case []string:
		return append([]string(nil), v...)
	case []map[string]any:
		return cloneFallbackConfigs(v)
	default:
		return value
	}
}

func extractPrompt(step core.Step) string {
	if len(step.Commands) == 0 {
		return ""
	}
	cmd := step.Commands[0]
	if cmd.CmdWithArgs != "" {
		return cmd.CmdWithArgs
	}
	if cmd.Command == "" {
		return ""
	}
	if len(cmd.Args) > 0 {
		return cmd.Command + " " + strings.Join(cmd.Args, " ")
	}
	return cmd.Command
}

func validateHarnessStep(step core.Step) error {
	if err := validatePromptCommand(step); err != nil {
		return err
	}
	// A containerized harness step runs the agent via the Moby SDK Client.Run,
	// which has no stdin; script input is piped to stdin only on the host
	// subprocess path. Reject the script + container combination at validation
	// time (fail fast at DAG load) instead of letting it pass and fail at run
	// time, since the executor advertises both Script and Container capability.
	// Use container.exec or drop script: to run a scripted harness in a container.
	if step.Container != nil && strings.TrimSpace(step.Script) != "" {
		return core.NewValidationError("script", nil,
			fmt.Errorf("action %q does not support script with a container; the containerized agent has no stdin", "harness"))
	}
	cfg := step.ExecutorConfig.Config
	if cfg == nil {
		return core.NewValidationError("with", nil, fmt.Errorf("config is required"))
	}

	if err := validateProviderConfigs(cfg); err != nil {
		return core.NewValidationError("with", nil, err)
	}
	return nil
}

func validatePromptCommand(step core.Step) error {
	if len(step.Commands) > 1 {
		return core.NewValidationError("command", nil, fmt.Errorf("action %q supports only one command", "harness"))
	}
	if len(step.Commands) == 0 || extractPrompt(step) == "" {
		return core.NewValidationError("command", nil, fmt.Errorf("command field (prompt) is required"))
	}
	return nil
}

func validateProviderConfigs(cfg map[string]any) error {
	if err := validateProviderConfig(cfg, true); err != nil {
		return err
	}

	fallbacks, err := fallbackConfigsFromValue(cfg["fallback"])
	if err != nil {
		return err
	}
	for i := range fallbacks {
		if err := validateProviderConfig(fallbacks[i], false); err != nil {
			return fmt.Errorf("harness: invalid fallback[%d]: %w", i, err)
		}
	}
	return nil
}

func validateProviderConfig(cfg map[string]any, allowFallback bool) error {
	providerStr, _ := cfg["provider"].(string)
	if _, exists := cfg["binary"]; exists {
		return fmt.Errorf("harness: config.binary is not supported; define a named harness under top-level harnesses and reference it via config.provider")
	}
	if _, exists := cfg["prompt_args"]; exists {
		return fmt.Errorf("harness: config.prompt_args is not supported; define a named harness under top-level harnesses and reference it via config.provider")
	}
	if !allowFallback {
		if _, exists := cfg["fallback"]; exists {
			return fmt.Errorf("harness: config.fallback is not supported inside fallback providers")
		}
	}
	if providerStr == "" {
		return fmt.Errorf("harness: config.provider is required")
	}
	if core.IsBuiltinAgentHarnessProvider(providerStr) {
		return core.ValidateBuiltinAgentHarnessConfig(cfg)
	}
	return nil
}

func (cfg providerConfig) binaryName() string {
	if cfg.provider != nil {
		return cfg.provider.BinaryName()
	}
	if cfg.definition != nil {
		return cfg.definition.Binary
	}
	return ""
}

func (cfg providerConfig) buildInvocation(prompt, script string) ([]string, io.Reader, error) {
	if cfg.provider != nil {
		args := cfg.provider.BaseArgs(prompt)
		args = append(args, configToFlags(cfg.flags, nil)...)

		if script == "" {
			return args, nil, nil
		}
		return args, strings.NewReader(script), nil
	}

	if cfg.definition == nil {
		return nil, nil, fmt.Errorf("harness: provider %q is not configured", cfg.name)
	}

	args := append([]string(nil), cfg.definition.PrefixArgs...)
	flags := configToFlags(cfg.flags, cfg.definition)

	switch cfg.definition.PromptMode {
	case core.HarnessPromptModeArg:
		promptArgs := []string{prompt}
		if cfg.definition.PromptPosition == core.HarnessPromptPositionAfterFlags {
			args = append(args, flags...)
			args = append(args, promptArgs...)
		} else {
			args = append(args, promptArgs...)
			args = append(args, flags...)
		}
		if script == "" {
			return args, nil, nil
		}
		return args, strings.NewReader(script), nil
	case core.HarnessPromptModeFlag:
		promptArgs := []string{cfg.definition.PromptFlag, prompt}
		if cfg.definition.PromptPosition == core.HarnessPromptPositionAfterFlags {
			args = append(args, flags...)
			args = append(args, promptArgs...)
		} else {
			args = append(args, promptArgs...)
			args = append(args, flags...)
		}
		if script == "" {
			return args, nil, nil
		}
		return args, strings.NewReader(script), nil
	case core.HarnessPromptModeStdin:
		args = append(args, flags...)
		return args, strings.NewReader(promptAndScript(prompt, script)), nil
	default:
		return nil, nil, fmt.Errorf("harness: unsupported prompt_mode %q for provider %q", cfg.definition.PromptMode, cfg.name)
	}
}

func flagTokenForKey(key string, definition *core.HarnessDefinition) string {
	if definition != nil && definition.OptionFlags != nil {
		if token, ok := definition.OptionFlags[key]; ok && strings.TrimSpace(token) != "" {
			return token
		}
	}
	if definition == nil {
		key = strings.ReplaceAll(key, "_", "-")
	}
	if definition != nil && definition.FlagStyle == core.HarnessFlagStyleSingleDash {
		return "-" + key
	}
	return "--" + key
}

func promptAndScript(prompt, script string) string {
	switch {
	case prompt == "":
		return script
	case script == "":
		return prompt
	default:
		return prompt + "\n\n" + script
	}
}

func knownProviders(defs core.HarnessDefinitions) []string {
	names := core.BuiltinHarnessProviderNames()
	for name, def := range defs {
		if def == nil {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func cloneDefinition(def *core.HarnessDefinition) *core.HarnessDefinition {
	if def == nil {
		return nil
	}
	return &core.HarnessDefinition{
		Binary:         def.Binary,
		PrefixArgs:     append([]string(nil), def.PrefixArgs...),
		PromptMode:     def.PromptMode,
		PromptFlag:     def.PromptFlag,
		PromptPosition: def.PromptPosition,
		FlagStyle:      def.FlagStyle,
		OptionFlags:    maps.Clone(def.OptionFlags),
	}
}

func isTemplatedValue(value string) bool {
	return strings.Contains(value, "${")
}

func resolveBinaryPath(binaryName, workDir string, envs map[string]string) (string, error) {
	if strings.TrimSpace(binaryName) == "" {
		return "", fmt.Errorf("empty binary name")
	}

	if hasPathSeparator(binaryName) {
		candidate := binaryName
		if !filepath.IsAbs(candidate) && workDir != "" {
			candidate = filepath.Join(workDir, candidate)
		}
		resolved, err := exec.LookPath(candidate)
		if err != nil {
			return "", err
		}
		return resolved, nil
	}

	pathValue := ""
	if envs != nil {
		pathValue = envs["PATH"]
	}
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}

	baseDir := workDir
	if baseDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory for PATH lookup: %w", err)
		}
		baseDir = wd
	}

	var lastErr error
	for _, dir := range filepath.SplitList(pathValue) {
		if dir == "" {
			dir = "."
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(baseDir, dir)
		}
		candidate := filepath.Join(dir, binaryName)
		resolved, err := exec.LookPath(candidate)
		if err == nil {
			return resolved, nil
		}
		if !errors.Is(err, exec.ErrNotFound) {
			lastErr = err
		}
	}

	if lastErr != nil {
		return "", lastErr
	}
	return "", &exec.Error{Name: binaryName, Err: exec.ErrNotFound}
}

func hasPathSeparator(path string) bool {
	return strings.Contains(path, string(os.PathSeparator)) || strings.Contains(path, "/")
}

func newStdoutSpool() (*os.File, error) {
	return os.CreateTemp("", "dagu-harness-stdout-*")
}

func cleanupStdoutSpool(file *os.File) error {
	if file == nil {
		return nil
	}

	name := file.Name()
	closeErr := file.Close()
	removeErr := fileutil.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return nil
}

func readSpoolTail(file *os.File, max int, encoding string) (string, error) {
	if file == nil || max <= 0 {
		return "", nil
	}

	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() == 0 {
		return "", nil
	}

	start := int64(0)
	if info.Size() > int64(max) {
		start = info.Size() - int64(max)
	}

	buf := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}

	return strings.TrimRight(fileutil.DecodeString(encoding, buf), "\r\n"), nil
}

func formatProcessFailure(err error, stderrTail, stdoutTail string) error {
	stderrTail = strings.TrimRight(stderrTail, "\r\n")
	stdoutTail = strings.TrimRight(stdoutTail, "\r\n")

	switch {
	case stderrTail != "" && stdoutTail != "":
		return fmt.Errorf("%w\nrecent stderr:\n%s\nrecent stdout:\n%s", err, stderrTail, stdoutTail)
	case stderrTail != "":
		return fmt.Errorf("%w\nrecent stderr:\n%s", err, stderrTail)
	case stdoutTail != "":
		return fmt.Errorf("%w\nrecent stdout:\n%s", err, stdoutTail)
	default:
		return err
	}
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func init() {
	caps := core.ExecutorCapabilities{
		Command:   true,
		Script:    true,
		Container: true,
	}
	executor.RegisterExecutor("harness", newHarness, validateHarnessStep, caps)
}
