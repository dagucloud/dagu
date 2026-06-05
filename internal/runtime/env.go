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
	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/mailer"
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

	// Unified scope chain for ALL environment variable lookups.
	// This is THE single source of truth for $VAR and ${VAR} expansion.
	// Layers (highest to lowest precedence): StepEnv > Outputs > Secrets > DAGEnv > OS
	Scope *eval.EnvScope

	// The current step being executed within this environment context
	Step core.Step

	// Maps step IDs to their execution information (stdout, stderr, exitCode)
	// allowing steps to reference outputs from other steps using expressions
	// like ${stepID.stdout} or ${stepID.exitCode} in their configurations.
	// NOTE: This is a SEPARATE system from env var expansion.
	StepMap map[string]eval.StepInfo

	// Resolved absolute path for the step's working directory, determined by:
	// 1. Step's Dir field if specified (resolved to absolute path)
	// 2. Current working directory if Dir is not specified
	// This path is also set as the PWD environment variable
	WorkingDir string
}

// AllEnvs returns all environment variables that needs to be passed to the command.
// Uses EnvScope as THE single source of truth.
func (e Env) AllEnvs() []string {
	if e.Scope == nil {
		return nil
	}
	return e.Scope.ToSlice()
}

// UserEnvsMap returns user-defined environment variables as a map,
// excluding OS environment (BaseEnv). Use this for isolated execution environments.
// Uses EnvScope as THE single source of truth.
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
		scope = eval.NewEnvScope(nil, true) // Fallback: OS layer only
	}
	scope = scope.WithEntries(stepEnvs, eval.EnvSourceStepEnv)

	return Env{
		Context:    rCtx,
		Scope:      scope,
		Step:       step,
		StepMap:    make(map[string]eval.StepInfo),
		WorkingDir: workingDir,
	}
}

func resolveWorkingDir(ctx context.Context, step core.Step, rCtx Context) string {
	dag := rCtx.DAG

	if step.Dir != "" {
		expandedDir := expandStepDir(step.Dir, dag)
		return resolveExpandedDir(ctx, expandedDir, step.Name, dag, rCtx)
	}

	if workDir := dagWorkingDir(ctx, dag, rCtx); workDir != "" {
		return workDir
	}

	return fallbackWorkingDir(ctx, step.Name)
}

// expandStepDir expands environment variables in step.Dir using DAG env vars.
func expandStepDir(dir string, dag *core.DAG) string {
	return os.Expand(dir, func(key string) string {
		if dag != nil {
			for _, env := range dag.Env {
				if k, v, ok := strings.Cut(env, "="); ok && k == key {
					return v
				}
			}
		}
		return os.Getenv(key)
	})
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
	wd := workingDir
	if rCtx.EnvScope != nil {
		wd = rCtx.EnvScope.Expand(wd)
	} else {
		wd = os.ExpandEnv(wd)
	}
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
	// Shell precedence: Step shell -> DAG shell -> Global default
	if e.Step.Shell != "" {
		shell, err := evalShellWithScope(ctx, e.Scope, e.Step.Shell, e.Step.ShellArgs)
		if err != nil {
			logger.Error(ctx, "Failed to evaluate step shell",
				tag.String("shell", e.Step.Shell),
				tag.Error(err),
			)
			return nil
		}
		return shell
	}

	if e.DAG != nil && e.DAG.Shell != "" {
		shell, err := evalShellWithScope(ctx, e.Scope, e.DAG.Shell, e.DAG.ShellArgs)
		if err != nil {
			logger.Error(ctx, "Failed to evaluate DAG shell",
				tag.String("shell", e.DAG.Shell),
				tag.Error(err),
			)
			return nil
		}
		return shell
	}

	return defaultShell(ctx)
}

// DAGShell returns the evaluated shell command for DAG-level operations.
// This is used for preconditions and other operations that run before any steps.
// Unlike Env.Shell(), this doesn't require a step context.
func DAGShell(ctx context.Context) []string {
	rCtx := GetDAGContext(ctx)
	dag := rCtx.DAG

	if dag == nil || dag.Shell == "" {
		return defaultShell(ctx)
	}

	scope := rCtx.EnvScope
	if scope == nil {
		scope = eval.NewEnvScope(nil, true) // Fallback: OS layer only
	}

	shell, err := evalShellWithScope(ctx, scope, dag.Shell, dag.ShellArgs)
	if err != nil {
		logger.Error(ctx, "Failed to evaluate DAG shell",
			tag.String("shell", dag.Shell),
			tag.Error(err),
		)
		return nil
	}
	return shell
}

// evalShellWithScope evaluates shell command and arguments using the given scope.
func evalShellWithScope(ctx context.Context, scope *eval.EnvScope, shell string, shellArgs []string) ([]string, error) {
	ctx = eval.WithEnvScope(ctx, scope)

	shellCmd, err := eval.String(ctx, shell)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate shell: %w", err)
	}

	result := []string{shellCmd}
	for _, arg := range shellArgs {
		evaluated, err := eval.String(ctx, arg)
		if err != nil {
			logger.Error(ctx, "Failed to evaluate shell argument",
				tag.String("arg", arg),
				tag.Error(err),
			)
			// Continue with unevaluated arg rather than failing completely
			evaluated = arg
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
	// Use Scope for variable resolution
	ctx = eval.WithEnvScope(ctx, e.Scope)
	return eval.StringFields(ctx, mailer.Config{
		Host:     e.DAG.SMTP.Host,
		Port:     e.DAG.SMTP.Port,
		Username: e.DAG.SMTP.Username,
		Password: e.DAG.SMTP.Password,
	})
}

// EvalString evaluates the given string with the variables within the execution context.
// Uses EnvScope as THE single source of truth for $VAR and ${VAR} expansion.
// StepMap is used separately for ${step.stdout} style references.
func (e Env) EvalString(ctx context.Context, s string, opts ...eval.Option) (string, error) {
	// Use EnvScope for variable resolution via context
	ctx = eval.WithEnvScope(ctx, e.Scope)

	// StepMap for ${step.stdout} syntax (separate system from env vars)
	opts = append(opts, eval.WithStepMap(e.StepMap))

	return eval.String(ctx, s, opts...)
}

// EvalBool evaluates the given value with the variables within the execution context
func (e Env) EvalBool(ctx context.Context, value any) (bool, error) {
	switch v := value.(type) {
	case string:
		s, err := e.EvalString(ctx, v)
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
	e.Scope = e.Scope.WithEntries(newEnvs, eval.EnvSourceStepEnv)
	return e
}

// Context key for storing Env in context
type envCtxKey struct{}

// WithEnv returns a new context with the given execution context.
func WithEnv(ctx context.Context, e Env) context.Context {
	return context.WithValue(ctx, envCtxKey{}, e)
}

// GetEnv returns the execution context from the given context.
func GetEnv(ctx context.Context) Env {
	v, ok := ctx.Value(envCtxKey{}).(Env)
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
