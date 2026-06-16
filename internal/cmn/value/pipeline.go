// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"fmt"
	"os"
)

// phase represents a single step in the evaluation pipeline.
type phase struct {
	name    string
	execute func(ctx context.Context, input string, opts *options) (string, error)
	enabled func(opts *options) bool // nil = always run
}

// pipeline is an ordered sequence of evaluation phases.
type pipeline struct {
	phases []phase
}

// execute runs all enabled phases in order on the input string.
func (p *pipeline) execute(ctx context.Context, input string, opts *options) (string, error) {
	if opts.NoExpansion {
		return input, nil
	}
	value := input
	if opts.EscapeDollar {
		ctx, value = withDollarEscapes(ctx, input)
	}
	for _, ph := range p.phases {
		if ph.enabled != nil && !ph.enabled(opts) {
			continue
		}
		var err error
		value, err = ph.execute(ctx, value, opts)
		if err != nil {
			return "", fmt.Errorf("phase %s: %w", ph.name, err)
		}
	}
	return value, nil
}

// defaultPipeline is the standard evaluation pipeline used by evalString().
// Phase order: quoted-refs → variables → substitute → shell-expand
var defaultPipeline = &pipeline{
	phases: []phase{
		{
			name:    "quoted-refs",
			execute: expandQuotedRefs,
		},
		{
			name:    "variables",
			execute: expandAllVariables,
		},
		{
			name:    "substitute",
			execute: substitutePhase,
			enabled: func(opts *options) bool { return opts.Substitute },
		},
		{
			name:    "shell-expand",
			execute: shellExpandPhase,
			enabled: func(opts *options) bool { return opts.ExpandEnv },
		},
		{
			name: "unescape-dollar",
			execute: func(ctx context.Context, input string, _ *options) (string, error) {
				return unescapeDollars(ctx, input), nil
			},
			enabled: func(opts *options) bool { return opts.EscapeDollar },
		},
	},
}

// expandQuotedRefs handles quoted references like "${FOO.bar}" and "${VAR}" within
// double quotes. Resolved values are re-quoted so surrounding JSON stays valid.
func expandQuotedRefs(ctx context.Context, input string, opts *options) (string, error) {
	r := newResolver(ctx, opts)
	return (template{source: input}).resolveQuotedReferences(ctx, r), nil
}

// expandAllVariables resolves JSON path references, step property references,
// and simple $VAR/${VAR} patterns from all variable sources.
func expandAllVariables(ctx context.Context, input string, opts *options) (string, error) {
	return expandVariables(ctx, input, opts), nil
}

// substitutePhase runs backtick command substitution.
func substitutePhase(ctx context.Context, input string, opts *options) (string, error) {
	value, err := substituteCommandsWithContext(ctx, input)
	if err != nil {
		return "", err
	}
	if !opts.SubstituteShellCommand {
		return value, nil
	}
	return substituteShellCommandsWithContext(ctx, value)
}

// regexExpandEnv performs regex-based variable expansion. When ExpandOS is true,
// os.LookupEnv is available as a fallback; otherwise only scoped entries are used.
func regexExpandEnv(ctx context.Context, input string, opts *options) string {
	if opts.ExpandOS {
		scope := GetEnvScope(ctx)
		if scope == nil {
			return expandWithLookup(input, os.LookupEnv, opts.RecognizeEscapedDollar)
		}
		return expandWithLookup(input, scope.Get, opts.RecognizeEscapedDollar)
	}
	scope := GetEnvScope(ctx)
	if scope == nil {
		return input
	}
	return expandWithLookup(input, func(key string) (string, bool) {
		if entry, ok := scope.GetEntry(key); ok && entry.Source != EnvSourceOS {
			return entry.Value, true
		}
		return "", false
	}, opts.RecognizeEscapedDollar)
}

// shellExpandPhase performs shell-style variable expansion.
// When ExpandShell is true (default), uses selective POSIX expansion via mvdan.cc/sh;
// defined variables with POSIX operators are expanded, undefined variables are preserved.
// When ExpandShell is false or POSIX expansion fails, falls back to regex-based expansion.
func shellExpandPhase(ctx context.Context, input string, opts *options) (string, error) {
	if !opts.ExpandShell {
		return regexExpandEnv(ctx, input, opts), nil
	}
	expanded, err := expandWithShellContext(ctx, input, opts)
	if err != nil {
		return regexExpandEnv(ctx, input, opts), nil
	}
	return expanded, nil
}

// evalStringValue applies variable expansion, substitution, and env expansion to a string.
// Used by stringFields and Object for struct/map field processing.
func evalStringValue(ctx context.Context, value string, opts *options) (string, error) {
	if opts.EscapeDollar {
		ctx, value = withDollarEscapes(ctx, value)
	}
	var err error
	value = expandVariables(ctx, value, opts)
	if opts.Substitute {
		value, err = substituteCommandsWithContext(ctx, value)
		if err != nil {
			return "", err
		}
	}
	if opts.ExpandEnv {
		value = regexExpandEnv(ctx, value, opts)
	}
	return unescapeDollars(ctx, value), nil
}
