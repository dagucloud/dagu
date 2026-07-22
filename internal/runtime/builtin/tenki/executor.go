// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package tenki provides an executor that runs a step's command or script
// inside a Tenki Cloud sandbox (https://tenki.cloud) via the Go sandbox SDK.
package tenki

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/TenkiCloud/tenki-sdk-go/sandbox"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

// Tenki executor runs a command or script inside a Tenki Cloud sandbox; auth and
// project/workspace settings come from `with:` (api_key/project_id/workspace_id) with TENKI_* environment fallbacks.
/* Example DAG:
```yaml
steps:
 - name: run-in-sandbox
   type: tenki
   with:
     name: my-sandbox    # optional
     cpu_cores: 2        # optional
     memory_mb: 4096     # optional
     env:                # optional session env
       - GREETING=Hello
   command: echo "$GREETING from Tenki sandboxes"

 - name: reuse-session
   type: tenki
   with:
     session_id: <existing-session-id>  # exec in an existing sandbox; not terminated
   command: uname -a
```
*/

var _ executor.Executor = (*tenkiExecutor)(nil)
var _ executor.ExitCoder = (*tenkiExecutor)(nil)

type tenkiExecutor struct {
	mu             sync.Mutex
	step           core.Step
	cfg            config
	stdout         io.Writer
	stderr         io.Writer
	cancel         context.CancelFunc
	client         *sandbox.Client
	session        *sandbox.Session
	ownsSession    bool
	cleanupTimeout time.Duration
	closed         bool
	exitCode       int
}

func newTenki(_ context.Context, step core.Step) (executor.Executor, error) {
	cfg, err := loadConfig(step.ExecutorConfig.Config)
	if err != nil {
		return nil, err
	}
	return &tenkiExecutor{
		step:   step,
		cfg:    cfg,
		stdout: os.Stdout,
		stderr: os.Stderr,
	}, nil
}

func (e *tenkiExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (e *tenkiExecutor) SetStderr(out io.Writer) { e.stderr = out }

// ExitCode implements executor.ExitCoder.
func (e *tenkiExecutor) ExitCode() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exitCode
}

func (e *tenkiExecutor) Run(ctx context.Context) error {
	if len(e.step.Commands) == 0 && e.step.Script == "" {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancel = cancel
	e.mu.Unlock()
	defer cancel()

	// Tail stderr so recent output can be surfaced in error messages.
	env := runtime.GetEnv(ctx)
	cleanupTimeout := cleanupTimeoutOf(env)
	tw := executor.NewTailWriterWithEncoding(e.stderr, 0, env.LogEncodingCharset)
	e.stderr = tw

	client, err := e.newClient()
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.client = client
	e.mu.Unlock()
	defer client.Close()

	session, ownsSession, err := e.resolveSession(ctx, client, cleanupTimeout)
	if err != nil {
		if tail := tw.Tail(); tail != "" {
			return fmt.Errorf("failed to set up tenki sandbox: %w\nrecent stderr (tail):\n%s", err, tail)
		}
		return fmt.Errorf("failed to set up tenki sandbox: %w", err)
	}
	e.mu.Lock()
	e.session = session
	e.ownsSession = ownsSession
	e.cleanupTimeout = cleanupTimeout
	e.mu.Unlock()

	if ownsSession && !e.cfg.KeepSession {
		defer func() {
			// Detach from the run context so teardown still runs after cancellation.
			termCtx, cancel := withCleanupTimeout(context.WithoutCancel(ctx), cleanupTimeout)
			defer cancel()
			_ = session.CloseIfOpen(termCtx)
		}()
	}

	return e.exec(ctx, session, tw)
}

// resolveSession returns an existing sandbox when session_id is set, otherwise
// creates a new one and reports ownership so Run knows whether to terminate it.
func (e *tenkiExecutor) resolveSession(ctx context.Context, client *sandbox.Client, cleanupTimeout time.Duration) (*sandbox.Session, bool, error) {
	if id := strings.TrimSpace(e.cfg.SessionID); id != "" {
		session, err := client.Get(ctx, id)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get sandbox session %q: %w", id, err)
		}
		return session, false, nil
	}

	session, err := client.Create(ctx, e.createOptions()...)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create sandbox session: %w", err)
	}
	// Wait for readiness ourselves so a sandbox that was created but never
	// becomes ready is torn down instead of leaking.
	if err := session.WaitReady(ctx, e.cfg.CreateTimeout); err != nil {
		termCtx, cancel := withCleanupTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		_ = session.CloseIfOpen(termCtx)
		return nil, false, fmt.Errorf("sandbox did not become ready: %w", err)
	}
	return session, true, nil
}

// createOptions builds the create options; WithWaitReady(false) returns the
// session immediately so readiness and cleanup on failure are handled here.
func (e *tenkiExecutor) createOptions() []sandbox.CreateOption {
	opts := []sandbox.CreateOption{sandbox.WithWaitReady(false)}
	if e.cfg.Name != "" {
		opts = append(opts, sandbox.WithName(e.cfg.Name))
	}
	if e.cfg.Image != "" {
		opts = append(opts, sandbox.WithImage(e.cfg.Image))
	}
	if e.cfg.CPUCores > 0 {
		opts = append(opts, sandbox.WithCPUCores(e.cfg.CPUCores))
	}
	if e.cfg.MemoryMB > 0 {
		opts = append(opts, sandbox.WithMemoryMB(e.cfg.MemoryMB))
	}
	if e.cfg.MaxDuration > 0 {
		opts = append(opts, sandbox.WithMaxDuration(e.cfg.MaxDuration))
	}
	if envMap := envSliceToMap(e.cfg.Env); len(envMap) > 0 {
		opts = append(opts, sandbox.WithEnvs(envMap))
	}
	if projectID := firstNonEmpty(e.cfg.ProjectID, os.Getenv("TENKI_PROJECT_ID")); projectID != "" {
		opts = append(opts, sandbox.WithProjectID(projectID))
	}
	if workspaceID := firstNonEmpty(e.cfg.WorkspaceID, os.Getenv("TENKI_WORKSPACE_ID")); workspaceID != "" {
		opts = append(opts, sandbox.WithWorkspaceID(workspaceID))
	}
	return opts
}

// exec streams the step command/script into the sandbox and reports the result.
func (e *tenkiExecutor) exec(ctx context.Context, session *sandbox.Session, tw *executor.TailWriter) error {
	shell, shellArgs := e.resolveShell()
	argv := append([]string{shell}, shellArgs...)
	argv = append(argv, "-c", e.buildScript())

	runOpts := sandbox.RunOptions{
		Env: e.execEnv(),
		Dir: e.workingDir(),
	}

	handle, err := session.Command(argv, runOpts).Stream(ctx)
	if err != nil {
		if tail := tw.Tail(); tail != "" {
			return fmt.Errorf("failed to start sandbox command: %w\nrecent stderr (tail):\n%s", err, tail)
		}
		return fmt.Errorf("failed to start sandbox command: %w", err)
	}
	_ = handle.Stdin.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(e.stdout, handle.Stdout) }()
	go func() { defer wg.Done(); _, _ = io.Copy(e.stderr, handle.Stderr) }()

	result, waitErr := handle.Wait()
	wg.Wait()

	if waitErr != nil {
		if tail := tw.Tail(); tail != "" {
			return fmt.Errorf("sandbox command failed: %w\nrecent stderr (tail):\n%s", waitErr, tail)
		}
		return fmt.Errorf("sandbox command failed: %w", waitErr)
	}

	e.setExitCode(int(result.ExitCode))

	if result.ExitCode != 0 || result.Status.IsFailed() || result.Status.IsTimedOut() {
		if tail := tw.Tail(); tail != "" {
			return fmt.Errorf("sandbox command exited with status %s (code %d)\nrecent stderr (tail):\n%s", result.Status, result.ExitCode, tail)
		}
		return fmt.Errorf("sandbox command exited with status %s (code %d)", result.Status, result.ExitCode)
	}
	return nil
}

func (e *tenkiExecutor) Kill(_ os.Signal) error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	if e.cancel != nil {
		e.cancel()
	}
	session, ownsSession, keepSession, timeout := e.session, e.ownsSession, e.cfg.KeepSession, e.cleanupTimeout
	e.mu.Unlock()

	if ownsSession && session != nil && !keepSession {
		ctx, cancel := withCleanupTimeout(context.Background(), timeout)
		defer cancel()
		return session.CloseIfOpen(ctx)
	}
	return nil
}

func cleanupTimeoutOf(env runtime.Env) time.Duration {
	if env.DAG != nil {
		return env.DAG.MaxCleanUpTime
	}
	return 0
}

func withCleanupTimeout(base context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(base, timeout)
	}
	return base, func() {}
}

func (e *tenkiExecutor) newClient() (*sandbox.Client, error) {
	var opts []sandbox.Option
	if e.cfg.APIKey != "" {
		opts = append(opts, sandbox.WithAuthToken(e.cfg.APIKey))
	}
	if e.cfg.APIURL != "" {
		opts = append(opts, sandbox.WithBaseURL(e.cfg.APIURL))
	}
	client, err := sandbox.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create tenki client: %w", err)
	}
	return client, nil
}

func (e *tenkiExecutor) setExitCode(code int) {
	e.mu.Lock()
	e.exitCode = code
	e.mu.Unlock()
}

// resolveShell returns the in-sandbox shell and its args: with.shell, then step
// shell, then /bin/sh.
func (e *tenkiExecutor) resolveShell() (string, []string) {
	if e.cfg.Shell != "" {
		return e.cfg.Shell, slices.Clone(e.cfg.ShellArgs)
	}
	if e.step.Shell != "" {
		return e.step.Shell, slices.Clone(e.step.ShellArgs)
	}
	return defaultShell, nil
}

// workingDir resolves the in-sandbox working directory; empty leaves the SDK
// default of the guest home.
func (e *tenkiExecutor) workingDir() string {
	if d := strings.TrimSpace(e.cfg.WorkingDir); d != "" {
		return d
	}
	return e.step.Dir
}

// execEnv merges with.env and step env so variables apply whether the sandbox
// is created or reused; step env wins on conflicts.
func (e *tenkiExecutor) execEnv() map[string]string {
	entries := make([]string, 0, len(e.cfg.Env)+len(e.step.Env))
	entries = append(entries, e.cfg.Env...)
	entries = append(entries, e.step.Env...)
	return envSliceToMap(entries)
}

// buildScript wraps the step command(s) or script for shell execution with
// fail-fast semantics.
func (e *tenkiExecutor) buildScript() string {
	var b strings.Builder
	b.WriteString("set -e\n")
	if e.step.Script != "" {
		b.WriteString(e.step.Script)
		if !strings.HasSuffix(e.step.Script, "\n") {
			b.WriteString("\n")
		}
		return b.String()
	}
	for _, c := range e.step.Commands {
		b.WriteString(commandString(c))
		b.WriteString("\n")
	}
	return b.String()
}

// commandString renders a command entry, preferring the original expanded
// string so variable references pass through to the sandbox shell.
func commandString(cmd core.CommandEntry) string {
	if cmd.CmdWithArgs != "" {
		return cmd.CmdWithArgs
	}
	if len(cmd.Args) == 0 {
		return cmd.Command
	}
	return cmd.Command + " " + cmdutil.ShellQuoteArgs(cmd.Args)
}

// firstNonEmpty returns the first non-empty, trimmed value.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func envSliceToMap(entries []string) map[string]string {
	if len(entries) == 0 {
		return nil
	}
	m := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, val, found := strings.Cut(entry, "=")
		if !found || key == "" {
			continue
		}
		m[key] = val
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func init() {
	caps := core.ExecutorCapabilities{
		Command:          true,
		MultipleCommands: true,
		Script:           true,
		Shell:            true,
		CommandContext: func(_ context.Context, step core.Step) cmnvalue.CommandContext {
			return cmnvalue.CommandContext{
				Target:          cmnvalue.CommandTargetSSH,
				ShellConfigured: hasShellConfigured(step),
			}
		},
		ScriptContext: func(_ context.Context, step core.Step) cmnvalue.CommandContext {
			return cmnvalue.CommandContext{
				Target:          cmnvalue.CommandTargetSSH,
				ShellConfigured: hasShellConfigured(step),
			}
		},
	}
	executor.RegisterExecutor(executorType, newTenki, nil, caps)
}

func hasShellConfigured(step core.Step) bool {
	if len(step.ExecutorConfig.Config) > 0 && cmdutil.IsShellValueSet(step.ExecutorConfig.Config["shell"]) {
		return true
	}
	return step.Shell != ""
}
