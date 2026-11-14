// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A retry restores outputs that were stored with a secret masked. Resolving a
// value that inserts one of them, or that a shell expands from one, is a read.
func TestMaskedRead(t *testing.T) {
	t.Parallel()

	captured := "*******"
	published := `{"token":"*******","region":"us"}`
	config := `{"token":"*******","region":"us"}`
	steps := map[string]StepInfo{
		"fetch": {
			Output:          &captured,
			Outputs:         &published,
			DeclaredOutputs: &published,
			Masked:          StepMasks{AllOutput: true, Outputs: []string{"token"}},
		},
		"config": {Output: &config, Masked: StepMasks{Output: []string{"token"}}},
	}
	restored := NewEnvScope(nil, false).
		WithStepOutputs(map[string]string{
			"TOKEN":  "*******",
			"REGION": "us",
			"CONFIG": `{"token":"*******","region":"us"}`,
			"PIN":    `{"pin":*******}`,
		}, "fetch", "TOKEN", "CONFIG", "PIN")
	workflow := WorkflowField("run")
	shell := ShellCommandField("run", CommandContext{Target: CommandTargetLocal, Shell: []string{"sh"}, ShellConfigured: true})
	script := CommandScriptField("script", CommandContext{Target: CommandTargetLocal, Shell: []string{"sh"}, ShellConfigured: true})

	tests := []struct {
		name  string
		raw   string
		field Field
		scope *EnvScope // the env scope; the restored one when nil
		want  string    // the output read; empty when none is
		step  string    // the step the output belongs to; fetch when empty
	}{
		{name: "variable", raw: "deploy ${TOKEN}", field: workflow, want: "TOKEN"},
		{name: "unbraced variable", raw: "deploy $TOKEN", field: workflow, want: "TOKEN"},
		{name: "env namespace", raw: "deploy ${env.TOKEN}", field: workflow, want: "TOKEN"},
		{name: "escaped variable", raw: `deploy \${TOKEN}`, field: workflow},
		{name: "unmasked variable", raw: "deploy ${REGION}", field: workflow},
		{
			name:  "variable a later entry shadows",
			raw:   "deploy ${TOKEN}",
			field: workflow,
			scope: restored.WithEntry("TOKEN", "local", EnvSourceStepEnv),
		},
		{name: "captured output", raw: "deploy ${fetch.output}", field: workflow, want: "output"},
		{name: "masked field of a captured object", raw: "deploy ${config.output.token}", field: workflow, want: "token", step: "config"},
		{name: "unmasked field of a captured object", raw: "deploy ${config.output.region}", field: workflow},
		{name: "published output", raw: "deploy ${fetch.outputs.token}", field: workflow, want: "token"},
		{name: "unmasked published output", raw: "deploy ${fetch.outputs.region}", field: workflow},
		{name: "declared output", raw: "deploy ${steps.fetch.outputs.token}", field: workflow, want: "token"},
		{name: "unmasked declared output", raw: "deploy ${steps.fetch.outputs.region}", field: workflow},
		// Executor config reads variables from a copy of the scope.
		{name: "executor config variable", raw: "token=${TOKEN}", field: ExecutorConfigField("with.message"), want: "TOKEN"},
		{name: "executor config unmasked variable", raw: "region=${REGION}", field: ExecutorConfigField("with.message")},
		{name: "path into a masked variable", raw: "deploy ${CONFIG.token}", field: workflow, want: "CONFIG"},
		{name: "unmasked path into a masked variable", raw: "deploy ${CONFIG.region}", field: workflow},
		// Masking a number leaves the text no longer JSON.
		{name: "path into masked text that is no longer JSON", raw: "deploy ${PIN.pin}", field: workflow, want: "PIN"},
		{name: "missing path into a masked variable", raw: "deploy ${CONFIG.missing}", field: workflow},
		// A POSIX shell expands these itself from its environment.
		{name: "shell variable", raw: `deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "shell parameter expansion", raw: `deploy "${TOKEN:-none}"`, field: shell, want: "TOKEN"},
		{name: "single-quoted shell variable", raw: `deploy '$TOKEN'`, field: shell},
		{name: "escaped shell variable", raw: `deploy \$TOKEN`, field: shell},
		{name: "shell comment", raw: "# $TOKEN is not needed\ndeploy", field: shell},
		{name: "shell variable after the script sets it", raw: `TOKEN=$(echo local); deploy "$TOKEN"`, field: shell},
		{name: "shell variable the script sets from itself", raw: `TOKEN="$TOKEN-x"; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "shell variable before the script sets it", raw: `deploy "$TOKEN"; TOKEN=local`, field: shell, want: "TOKEN"},
		{name: "shell variable for a command only", raw: `TOKEN=local deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "shell loop variable", raw: `for TOKEN in a b; do deploy "$TOKEN"; done`, field: shell},
		{name: "shell variable read from input", raw: `read -r TOKEN < file; deploy "$TOKEN"`, field: shell},
		{name: "quoted heredoc", raw: "cat <<'EOF'\n$TOKEN\nEOF", field: shell},
		{name: "heredoc", raw: "cat <<EOF\n$TOKEN\nEOF", field: shell, want: "TOKEN"},
		{name: "apostrophe in a comment", raw: "# don't log it\ndeploy \"$TOKEN\"", field: shell, want: "TOKEN"},
		{name: "shell arithmetic", raw: `deploy $((TOKEN + 1))`, field: shell, want: "TOKEN"},
		{name: "shell append", raw: `TOKEN+=x; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "shell variable after unset", raw: `unset TOKEN; deploy "${TOKEN:-none}"`, field: shell},
		{name: "shell variable printf sets", raw: `printf -v TOKEN '%s' local; deploy "$TOKEN"`, field: shell},
		{name: "function called after the script sets it", raw: `f() { deploy "$TOKEN"; }; TOKEN=local; f`, field: shell},
		{name: "function called before the script sets it", raw: `f() { deploy "$TOKEN"; }; f; TOKEN=local`, field: shell, want: "TOKEN"},
		// trap, command, or eval can run a function no call names.
		{name: "function no call names", raw: `f() { deploy "$TOKEN"; }`, field: shell, want: "TOKEN"},
		{name: "function a later call runs", raw: `show() { deploy "$TOKEN"; }; main() { show; }; TOKEN=local; main`, field: shell},
		{name: "function called after its caller sets it", raw: `show() { deploy "$TOKEN"; }; main() { TOKEN=local; show; }; main`, field: shell},
		{name: "function called before its caller sets it", raw: `show() { deploy "$TOKEN"; }; main() { show; TOKEN=local; }; main`, field: shell, want: "TOKEN"},
		{name: "recursive function", raw: `f() { deploy "$TOKEN"; f; }; f`, field: shell, want: "TOKEN"},
		{name: "after a function a branch calls", raw: `f() { TOKEN=x; }; if c; then f; fi; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "after a function a subshell calls", raw: `f() { TOKEN=x; }; (f); deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "function a trap runs", raw: `notify() { deploy "$TOKEN"; }; trap notify EXIT`, field: shell, want: "TOKEN"},
		{name: "function a trap runs after the script sets it", raw: `notify() { deploy "$TOKEN"; }; trap notify EXIT; TOKEN=local`, field: shell},
		{name: "after a loop that may not run", raw: `for TOKEN in $EMPTY; do :; done; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "local outside a function", raw: `local TOKEN=x; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "declare without a value", raw: `declare TOKEN; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "export without a value", raw: `export TOKEN; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		// dash keeps the inherited value for a local without one.
		{name: "local without a value", raw: `f() { local TOKEN; deploy "$TOKEN"; }; f`, field: shell, want: "TOKEN"},
		{name: "after a background job sets it", raw: `TOKEN=x & wait; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "after a function a background job runs", raw: `f() { TOKEN=x; }; f & wait; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "after a coprocess sets it", raw: `coproc { TOKEN=x; }; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "after a redefined function", raw: `f() { TOKEN=x; }; f() { :; }; f; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "function local", raw: `f() { local TOKEN=x; deploy "$TOKEN"; }; f`, field: shell},
		{name: "after a function local", raw: `f() { local TOKEN=x; }; f; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "after a function sets it", raw: `f() { TOKEN=x; }; f; deploy "$TOKEN"`, field: shell},
		{name: "after a subshell sets it", raw: `(TOKEN=x); deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "inside the subshell that sets it", raw: `(TOKEN=x; deploy "$TOKEN")`, field: shell},
		{name: "after a command substitution sets it", raw: `x=$(TOKEN=x; echo); deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "after a branch sets it", raw: `if false; then TOKEN=x; fi; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "inside the branch that sets it", raw: `if true; then TOKEN=x; deploy "$TOKEN"; fi`, field: shell},
		{name: "after every branch sets it", raw: `if c; then TOKEN=a; elif d; then TOKEN=b; else TOKEN=c; fi; deploy "$TOKEN"`, field: shell},
		{name: "after an elif without else sets it", raw: `if c; then TOKEN=a; elif d; then TOKEN=b; fi; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "after an && sets it", raw: `false && TOKEN=x; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "after a pipeline reads it", raw: `echo x | read TOKEN; deploy "$TOKEN"`, field: shell, want: "TOKEN"},
		{name: "after mapfile sets it", raw: `mapfile -t TOKEN < file; deploy "${TOKEN[0]}"`, field: shell},
		{name: "after getopts sets it", raw: `getopts t TOKEN; deploy "$TOKEN"`, field: shell},
		{name: "single-quoted braced shell variable", raw: `deploy '${TOKEN}'`, field: shell},
		// Dagu inserts a quoted reference in a single-quoted span itself.
		{name: "quoted reference in single quotes", raw: `curl -d '{"token":"${TOKEN}"}'`, field: shell, want: "TOKEN"},
		{name: "script passed on as written", raw: "console.log(`${TOKEN} ${fetch.output}`)", field: TemplateScriptField("script")},
		{name: "shell script", raw: "#!/bin/bash\ndeploy \"$TOKEN\"", field: script, want: "TOKEN"},
		{name: "shell script through env", raw: "#!/usr/bin/env -S bash -e\ndeploy \"$TOKEN\"", field: script, want: "TOKEN"},
		// Another interpreter's variables are its own.
		{name: "perl script", raw: "#!/usr/bin/perl\nmy $TOKEN = 'local'; print $TOKEN;", field: script},
		{name: "python script through env", raw: "#!/usr/bin/env python3\nprint('$TOKEN')", field: script},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scope := tt.scope
			if scope == nil {
				scope = restored
			}
			ctx := TrackMaskedReads(WithEnvScope(context.Background(), scope))
			resolver := NewResolver(StaticScope{}, RuntimeScope{Env: scope, Steps: steps})
			_, err := resolver.String(ctx, tt.raw, tt.field)
			require.NoError(t, err)

			err = MaskedRead(ctx)
			if tt.want == "" {
				require.NoError(t, err)
				return
			}
			step := tt.step
			if step == "" {
				step = "fetch"
			}
			var read *MaskedReadError
			require.ErrorAs(t, err, &read)
			assert.Equal(t, MaskedReadError{Step: step, Output: tt.want}, *read)
		})
	}
}

// Once a step starts, resolution fails at a masked read, so an executor that
// resolves a field while it runs stops before it uses the value.
func TestRejectMaskedReads(t *testing.T) {
	t.Parallel()

	scope := NewEnvScope(nil, false).
		WithStepOutputs(map[string]string{"TOKEN": "*******", "REGION": "us"}, "fetch", "TOKEN")
	ctx := TrackMaskedReads(WithEnvScope(context.Background(), scope))
	resolver := NewResolver(StaticScope{}, RuntimeScope{Env: scope})
	RejectMaskedReads(ctx)

	got, err := resolver.String(ctx, "${REGION}", WorkflowField("items"))
	require.NoError(t, err)
	assert.Equal(t, "us", got)

	_, err = resolver.String(ctx, "${TOKEN}", WorkflowField("items"))
	var read *MaskedReadError
	require.ErrorAs(t, err, &read)
	assert.Equal(t, MaskedReadError{Step: "fetch", Output: "TOKEN"}, *read)
}
