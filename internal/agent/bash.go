// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/llm"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func init() {
	RegisterTool(ToolRegistration{
		Name:           "bash",
		Label:          "Bash",
		Description:    "Run shell commands",
		DefaultEnabled: true,
		Factory:        func(_ ToolConfig) *AgentTool { return NewBashTool() },
	})
}

const (
	defaultBashTimeout = 120 * time.Second
	maxBashTimeout     = 10 * time.Minute
	maxOutputLength    = 100000
)

type bashPathFinder func() (string, bool)

type cappedWriter struct {
	limit     int
	buf       bytes.Buffer
	truncated bool
}

type commandRunResult struct {
	stdout string
	stderr string
	err    error
}

// BashToolInput defines the input parameters for the bash tool.
type BashToolInput struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty" lenient:"true"`
}

// NewBashTool creates a new bash tool for shell command execution.
func NewBashTool() *AgentTool {
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "bash",
				Description: "Execute a shell command and return the output. Use this for file listing, git operations, running scripts, etc.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{
							"type":        "string",
							"description": "The shell command to execute",
						},
						"timeout": map[string]any{
							"type":        "integer",
							"description": "Optional timeout in seconds (default: 120, max: 600)",
						},
					},
					"required": []string{"command"},
				},
			},
		},
		Run: bashRun,
		Audit: &AuditInfo{
			Action:          "bash_exec",
			DetailExtractor: ExtractFields("command"),
		},
	}
}

func bashRun(toolCtx ToolContext, input json.RawMessage) ToolOut {
	var args BashToolInput
	if err := decodeToolInput(input, &args); err != nil {
		return toolError("Failed to parse input: %v", err)
	}
	if args.Command == "" {
		return toolError("Command is required")
	}
	if toolCtx.Role.IsSet() && !toolCtx.Role.CanExecute() {
		return toolError("Permission denied: bash requires execute permission")
	}

	return executeCommand(toolCtx, args)
}

func executeCommand(toolCtx ToolContext, args BashToolInput) ToolOut {
	return executeCommandWithFinder(toolCtx, args, findBashPath)
}

func executeCommandWithFinder(toolCtx ToolContext, args BashToolInput, findPath bashPathFinder) ToolOut {
	timeout := resolveTimeout(args.Timeout)
	parentCtx := toolCtx.Context
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	var result commandRunResult
	if bashPath, ok := findPath(); ok {
		result = executeWithBash(ctx, bashPath, args.Command, toolCtx.WorkingDir)
	} else {
		result = executeWithInterpreter(ctx, args.Command, toolCtx.WorkingDir)
	}

	output := buildOutput(result.stdout, result.stderr)

	if result.err != nil {
		return buildCommandError(result.err, output, timeout, parentCtx)
	}
	if output == "" {
		return ToolOut{Content: "(no output)"}
	}
	return ToolOut{Content: output}
}

func findBashPath() (string, bool) {
	path, err := exec.LookPath("bash")
	if err != nil {
		return "", false
	}
	return path, true
}

func executeWithBash(ctx context.Context, bashPath, command, workDir string) commandRunResult {
	stdout := newCappedWriter(maxOutputLength)
	stderr := newCappedWriter(maxOutputLength)

	cmd := exec.Command(bashPath, "-c", command) //nolint:gosec // command is intentionally interpreted by the configured shell tool
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmdutil.SetupCommand(cmd)

	if err := cmd.Start(); err != nil {
		return commandRunResult{
			stdout: stdout.String(),
			stderr: stderr.String(),
			err:    err,
		}
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case err := <-waitCh:
		return commandRunResult{
			stdout: stdout.String(),
			stderr: stderr.String(),
			err:    err,
		}
	case <-ctx.Done():
		select {
		case err := <-waitCh:
			return commandRunResult{
				stdout: stdout.String(),
				stderr: stderr.String(),
				err:    err,
			}
		default:
		}

		_ = cmdutil.KillProcessGroup(cmd, os.Kill)
		<-waitCh

		return commandRunResult{
			stdout: stdout.String(),
			stderr: stderr.String(),
			err:    ctx.Err(),
		}
	}
}

func executeWithInterpreter(ctx context.Context, command, workDir string) commandRunResult {
	stdout := newCappedWriter(maxOutputLength)
	stderr := newCappedWriter(maxOutputLength)

	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(bytes.NewBufferString(command), "")
	if err != nil {
		return commandRunResult{
			err: fmt.Errorf("failed to parse command: %w", err),
		}
	}

	opts := []interp.RunnerOption{
		interp.Env(nil),
		interp.StdIO(nil, stdout, stderr),
	}
	if workDir != "" {
		opts = append(opts, interp.Dir(workDir))
	}

	runner, err := interp.New(opts...)
	if err != nil {
		return commandRunResult{
			err: fmt.Errorf("failed to initialize shell interpreter: %w", err),
		}
	}

	err = runner.Run(ctx, file)
	return commandRunResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
		err:    err,
	}
}

func newCappedWriter(limit int) *cappedWriter {
	return &cappedWriter{limit: limit}
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.limit <= 0 {
		if len(p) > 0 {
			w.truncated = true
		}
		return len(p), nil
	}
	if w.truncated {
		return len(p), nil
	}

	remaining := w.limit - w.buf.Len()
	if remaining <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = w.buf.Write(p[:remaining])
		w.truncated = true
		return len(p), nil
	}

	_, _ = w.buf.Write(p)
	return len(p), nil
}

func (w *cappedWriter) String() string {
	if !w.truncated {
		return w.buf.String()
	}
	if w.limit <= len(outputTruncationMarker) {
		return outputTruncationMarker[:w.limit]
	}

	s := w.buf.String()
	keep := min(w.limit-len(outputTruncationMarker), len(s))
	return s[:keep] + outputTruncationMarker
}

func buildCommandError(err error, output string, timeout time.Duration, parentCtx context.Context) ToolOut {
	if parentCtx != nil && parentCtx.Err() != nil {
		return toolErrorWithOutput(fmt.Sprintf("Command canceled: %v", parentCtx.Err()), output)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return toolErrorWithOutput(fmt.Sprintf("Command timed out after %v", timeout), output)
	}
	if errors.Is(err, context.Canceled) {
		return toolErrorWithOutput(fmt.Sprintf("Command canceled: %v", err), output)
	}
	return toolErrorWithOutput(fmt.Sprintf("Command failed: %v", err), output)
}

func toolErrorWithOutput(message, output string) ToolOut {
	if output == "" {
		return toolError("%s", message)
	}
	return toolError("%s\n%s", message, output)
}

func resolveTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultBashTimeout
	}
	return min(time.Duration(seconds)*time.Second, maxBashTimeout)
}

func buildOutput(stdout, stderr string) string {
	stdout = truncateOutput(stdout)
	stderr = truncateOutput(stderr)

	switch {
	case stderr == "":
		return stdout
	case stdout == "":
		return "STDERR:\n" + stderr
	default:
		return stdout + "\nSTDERR:\n" + stderr
	}
}

func truncateOutput(s string) string {
	if len(s) > maxOutputLength {
		return s[:maxOutputLength] + outputTruncationMarker
	}
	return s
}

const outputTruncationMarker = "\n... [output truncated]"
