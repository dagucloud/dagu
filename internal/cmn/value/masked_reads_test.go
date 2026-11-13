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
	steps := map[string]StepInfo{"fetch": {
		Output:          &captured,
		Outputs:         &published,
		DeclaredOutputs: &published,
		Masked:          StepMasks{Output: true, Outputs: []string{"token"}},
	}}
	restored := NewEnvScope(nil, false).
		WithStepOutputs(map[string]string{"TOKEN": "*******", "REGION": "us"}, "fetch", "TOKEN")
	workflow := WorkflowField("run")
	shell := ShellCommandField("run", CommandContext{Target: CommandTargetLocal, Shell: []string{"sh"}, ShellConfigured: true})

	tests := []struct {
		name  string
		raw   string
		field Field
		scope *EnvScope // the env scope; the restored one when nil
		want  string    // the output read; empty when none is
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
		{name: "published output", raw: "deploy ${fetch.outputs.token}", field: workflow, want: "token"},
		{name: "unmasked published output", raw: "deploy ${fetch.outputs.region}", field: workflow},
		{name: "declared output", raw: "deploy ${steps.fetch.outputs.token}", field: workflow, want: "token"},
		{name: "unmasked declared output", raw: "deploy ${steps.fetch.outputs.region}", field: workflow},
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
		{name: "single-quoted braced shell variable", raw: `deploy '${TOKEN}'`, field: shell},
		// Dagu inserts a quoted reference in a single-quoted span itself.
		{name: "quoted reference in single quotes", raw: `curl -d '{"token":"${TOKEN}"}'`, field: shell, want: "TOKEN"},
		{name: "script passed on as written", raw: "console.log(`${TOKEN} ${fetch.output}`)", field: TemplateScriptField("script")},
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
			var read *MaskedReadError
			require.ErrorAs(t, err, &read)
			assert.Equal(t, MaskedReadError{Step: "fetch", Output: tt.want}, *read)
		})
	}
}
