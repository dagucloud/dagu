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
	"regexp"
	"text/template"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/templatefuncs"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
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

func newTemplate(ctx context.Context, step ir.Step) (executor.Executor, error) {
	var cfg templateConfig
	if step.ExecutorConfig.Config != nil {
		if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
			return nil, fmt.Errorf("template: %w", err)
		}
	}

	if step.Script == "" {
		return nil, ir.NewValidationError("script", nil, fmt.Errorf("script field is required"))
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
	tmpl, err := parseTemplate(e.script, e.data)
	if err != nil {
		return err
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

func validateTemplate(step ir.Step) error {
	refValue, hasRef := step.ExecutorConfig.Config["template_ref"]
	if step.Script != "" && hasRef {
		return ir.NewValidationError("with.template_ref", refValue, fmt.Errorf("template step cannot use both script and with.template_ref"))
	}
	if step.Script != "" {
		if _, err := parseTemplate(step.Script, dataFromConfig(step.ExecutorConfig.Config)); err != nil {
			return ir.NewValidationError("with.template", nil, err)
		}
		return nil
	}
	if !hasRef {
		return ir.NewValidationError("script", nil, fmt.Errorf("script field is required"))
	}
	ref, ok := refValue.(string)
	if !ok || !cmnvalue.IsExactRef(ref) {
		return ir.NewValidationError("with.template_ref", refValue, fmt.Errorf("must be one complete scoped value reference such as ${env.NAME}"))
	}
	return nil
}

// undefinedFuncPattern matches the text/template parse error for a bare
// identifier. The package reports it only as a string.
var undefinedFuncPattern = regexp.MustCompile(`function "([^"]+)" not defined`)

// parseTemplate parses script against the template function map. A bare
// identifier that matches a data key gets a hint about the missing dot, since
// {{ key }} is the most common way to misspell {{ .key }}. The hint names the
// key only, because the full expression may continue past it.
func parseTemplate(script string, data map[string]any) (*template.Template, error) {
	tmpl, err := template.New("template").
		Option("missingkey=error").
		Funcs(funcMap).
		Parse(script)
	if err == nil {
		return tmpl, nil
	}
	if m := undefinedFuncPattern.FindStringSubmatch(err.Error()); m != nil {
		if _, isDataKey := data[m[1]]; isDataKey {
			return nil, fmt.Errorf("template: parse error: %w: %q is a with.data key and needs a leading dot (.%s)", err, m[1], m[1])
		}
	}
	return nil, fmt.Errorf("template: parse error: %w", err)
}

// dataFromConfig returns with.data when it is already a map. Before a run it
// may still be an unresolved reference string, which carries no keys.
func dataFromConfig(config map[string]any) map[string]any {
	data, _ := config["data"].(map[string]any)
	return data
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
	executor.RegisterExecutor("template", newTemplate, validateTemplate, registry.ExecutorCapabilities{
		Script: true,
	})
}
