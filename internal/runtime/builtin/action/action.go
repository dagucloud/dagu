// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package action

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/google/jsonschema-go/jsonschema"
)

const (
	executorType = core.ExecutorTypeAction
)

var _ runtimeexec.Executor = (*Executor)(nil)
var _ runtimeexec.DAGExecutor = (*Executor)(nil)
var _ runtimeexec.SubRunProvider = (*Executor)(nil)

type config struct {
	Ref   string
	Input map[string]any
}

type Executor struct {
	cfg    config
	stdout io.Writer
	stderr io.Writer

	mu  sync.Mutex
	run runtimeexec.RunParams
	dag *runtimeexec.SubDAGExecutor

	subRuns []coreexec.SubDAGRun
}

func newAction(_ context.Context, step core.Step) (runtimeexec.Executor, error) {
	cfg, err := parseConfig(step.ExecutorConfig.Config)
	if err != nil {
		return nil, err
	}
	return &Executor{
		cfg:    cfg,
		stdout: os.Stdout,
		stderr: os.Stderr,
	}, nil
}

func parseConfig(raw map[string]any) (config, error) {
	if raw == nil {
		return config{}, fmt.Errorf("action: ref is required")
	}
	for key := range raw {
		if key != "ref" && key != "input" {
			return config{}, fmt.Errorf("action: field %q is not supported", key)
		}
	}
	ref, ok := raw["ref"].(string)
	if !ok || strings.TrimSpace(ref) == "" {
		return config{}, fmt.Errorf("action: ref must be a non-empty string")
	}
	input := map[string]any{}
	if rawInput, ok := raw["input"]; ok && rawInput != nil {
		mapped, ok := rawInput.(map[string]any)
		if !ok {
			return config{}, fmt.Errorf("action: input must be an object")
		}
		input = mapped
	}
	return config{Ref: strings.TrimSpace(ref), Input: input}, nil
}

func (e *Executor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *Executor) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *Executor) Kill(sig os.Signal) error {
	e.mu.Lock()
	child := e.dag
	e.mu.Unlock()

	if child != nil {
		return child.Kill(sig)
	}
	return nil
}

func (e *Executor) Run(ctx context.Context) error {
	env := runtime.GetEnv(ctx)
	envMap := env.UserEnvsMap()
	bundle, err := resolveBundle(ctx, e.cfg.Ref, resolveOptions{
		ToolsDir: actionToolsDir(ctx, envMap),
		WorkDir:  env.WorkingDir,
	})
	if err != nil {
		return err
	}
	m, err := loadManifest(bundle.RootDir)
	if err != nil {
		return err
	}
	if err := m.validateInput(e.cfg.Input); err != nil {
		return err
	}
	return e.runActionDAG(ctx, bundle, m)
}

func (e *Executor) runActionDAG(ctx context.Context, bundle *actionBundle, m *manifest) error {
	dagPath, err := safeRelativePath(bundle.RootDir, m.DAG)
	if err != nil {
		return err
	}
	dag, err := spec.Load(ctx, dagPath)
	if err != nil {
		return fmt.Errorf("load action DAG: %w", err)
	}
	if err := validateActionDAG(dag); err != nil {
		return err
	}
	child, err := runtimeexec.NewSubDAGExecutorForDAG(ctx, dag)
	if err != nil {
		return err
	}
	e.setSubDAGExecutor(child)
	defer e.setSubDAGExecutor(nil)
	defer func() {
		if err := child.Cleanup(ctx); err != nil && e.stderr != nil {
			_, _ = fmt.Fprintf(e.stderr, "failed to cleanup action sub DAG: %v\n", err)
		}
	}()

	params, err := actionInputParams(e.cfg.Input)
	if err != nil {
		return err
	}
	run := e.runParams()
	if run.RunID == "" {
		run.RunID = runtime.GenerateSubDAGRunIDForTarget(ctx, dag.Name, params, false)
	}
	run.Params = params
	run.DAGName = dag.Name
	e.setSubRuns([]coreexec.SubDAGRun{{
		DAGRunID: run.RunID,
		Params:   params,
		DAGName:  dag.Name,
	}})

	result, execErr := child.Execute(ctx, run, "")
	if result == nil {
		return execErr
	}
	if err := e.writeJSONOutput(result.Outputs, m); err != nil {
		return err
	}
	return execErr
}

func actionInputParams(input map[string]any) (string, error) {
	if len(input) == 0 {
		return "", nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal action input params: %w", err)
	}
	return string(data), nil
}

func validateActionDAG(dag *core.DAG) error {
	if dag == nil {
		return fmt.Errorf("action DAG is required")
	}
	if dag.WorkingDirExplicit {
		return fmt.Errorf("action DAG %q must not set working_dir; action DAGs run in the sub-DAG attempt work directory", dag.Name)
	}
	for _, localDAG := range dag.LocalDAGs {
		if err := validateActionDAG(localDAG); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) SetParams(params runtimeexec.RunParams) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.run = params
}

func (e *Executor) runParams() runtimeexec.RunParams {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.run
}

func (e *Executor) setSubDAGExecutor(child *runtimeexec.SubDAGExecutor) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dag = child
}

func (e *Executor) GetSubRuns() []coreexec.SubDAGRun {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]coreexec.SubDAGRun(nil), e.subRuns...)
}

func (e *Executor) setSubRuns(subRuns []coreexec.SubDAGRun) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.subRuns = append([]coreexec.SubDAGRun(nil), subRuns...)
}

func (e *Executor) writeJSONOutput(output any, m *manifest) error {
	if output == nil {
		output = map[string]string{}
	}
	if err := m.validateOutput(output); err != nil {
		return err
	}
	data, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("marshal action output: %w", err)
	}
	return e.writeJSONData(data)
}

func (e *Executor) writeJSONData(data []byte) error {
	if _, err := e.stdout.Write(data); err != nil {
		return err
	}
	if !strings.HasSuffix(string(data), "\n") {
		_, err := io.WriteString(e.stdout, "\n")
		return err
	}
	return nil
}

var configSchema = &jsonschema.Schema{
	Type:     "object",
	Required: []string{"ref"},
	Properties: map[string]*jsonschema.Schema{
		"ref": {
			Type:        "string",
			Description: "External action reference. Use owner/repo@version for GitHub actions or source:target@version for explicit source actions.",
		},
		"input": {
			Type:        "object",
			Description: "Action input object produced from the step with field.",
		},
	},
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
}

func init() {
	core.RegisterExecutorConfigSchema(executorType, configSchema)
	runtimeexec.RegisterExecutor(executorType, newAction, validateStep, core.ExecutorCapabilities{
		SubDAG: true,
	})
}

func validateStep(step core.Step) error {
	_, err := parseConfig(step.ExecutorConfig.Config)
	return err
}
