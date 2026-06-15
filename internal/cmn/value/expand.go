// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"os"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// ExpandEnvContext expands ${VAR} and $VAR in s using EnvScope from context,
// falling back to os.LookupEnv if no scope in context.
// Variables not found are preserved in their original form.
func ExpandEnvContext(ctx context.Context, s string) string {
	scope := GetEnvScope(ctx)
	if scope == nil {
		return expandWithLookup(s, os.LookupEnv, true)
	}
	return scope.Expand(s)
}

// expandEnvScopeOnly expands $VAR and ${VAR} using only non-OS-sourced
// entries from the EnvScope in context. Unknown variables are preserved.
func expandEnvScopeOnly(ctx context.Context, s string) string {
	scope := GetEnvScope(ctx)
	if scope == nil {
		return s
	}
	return expandWithLookup(s, func(key string) (string, bool) {
		if entry, ok := scope.GetEntry(key); ok && entry.Source != EnvSourceOS {
			return entry.Value, true
		}
		return "", false
	}, true)
}

// shellEnviron implements expand.Environ for POSIX shell expansion.
// Unlike expand.FuncEnviron, it properly distinguishes between
// variables set to empty string and unset variables.
type shellEnviron struct {
	resolver *resolver
}

func (e *shellEnviron) Get(name string) expand.Variable {
	val, ok := e.resolver.resolveForShell(name)
	if !ok {
		return expand.Variable{}
	}
	return expand.Variable{Set: true, Exported: true, Kind: expand.String, Str: val}
}

func (e *shellEnviron) Each(func(name string, vr expand.Variable) bool) {}

// extractPOSIXVarName returns the base variable name from inside a ${...} expression.
// For "VAR:0:3" this returns "VAR"; for "#VAR" (length operator) this returns "VAR".
func extractPOSIXVarName(inner string) string {
	// ${#VAR} is the length operator — # precedes the var name
	if strings.HasPrefix(inner, "#") && !strings.ContainsAny(inner, ":%/+-=?") {
		return inner[1:]
	}
	for i, c := range inner {
		switch c {
		case ':', '-', '+', '=', '?', '#', '%', '/', '.':
			return inner[:i]
		}
	}
	return inner
}

// expandPOSIXExpression expands a single POSIX expression (e.g. "${VAR:0:3}")
// using the mvdan.cc/sh shell parser and expander.
func expandPOSIXExpression(expr string, env *shellEnviron) (string, error) {
	word, err := syntax.NewParser().Document(strings.NewReader(expr))
	if err != nil {
		return expr, nil // preserve malformed expression
	}
	if word == nil {
		return "", nil
	}
	return expand.Literal(&expand.Config{Env: env}, word)
}

// expandWithShellContext performs selective POSIX shell-style variable expansion.
// For each variable expression in the input:
//   - Defined variables with POSIX operators (e.g. ${VAR:0:3}) are expanded via mvdan.cc/sh.
//   - Defined simple variables (${VAR}, $VAR) are resolved directly.
//   - When ExpandOS is false (default): undefined variables are preserved for later OS shell evaluation.
//   - When ExpandOS is true: undefined variables follow POSIX rules (empty, defaults applied, etc.).
//   - Single-quoted variables are preserved as-is.
func expandWithShellContext(ctx context.Context, input string, opts *options) (string, error) {
	if !opts.ExpandShell && !opts.ExpandEnv {
		return input, nil
	}
	if !opts.ExpandShell {
		return ExpandEnvContext(ctx, input), nil
	}

	r := newResolver(ctx, opts)
	env := &shellEnviron{resolver: r}

	matches := reVarSubstitution.FindAllStringSubmatchIndex(input, -1)
	if len(matches) == 0 {
		return input, nil
	}

	var b strings.Builder
	last := 0
	for _, loc := range matches {
		b.WriteString(input[last:loc[0]])
		last = loc[1]
		match := input[loc[0]:loc[1]]

		// Single-quoted: preserve as-is.
		if isSingleQuotedVar(input, loc[0], loc[1]) ||
			(opts.RecognizeEscapedDollar && isEscapedDollar(input, loc[0])) {
			b.WriteString(match)
			continue
		}

		// Extract variable name and detect POSIX operators.
		var varName string
		var hasPOSIXOp bool
		if loc[2] >= 0 { // Group 1: ${...}
			inner := input[loc[2]:loc[3]]
			varName = extractPOSIXVarName(inner)
			hasPOSIXOp = inner != varName
		} else { // Group 2: $VAR
			varName = input[loc[4]:loc[5]]
		}

		val, defined := r.resolveForShell(varName)

		// Undefined without OS expansion: preserve for later shell evaluation.
		if !defined && !opts.ExpandOS {
			b.WriteString(match)
			continue
		}

		// POSIX operator present: expand via shell parser.
		if hasPOSIXOp {
			expanded, err := expandPOSIXExpression(match, env)
			if err != nil {
				return "", err
			}
			b.WriteString(expanded)
		} else {
			b.WriteString(val)
		}
	}
	b.WriteString(input[last:])
	return b.String(), nil
}
