// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"os"
)

// resolver provides unified variable resolution across explicit variable maps,
// EnvScope, and OS environment.
type resolver struct {
	variables              []map[string]string
	stepMap                map[string]StepInfo
	scope                  *EnvScope
	expandOS               bool
	deferShellVars         bool
	recognizeEscapedDollar bool
}

// newResolver creates a resolver from the given context and options.
func newResolver(ctx context.Context, opts *options) *resolver {
	return &resolver{
		variables:              opts.Variables,
		stepMap:                opts.StepMap,
		scope:                  GetEnvScope(ctx),
		expandOS:               opts.ExpandOS,
		deferShellVars:         opts.DeferShellVars,
		recognizeEscapedDollar: opts.RecognizeEscapedDollar,
	}
}

// lookupVariable searches the explicit variable maps for the given name.
func (r *resolver) lookupVariable(name string) (string, bool) {
	for _, vars := range r.variables {
		if val, ok := vars[name]; ok {
			return val, true
		}
	}
	return "", false
}

// lookupScopeNonOS searches the scope for a non-OS-sourced entry.
func (r *resolver) lookupScopeNonOS(name string) (string, bool) {
	if r.scope == nil {
		return "", false
	}
	entry, ok := r.scope.GetEntry(name)
	if ok && entry.Source != EnvSourceOS {
		return entry.Value, true
	}
	return "", false
}

// resolve looks up a variable from explicit variable maps and scope.
// Only user-defined scope entries are checked (OS-sourced entries are skipped).
func (r *resolver) resolve(name string) (string, bool) {
	if val, ok := r.lookupVariable(name); ok {
		return val, true
	}
	return r.lookupScopeNonOS(name)
}

// resolveForReplace resolves a variable for simple $VAR replacement.
// When deferShellVars is true, most scope variables are deferred to the
// shell at runtime. Numeric variables ($1, $2, ...) are always expanded
// because shells treat $N as positional arguments, not environment variables.
func (r *resolver) resolveForReplace(name string) (string, bool) {
	if val, ok := r.lookupVariable(name); ok {
		return val, true
	}
	if r.deferShellVars && !isNumericVar(name) {
		return "", false
	}
	return r.lookupScopeNonOS(name)
}

// isNumericVar reports whether name consists entirely of digits (e.g., "1", "2").
// These correspond to shell positional parameters ($1, $2, ...) which cannot
// be set via environment variables, so they must always be expanded by Dagu.
func isNumericVar(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// resolveForShell looks up a variable for shell expansion.
// Like resolve but includes OS environment as a final fallback when expandOS is true.
func (r *resolver) resolveForShell(name string) (string, bool) {
	if val, ok := r.resolve(name); ok {
		return val, true
	}
	if r.expandOS {
		return os.LookupEnv(name)
	}
	return "", false
}

// resolveReference resolves a dotted reference (step property or JSON path).
func (r *resolver) resolveReference(ctx context.Context, varName, path string) (string, bool) {
	if r.stepMap != nil {
		if value, ok := resolveStepProperty(ctx, varName, path, r.stepMap); ok {
			return value, true
		}
	}
	jsonStr, ok := r.resolveJSONSource(varName)
	if !ok {
		return "", false
	}
	return resolveJSONPath(ctx, varName, jsonStr, path)
}

// resolveJSONSource looks up a variable's raw value for JSON path resolution.
// Unlike resolve, this includes OS-sourced scope entries when expandOS is true,
// because JSON path resolution needs the actual value regardless of source.
func (r *resolver) resolveJSONSource(name string) (string, bool) {
	if val, ok := r.lookupVariable(name); ok {
		return val, true
	}
	if r.expandOS {
		if r.scope != nil {
			if val, ok := r.scope.Get(name); ok {
				return val, true
			}
		}
		return os.LookupEnv(name)
	}
	return r.lookupScopeNonOS(name)
}

// replaceVars substitutes $VAR and ${VAR} patterns using all resolver sources.
// JSON path references (containing dots) are skipped; those are handled by expandReferences.
func (r *resolver) replaceVars(input string) string {
	return (template{source: input}).resolveVariables(r)
}

// expandReferences resolves JSON path and step property references in the input.
func (r *resolver) expandReferences(ctx context.Context, input string) string {
	return (template{source: input}).resolveReferences(ctx, r)
}
