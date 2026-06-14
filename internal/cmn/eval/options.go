// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eval

// Options controls the behavior of string evaluation.
type Options struct {
	ExpandEnv    bool // Enable environment variable expansion
	ExpandShell  bool // Enable shell-based variable expansion (e.g., ${VAR:0:3})
	ExpandOS     bool // Enable os.LookupEnv fallback and OS-sourced scope entries
	Substitute   bool // Enable backtick command substitution
	EscapeDollar bool // Enable \$ → $ escape before variable expansion

	// DeferShellVars skips simple $VAR/${VAR} expansion in the variables
	// phase, deferring to the shell at runtime. JSON path references like
	// ${step.stdout} are still expanded by Dagu since shells cannot handle
	// them. This prevents shell-special characters (backticks, $(), etc.)
	// in variable values from being interpreted when the script executes.
	DeferShellVars bool

	// NoExpansion skips all expansion and returns the input unchanged.
	// Used by executors like template that treat the script body as a
	// literal template, not a shell expression.
	NoExpansion bool

	Variables []map[string]string // Ordered variable maps for expansion
	StepMap   map[string]StepInfo // Step info map for step reference expansion
	Bindings  Scope               // Reserved binding namespace values
}

// NewOptions returns default Options with ExpandEnv, ExpandShell, and
// Substitute enabled. ExpandOS is disabled by default.
func NewOptions() *Options {
	return &Options{
		ExpandEnv:    true,
		ExpandShell:  true,
		Substitute:   true,
		EscapeDollar: true,
	}
}

// Option is a functional option for configuring evaluation.
type Option func(*Options)

// WithVariables adds a variable map for expansion.
func WithVariables(vars map[string]string) Option {
	return func(opts *Options) {
		opts.Variables = append(opts.Variables, vars)
	}
}

// WithStepMap sets the step info map for step reference expansion.
func WithStepMap(stepMap map[string]StepInfo) Option {
	return func(opts *Options) {
		opts.StepMap = stepMap
	}
}

// WithBindingScope sets reserved binding namespace values.
func WithBindingScope(scope Scope) Option {
	return func(opts *Options) {
		opts.Bindings = scope
	}
}

// WithoutExpandEnv disables environment variable expansion.
func WithoutExpandEnv() Option {
	return func(opts *Options) {
		opts.ExpandEnv = false
	}
}

// WithoutExpandShell disables shell-based variable expansion.
func WithoutExpandShell() Option {
	return func(opts *Options) {
		opts.ExpandShell = false
	}
}

// WithoutSubstitute disables backtick command substitution.
func WithoutSubstitute() Option {
	return func(opts *Options) {
		opts.Substitute = false
	}
}

// WithoutDollarEscape preserves backslash-dollar sequences for downstream executors.
func WithoutDollarEscape() Option {
	return func(opts *Options) {
		opts.EscapeDollar = false
	}
}

// WithOSExpansion enables OS environment variable resolution.
// When set, os.LookupEnv is used as a fallback and OS-sourced scope entries
// are included. Without this option, undefined variables are preserved as-is.
func WithOSExpansion() Option {
	return func(opts *Options) {
		opts.ExpandOS = true
	}
}

// WithNoExpansion skips all expansion phases and returns the input unchanged.
// Used by executors that treat the script body as a literal template.
func WithNoExpansion() Option {
	return func(opts *Options) {
		opts.NoExpansion = true
	}
}

// OnlyReplaceVars disables env expansion, command substitution, and simple
// $VAR text substitution. JSON path references like ${step.stdout} are still
// expanded since shells cannot handle them. Simple $VAR references are
// deferred to the shell at runtime via env vars, preventing shell-special
// characters (backticks, $(), etc.) in values from being interpreted.
func OnlyReplaceVars() Option {
	return func(opts *Options) {
		opts.ExpandEnv = false
		opts.Substitute = false
		opts.DeferShellVars = true
	}
}
