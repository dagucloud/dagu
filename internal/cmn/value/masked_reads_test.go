// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A retry restores outputs that were stored with a secret masked. Resolving a
// value that inserts one of them is a read; a name Dagu leaves to a POSIX shell
// is not.
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
			Masked:          StepMasks{Output: true, Outputs: true, Declared: []string{"token"}},
		},
		"config": {Output: &config, Masked: StepMasks{Output: true}},
	}
	restored := NewEnvScope(nil, false).
		WithStepOutputs(map[string]string{
			"TOKEN":  "*******",
			"REGION": "us",
			"CONFIG": `{"token":"*******","region":"us"}`,
		}, "fetch", "TOKEN", "CONFIG")
	workflow := WorkflowField("run")
	shell := ShellCommandField("run", CommandContext{Target: CommandTargetLocal, Shell: []string{"sh"}, ShellConfigured: true})

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
		// Any path into a masked stored output reads it, whatever part it
		// selects.
		{name: "captured output", raw: "deploy ${fetch.output}", field: workflow, want: "output"},
		{name: "slice of a captured output", raw: "deploy ${fetch.output:0:3}", field: workflow, want: "output"},
		{name: "field of a captured object", raw: "deploy ${config.output.region}", field: workflow, want: "output", step: "config"},
		{name: "published outputs", raw: "deploy ${fetch.outputs.token}", field: workflow, want: "outputs"},
		{name: "other published output", raw: `deploy ${fetch.outputs["region"]}`, field: workflow, want: "outputs"},
		// A declared output is read by its exact name.
		{name: "declared output", raw: "deploy ${steps.fetch.outputs.token}", field: workflow, want: "token"},
		{name: "unmasked declared output", raw: "deploy ${steps.fetch.outputs.region}", field: workflow},
		// Executor config reads variables from a copy of the scope.
		{name: "executor config variable", raw: "token=${TOKEN}", field: ExecutorConfigField("with.message"), want: "TOKEN"},
		{name: "executor config unmasked variable", raw: "region=${REGION}", field: ExecutorConfigField("with.message")},
		{name: "path into a masked variable", raw: "deploy ${CONFIG.region}", field: workflow, want: "CONFIG"},
		// A POSIX shell expands these itself, like any program reading its
		// environment, so Dagu sees no read.
		{name: "shell variable", raw: `deploy "$TOKEN"`, field: shell},
		{name: "braced shell variable", raw: `deploy "${TOKEN}"`, field: shell},
		// Dagu inserts these into the command itself.
		{name: "env namespace in a shell command", raw: `deploy "${env.TOKEN}"`, field: shell, want: "TOKEN"},
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

	// A command substitution does not run on the value.
	ran := filepath.Join(t.TempDir(), "ran")
	_, err = resolver.String(ctx, "`echo ${TOKEN} > "+ran+"`", ConditionEvalField("preconditions"))
	require.ErrorAs(t, err, &read)
	assert.NoFileExists(t, ran)
}
