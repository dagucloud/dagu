// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/resourcelimit"
)

const (
	// acpProgressInterval bounds run-status writes caused by streamed session updates.
	acpProgressInterval = time.Second
	// acpCancelTimeout bounds delivery of a session/cancel notification.
	acpCancelTimeout = time.Second
	// acpExitGrace lets a local adapter exit on its own after a failed turn.
	acpExitGrace = time.Second

	// acpPermissionKey is the reserved option selecting the ACP permission policy.
	acpPermissionKey = "acp_permission"
)

// runACP executes one prompt over the configured ACP transport and spools its text output.
func (e *harnessExecutor) runACP(parent context.Context, cfg providerConfig) (stdout *os.File, err error) {
	e.exitCode = 1
	// A DAG-level container does not apply to agents reached over the network.
	remote := cfg.definition != nil && cfg.definition.IsRemote()
	if e.step.Container != nil || (!remote && rootContainerConfigured(parent)) {
		return nil, errors.New("harness: ACP is not supported inside containers")
	}
	policy := "deny"
	flags := cloneConfigMap(cfg.flags)
	if raw, ok := flags[acpPermissionKey]; ok {
		policy, _ = raw.(string)
		delete(flags, acpPermissionKey)
	}
	if policy != "deny" && policy != "allow" {
		return nil, errors.New("harness: acp_permission must be deny or allow")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	spool, err := newStdoutSpool()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = cleanupStdoutSpool(spool)
		}
	}()
	client, releaseClient := e.newACPClient(spool, policy == "allow")
	defer releaseClient()
	// Register before setup so a Stop that arrives while the agent starts is not lost.
	e.mu.Lock()
	if e.stopRequested {
		e.mu.Unlock()
		return nil, context.Canceled
	}
	e.acpCancel, e.acpClient = cancel, client
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.acpCancel, e.acpClient = nil, nil
		e.mu.Unlock()
	}()
	switch cfg.definition.Transport {
	case ir.HarnessTransportWebSocket, ir.HarnessTransportHTTP:
		if err = e.runACPRemote(ctx, cfg, client); err != nil {
			return nil, err
		}
		if _, err = spool.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		return spool, nil
	case "", ir.HarnessTransportStdio:
	default:
		return nil, fmt.Errorf("harness: unsupported ACP transport %q", cfg.definition.Transport)
	}
	env := runtime.GetEnv(ctx)
	tw := executor.NewTailWriterWithEncoding(e.stderrWriter(), 0, env.LogEncodingCharset)
	e.stderrTail = tw
	args := append([]string(nil), cfg.definition.PrefixArgs...)
	args = append(args, configToFlags(flags, cfg.definition)...)
	cmd, err := e.invocationCommand(ctx, cfg, args, nil, nil, tw)
	if err != nil {
		return nil, err
	}
	cmd.Dir, err = filepath.Abs(cmd.Dir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(cmd.Dir, 0o750); err != nil {
		return nil, err
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	defer func() { _ = input.Close() }()
	cmd.Stdout = nil
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	defer func() { _ = output.Close() }()
	// Bound cleanup when agent descendants keep the stderr pipe open.
	cmd.WaitDelay = time.Second
	e.mu.Lock()
	process, err := cmdutil.StartManagedProcess(cmd)
	if err != nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("harness: start ACP agent: %w", err)
	}
	if guard := resourcelimit.FromContext(ctx); guard != nil {
		if assignErr := guard.AssignProcess(process.PID()); assignErr != nil {
			logger.Warn(ctx, "Resource limits requested but process assignment failed", tag.Error(assignErr))
		}
	}
	e.process = process
	e.mu.Unlock()
	conn := acp.NewClientSideConnection(client, input, output)
	conn.SetLogger(acpLogger(tw))
	defer func() {
		_ = input.Close()
		failed := err != nil && ctx.Err() == nil
		var grace time.Duration
		if failed {
			grace = acpExitGrace
		}
		waitErr := stopACPAgent(process, grace)
		if failed {
			if code := exitCodeFromError(waitErr); code > 0 {
				e.exitCode = code
			}
			err = formatProcessFailure(err, tw.Tail(), "")
		}
		_ = output.Close()
		<-conn.Done()
		_ = process.Release()
		e.mu.Lock()
		e.process = nil
		e.mu.Unlock()
	}()
	defer watchACPCancellation(ctx, conn, client, func() {
		_ = input.Close()
		_, _ = process.Stop(cmdutil.StopRequest{Intent: cmdutil.ForceTermination()})
	})()
	if err = e.acpTurn(ctx, conn, client, cmd.Dir); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, fmt.Errorf("harness: ACP: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return spool, nil
}

// stopACPAgent ends a local adapter and returns its wait result. A positive
// grace lets the adapter exit on its own first, so its exit code survives.
func stopACPAgent(process *cmdutil.ManagedProcess, grace time.Duration) error {
	waited := make(chan error, 1)
	go func() { waited <- process.Wait() }()
	if grace > 0 {
		select {
		case err := <-waited:
			return err
		case <-time.After(grace):
		}
	}
	_, _ = process.Stop(cmdutil.StopRequest{Intent: cmdutil.ForceTermination()})
	return <-waited
}

// acpLogger keeps SDK diagnostics in the step log and drops routine notices.
func acpLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// watchACPCancellation sends session/cancel before interrupting a blocked transport.
// Its cleanup waits for the callback, so process and stream resources are not released early.
func watchACPCancellation(ctx context.Context, conn *acp.ClientSideConnection, client *acpHarnessClient, interrupt func()) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		client.mu.Lock()
		session := client.session
		client.mu.Unlock()
		if session == "" {
			interrupt()
			return
		}
		cancelCtx, cancel := context.WithTimeout(context.Background(), acpCancelTimeout)
		defer cancel()
		sent := make(chan struct{})
		go func() {
			defer close(sent)
			_ = conn.Cancel(cancelCtx, acp.CancelNotification{SessionId: session})
		}()
		select {
		case <-sent:
		case <-cancelCtx.Done():
		}
		interrupt()
		<-sent
	})
	return func() {
		if !stop() {
			<-done
		}
	}
}

// acpTurn negotiates ACP v1, creates a session, and checks the prompt result.
func (e *harnessExecutor) acpTurn(ctx context.Context, conn *acp.ClientSideConnection, client *acpHarnessClient, directory string) error {
	init, err := conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if init.ProtocolVersion < 1 || init.ProtocolVersion > acp.ProtocolVersionNumber {
		return fmt.Errorf("unsupported protocol version %d", init.ProtocolVersion)
	}
	session, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: directory, McpServers: []acp.McpServer{}})
	if err != nil {
		return fmt.Errorf("new session (authenticate the adapter before running Dagu): %w", err)
	}
	if session.SessionId == "" {
		return errors.New("agent returned an empty session ID")
	}
	client.mu.Lock()
	client.conn, client.session = conn, session.SessionId
	client.mu.Unlock()
	prompt := promptAndScript(e.effectivePrompt(), e.script)
	response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: session.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock(prompt)}})
	client.mu.Lock()
	defer client.mu.Unlock()
	// The SDK can still deliver queued updates after the response; they must not
	// reach the stdout spool once the caller rewinds it.
	client.finished = true
	if err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	if client.cancelled && response.StopReason == acp.StopReasonCancelled {
		return context.Canceled
	}
	if client.err != nil {
		return client.err
	}
	if response.StopReason != acp.StopReasonEndTurn {
		return fmt.Errorf("prompt stopped: %s", response.StopReason)
	}
	return nil
}

type acpHarnessClient struct {
	mu        sync.Mutex
	out       io.Writer
	allow     bool
	conn      *acp.ClientSideConnection
	session   acp.SessionId
	err       error
	cancelled bool
	finished  bool
	progress  chan struct{}
}

var _ acp.Client = (*acpHarnessClient)(nil)

// newACPClient returns a client for one attempt and a release func.
// Session updates report progress at most once per acpProgressInterval, and
// none are reported after release returns.
func (e *harnessExecutor) newACPClient(out io.Writer, allow bool) (*acpHarnessClient, func()) {
	client := &acpHarnessClient{out: out, allow: allow, progress: make(chan struct{}, 1)}
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-quit:
				return
			case <-client.progress:
			}
			e.notifyProgress()
			select {
			case <-quit:
				return
			case <-time.After(acpProgressInterval):
			}
		}
	}()
	return client, func() {
		close(quit)
		<-done
	}
}

// cancelTurn asks the agent to end the active prompt with session/cancel.
// It reports false when no prompt is in flight to cancel.
func (c *acpHarnessClient) cancelTurn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil || c.finished {
		return false
	}
	if !c.cancelled {
		c.cancelled = true
		conn, session := c.conn, c.session
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), acpCancelTimeout)
			defer cancel()
			_ = conn.Cancel(ctx, acp.CancelNotification{SessionId: session})
		}()
	}
	return true
}

// SessionUpdate captures ordered assistant text for the active session.
func (c *acpHarnessClient) SessionUpdate(_ context.Context, params acp.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if params.SessionId != c.session || c.finished {
		return nil
	}
	select {
	case c.progress <- struct{}{}:
	default:
	}
	if chunk := params.Update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil {
		_, err := io.WriteString(c.out, chunk.Content.Text.Text)
		if err != nil {
			c.err = err
		}
		return err
	}
	return nil
}

// RequestPermission accepts allow_once options only when explicitly enabled and
// otherwise rejects the tool call. Denials fail the attempt even if the agent
// subsequently returns end_turn.
func (c *acpHarnessClient) RequestPermission(_ context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cancelled := acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}
	// ACP requires cancelled outcomes for requests pending after session/cancel.
	if c.cancelled {
		return cancelled, nil
	}
	if params.SessionId == c.session && c.allow {
		if response, ok := selectPermission(params.Options, acp.PermissionOptionKindAllowOnce); ok {
			return response, nil
		}
		c.err = errors.New("ACP permission request denied; the agent offered no allow_once option")
	} else {
		c.err = errors.New("ACP permission request denied; set acp_permission: allow to accept allow_once requests")
	}
	for _, kind := range []acp.PermissionOptionKind{acp.PermissionOptionKindRejectOnce, acp.PermissionOptionKindRejectAlways} {
		if response, ok := selectPermission(params.Options, kind); ok {
			return response, nil
		}
	}
	return cancelled, nil
}

// selectPermission chooses the first offered option of the given kind.
func selectPermission(options []acp.PermissionOption, kind acp.PermissionOptionKind) (acp.RequestPermissionResponse, bool) {
	for _, option := range options {
		if option.Kind == kind {
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: option.OptionId}}}, true
		}
	}
	return acp.RequestPermissionResponse{}, false
}

// ReadTextFile rejects client filesystem access, which is not advertised.
func (*acpHarnessClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound("fs/read_text_file")
}

// WriteTextFile rejects client filesystem writes, which are not advertised.
func (*acpHarnessClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound("fs/write_text_file")
}

// CreateTerminal rejects terminal creation, which is not advertised.
func (*acpHarnessClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound("terminal")
}

// TerminalOutput rejects terminal output requests because no client terminals exist.
func (*acpHarnessClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound("terminal")
}

// ReleaseTerminal rejects terminal release because no client terminals exist.
func (*acpHarnessClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound("terminal")
}

// WaitForTerminalExit rejects terminal waits because no client terminals exist.
func (*acpHarnessClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound("terminal")
}

// KillTerminal rejects terminal termination because no client terminals exist.
func (*acpHarnessClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound("terminal")
}
