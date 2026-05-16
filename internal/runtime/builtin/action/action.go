// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package action

import (
	"context"
	"encoding/json"
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
	"github.com/dagucloud/dagu/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/internal/runtime/executor"
	dagutools "github.com/dagucloud/dagu/internal/tools"
	"github.com/google/jsonschema-go/jsonschema"
)

const (
	executorType = core.ExecutorTypeAction

	envActionInput     = "DAGU_ACTION_INPUT"
	envActionOutput    = "DAGU_ACTION_OUTPUT"
	envActionOutputDir = "DAGU_ACTION_OUTPUT_DIR"
	envActionDir       = "DAGU_ACTION_DIR"
	envActionRef       = "DAGU_ACTION_REF"
	envActionRegistry  = "DAGU_ACTION_REGISTRY_DIR"
)

var _ runtimeexec.Executor = (*Executor)(nil)

type config struct {
	Ref   string
	Input map[string]any
}

type Executor struct {
	cfg    config
	stdout io.Writer
	stderr io.Writer

	mu  sync.Mutex
	cmd *exec.Cmd
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
	defer e.mu.Unlock()
	if e.cmd == nil || e.cmd.Process == nil {
		return nil
	}
	return e.cmd.Process.Signal(sig)
}

func (e *Executor) Run(ctx context.Context) error {
	env := runtime.GetEnv(ctx)
	envMap := env.UserEnvsMap()
	bundle, err := resolveBundle(ctx, e.cfg.Ref, resolveOptions{
		ToolsDir:    envMap[dagutools.EnvToolsDir],
		WorkDir:     env.WorkingDir,
		RegistryDir: envMap[envActionRegistry],
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
		envActionRef + "=" + bundle.OriginalRef,
	}
	if err := e.runDeno(ctx, denoPath, bundle.RootDir, denoRunArgs(bundle.RootDir, entrypoint, inputPath, runDir, m.Permissions), e.stderr, e.stderr, actionEnv); err != nil {
		return err
	}
	return e.writeActionOutput(outputPath)
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

func (e *Executor) writeActionOutput(path string) error {
	data, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read action output: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	if !json.Valid(data) {
		return fmt.Errorf("action output must be JSON")
	}
	if _, err := e.stdout.Write(data); err != nil {
		return err
	}
	if !strings.HasSuffix(string(data), "\n") {
		_, err = io.WriteString(e.stdout, "\n")
	}
	return err
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
			Description: "External action reference. Supported prefixes are source: and pkg:.",
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
	runtimeexec.RegisterExecutor(executorType, newAction, validateStep, core.ExecutorCapabilities{})
}

func validateStep(step core.Step) error {
	_, err := parseConfig(step.ExecutorConfig.Config)
	return err
}
