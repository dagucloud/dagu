// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package outputs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/google/jsonschema-go/jsonschema"
)

const (
	executorType = "outputs"
	opWrite      = "write"
)

var (
	errConfig      = errors.New("outputs: configuration error")
	errUnsupported = errors.New("outputs: unsupported operation")
)

var _ executor.Executor = (*executorImpl)(nil)
var _ executor.OutputsProvider = (*executorImpl)(nil)

type executorImpl struct {
	mu      sync.Mutex
	stdout  io.Writer
	stderr  io.Writer
	op      string
	values  map[string]any
	outputs map[string]any
}

func init() {
	executor.RegisterExecutor(executorType, newExecutor, validateStep, core.ExecutorCapabilities{Command: true})
	core.RegisterExecutorConfigSchema(executorType, configSchema)
}

func newExecutor(_ context.Context, step core.Step) (executor.Executor, error) {
	op := stepOperation(step)
	values, err := parseConfig(step.ExecutorConfig.Config)
	if err != nil {
		return nil, err
	}
	if err := validateConfig(op, values); err != nil {
		return nil, err
	}
	return &executorImpl{
		stdout: os.Stdout,
		stderr: os.Stderr,
		op:     op,
		values: values,
	}, nil
}

func validateStep(step core.Step) error {
	if step.ExecutorConfig.Type != executorType {
		return nil
	}
	values, err := parseConfig(step.ExecutorConfig.Config)
	if err != nil {
		return err
	}
	return validateConfig(stepOperation(step), values)
}

func stepOperation(step core.Step) string {
	if len(step.Commands) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(step.Commands[0].Command))
}

func parseConfig(raw map[string]any) (map[string]any, error) {
	if raw == nil {
		raw = map[string]any{}
	}
	for key := range raw {
		if key != "values" {
			return nil, fmt.Errorf("%w: field %q is not supported", errConfig, key)
		}
	}
	rawValues, ok := raw["values"]
	if !ok {
		return nil, fmt.Errorf("%w: values is required for write", errConfig)
	}
	values, ok := rawValues.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: values must be an object", errConfig)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("%w: values must not be empty", errConfig)
	}
	for key := range values {
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("%w: values contains an empty key", errConfig)
		}
	}
	return values, nil
}

func validateConfig(op string, values map[string]any) error {
	if op != opWrite {
		return fmt.Errorf("%w %q", errUnsupported, op)
	}
	if len(values) == 0 {
		return fmt.Errorf("%w: values must not be empty", errConfig)
	}
	return nil
}

func (e *executorImpl) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *executorImpl) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *executorImpl) Kill(os.Signal) error {
	return nil
}

func (e *executorImpl) Run(context.Context) error {
	if e.op != opWrite {
		return fmt.Errorf("%w %q", errUnsupported, e.op)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.outputs = make(map[string]any, len(e.values))
	maps.Copy(e.outputs, e.values)
	return nil
}

func (e *executorImpl) GetOutputs() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.outputs) == 0 {
		return nil
	}
	clone := make(map[string]any, len(e.outputs))
	maps.Copy(clone, e.outputs)
	return clone
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	Required:             []string{"values"},
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Properties: map[string]*jsonschema.Schema{
		"values": {
			Type:                 "object",
			AdditionalProperties: &jsonschema.Schema{},
			Description:          "Output values to publish to the DAG/action outputs object.",
		},
	},
}
