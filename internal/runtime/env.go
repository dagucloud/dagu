// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/mailer"
	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// Env holds information about the DAG and the current step to execute
// including the variables (environment variables and DAG variables) that are
// available to the step.
type Env struct {
	// Embedded execution metadata from parent DAG run containing DAGRunID,
	// RootDAGRun reference, DAG configuration, database interface, and
	// coordinator dispatcher
	Context

	// Unified scope chain for environment variable lookups.
	// This scope is the source for $VAR and ${VAR} expansion.
	// Layers (highest to lowest precedence): StepEnv > Outputs > Secrets > DAGEnv > OS
	Scope *cmnvalue.EnvScope

	// The current step being executed within this environment context
	Step core.Step

	// Maps step IDs to their execution information (stdout, stderr, exitCode)
	// allowing steps to reference outputs from other steps using expressions
	// like ${stepID.stdout} or ${stepID.exitCode} in their configurations.
	// Step references are resolved separately from environment variables.
	StepMap map[string]cmnvalue.StepInfo

	// Resolved absolute path for the step's working directory, determined by:
	// 1. Step's Dir field if specified (resolved to absolute path)
	// 2. Current working directory if Dir is not specified
	// This path is also set as the PWD environment variable
	WorkingDir string
}

// AllEnvs returns all environment variables that needs to be passed to the command.
// Uses EnvScope as the source of environment variables.
func (e Env) AllEnvs() []string {
	if e.Scope == nil {
		return nil
	}
	return e.Scope.ToSlice()
}

// UserEnvsMap returns user-defined environment variables as a map,
// excluding OS environment (BaseEnv). Use this for isolated execution environments.
// Uses EnvScope as the source of environment variables.
func (e Env) UserEnvsMap() map[string]string {
	if e.Scope == nil {
		return make(map[string]string)
	}
	return e.Scope.AllUserEnvs()
}

// NewEnv creates a new Env configured for executing the provided step.
// It resolves the step's working directory and sets initial per-step environment
// variables: PWD to the resolved working directory and the DAG run step name.
func NewEnv(ctx context.Context, step core.Step) Env {
	rCtx := GetDAGContext(ctx)
	workingDir := resolveWorkingDir(ctx, step, rCtx)
	return newEnv(step, rCtx, workingDir)
}

// NewEnvWithError creates an Env and returns working directory resolution errors.
func NewEnvWithError(ctx context.Context, step core.Step) (Env, error) {
	rCtx := GetDAGContext(ctx)
	workingDir, err := resolveWorkingDirStrict(ctx, step, rCtx)
	if err != nil {
		return Env{}, err
	}
	return newEnv(step, rCtx, workingDir), nil
}

func newEnv(step core.Step, rCtx Context, workingDir string) Env {
	// Build step-specific env vars
	stepEnvs := map[string]string{
		exec.EnvKeyDAGRunStepName: step.Name,
		"PWD":                     workingDir,
	}

	// Build scope from DAG context + step envs.
	// The scope chain inherits from rCtx.EnvScope (filtered BaseEnv + DAG env + secrets).
	// and adds step-specific environment variables
	scope := rCtx.EnvScope
	if scope == nil {
		scope = cmnvalue.NewEnvScope(nil, true) // Fallback: OS layer only
	}
	scope = scope.WithEntries(stepEnvs, cmnvalue.EnvSourceStepEnv)

	return Env{
		Context:    rCtx,
		Scope:      scope,
		Step:       step,
		StepMap:    make(map[string]cmnvalue.StepInfo),
		WorkingDir: workingDir,
	}
}

func resolveWorkingDir(ctx context.Context, step core.Step, rCtx Context) string {
	dag := rCtx.DAG

	if step.Dir != "" {
		expandedDir := expandStepDir(ctx, step.Dir, dag)
		return resolveExpandedDir(ctx, expandedDir, step.Name, dag, rCtx)
	}

	if workDir := dagWorkingDir(ctx, dag, rCtx); workDir != "" {
		return workDir
	}

	return fallbackWorkingDir(ctx, step.Name)
}

func resolveWorkingDirStrict(ctx context.Context, step core.Step, rCtx Context) (string, error) {
	dag := rCtx.DAG

	if step.Dir != "" {
		expandedDir, err := expandStepDirStrict(ctx, step.Dir, dag)
		if err != nil {
			return "", err
		}
		return resolveExpandedDirStrict(ctx, expandedDir, step.Name, dag, rCtx)
	}

	workDir, err := dagWorkingDirStrict(ctx, dag, rCtx)
	if err != nil {
		return "", err
	}
	if workDir != "" {
		return workDir, nil
	}

	return fallbackWorkingDir(ctx, step.Name), nil
}

func expandRuntimeValue(ctx context.Context, raw string, dag *core.DAG, scope *cmnvalue.EnvScope, field cmnvalue.Field) (string, error) {
	var consts cmnvalue.Values
	var params cmnvalue.Values
	var paramDeclarations cmnvalue.Values
	if dag != nil {
		consts = cmnvalue.Values(dag.Consts)
		params = dag.ParamValues()
		paramDeclarations = dag.ParamDeclarations()
	}
	resolver := cmnvalue.NewResolver(
		cmnvalue.StaticScope{Consts: consts, Params: paramDeclarations},
		cmnvalue.RuntimeScope{Consts: consts, Params: params, Env: scope},
	)
	return resolver.String(ctx, raw, field)
}

// expandStepDir expands value references and environment variables in step.Dir.
func expandStepDir(ctx context.Context, dir string, dag *core.DAG) string {
	expanded, err := expandRuntimeValue(ctx, dir, dag, nil, cmnvalue.StepDirField("working_dir"))
	if err != nil {
		logger.Warn(ctx, "Failed to evaluate step working directory",
			tag.Dir(dir),
			tag.Error(err),
		)
		expanded = dir
	}
	return expandStepDirEnvOnly(expanded, dag)
}

func expandStepDirStrict(ctx context.Context, dir string, dag *core.DAG) (string, error) {
	expanded, err := expandRuntimeValue(ctx, dir, dag, nil, cmnvalue.StepDirField("working_dir"))
	if err != nil {
		return "", fmt.Errorf("failed to evaluate step working directory %q: %w", dir, err)
	}
	return expandStepDirEnvOnly(expanded, dag), nil
}

func expandStepDirEnvOnly(dir string, dag *core.DAG) string {
	scope := cmnvalue.NewEnvScope(nil, true)
	if dag != nil {
		for _, env := range dag.Env {
			if k, v, ok := strings.Cut(env, "="); ok {
				scope = scope.WithEntry(k, v, cmnvalue.EnvSourceDAGEnv)
			}
		}
	}
	return scope.Expand(dir)
}

// resolveExpandedDir resolves an expanded directory path to an absolute path.
func resolveExpandedDir(ctx context.Context, expandedDir, stepName string, dag *core.DAG, rCtx Context) string {
	if filepath.IsAbs(expandedDir) || strings.HasPrefix(expandedDir, "~") {
		dir, err := fileutil.ResolvePath(expandedDir)
		if err != nil {
			logger.Warn(ctx, "Failed to resolve working directory for step",
				tag.Step(stepName),
				tag.Dir(expandedDir),
				tag.Error(err),
			)
			return expandedDir
		}
		return dir
	}

	if workDir := dagWorkingDir(ctx, dag, rCtx); workDir != "" {
		return filepath.Clean(filepath.Join(workDir, expandedDir))
	}

	logger.Warn(ctx, "Failed to resolve working directory for step",
		tag.Step(stepName),
		tag.Dir(expandedDir),
	)
	return expandedDir
}

func resolveExpandedDirStrict(ctx context.Context, expandedDir, stepName string, dag *core.DAG, rCtx Context) (string, error) {
	if filepath.IsAbs(expandedDir) || strings.HasPrefix(expandedDir, "~") {
		dir, err := fileutil.ResolvePath(expandedDir)
		if err != nil {
			return "", fmt.Errorf("failed to resolve working directory for step %q: %w", stepName, err)
		}
		return dir, nil
	}

	workDir, err := dagWorkingDirStrict(ctx, dag, rCtx)
	if err != nil {
		return "", err
	}
	if workDir != "" {
		return filepath.Clean(filepath.Join(workDir, expandedDir)), nil
	}

	return expandedDir, nil
}

func dagWorkingDir(ctx context.Context, dag *core.DAG, rCtx Context) string {
	if dag != nil && dag.WorkingDirExplicit && dag.WorkingDir != "" {
		return expandDAGWorkingDir(ctx, dag.WorkingDir, rCtx)
	}
	if workDir := dagRunWorkDir(rCtx); workDir != "" {
		return workDir
	}
	if dag != nil && dag.WorkingDir != "" {
		return expandDAGWorkingDir(ctx, dag.WorkingDir, rCtx)
	}
	return ""
}

func dagWorkingDirStrict(ctx context.Context, dag *core.DAG, rCtx Context) (string, error) {
	if dag != nil && dag.WorkingDirExplicit && dag.WorkingDir != "" {
		return expandDAGWorkingDirStrict(ctx, dag.WorkingDir, rCtx)
	}
	if workDir := dagRunWorkDir(rCtx); workDir != "" {
		return workDir, nil
	}
	if dag != nil && dag.WorkingDir != "" {
		return expandDAGWorkingDirStrict(ctx, dag.WorkingDir, rCtx)
	}
	return "", nil
}

func dagRunWorkDir(rCtx Context) string {
	if rCtx.EnvScope == nil {
		return ""
	}
	workDir, ok := rCtx.EnvScope.Get(exec.EnvKeyDAGRunWorkDir)
	if !ok {
		return ""
	}
	return strings.TrimSpace(workDir)
}

func expandDAGWorkingDir(ctx context.Context, workingDir string, rCtx Context) string {
	wd, err := expandRuntimeValue(ctx, workingDir, rCtx.DAG, rCtx.EnvScope, cmnvalue.DAGWorkingDirField("working_dir"))
	if err != nil {
		logger.Warn(ctx, "Failed to evaluate working directory",
			tag.Dir(workingDir),
			tag.Error(err),
		)
		wd = workingDir
	}
	wd = expandDAGWorkingDirEnvOnly(wd, rCtx.EnvScope)
	if strings.HasPrefix(wd, "~") {
		resolved, err := fileutil.ResolvePath(wd)
		if err != nil {
			logger.Warn(ctx, "Failed to resolve working directory",
				tag.Dir(wd),
				tag.Error(err),
			)
		} else {
			wd = resolved
		}
	}
	return wd
}

func expandDAGWorkingDirStrict(ctx context.Context, workingDir string, rCtx Context) (string, error) {
	wd, err := expandRuntimeValue(ctx, workingDir, rCtx.DAG, rCtx.EnvScope, cmnvalue.DAGWorkingDirField("working_dir"))
	if err != nil {
		return "", fmt.Errorf("failed to evaluate working directory %q: %w", workingDir, err)
	}
	wd = expandDAGWorkingDirEnvOnly(wd, rCtx.EnvScope)
	if strings.HasPrefix(wd, "~") {
		resolved, err := fileutil.ResolvePath(wd)
		if err != nil {
			return "", fmt.Errorf("failed to resolve working directory %q: %w", wd, err)
		}
		wd = resolved
	}
	return wd, nil
}

func expandDAGWorkingDirEnvOnly(workingDir string, scope *cmnvalue.EnvScope) string {
	if scope != nil {
		return scope.Expand(workingDir)
	}
	return cmnvalue.NewEnvScope(nil, true).Expand(workingDir)
}

// fallbackWorkingDir returns a fallback working directory when none is specified.
func fallbackWorkingDir(ctx context.Context, stepName string) string {
	logger.Warn(ctx, "Failed to resolve working directory for step",
		tag.Step(stepName),
	)

	wd, err := os.Getwd()
	if err == nil {
		return wd
	}
	logger.Error(ctx, "Failed to get current working directory", tag.Error(err))

	dir, err := os.UserHomeDir()
	if err != nil {
		logger.Error(ctx, "Failed to get user home directory", tag.Error(err))
	}
	return dir
}

// Shell returns the shell command to use for this execution context.
func (e Env) Shell(ctx context.Context) []string {
	shell, err := e.ResolveShell(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to evaluate shell", tag.Error(err))
		return nil
	}
	return shell
}

// ResolveShell returns the shell command to use for this execution context.
func (e Env) ResolveShell(ctx context.Context) ([]string, error) {
	// Shell precedence: Step shell -> DAG shell -> Global default
	if e.Step.Shell != "" {
		shell, err := evalShellWithScope(ctx, e.DAG, e.Scope, e.Step.Shell, e.Step.ShellArgs, cmnvalue.StepShellField)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate step shell %q: %w", e.Step.Shell, err)
		}
		return shell, nil
	}

	if e.Step.ShellArgs != nil {
		if e.DAG != nil && e.DAG.Shell != "" {
			shell, err := evalShellInvocationWithScope(ctx, e.DAG, e.Scope, e.DAG.Shell, e.Step.ShellArgs, cmnvalue.DAGShellField, cmnvalue.StepShellField)
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate inherited DAG shell with step shell args: %w", err)
			}
			return shell, nil
		}

		shell, err := evalShellArgsWithScope(ctx, e.DAG, e.Scope, defaultShell(ctx), e.Step.ShellArgs, cmnvalue.StepShellField)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate step shell arguments: %w", err)
		}
		return shell, nil
	}

	if e.DAG != nil && e.DAG.Shell != "" {
		shell, err := evalShellWithScope(ctx, e.DAG, e.Scope, e.DAG.Shell, e.DAG.ShellArgs, cmnvalue.DAGShellField)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate DAG shell %q: %w", e.DAG.Shell, err)
		}
		return shell, nil
	}

	return defaultShell(ctx), nil
}

// DAGShell returns the evaluated shell command for DAG-level operations.
// This is used for preconditions and other operations that run before any steps.
// Unlike Env.Shell(), this doesn't require a step context.
func DAGShell(ctx context.Context) []string {
	shell, err := ResolveDAGShell(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to evaluate DAG shell", tag.Error(err))
		return nil
	}
	return shell
}

// ResolveDAGShell returns the evaluated shell command for DAG-level operations.
func ResolveDAGShell(ctx context.Context) ([]string, error) {
	rCtx := GetDAGContext(ctx)
	dag := rCtx.DAG

	if dag == nil || dag.Shell == "" {
		return defaultShell(ctx), nil
	}

	scope := rCtx.EnvScope
	if scope == nil {
		scope = cmnvalue.NewEnvScope(nil, true) // Fallback: OS layer only
	}

	shell, err := evalShellWithScope(ctx, dag, scope, dag.Shell, dag.ShellArgs, cmnvalue.DAGShellField)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate DAG shell %q: %w", dag.Shell, err)
	}
	return shell, nil
}

// evalShellWithScope evaluates shell command and arguments using the given scope.
func evalShellWithScope(ctx context.Context, dag *core.DAG, scope *cmnvalue.EnvScope, shell string, shellArgs []string, fieldForPath func(string) cmnvalue.Field) ([]string, error) {
	return evalShellInvocationWithScope(ctx, dag, scope, shell, shellArgs, fieldForPath, fieldForPath)
}

func evalShellInvocationWithScope(ctx context.Context, dag *core.DAG, scope *cmnvalue.EnvScope, shell string, shellArgs []string, shellFieldForPath func(string) cmnvalue.Field, argFieldForPath func(string) cmnvalue.Field) ([]string, error) {
	var consts cmnvalue.Values
	var params cmnvalue.Values
	var paramDeclarations cmnvalue.Values
	if dag != nil {
		consts = cmnvalue.Values(dag.Consts)
		params = dag.ParamValues()
		paramDeclarations = dag.ParamDeclarations()
	}
	resolver := cmnvalue.NewResolver(
		cmnvalue.StaticScope{Consts: consts, Params: paramDeclarations},
		cmnvalue.RuntimeScope{Consts: consts, Params: params, Env: scope},
	)
	shellCmd, err := resolver.String(ctx, shell, shellFieldForPath("shell"))
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate shell: %w", err)
	}

	return evalShellArgsWithResolver(ctx, []string{shellCmd}, shellArgs, argFieldForPath, resolver)
}

func evalShellArgsWithScope(ctx context.Context, dag *core.DAG, scope *cmnvalue.EnvScope, shell []string, shellArgs []string, fieldForPath func(string) cmnvalue.Field) ([]string, error) {
	var consts cmnvalue.Values
	var params cmnvalue.Values
	var paramDeclarations cmnvalue.Values
	if dag != nil {
		consts = cmnvalue.Values(dag.Consts)
		params = dag.ParamValues()
		paramDeclarations = dag.ParamDeclarations()
	}
	resolver := cmnvalue.NewResolver(
		cmnvalue.StaticScope{Consts: consts, Params: paramDeclarations},
		cmnvalue.RuntimeScope{Consts: consts, Params: params, Env: scope},
	)
	return evalShellArgsWithResolver(ctx, shell, shellArgs, fieldForPath, resolver)
}

func evalShellArgsWithResolver(ctx context.Context, shell []string, shellArgs []string, fieldForPath func(string) cmnvalue.Field, resolver cmnvalue.Resolver) ([]string, error) {
	result := append([]string(nil), shell...)
	if len(result) == 0 {
		return result, nil
	}
	for i, arg := range shellArgs {
		evaluated, err := resolver.String(ctx, arg, fieldForPath(fmt.Sprintf("shell_args[%d]", i)))
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate shell argument %q: %w", arg, err)
		}
		result = append(result, evaluated)
	}
	return result, nil
}

// defaultShell returns the global default shell.
func defaultShell(ctx context.Context) []string {
	shellCmd := cmdutil.GetShellCommand(config.GetConfig(ctx).Core.DefaultShell)
	if shellCmd != "" {
		return []string{shellCmd}
	}
	logger.Debug(ctx, "Global default shell is not set or could not be determined")
	return nil
}

// DAGRunRef returns the DAGRunRef for the current execution context.
func (e Env) DAGRunRef() exec.DAGRunRef {
	return exec.NewDAGRunRef(e.DAG.Name, e.DAGRunID)
}

// MailerConfig returns the SMTP mailer configuration with variables evaluated.
func (e Env) MailerConfig(ctx context.Context) (mailer.Config, error) {
	if e.DAG.SMTP == nil {
		return mailer.Config{}, nil
	}
	resolver := resolverFromEnv(e)
	got, err := resolver.Object(ctx, mailer.Config{
		Host:     e.DAG.SMTP.Host,
		Port:     e.DAG.SMTP.Port,
		Username: e.DAG.SMTP.Username,
		Password: e.DAG.SMTP.Password,
	}, cmnvalue.HostConfigObjectField("smtp"))
	if err != nil {
		return mailer.Config{}, err
	}
	config, ok := got.(mailer.Config)
	if !ok {
		return mailer.Config{}, fmt.Errorf("type assertion failed: expected mailer.Config, got %T", got)
	}
	return config, nil
}

// EvalBool evaluates the given value with the variables within the execution context
func (e Env) EvalBool(ctx context.Context, value any) (bool, error) {
	switch v := value.(type) {
	case string:
		s, err := resolverFromEnv(e).String(ctx, v, cmnvalue.WorkflowField("bool"))
		if err != nil {
			return false, err
		}
		return strconv.ParseBool(s)
	case bool:
		return v, nil
	default:
		return false, fmt.Errorf("unsupported type %T for bool (value: %+v)", value, value)
	}
}

// WithEnvVars returns a new Env with the given environment variable(s) added to the Scope.
func (e Env) WithEnvVars(envs ...string) Env {
	if len(envs)%2 != 0 {
		panic("invalid number of arguments")
	}
	newEnvs := make(map[string]string)
	for i := 0; i+1 < len(envs); i += 2 {
		newEnvs[envs[i]] = envs[i+1]
	}
	e.Scope = e.Scope.WithEntries(newEnvs, cmnvalue.EnvSourceStepEnv)
	return e
}

// Context key for storing Env in context
type envCtxKey struct{}

// WithEnv returns a new context with the given execution context.
func WithEnv(ctx context.Context, e Env) context.Context {
	return context.WithValue(ctx, envCtxKey{}, e)
}

// LookupEnv returns the execution environment when one is present in ctx.
func LookupEnv(ctx context.Context) (Env, bool) {
	v, ok := ctx.Value(envCtxKey{}).(Env)
	return v, ok
}

// GetEnv returns the execution context from the given context.
func GetEnv(ctx context.Context) Env {
	v, ok := LookupEnv(ctx)
	if !ok {
		return NewEnv(ctx, core.Step{})
	}
	return v
}

// AllEnvs returns all environment variables that needs to be passed to the command.
// Each element is in the form of "key=value".
func AllEnvs(ctx context.Context) []string {
	return GetEnv(ctx).AllEnvs()
}

// AllEnvsMap builds a map of environment variables from the current Env.
// It returns the EnvScope's ToMap directly, avoiding the round-trip through
// string splitting.
func AllEnvsMap(ctx context.Context) map[string]string {
	env := GetEnv(ctx)
	if env.Scope == nil {
		return make(map[string]string)
	}
	return env.Scope.ToMap()
}
