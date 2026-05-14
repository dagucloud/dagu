// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/goccy/go-yaml"
)

var _ executor.Executor = (*harnessExecutor)(nil)
var _ executor.ExitCoder = (*harnessExecutor)(nil)
var _ executor.PushBackAware = (*harnessExecutor)(nil)
var _ executor.PushBackPreviousStdoutAware = (*harnessExecutor)(nil)

const failedStdoutTailLimit = 1024

type providerConfig struct {
	name       string
	provider   Provider
	definition *core.HarnessDefinition
	flags      map[string]any
}

type defaultConfigProvider interface {
	DefaultConfig() map[string]any
}

type harnessExecutor struct {
	mu                     sync.Mutex
	cmd                    *exec.Cmd
	stdout                 io.Writer
	stderr                 io.Writer
	exitCode               int
	stderrTail             *executor.TailWriter
	configs                []providerConfig
	prompt                 string
	script                 string // piped to stdin if present
	workDir                string
	pushBackInputs         map[string]string
	pushBackIteration      int
	pushBackPreviousStdout string
}

func (e *harnessExecutor) ExitCode() int {
	return e.exitCode
}

func (e *harnessExecutor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *harnessExecutor) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *harnessExecutor) Kill(sig os.Signal) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cmdutil.KillProcessGroup(e.cmd, sig)
}

func (e *harnessExecutor) SetPushBackContext(inputs map[string]string, iteration int) {
	e.pushBackInputs = maps.Clone(inputs)
	e.pushBackIteration = iteration
}

func (e *harnessExecutor) SetPushBackPreviousStdout(path string) {
	e.pushBackPreviousStdout = path
}

func (e *harnessExecutor) effectivePrompt() string {
	if e.pushBackIteration == 0 {
		return e.prompt
	}

	var sb strings.Builder
	sb.WriteString(e.prompt)
	sb.WriteString("\n\n## Push-back Context\n\n")
	fmt.Fprintf(&sb, "Push-back iteration: %d\n", e.pushBackIteration)
	if e.pushBackPreviousStdout != "" {
		fmt.Fprintf(&sb, "Previous stdout log: %s\n", e.pushBackPreviousStdout)
	}

	keys := make([]string, 0, len(e.pushBackInputs))
	for key := range e.pushBackInputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		sb.WriteString("\nReviewer feedback:\n")
		for _, key := range keys {
			fmt.Fprintf(&sb, "- %s: %s\n", key, e.pushBackInputs[key])
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

func (e *harnessExecutor) Run(ctx context.Context) error {
	var lastErr error

	for i, cfg := range e.configs {
		stdout, err := e.runOnce(ctx, cfg)
		if err == nil {
			e.exitCode = 0
			if writeErr := e.writeStdout(stdout); writeErr != nil {
				e.exitCode = 1
				return fmt.Errorf("harness: failed to write stdout: %w", writeErr)
			}
			return nil
		}

		lastErr = err
		if ctx.Err() != nil {
			return err
		}
		if i+1 < len(e.configs) {
			next := e.configs[i+1]
			_, _ = fmt.Fprintf(
				e.stderrWriter(),
				"harness: attempt %d/%d with %s failed; trying fallback %d/%d with %s\n",
				i+1,
				len(e.configs),
				cfg.name,
				i+2,
				len(e.configs),
				next.name,
			)
		}
	}

	if lastErr != nil {
		return lastErr
	}
	return nil
}

func (e *harnessExecutor) runOnce(ctx context.Context, cfg providerConfig) (*os.File, error) {
	e.mu.Lock()

	env := runtime.GetEnv(ctx)
	tw := executor.NewTailWriterWithEncoding(e.stderrWriter(), 0, env.LogEncodingCharset)
	e.stderrTail = tw
	args, stdin, err := cfg.buildInvocation(e.effectivePrompt(), e.script)
	if err != nil {
		e.exitCode = 1
		e.mu.Unlock()
		return nil, err
	}

	binaryPath, err := resolveBinaryPath(cfg.binaryName(), e.workDir, runtime.AllEnvsMap(ctx))
	if err != nil {
		e.exitCode = exitCodeFromError(err)
		e.mu.Unlock()
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("harness: %q CLI not found in PATH; install it first: %w", cfg.binaryName(), err)
		}
		return nil, fmt.Errorf("harness: failed to resolve binary %q: %w", cfg.binaryName(), err)
	}

	stdout, err := newStdoutSpool()
	if err != nil {
		e.exitCode = 1
		e.mu.Unlock()
		return nil, fmt.Errorf("harness: failed to create stdout spool: %w", err)
	}

	cmd := exec.CommandContext(ctx, binaryPath, args...)
	if len(cmd.Args) > 0 {
		cmd.Args[0] = cfg.binaryName()
	}
	cmd.Env = append(cmd.Env, runtime.AllEnvs(ctx)...)
	cmd.Dir = e.workDir
	cmd.Stdout = stdout
	cmd.Stderr = tw
	cmdutil.SetupCommand(cmd)

	if cmd.Dir != "" {
		if err := os.MkdirAll(cmd.Dir, 0o750); err != nil {
			e.exitCode = 1
			_ = cleanupStdoutSpool(stdout)
			e.mu.Unlock()
			return nil, fmt.Errorf("harness: failed to create working directory: %w", err)
		}
	}

	if stdin != nil {
		cmd.Stdin = stdin
	}

	e.cmd = cmd

	if err := cmd.Start(); err != nil {
		e.exitCode = exitCodeFromError(err)
		_ = cleanupStdoutSpool(stdout)
		e.mu.Unlock()
		return nil, formatProcessFailure(err, tw.Tail(), "")
	}
	e.mu.Unlock()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		_ = e.Kill(os.Kill)
		<-waitDone
		e.exitCode = 124
		_ = cleanupStdoutSpool(stdout)
		return nil, ctx.Err()
	case err := <-waitDone:
		if err != nil {
			e.exitCode = exitCodeFromError(err)
			stdoutTail, tailErr := readSpoolTail(stdout, failedStdoutTailLimit, env.LogEncodingCharset)
			_ = cleanupStdoutSpool(stdout)
			if tailErr != nil {
				return nil, fmt.Errorf("harness: failed to read stdout tail: %w", tailErr)
			}
			if stdoutTail != "" {
				_, _ = fmt.Fprintf(e.stderrWriter(), "recent stdout (tail):\n%s\n", stdoutTail)
			}
			return nil, formatProcessFailure(err, tw.Tail(), stdoutTail)
		}
		if _, err := stdout.Seek(0, io.SeekStart); err != nil {
			e.exitCode = 1
			_ = cleanupStdoutSpool(stdout)
			return nil, fmt.Errorf("harness: failed to rewind stdout spool: %w", err)
		}
		return stdout, nil
	}
}

func (e *harnessExecutor) writeStdout(stdout *os.File) error {
	if stdout == nil {
		return nil
	}
	defer func() {
		_ = cleanupStdoutSpool(stdout)
	}()

	_, err := io.Copy(e.stdoutWriter(), stdout)
	return err
}

func (e *harnessExecutor) stdoutWriter() io.Writer {
	if e.stdout == nil {
		return io.Discard
	}
	return e.stdout
}

func (e *harnessExecutor) stderrWriter() io.Writer {
	if e.stderr == nil {
		return io.Discard
	}
	return e.stderr
}

// reservedKeys are config keys consumed by the harness executor itself, not passed as CLI flags.
var reservedKeys = map[string]bool{
	"provider": true,
	"fallback": true,
}

// configToFlags converts config map entries into CLI flags.
// Keys become --key, values are type-dependent:
//   - string → --key value
//   - bool true → --key (false is omitted)
//   - number → --key N
//   - []any → --key v1 --key v2 (repeated)
//
// Reserved keys are skipped. Built-in providers normalize snake_case keys to
// kebab-case. Keys are sorted for deterministic output.
func configToFlags(cfg map[string]any, definition *core.HarnessDefinition) []string {
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		if reservedKeys[k] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var args []string
	for _, key := range keys {
		flag := flagTokenForKey(key, definition)
		switch v := cfg[key].(type) {
		case bool:
			if v {
				args = append(args, flag)
			}
		case string:
			if v != "" {
				args = append(args, flag, v)
			}
		case int:
			args = append(args, flag, strconv.Itoa(v))
		case int8:
			args = append(args, flag, strconv.FormatInt(int64(v), 10))
		case int16:
			args = append(args, flag, strconv.FormatInt(int64(v), 10))
		case int32:
			args = append(args, flag, strconv.FormatInt(int64(v), 10))
		case int64:
			args = append(args, flag, strconv.FormatInt(v, 10))
		case uint:
			args = append(args, flag, strconv.FormatUint(uint64(v), 10))
		case uint8:
			args = append(args, flag, strconv.FormatUint(uint64(v), 10))
		case uint16:
			args = append(args, flag, strconv.FormatUint(uint64(v), 10))
		case uint32:
			args = append(args, flag, strconv.FormatUint(uint64(v), 10))
		case uint64:
			args = append(args, flag, strconv.FormatUint(v, 10))
		case float32:
			args = append(args, flag, strconv.FormatFloat(float64(v), 'f', -1, 32))
		case float64:
			if v == float64(int(v)) {
				args = append(args, flag, strconv.Itoa(int(v)))
			} else {
				args = append(args, flag, strconv.FormatFloat(v, 'f', -1, 64))
			}
		case []string:
			for _, item := range v {
				args = append(args, flag, item)
			}
		case []any:
			for _, item := range v {
				args = append(args, flag, fmt.Sprint(item))
			}
		}
	}
	return args
}

func newHarness(ctx context.Context, step core.Step) (executor.Executor, error) {
	if err := validatePromptCommand(step); err != nil {
		return nil, err
	}

	cfg := normalizeConfigMap(step.ExecutorConfig.Config)
	var defs core.HarnessDefinitions
	env := runtime.GetEnv(ctx)
	if env.DAG != nil {
		defs = env.DAG.Harnesses
	}
	configs, err := buildProviderConfigs(cfg, defs)
	if err != nil {
		return nil, err
	}

	prompt := extractPrompt(step)

	return &harnessExecutor{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		configs: configs,
		prompt:  prompt,
		script:  step.Script,
		workDir: env.WorkingDir,
	}, nil
}

func buildProviderConfigs(cfg map[string]any, defs core.HarnessDefinitions) ([]providerConfig, error) {
	if err := validateProviderConfigs(cfg); err != nil {
		return nil, err
	}

	primary, fallbacks, err := extractFallbackConfigs(cfg)
	if err != nil {
		return nil, err
	}

	attempts := make([]map[string]any, 0, 1+len(fallbacks))
	attempts = append(attempts, primary)
	attempts = append(attempts, fallbacks...)

	configs := make([]providerConfig, 0, len(attempts))
	for i := range attempts {
		resolved, err := resolveProvider(attempts[i], defs)
		if err != nil {
			if i == 0 {
				return nil, err
			}
			return nil, fmt.Errorf("harness: invalid fallback[%d]: %w", i-1, err)
		}
		resolved.flags = mergeProviderDefaultConfig(resolved.provider, attempts[i])
		configs = append(configs, resolved)
	}

	return configs, nil
}

func extractFallbackConfigs(cfg map[string]any) (map[string]any, []map[string]any, error) {
	primary := cloneConfigMap(cfg)
	raw, ok := primary["fallback"]
	if !ok {
		return primary, nil, nil
	}
	delete(primary, "fallback")

	fallbacks, err := fallbackConfigsFromValue(raw)
	if err != nil {
		return nil, nil, err
	}
	return primary, fallbacks, nil
}

func fallbackConfigsFromValue(raw any) ([]map[string]any, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case []map[string]any:
		return cloneFallbackConfigs(v), nil
	case []any:
		fallbacks := make([]map[string]any, len(v))
		for i := range v {
			item, ok := v[i].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("harness: fallback[%d] must be an object", i)
			}
			fallbacks[i] = cloneConfigMap(item)
		}
		return fallbacks, nil
	default:
		return nil, fmt.Errorf("harness: fallback must be an array of objects")
	}
}

func resolveProvider(cfg map[string]any, defs core.HarnessDefinitions) (providerConfig, error) {
	providerName, _ := cfg["provider"].(string)
	if providerName == "" {
		return providerConfig{}, fmt.Errorf("harness: config.provider is required")
	}
	if isTemplatedValue(providerName) {
		return providerConfig{}, fmt.Errorf("harness: unresolved provider template %q", providerName)
	}
	if core.IsBuiltinHarnessProvider(providerName) {
		provider, err := getProvider(providerName)
		if err != nil {
			return providerConfig{}, err
		}
		return providerConfig{
			name:     provider.Name(),
			provider: provider,
		}, nil
	}
	if defs != nil {
		if def, ok := defs[providerName]; ok && def != nil {
			return providerConfig{
				name:       providerName,
				definition: cloneDefinition(def),
			}, nil
		}
	}
	return providerConfig{}, fmt.Errorf("harness: unknown provider %q; registered: %v", providerName, knownProviders(defs))
}

func mergeProviderDefaultConfig(provider Provider, cfg map[string]any) map[string]any {
	merged := cloneConfigMap(cfg)
	if provider == nil {
		return merged
	}
	defaultProvider, ok := provider.(defaultConfigProvider)
	if !ok {
		return merged
	}
	defaults := defaultProvider.DefaultConfig()
	if len(defaults) == 0 {
		return merged
	}
	defaults = core.NormalizeBuiltinHarnessFlagKeys(defaults)
	merged = core.NormalizeBuiltinHarnessFlagKeys(merged)
	withDefaults := cloneConfigMap(defaults)
	maps.Copy(withDefaults, merged)
	return withDefaults
}

func normalizeConfigMap(cfg map[string]any) map[string]any {
	if cfg == nil {
		return nil
	}

	normalized := make(map[string]any, len(cfg))
	for key, value := range cfg {
		normalized[key] = normalizeConfigValue(value)
	}
	return normalized
}

func normalizeConfigValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return normalizeConfigMap(v)
	case []any:
		normalized := make([]any, len(v))
		for i := range v {
			normalized[i] = normalizeConfigValue(v[i])
		}
		return normalized
	case []string:
		normalized := make([]string, len(v))
		for i := range v {
			normalized[i] = fmt.Sprint(coerceScalarString(v[i]))
		}
		return normalized
	case []map[string]any:
		normalized := make([]map[string]any, len(v))
		for i := range v {
			normalized[i] = normalizeConfigMap(v[i])
		}
		return normalized
	case string:
		return coerceScalarString(v)
	default:
		return value
	}
}

func coerceScalarString(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return value
	}

	var parsed any
	if err := yaml.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return value
	}

	switch parsed.(type) {
	case bool, int, int64, uint64, float32, float64:
		return parsed
	default:
		return value
	}
}

func cloneConfigMap(cfg map[string]any) map[string]any {
	if cfg == nil {
		return nil
	}

	cloned := make(map[string]any, len(cfg))
	for key, value := range cfg {
		cloned[key] = cloneConfigValue(value)
	}
	return cloned
}

func cloneFallbackConfigs(cfgs []map[string]any) []map[string]any {
	if cfgs == nil {
		return nil
	}

	cloned := make([]map[string]any, len(cfgs))
	for i := range cfgs {
		cloned[i] = cloneConfigMap(cfgs[i])
	}
	return cloned
}

func cloneConfigValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneConfigMap(v)
	case []any:
		cloned := make([]any, len(v))
		for i := range v {
			cloned[i] = cloneConfigValue(v[i])
		}
		return cloned
	case []string:
		return append([]string(nil), v...)
	case []map[string]any:
		return cloneFallbackConfigs(v)
	default:
		return value
	}
}

func extractPrompt(step core.Step) string {
	if len(step.Commands) == 0 {
		return ""
	}
	cmd := step.Commands[0]
	if cmd.CmdWithArgs != "" {
		return cmd.CmdWithArgs
	}
	if cmd.Command == "" {
		return ""
	}
	if len(cmd.Args) > 0 {
		return cmd.Command + " " + strings.Join(cmd.Args, " ")
	}
	return cmd.Command
}

func validateHarnessStep(step core.Step) error {
	if err := validatePromptCommand(step); err != nil {
		return err
	}
	cfg := step.ExecutorConfig.Config
	if cfg == nil {
		return core.NewValidationError("with", nil, fmt.Errorf("config is required"))
	}

	if err := validateProviderConfigs(cfg); err != nil {
		return core.NewValidationError("with", nil, err)
	}
	return nil
}

func validatePromptCommand(step core.Step) error {
	if len(step.Commands) > 1 {
		return core.NewValidationError("command", nil, fmt.Errorf("action %q supports only one command", "harness"))
	}
	if len(step.Commands) == 0 || extractPrompt(step) == "" {
		return core.NewValidationError("command", nil, fmt.Errorf("command field (prompt) is required"))
	}
	return nil
}

func validateProviderConfigs(cfg map[string]any) error {
	if err := validateProviderConfig(cfg, true); err != nil {
		return err
	}

	fallbacks, err := fallbackConfigsFromValue(cfg["fallback"])
	if err != nil {
		return err
	}
	for i := range fallbacks {
		if err := validateProviderConfig(fallbacks[i], false); err != nil {
			return fmt.Errorf("harness: invalid fallback[%d]: %w", i, err)
		}
	}
	return nil
}

func validateProviderConfig(cfg map[string]any, allowFallback bool) error {
	providerStr, _ := cfg["provider"].(string)
	if _, exists := cfg["binary"]; exists {
		return fmt.Errorf("harness: config.binary is not supported; define a named harness under top-level harnesses and reference it via config.provider")
	}
	if _, exists := cfg["prompt_args"]; exists {
		return fmt.Errorf("harness: config.prompt_args is not supported; define a named harness under top-level harnesses and reference it via config.provider")
	}
	if !allowFallback {
		if _, exists := cfg["fallback"]; exists {
			return fmt.Errorf("harness: config.fallback is not supported inside fallback providers")
		}
	}
	if providerStr == "" {
		return fmt.Errorf("harness: config.provider is required")
	}
	return nil
}

func (cfg providerConfig) binaryName() string {
	if cfg.provider != nil {
		return cfg.provider.BinaryName()
	}
	if cfg.definition != nil {
		return cfg.definition.Binary
	}
	return ""
}

func (cfg providerConfig) buildInvocation(prompt, script string) ([]string, io.Reader, error) {
	if cfg.provider != nil {
		args := cfg.provider.BaseArgs(prompt)
		args = append(args, configToFlags(cfg.flags, nil)...)

		if script == "" {
			return args, nil, nil
		}
		return args, strings.NewReader(script), nil
	}

	if cfg.definition == nil {
		return nil, nil, fmt.Errorf("harness: provider %q is not configured", cfg.name)
	}

	args := append([]string(nil), cfg.definition.PrefixArgs...)
	flags := configToFlags(cfg.flags, cfg.definition)

	switch cfg.definition.PromptMode {
	case core.HarnessPromptModeArg:
		promptArgs := []string{prompt}
		if cfg.definition.PromptPosition == core.HarnessPromptPositionAfterFlags {
			args = append(args, flags...)
			args = append(args, promptArgs...)
		} else {
			args = append(args, promptArgs...)
			args = append(args, flags...)
		}
		if script == "" {
			return args, nil, nil
		}
		return args, strings.NewReader(script), nil
	case core.HarnessPromptModeFlag:
		promptArgs := []string{cfg.definition.PromptFlag, prompt}
		if cfg.definition.PromptPosition == core.HarnessPromptPositionAfterFlags {
			args = append(args, flags...)
			args = append(args, promptArgs...)
		} else {
			args = append(args, promptArgs...)
			args = append(args, flags...)
		}
		if script == "" {
			return args, nil, nil
		}
		return args, strings.NewReader(script), nil
	case core.HarnessPromptModeStdin:
		args = append(args, flags...)
		return args, strings.NewReader(promptAndScript(prompt, script)), nil
	default:
		return nil, nil, fmt.Errorf("harness: unsupported prompt_mode %q for provider %q", cfg.definition.PromptMode, cfg.name)
	}
}

func flagTokenForKey(key string, definition *core.HarnessDefinition) string {
	if definition != nil && definition.OptionFlags != nil {
		if token, ok := definition.OptionFlags[key]; ok && strings.TrimSpace(token) != "" {
			return token
		}
	}
	if definition == nil {
		key = strings.ReplaceAll(key, "_", "-")
	}
	if definition != nil && definition.FlagStyle == core.HarnessFlagStyleSingleDash {
		return "-" + key
	}
	return "--" + key
}

func promptAndScript(prompt, script string) string {
	switch {
	case prompt == "":
		return script
	case script == "":
		return prompt
	default:
		return prompt + "\n\n" + script
	}
}

func knownProviders(defs core.HarnessDefinitions) []string {
	names := core.BuiltinHarnessProviderNames()
	for name, def := range defs {
		if def == nil {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func cloneDefinition(def *core.HarnessDefinition) *core.HarnessDefinition {
	if def == nil {
		return nil
	}
	return &core.HarnessDefinition{
		Binary:         def.Binary,
		PrefixArgs:     append([]string(nil), def.PrefixArgs...),
		PromptMode:     def.PromptMode,
		PromptFlag:     def.PromptFlag,
		PromptPosition: def.PromptPosition,
		FlagStyle:      def.FlagStyle,
		OptionFlags:    maps.Clone(def.OptionFlags),
	}
}

func isTemplatedValue(value string) bool {
	return strings.Contains(value, "${")
}

func resolveBinaryPath(binaryName, workDir string, envs map[string]string) (string, error) {
	if strings.TrimSpace(binaryName) == "" {
		return "", fmt.Errorf("empty binary name")
	}

	if hasPathSeparator(binaryName) {
		candidate := binaryName
		if !filepath.IsAbs(candidate) && workDir != "" {
			candidate = filepath.Join(workDir, candidate)
		}
		resolved, err := exec.LookPath(candidate)
		if err != nil {
			return "", err
		}
		return resolved, nil
	}

	pathValue := ""
	if envs != nil {
		pathValue = envs["PATH"]
	}
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}

	baseDir := workDir
	if baseDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory for PATH lookup: %w", err)
		}
		baseDir = wd
	}

	var lastErr error
	for _, dir := range filepath.SplitList(pathValue) {
		if dir == "" {
			dir = "."
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(baseDir, dir)
		}
		candidate := filepath.Join(dir, binaryName)
		resolved, err := exec.LookPath(candidate)
		if err == nil {
			return resolved, nil
		}
		if !errors.Is(err, exec.ErrNotFound) {
			lastErr = err
		}
	}

	if lastErr != nil {
		return "", lastErr
	}
	return "", &exec.Error{Name: binaryName, Err: exec.ErrNotFound}
}

func hasPathSeparator(path string) bool {
	return strings.Contains(path, string(os.PathSeparator)) || strings.Contains(path, "/")
}

func newStdoutSpool() (*os.File, error) {
	return os.CreateTemp("", "dagu-harness-stdout-*")
}

func cleanupStdoutSpool(file *os.File) error {
	if file == nil {
		return nil
	}

	name := file.Name()
	closeErr := file.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return nil
}

func readSpoolTail(file *os.File, max int, encoding string) (string, error) {
	if file == nil || max <= 0 {
		return "", nil
	}

	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() == 0 {
		return "", nil
	}

	start := int64(0)
	if info.Size() > int64(max) {
		start = info.Size() - int64(max)
	}

	buf := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}

	return strings.TrimRight(fileutil.DecodeString(encoding, buf), "\r\n"), nil
}

func formatProcessFailure(err error, stderrTail, stdoutTail string) error {
	stderrTail = strings.TrimRight(stderrTail, "\r\n")
	stdoutTail = strings.TrimRight(stdoutTail, "\r\n")

	switch {
	case stderrTail != "" && stdoutTail != "":
		return fmt.Errorf("%w\nrecent stderr:\n%s\nrecent stdout:\n%s", err, stderrTail, stdoutTail)
	case stderrTail != "":
		return fmt.Errorf("%w\nrecent stderr:\n%s", err, stderrTail)
	case stdoutTail != "":
		return fmt.Errorf("%w\nrecent stdout:\n%s", err, stdoutTail)
	default:
		return err
	}
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func init() {
	caps := core.ExecutorCapabilities{
		Command: true,
		Script:  true,
	}
	executor.RegisterExecutor("harness", newHarness, validateHarnessStep, caps)
}
