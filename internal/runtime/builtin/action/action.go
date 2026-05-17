// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/google/jsonschema-go/jsonschema"
)

const (
	executorType = core.ExecutorTypeAction

	envActionInput     = "DAGU_ACTION_INPUT"
	envActionOutput    = "DAGU_ACTION_OUTPUT"
	envActionOutputDir = "DAGU_ACTION_OUTPUT_DIR"
	envActionDir       = "DAGU_ACTION_DIR"
	envActionRef       = "DAGU_ACTION_REF"
)

var _ runtimeexec.Executor = (*Executor)(nil)
var _ runtimeexec.DAGExecutor = (*Executor)(nil)
var _ runtimeexec.SubRunProvider = (*Executor)(nil)

type config struct {
	Ref         string
	OriginalRef string
	Input       map[string]any
}

type Executor struct {
	cfg    config
	stdout io.Writer
	stderr io.Writer

	mu  sync.Mutex
	cmd *exec.Cmd
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
	var originalRef string
	if rawOriginalRef, ok := raw["original_ref"]; ok && rawOriginalRef != nil {
		value, ok := rawOriginalRef.(string)
		if !ok {
			return config{}, fmt.Errorf("action: original_ref must be a string")
		}
		originalRef = strings.TrimSpace(value)
	}
	return config{Ref: strings.TrimSpace(ref), OriginalRef: originalRef, Input: input}, nil
}

func (e *Executor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *Executor) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *Executor) Kill(sig os.Signal) error {
	e.mu.Lock()
	cmd := e.cmd
	child := e.dag
	e.mu.Unlock()

	var errs []error
	if cmd != nil && cmd.Process != nil {
		errs = append(errs, cmd.Process.Signal(sig))
	}
	if child != nil {
		errs = append(errs, child.Kill(sig))
	}
	return errors.Join(errs...)
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
	if m.Runtime.Type == runtimeTypeDagu {
		return e.runDaguAction(ctx, bundle, m)
	}
	denoPath, err := resolveDeno(ctx, m.Runtime.Deno, envMap, env.WorkingDir)
	if err != nil {
		return err
	}

	runDir, cleanup, err := e.actionRunDir(envMap, env.Step.Name)
	if err != nil {
		return err
	}
	defer cleanup()

	inputPath := filepath.Join(runDir, "input.json")
	outputPath := filepath.Join(runDir, "output.json")
	if err := writeJSONFile(inputPath, e.cfg.Input); err != nil {
		return err
	}

	entrypoint, err := safeRelativePath(bundle.RootDir, m.Runtime.Entrypoint)
	if err != nil {
		return err
	}
	if err := e.runDeno(ctx, denoPath, bundle.RootDir, denoInstallArgs(bundle.RootDir, entrypoint), e.stderr, e.stderr, nil); err != nil {
		return fmt.Errorf("prepare Deno action dependencies: %w", err)
	}
	actionEnv := []string{
		envActionInput + "=" + inputPath,
		envActionOutput + "=" + outputPath,
		envActionOutputDir + "=" + runDir,
		envActionDir + "=" + bundle.RootDir,
		envActionRef + "=" + e.originalRef(bundle),
	}
	if err := e.runDeno(ctx, denoPath, bundle.RootDir, denoRunArgs(bundle.RootDir, entrypoint, inputPath, runDir, m.Permissions), e.stderr, e.stderr, actionEnv); err != nil {
		return err
	}
	return e.writeActionOutput(outputPath, m)
}

func (e *Executor) runDaguAction(ctx context.Context, bundle *actionBundle, m *manifest) error {
	dagPath, err := safeRelativePath(bundle.RootDir, m.Runtime.DAG)
	if err != nil {
		return err
	}
	dag, err := spec.Load(ctx, dagPath, spec.WithDefaultWorkingDir(bundle.RootDir))
	if err != nil {
		return fmt.Errorf("load action DAG: %w", err)
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

	result, execErr := child.Execute(ctx, run, bundle.RootDir)
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

func (e *Executor) originalRef(bundle *actionBundle) string {
	if e.cfg.OriginalRef != "" {
		return e.cfg.OriginalRef
	}
	if bundle != nil {
		return bundle.OriginalRef
	}
	return e.cfg.Ref
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

func (e *Executor) actionRunDir(envMap map[string]string, stepName string) (string, func(), error) {
	base := strings.TrimSpace(envMap["DAG_RUN_ARTIFACTS_DIR"])
	if base == "" {
		dir, err := os.MkdirTemp("", "dagu-action-")
		if err != nil {
			return "", func() {}, fmt.Errorf("create action temp dir: %w", err)
		}
		return dir, func() { _ = os.RemoveAll(dir) }, nil
	}
	if err := os.MkdirAll(base, 0o750); err != nil {
		return "", func() {}, fmt.Errorf("create action artifacts dir: %w", err)
	}
	dir, err := os.MkdirTemp(base, "action-"+safeName(stepName)+"-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create action run dir: %w", err)
	}
	return dir, func() {}, nil
}

func (e *Executor) runDeno(ctx context.Context, denoPath, dir string, args []string, stdout, stderr io.Writer, extraEnv []string) error {
	cmd := exec.CommandContext(ctx, denoPath, args...) //nolint:gosec
	cmd.Dir = dir
	cmd.Env = append(runtime.AllEnvs(ctx), extraEnv...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmdutil.SetupCommand(cmd)

	e.mu.Lock()
	e.cmd = cmd
	e.mu.Unlock()
	err := cmd.Run()
	e.mu.Lock()
	if e.cmd == cmd {
		e.cmd = nil
	}
	e.mu.Unlock()
	return err
}

func (e *Executor) writeActionOutput(path string, m *manifest) error {
	data, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			if len(m.Outputs) > 0 {
				return fmt.Errorf("action output is required")
			}
			return nil
		}
		return fmt.Errorf("read action output: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		if len(m.Outputs) > 0 {
			return fmt.Errorf("action output is required")
		}
		return nil
	}
	if !json.Valid(data) {
		return fmt.Errorf("action output must be JSON")
	}
	var output any
	if err := json.Unmarshal(data, &output); err != nil {
		return fmt.Errorf("parse action output: %w", err)
	}
	if err := m.validateOutput(output); err != nil {
		return err
	}
	return e.writeJSONData(data)
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

func denoInstallArgs(rootDir, entrypoint string) []string {
	args := []string{"install"}
	args = append(args, denoLockArgs(rootDir)...)
	args = append(args, "--entrypoint", entrypoint)
	return args
}

func denoRunArgs(rootDir, entrypoint, inputPath, outputDir string, perms permissionManifest) []string {
	args := []string{
		"run",
		"--cached-only",
		"--no-prompt",
		"--allow-read=" + strings.Join(readPermissions(rootDir, inputPath, perms.Read), ","),
		"--allow-write=" + strings.Join(writePermissions(rootDir, outputDir, perms.Write), ","),
	}
	args = append(args, denoLockArgs(rootDir)...)
	if len(perms.Net) > 0 {
		args = append(args, "--allow-net="+strings.Join(trimmedStrings(perms.Net), ","))
	}
	envPerms := actionEnvPermissions(perms.Env)
	if len(envPerms) > 0 {
		args = append(args, "--allow-env="+strings.Join(envPerms, ","))
	}
	args = append(args, entrypoint)
	return args
}

func denoLockArgs(rootDir string) []string {
	lockPath := filepath.Join(rootDir, "deno.lock")
	if info, err := os.Stat(lockPath); err == nil && !info.IsDir() {
		return []string{"--lock=" + filepath.Clean(lockPath), "--frozen"}
	}
	return nil
}

func actionEnvPermissions(extra []string) []string {
	return dedupeStrings(append([]string{
		envActionInput,
		envActionOutput,
		envActionOutputDir,
		envActionDir,
		envActionRef,
	}, trimmedStrings(extra)...))
}

func readPermissions(rootDir, inputPath string, extra []string) []string {
	paths := []string{filepath.Clean(rootDir), filepath.Clean(inputPath)}
	for _, item := range extra {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if path, err := safeRelativePath(rootDir, item); err == nil {
			paths = append(paths, path)
		}
	}
	return dedupeStrings(paths)
}

func writePermissions(rootDir, outputDir string, extra []string) []string {
	paths := []string{filepath.Clean(outputDir)}
	for _, item := range extra {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if path, err := safeRelativePath(rootDir, item); err == nil {
			paths = append(paths, path)
		}
	}
	return dedupeStrings(paths)
}

func writeJSONFile(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal action input: %w", err)
	}
	if err := os.WriteFile(filepath.Clean(path), data, 0o600); err != nil {
		return fmt.Errorf("write action input: %w", err)
	}
	return nil
}

func trimmedStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

var safeNameRegexp = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func safeName(name string) string {
	name = strings.Trim(safeNameRegexp.ReplaceAllString(name, "-"), "-")
	if name == "" {
		return "step"
	}
	return name
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
		"original_ref": {
			Type:        "string",
			Description: "Original action reference before shorthand normalization.",
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
