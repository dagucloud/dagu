// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package runtimeenv resolves the environment used by a DAG run.
package runtimeenv

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/joho/godotenv"
)

// Result contains resolved environment entries and non-fatal load warnings.
type Result struct {
	Env      []string
	Warnings []string
}

// Resolve returns the runtime environment without mutating the DAG definition.
func Resolve(ctx context.Context, dag *ir.DAG) (Result, error) {
	if dag == nil {
		return Result{}, nil
	}

	result := Result{Env: append([]string(nil), dag.Env...)}
	if dag.RuntimeResolved || len(dag.Dotenv) == 0 {
		return result, nil
	}

	scope := dotenvEnvScope(dag)
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = cmnvalue.WithEnvScope(ctx, scope)

	workingDir := expandDotenvPath(dag, dag.WorkingDir, scope)
	relativeTos := []string{workingDir}
	if fileDir := filepath.Dir(dag.Location); dag.Location != "" && fileDir != workingDir {
		relativeTos = append(relativeTos, fileDir)
	}
	resolver := fileutil.NewFileResolver(relativeTos)

	var errs ir.ErrorList
	for _, path := range deduplicate(append([]string{".env"}, dag.Dotenv...)) {
		if err := loadFile(ctx, dag, resolver, path, &result); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return result, errs
	}
	return result, nil
}

// LoadEnvFiles reads dotenv-syntax files and returns their variables as
// KEY=VALUE entries. Later files override earlier ones. Relative paths are
// looked up in workingDir, then in the directory of dagLocation. Blank paths
// are ignored; a missing or unreadable file is an error.
func LoadEnvFiles(paths []string, workingDir, dagLocation string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	var relativeTos []string
	if dir := strings.TrimSpace(workingDir); dir != "" {
		relativeTos = append(relativeTos, dir)
	}
	if dagLocation != "" {
		if dagDir := filepath.Dir(dagLocation); !slices.Contains(relativeTos, dagDir) {
			relativeTos = append(relativeTos, dagDir)
		}
	}
	resolver := fileutil.NewFileResolver(relativeTos)

	var entries []string
	indexByKey := make(map[string]int)
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		resolved, err := resolver.ResolveFilePathLiteral(path)
		if err != nil {
			return nil, fmt.Errorf("env file %q: %w", path, err)
		}
		vars, err := godotenv.Read(resolved)
		if err != nil {
			return nil, fmt.Errorf("failed to read env file %q: %w", resolved, err)
		}
		for _, key := range slices.Sorted(maps.Keys(vars)) {
			entry := key + "=" + vars[key]
			if idx, ok := indexByKey[key]; ok {
				entries[idx] = entry
				continue
			}
			indexByKey[key] = len(entries)
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// ResolveWorkingDir resolves a DAG working directory using values available before execution.
func ResolveWorkingDir(ctx context.Context, dag *ir.DAG) (string, error) {
	if dag == nil || strings.TrimSpace(dag.WorkingDir) == "" {
		return "", nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	resolvedEnv, err := Resolve(ctx, dag)
	if err != nil {
		return "", err
	}
	resolvedDAG := dag.Clone()
	resolvedDAG.Env = resolvedEnv.Env
	scope := dotenvEnvScope(resolvedDAG)
	var notices cmnvalue.ValueReferenceNoticeCollector
	resolver := cmnvalue.NewResolver(
		cmnvalue.StaticScope{
			Consts: cmnvalue.Values(dag.Consts),
			Params: dag.ParamDeclarations(),
		},
		cmnvalue.RuntimeScope{
			Consts:     cmnvalue.Values(dag.Consts),
			Params:     dag.ParamValues(),
			ParamsJSON: dag.ParamsJSON,
			Env:        scope,
		},
		cmnvalue.WithValueReferenceNotices(&notices),
	)
	field := cmnvalue.DAGWorkingDirField("working_dir")
	workingDir, err := resolver.String(ctx, dag.WorkingDir, field)
	if err != nil {
		return "", err
	}
	workingDir = scope.ExpandPath(workingDir)
	cmnvalue.ReportUnresolvedEnvExpansionNotices(workingDir, field, scope, &notices)
	if unresolved := notices.Notices(); len(unresolved) > 0 {
		return "", fmt.Errorf("working_dir %q could not be resolved: %s", dag.WorkingDir, unresolved[0].Message)
	}
	if strings.TrimSpace(workingDir) == "" {
		return "", fmt.Errorf("working_dir %q resolved to an empty path", dag.WorkingDir)
	}
	return fileutil.ResolvePath(workingDir)
}

func dotenvEnvScope(dag *ir.DAG) *cmnvalue.EnvScope {
	scope := cmnvalue.NewEnvScope(nil, true)
	if params := buildenv.ToMap(dag.Params); len(params) > 0 {
		scope = scope.WithEntries(params, cmnvalue.EnvSourceParam)
	}
	if len(dag.PresolvedBuildEnv) > 0 {
		scope = scope.WithEntries(dag.PresolvedBuildEnv, cmnvalue.EnvSourcePresolved)
	}
	if envs := buildenv.ToMap(dag.Env); len(envs) > 0 {
		scope = scope.WithEntries(envs, cmnvalue.EnvSourceDAGEnv)
	}
	return scope
}

func expandDotenvPath(dag *ir.DAG, path string, scope *cmnvalue.EnvScope) string {
	resolver := cmnvalue.NewResolver(
		cmnvalue.StaticScope{Consts: cmnvalue.Values(dag.Consts)},
		cmnvalue.RuntimeScope{Consts: cmnvalue.Values(dag.Consts)},
	)
	expanded, err := resolver.String(context.Background(), path, cmnvalue.DAGWorkingDirField("working_dir"))
	if err != nil {
		expanded = path
	}
	return scope.ExpandPath(expanded)
}

func loadFile(
	ctx context.Context,
	dag *ir.DAG,
	resolver *fileutil.FileResolver,
	path string,
	result *Result,
) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}

	valueResolver := cmnvalue.NewResolver(
		cmnvalue.StaticScope{Consts: cmnvalue.Values(dag.Consts), Params: dag.ParamDeclarations()},
		cmnvalue.RuntimeScope{
			Consts:     cmnvalue.Values(dag.Consts),
			Params:     dag.ParamValues(),
			ParamsJSON: dag.ParamsJSON,
			Env:        cmnvalue.GetEnvScope(ctx),
		},
	)
	evaluatedPath, err := valueResolver.String(ctx, path, cmnvalue.DotenvPathField("dotenv"))
	if err != nil {
		return fmt.Errorf("failed to evaluate dotenv path %q: %w", path, err)
	}

	resolvedPath, err := resolver.ResolveFilePathLiteral(evaluatedPath)
	if err != nil || !fileutil.FileExists(resolvedPath) {
		return nil
	}

	vars, err := godotenv.Read(resolvedPath)
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("failed to load .env file %q: %v", resolvedPath, err))
		return nil
	}
	for key, value := range vars {
		result.Env = append(result.Env, key+"="+value)
	}
	return nil
}

func deduplicate(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
