// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package log

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/google/jsonschema-go/jsonschema"
)

const executorType = "log"

var _ executor.Executor = (*logExecutor)(nil)

type logExecutor struct {
	stdout  io.Writer
	message string
}

func newLog(_ context.Context, step core.Step) (executor.Executor, error) {
	message, err := parseMessage(step.ExecutorConfig.Config)
	if err != nil {
		return nil, err
	}
	return &logExecutor{
		stdout:  os.Stdout,
		message: message,
	}, nil
}

func parseMessage(config map[string]any) (string, error) {
	if config == nil {
		return "", fmt.Errorf("log: message is required")
	}
	raw, ok := config["message"]
	if !ok {
		return "", fmt.Errorf("log: message is required")
	}
	message, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("log: message must be a string")
	}
	return message, nil
}

func (e *logExecutor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (*logExecutor) SetStderr(_ io.Writer) {}

func (*logExecutor) Kill(_ os.Signal) error { return nil }

func (e *logExecutor) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := io.WriteString(e.stdout, e.message); err != nil {
		return err
	}
	if strings.HasSuffix(e.message, "\n") {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := io.WriteString(e.stdout, "\n")
	return err
}

func validateLogStep(step core.Step) error {
	_, err := parseMessage(step.ExecutorConfig.Config)
	return err
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	Required:             []string{"message"},
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Properties: map[string]*jsonschema.Schema{
		"message": {
			Type:        "string",
			Description: "Message to write to stdout. Supports Dagu variable substitution.",
		},
	},
}

func init() {
	core.RegisterExecutorConfigSchema(executorType, configSchema)
	executor.RegisterExecutor(executorType, newLog, validateLogStep, core.ExecutorCapabilities{})
}
