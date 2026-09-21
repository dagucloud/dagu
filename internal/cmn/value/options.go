// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

// options controls the behavior of string evaluation.
type options struct {
	ExpandEnv              bool // Enable environment variable expansion
	ExpandShell            bool // Enable shell-based variable expansion (e.g., ${VAR:0:3})
	ExpandOS               bool // Enable os.LookupEnv fallback and OS-sourced scope entries
	Substitute             bool // Enable backtick command substitution
	SubstituteShellCommand bool // Enable $() command substitution
	EscapeDollar           bool // Enable \$ → $ escape before variable expansion
	RecognizeEscapedDollar bool // Treat \$ as a literal-dollar marker during variable expansion

	// DeferShellVars skips simple $VAR/${VAR} expansion in the variables
	// phase, deferring to the shell at runtime. JSON path references like
	// ${step.stdout} are still expanded by Dagu since shells cannot handle
	// them. This prevents shell-special characters (backticks, $(), etc.)
	// in variable values from being interpreted when the script executes.
	DeferShellVars bool

	// QuotedRefStyle selects the escape used to re-quote a resolved "${ref}"
	// value inside a double-quoted span so embedded quotes stay literal for
	// the command interpreter that parses the result.
	QuotedRefStyle quotedRefStyle

	// NoExpansion skips all expansion and returns the input unchanged.
	// Used by executors like template that treat the script body as a
	// literal template, not a shell expression.
	NoExpansion bool

	Variables []map[string]string // Ordered variable maps for expansion
	StepMap   map[string]StepInfo // Step info map for step reference expansion
}

// newOptions returns default options with ExpandEnv, ExpandShell, and
// Substitute enabled. ExpandOS is disabled by default.
func newOptions() *options {
	return &options{
		ExpandEnv:              true,
		ExpandShell:            true,
		Substitute:             true,
		EscapeDollar:           true,
		RecognizeEscapedDollar: true,
	}
}

// quotedRefStyle identifies the escape convention applied when re-quoting a
// resolved "${ref}" value that sits inside a double-quoted span.
type quotedRefStyle int

const (
	// quotedRefPOSIX escapes with strconv.Quote ("\""), which POSIX shells
	// and JSON-style contexts resolve back to a literal quote.
	quotedRefPOSIX quotedRefStyle = iota
	// quotedRefPowerShell escapes with the conventions PowerShell reads
	// inside "...": "" for a literal quote and the backtick for everything
	// else that would otherwise be interpreted.
	quotedRefPowerShell
)

// option is a functional option for configuring evaluation.
type option func(*options)

// withVariables adds a variable map for expansion.
func withVariables(vars map[string]string) option {
	return func(opts *options) {
		opts.Variables = append(opts.Variables, vars)
	}
}

// withStepMap sets the step info map for step reference expansion.
func withStepMap(stepMap map[string]StepInfo) option {
	return func(opts *options) {
		opts.StepMap = stepMap
	}
}

// withoutExpandEnv disables environment variable expansion.
func withoutExpandEnv() option {
	return func(opts *options) {
		opts.ExpandEnv = false
	}
}

// withoutExpandShell disables shell-based variable expansion.
func withoutExpandShell() option {
	return func(opts *options) {
		opts.ExpandShell = false
	}
}

// withoutSubstitute disables backtick command substitution.
func withoutSubstitute() option {
	return func(opts *options) {
		opts.Substitute = false
	}
}

func withoutShellCommandSubstitution() option {
	return func(opts *options) {
		opts.SubstituteShellCommand = false
	}
}

func withShellCommandSubstitution() option {
	return func(opts *options) {
		opts.SubstituteShellCommand = true
	}
}

// withoutDollarEscape preserves backslash-dollar sequences for downstream executors.
func withoutDollarEscape() option {
	return func(opts *options) {
		opts.EscapeDollar = false
	}
}

// withoutEscapedDollarRecognition treats backslash-dollar as ordinary text.
func withoutEscapedDollarRecognition() option {
	return func(opts *options) {
		opts.RecognizeEscapedDollar = false
	}
}

// withOSExpansion enables OS environment variable resolution.
// When set, os.LookupEnv is used as a fallback and OS-sourced scope entries
// are included. Without this option, undefined variables are preserved as-is.
func withOSExpansion() option {
	return func(opts *options) {
		opts.ExpandOS = true
	}
}

// withNoExpansion skips all expansion phases and returns the input unchanged.
// Used by executors that treat the script body as a literal template.
func withNoExpansion() option {
	return func(opts *options) {
		opts.NoExpansion = true
	}
}

// withQuotedRefStyle sets the escape convention used by the quoted-refs phase.
func withQuotedRefStyle(style quotedRefStyle) option {
	return func(opts *options) {
		opts.QuotedRefStyle = style
	}
}

// onlyReplaceVars disables env expansion, command substitution, and simple
// $VAR text substitution. JSON path references like ${step.stdout} are still
// expanded since shells cannot handle them. Simple $VAR references are
// deferred to the shell at runtime via env vars, preventing shell-special
// characters (backticks, $(), etc.) in values from being interpreted.
func onlyReplaceVars() option {
	return func(opts *options) {
		opts.ExpandEnv = false
		opts.Substitute = false
		opts.DeferShellVars = true
	}
}
