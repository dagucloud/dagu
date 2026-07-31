// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/spec"
	"github.com/goccy/go-yaml"
)

// ExecOptions captures the inline configuration for building an ad-hoc DAG.
type ExecOptions struct {
	Name          string
	CommandArgs   []string
	ShellOverride string
	WorkingDir    string
	Env           []string
	DotenvFiles   []string
	BaseConfig    string
	WorkerLabels  map[string]string
}

type execSpec struct {
	Name           string            `yaml:"name,omitempty"`
	Type           string            `yaml:"type,omitempty"`
	WorkingDir     string            `yaml:"working_dir,omitempty"`
	Env            []string          `yaml:"env,omitempty"`
	Dotenv         []string          `yaml:"dotenv,omitempty"`
	WorkerSelector map[string]string `yaml:"worker_selector,omitempty"`
	Steps          []execStep        `yaml:"steps"`
}

type execStep struct {
	Name    string `yaml:"name"`
	Command string `yaml:"command,omitempty"`
	Shell   string `yaml:"shell,omitempty"`
}

func buildExecDAG(ctx *Context, opts ExecOptions) (*core.DAG, string, error) {
	if len(opts.CommandArgs) == 0 {
		return nil, "", fmt.Errorf("command is required to build exec DAG")
	}

	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = defaultExecName(opts.CommandArgs[0])
	}
	if err := core.ValidateDAGName(name); err != nil {
		return nil, "", fmt.Errorf("invalid DAG name: %w", err)
	}

	// Build command string from args
	commandStr := cmdutil.BuildCommandEscapedString(opts.CommandArgs[0], opts.CommandArgs[1:])

	specDoc := execSpec{
		Name:           name,
		Type:           core.TypeChain,
		WorkingDir:     opts.WorkingDir,
		Env:            opts.Env,
		Dotenv:         opts.DotenvFiles,
		WorkerSelector: opts.WorkerLabels,
		Steps: []execStep{
			{
				Name:    defaultStepName,
				Command: commandStr,
				Shell:   opts.ShellOverride,
			},
		},
	}

	specYAML, err := yaml.Marshal(specDoc)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal generated DAG spec: %w", err)
	}

	tempFile, err := os.CreateTemp("", fmt.Sprintf("dagu-exec-%s-*.yaml", fileutil.SafeName(name)))
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temporary DAG file: %w", err)
	}
	tempPath := tempFile.Name()
	if _, err = tempFile.Write(specYAML); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return nil, "", fmt.Errorf("failed to write temporary DAG file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return nil, "", fmt.Errorf("failed to close temporary DAG file: %w", err)
	}
	defer func() {
		_ = os.Remove(tempPath)
	}()

	loadOpts := []spec.LoadOption{spec.WithName(name)}
	if base := ctx.Config.Paths.BaseConfig; base != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfig(base))
	}
	if opts.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfig(opts.BaseConfig))
	}

	dag, err := spec.Load(ctx, tempPath, loadOpts...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load generated DAG: %w", err)
	}

	dag.Name = name
	dag.WorkingDir = opts.WorkingDir
	if len(opts.WorkerLabels) > 0 {
		dag.WorkerSelector = opts.WorkerLabels
	}
	dag.Location = ""
	dag.SourceFile = ""

	return dag, string(specYAML), nil
}

func defaultExecName(command string) string {
	base := fileutil.SafeName(filepath.Base(command))
	if base == "" {
		base = "command"
	}
	name := "exec-" + base
	return stringutil.TruncString(name, core.DAGNameMaxLen)
}
