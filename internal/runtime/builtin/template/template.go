// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package template

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/template"

	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/templatefuncs"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/go-viper/mapstructure/v2"
)

const defaultDirPermissions = 0750

var _ executor.Executor = (*templateExec)(nil)

type templateExec struct {
	stdout     io.Writer
	stderr     io.Writer
	script     string
	data       map[string]any
	outputFile string
}

type templateConfig struct {
	Data   map[string]any `mapstructure:"data"`
	Output string         `mapstructure:"output"`
}

func newTemplate(ctx context.Context, step core.Step) (executor.Executor, error) {
	var cfg templateConfig
	if step.ExecutorConfig.Config != nil {
		if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
			return nil, fmt.Errorf("template: %w", err)
		}
	}

	if step.Script == "" {
		return nil, core.NewValidationError("script", nil, fmt.Errorf("script field is required"))
	}

	outputFile := cfg.Output
	if outputFile != "" && !filepath.IsAbs(outputFile) {
		env := runtime.GetEnv(ctx)
		outputFile = filepath.Join(env.WorkingDir, outputFile)
	}

	data := cfg.Data
	if data == nil {
		data = make(map[string]any)
	}

	return &templateExec{
		stdout:     os.Stdout,
		stderr:     os.Stderr,
		script:     step.Script,
		data:       data,
		outputFile: outputFile,
	}, nil
}

func (e *templateExec) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *templateExec) SetStderr(out io.Writer) {
	e.stderr = out
}

func (*templateExec) Kill(_ os.Signal) error {
	return nil
}

func (e *templateExec) Run(_ context.Context) error {
	tmpl, err := template.New("template").
		Option("missingkey=error").
		Funcs(funcMap).
		Parse(e.script)
	if err != nil {
		return fmt.Errorf("template: parse error: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, e.data); err != nil {
		return fmt.Errorf("template: execution error: %w", err)
	}

	if e.outputFile != "" {
		return e.writeToFile(buf.Bytes())
	}

	_, err = e.stdout.Write(buf.Bytes())
	return err
}

func (e *templateExec) writeToFile(data []byte) error {
	if err := os.MkdirAll(filepath.Dir(e.outputFile), defaultDirPermissions); err != nil {
		return fmt.Errorf("template: failed to create output directory: %w", err)
	}

	if err := fileutil.WriteFileAtomic(e.outputFile, data, 0600); err != nil {
		return fmt.Errorf("template: failed to write output file: %w", err)
	}

	return nil
}

func decodeConfig(dat map[string]any, cfg *templateConfig) error {
	md, _ := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		WeaklyTypedInput: true,
		ErrorUnused:      false,
		Result:           cfg,
	})
	return md.Decode(dat)
}

func validateTemplate(step core.Step) error {
	if step.Script == "" {
		return core.NewValidationError("script", nil, fmt.Errorf("script field is required"))
	}
	return nil
}

// funcMap provides template functions for pipeline-compatible usage.
// Built from the hermetic slim-sprig base with Dagu-specific overrides.
// Functions that accept a pipeline value take it as the last argument.
var funcMap = buildFuncMap()

var blockedFuncs = templatefuncs.BlockedFuncNames()

func buildFuncMap() template.FuncMap {
	return templatefuncs.FuncMap()
}

func init() {
	executor.RegisterExecutor("template", newTemplate, validateTemplate, core.ExecutorCapabilities{
		Script: true,
		GetScriptEvalOptions: func(_ context.Context, _ core.Step) []eval.Option {
			return []eval.Option{eval.WithNoExpansion()}
		},
	})
}
