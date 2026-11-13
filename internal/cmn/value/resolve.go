// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
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
	maskedReads            *maskedReads
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
		maskedReads:            maskedReadsFrom(ctx),
	}
}

// lookupVariable searches the explicit variable maps for the given name and
// records the read.
func (r *resolver) lookupVariable(name string) (string, bool) {
	val, ok := r.peekVariable(name)
	if ok {
		r.maskedReads.noteEntry(r.scopeEntry(name, val))
	}
	return val, ok
}

// peekVariable searches the explicit variable maps for the given name
// without recording a read.
func (r *resolver) peekVariable(name string) (string, bool) {
	for _, vars := range r.variables {
		if val, ok := vars[name]; ok {
			return val, true
		}
	}
	return "", false
}

// scopeEntry returns the scope entry that holds value under name, such as
// the one an explicit variable map copied it from, which keeps its masked
// mark.
func (r *resolver) scopeEntry(name, value string) EnvEntry {
	if r.maskedReads == nil || !r.scope.hasMaskedOutput() {
		return EnvEntry{}
	}
	if entry, ok := r.scope.GetEntry(name); ok && entry.Value == value {
		return entry
	}
	return EnvEntry{}
}

// lookupScopeNonOS searches the scope for a non-OS-sourced entry.
func (r *resolver) lookupScopeNonOS(name string) (string, bool) {
	entry, ok := r.lookupScopeNonOSEntry(name)
	if !ok {
		return "", false
	}
	return entry.Value, true
}

// lookupScopeNonOSEntry returns the non-OS-sourced entry for name and
// records it as read.
func (r *resolver) lookupScopeNonOSEntry(name string) (EnvEntry, bool) {
	entry, ok := r.peekScopeNonOSEntry(name)
	if ok {
		r.maskedReads.noteEntry(entry)
	}
	return entry, ok
}

// peekScopeNonOSEntry returns the non-OS-sourced entry for name without
// recording a read.
func (r *resolver) peekScopeNonOSEntry(name string) (EnvEntry, bool) {
	if r.scope == nil {
		return EnvEntry{}, false
	}
	entry, ok := r.scope.GetEntry(name)
	if ok && entry.Source != EnvSourceOS {
		return entry, true
	}
	return EnvEntry{}, false
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
		// A safe home-relative path is inlined so the shell expands its ~.
		// A secret is never written into the command text.
		if entry, ok := r.peekScopeNonOSEntry(name); ok && entry.Source != EnvSourceSecret && isSafeHomeRelativeShellWord(entry.Value) {
			r.maskedReads.noteEntry(entry)
			return entry.Value, true
		}
		return "", false
	}
	return r.lookupScopeNonOS(name)
}

// resolveTemplateName resolves a bare "${NAME}" in a template slot that the
// shell does not expand. When shell variables are deferred, a secret is still
// left to the shell, so its value never enters the command text.
func (r *resolver) resolveTemplateName(name string) (string, bool) {
	if val, ok := r.lookupVariable(name); ok {
		return val, true
	}
	if !r.deferShellVars || isNumericVar(name) {
		return r.lookupScopeNonOS(name)
	}
	entry, ok := r.lookupScopeNonOSEntry(name)
	if !ok || entry.Source == EnvSourceSecret {
		return "", false
	}
	return entry.Value, true
}

// resolveQuotedName resolves a bare "${NAME}" for the quoted-refs phase. When
// shell variables are deferred, the shell owns the name; it does not expand a
// leading ~ inside double quotes, so no value is inlined.
func (r *resolver) resolveQuotedName(name string) (string, bool) {
	if val, ok := r.lookupVariable(name); ok {
		return val, true
	}
	if r.deferShellVars && !isNumericVar(name) {
		return "", false
	}
	return r.lookupScopeNonOS(name)
}

func isSafeHomeRelativeShellWord(value string) bool {
	if value != "~" && !strings.HasPrefix(value, "~/") {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("~/._-+=,:@", r):
		default:
			return false
		}
	}
	return true
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
		return lookupProcessEnv(name)
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
	value, ok := resolveJSONPath(ctx, varName, jsonStr, path)
	// A path reads only the part of the variable it selects, which masking
	// changed only where it holds the mask.
	if ok && strings.Contains(value, masking.DefaultMaskString) {
		r.maskedReads.noteEntry(r.scopeEntry(varName, jsonStr))
	}
	return value, ok
}

// resolveJSONSource looks up a variable's raw value for JSON path resolution.
// Unlike resolve, this includes OS-sourced scope entries when expandOS is true,
// because JSON path resolution needs the actual value regardless of source.
// It records no read: the path decides what the reference reads.
func (r *resolver) resolveJSONSource(name string) (string, bool) {
	if val, ok := r.peekVariable(name); ok {
		return val, true
	}
	if r.expandOS {
		if r.scope != nil {
			if val, ok := r.scope.Get(name); ok {
				return val, true
			}
		}
		return lookupProcessEnv(name)
	}
	entry, ok := r.peekScopeNonOSEntry(name)
	return entry.Value, ok
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
