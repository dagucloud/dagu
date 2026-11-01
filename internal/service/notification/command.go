// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package notification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
)

const (
	defaultNotificationCommandTimeout = 30 * time.Second
	defaultExecutionLogLimit          = 1 << 20
	commandStderrLimit                = 4 << 10
	executionLogTruncatedNote         = "\n# log truncated\n"
)

type commandEnvField struct {
	env   string
	token string
}

// commandEnvFields are the shell-safe names for notification template fields.
// Dotted template names such as dag.name are not expandable as $dag.name.
var commandEnvFields = []commandEnvField{
	{"DAG_NAME", "dag.name"},
	{"DAG_RUN_ID", "run.id"},
	{"STATUS", "run.status"},
	{"EVENT_TYPE", "event.type"},
	{"ERROR", "run.error"},
	{"WORKSPACE", "workspace"},
	{"WORKER_ID", "worker.id"},
	{"ATTEMPT_ID", "run.attemptId"},
	{"STARTED_AT", "run.startedAt"},
	{"FINISHED_AT", "run.finishedAt"},
	{"OBSERVED_AT", "event.observedAt"},
	{"RUN_PATH", "run.path"},
	{"RUN_URL", "run.url"},
	{"RUN_LINK", "run.link"},
	{"FAILED_STEPS", "run.failed_steps"},
	{"PARTIALLY_SUCCEEDED_STEPS", "run.partially_succeeded_steps"},
	{"ABORTED_STEPS", "run.aborted_steps"},
	{"SUCCEEDED_STEPS", "run.succeeded_steps"},
}

func (s *Service) sendCommand(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) error {
	if !s.commandChannelsEnabled {
		return errors.New("command channels are disabled on this server")
	}
	if target.Command == nil || strings.TrimSpace(target.Command.Command) == "" {
		return errors.New("command is not configured")
	}
	publicURL := s.publicURL()
	for _, event := range events {
		if event.Status == nil {
			continue
		}
		if err := s.runNotificationCommand(ctx, target.Command.Command, event, publicURL); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) runNotificationCommand(
	ctx context.Context,
	command string,
	event chatbridge.NotificationEvent,
	publicURL string,
) error {
	values := notificationTemplateValues(event, publicURL)

	timeout := s.notificationCommandTimeout
	if timeout <= 0 {
		timeout = defaultNotificationCommandTimeout
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	logPath, err := writeExecutionLog(cmdCtx, s.commandLogs, event.Status, s.executionLogLimit())
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("command timed out after %s", timeout)
		}
		return err
	}
	if logPath != "" {
		defer func() { _ = os.Remove(logPath) }()
	}

	shell, args, err := notificationShellCommand(command)
	if err != nil {
		return err
	}

	stderr := &limitedBuffer{remaining: commandStderrLimit}
	proc, err := startNotificationProcess(shell, args, commandEnvironment(s.commandBaseEnv, values, logPath), stderr)
	if err != nil {
		return fmt.Errorf("run notification command: %w", err)
	}
	defer func() { _ = proc.Release() }()

	waitDone := make(chan error, 1)
	go func() { waitDone <- proc.Wait() }()

	var runErr error
	select {
	case runErr = <-waitDone:
	case <-cmdCtx.Done():
		_, _ = proc.Stop(cmdutil.StopRequest{
			Intent: cmdutil.ForceTermination(),
			Reason: cmdutil.StopReasonTimeout,
		})
		<-waitDone
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("command timed out after %s", timeout)
	}
	// Children the shell left behind would keep running after delivery
	// returns. On Windows, releasing the process's job object stops them.
	if runtime.GOOS != "windows" {
		_, _ = proc.Stop(cmdutil.StopRequest{Intent: cmdutil.ForceTermination()})
	}
	if runErr == nil {
		return nil
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
		detail := stderr.String()
		if detail != "" {
			return fmt.Errorf("command exited with status %d: %s", exitErr.ExitCode(), detail)
		}
		return fmt.Errorf("command exited with status %d", exitErr.ExitCode())
	}
	return fmt.Errorf("run notification command: %w", runErr)
}

func (s *Service) executionLogLimit() int {
	if s != nil && s.notificationLogLimit > 0 {
		return s.notificationLogLimit
	}
	return defaultExecutionLogLimit
}

// notificationShellCommand returns the shell and arguments that run command:
// sh on Unix, and PowerShell on Windows, where cmd.exe would expand %VAR%
// before parsing and let run values act as syntax.
func notificationShellCommand(command string) (string, []string, error) {
	if runtime.GOOS != "windows" {
		return "/bin/sh", []string{"-c", command}, nil
	}
	for _, name := range []string{"pwsh", "powershell"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, []string{"-NoProfile", "-NonInteractive", "-Command", command}, nil
		}
	}
	return "", nil, errors.New("command channels on Windows need PowerShell (pwsh or powershell)")
}

func startNotificationProcess(shell string, args []string, env []string, stderr io.Writer) (*cmdutil.ManagedProcess, error) {
	cmd := exec.Command(shell, args...) // #nosec G204 -- shell is the platform command shell; args carry the configured notification command.
	cmd.WaitDelay = time.Second
	cmd.Env = env
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	return cmdutil.StartManagedProcess(cmd)
}

func commandEnvironment(base []string, values map[string]string, logPath string) []string {
	extra := make(map[string]string, len(commandEnvFields)+1)
	for _, field := range commandEnvFields {
		extra[field.env] = sanitizeEnvValue(values[field.token])
	}
	extra["EXECUTION_LOG"] = sanitizeEnvValue(logPath)
	skip := make(map[string]struct{}, len(extra))
	for key := range extra {
		skip[envLookupKey(key)] = struct{}{}
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, found := skip[envLookupKey(key)]; found {
			continue
		}
		out = append(out, entry)
	}
	for _, key := range slices.Sorted(maps.Keys(extra)) {
		out = append(out, key+"="+extra[key])
	}
	return out
}

func envLookupKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

func sanitizeEnvValue(value string) string {
	return strings.ReplaceAll(value, "\x00", "")
}

type commandLogSection struct {
	label string
	path  string
}

func executionLogSections(status *ir.DAGRunStatus) []commandLogSection {
	if status == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var sections []commandLogSection
	add := func(label, path string) {
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		sections = append(sections, commandLogSection{label: label, path: path})
	}
	add("DAG log", status.Log)
	for _, node := range status.NodesInRunOrder() {
		if node == nil {
			continue
		}
		name := node.Step.Name
		if name == "" {
			name = "step"
		}
		add("stdout "+name, node.Stdout)
		add("stderr "+name, node.Stderr)
	}
	return sections
}

func writeExecutionLog(ctx context.Context, logs LogOpener, status *ir.DAGRunStatus, limit int) (string, error) {
	sections := executionLogSections(status)
	if len(sections) == 0 {
		return "", nil
	}
	if limit <= 0 {
		limit = defaultExecutionLogLimit
	}
	file, err := os.CreateTemp("", "dagu-notification-log-*")
	if err != nil {
		return "", fmt.Errorf("create notification log file: %w", err)
	}
	path := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(path)
	}
	remaining := limit
	if len(executionLogTruncatedNote) < remaining {
		remaining -= len(executionLogTruncatedNote)
	}
	wrote := false
	truncated := false
	for _, section := range sections {
		if err := ctx.Err(); err != nil {
			cleanup()
			return "", err
		}
		if remaining <= 0 {
			truncated = true
			break
		}
		src, err := logs.OpenLog(ctx, section.path)
		if err != nil {
			continue
		}
		header := ""
		if wrote {
			header = "\n"
		}
		header += "# " + section.label + "\n"
		if len(header) > remaining {
			truncated = true
			_ = src.Close()
			break
		}
		if _, err := io.WriteString(file, header); err != nil {
			_ = src.Close()
			cleanup()
			return "", err
		}
		remaining -= len(header)
		copied, more, err := copyLimited(ctx, file, src, remaining)
		_ = src.Close()
		if err != nil {
			cleanup()
			return "", err
		}
		remaining -= copied
		wrote = true
		if more {
			truncated = true
			break
		}
	}
	if truncated {
		if _, err := io.WriteString(file, executionLogTruncatedNote); err != nil {
			cleanup()
			return "", err
		}
		wrote = true
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if !wrote {
		_ = os.Remove(path)
		return "", nil
	}
	return path, nil
}

func copyLimited(ctx context.Context, dst io.Writer, src io.Reader, remaining int) (int, bool, error) {
	buf := make([]byte, 32*1024)
	written := 0
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return written, false, err
		}
		chunk := buf
		if remaining < len(chunk) {
			chunk = chunk[:remaining]
		}
		n, err := src.Read(chunk)
		if n > 0 {
			if _, werr := dst.Write(chunk[:n]); werr != nil {
				return written, false, werr
			}
			written += n
			remaining -= n
		}
		if err == io.EOF {
			return written, false, nil
		}
		if err != nil {
			return written, false, err
		}
	}
	var extra [1]byte
	n, err := src.Read(extra[:])
	if n > 0 {
		return written, true, nil
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return written, false, err
	}
	return written, false, nil
}

type limitedBuffer struct {
	buf       bytes.Buffer
	remaining int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > b.remaining {
		_, _ = b.buf.Write(p[:b.remaining])
		b.remaining = 0
		b.truncated = true
		return len(p), nil
	}
	n, _ := b.buf.Write(p)
	b.remaining -= n
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	text := strings.TrimSpace(b.buf.String())
	if b.truncated && text != "" {
		return text + "..."
	}
	return text
}
