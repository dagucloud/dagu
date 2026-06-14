// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalStringValueResolutionNamespaces(t *testing.T) {
	t.Parallel()

	outputs := `{"image":"v1.2.3","enabled":true,"count":3,"ratio":1.5,"meta":{"version":"v2"}}`
	ctx := valueResolutionContext(&core.DAG{
		Name:   "value-resolution",
		Consts: valueResolutionConsts(),
		Params: []string{"environment=prod"},
	}, map[string]eval.StepInfo{
		"build": {Outputs: &outputs},
	})

	got, err := runtime.EvalString(ctx, "${consts.service} ${params.environment} ${steps.build.outputs.image} ${steps.build.outputs.meta}")
	require.NoError(t, err)
	assert.Equal(t, `api prod v1.2.3 {"version":"v2"}`, got)

	got, err = runtime.EvalString(ctx, "${consts.i} ${consts.i8} ${consts.i16} ${consts.i32} ${consts.i64} ${consts.u} ${consts.u8} ${consts.u16} ${consts.u32} ${consts.u64} ${consts.f32} ${consts.f64} ${consts.number} ${consts.object}")
	require.NoError(t, err)
	assert.Equal(t, `1 2 3 4 5 6 7 8 9 10 1.25 2.5 3.75 {"kind":"api"}`, got)
}

func TestEvalStringValueResolutionPreservesLegacyAndNoExpansion(t *testing.T) {
	t.Parallel()

	ctx := valueResolutionContext(&core.DAG{
		Name:   "value-resolution",
		Consts: map[string]any{"service": "api"},
	}, nil)
	env := runtime.GetEnv(ctx)
	env.Scope = env.Scope.WithEntries(map[string]string{"LEGACY": "legacy-value"}, eval.EnvSourceStepEnv)
	ctx = runtime.WithEnv(ctx, env)

	got, err := runtime.EvalString(ctx, "${LEGACY} ${unknown.value}")
	require.NoError(t, err)
	assert.Equal(t, "legacy-value ${unknown.value}", got)

	got, err = runtime.EvalString(ctx, "${consts.service}", eval.WithNoExpansion())
	require.NoError(t, err)
	assert.Equal(t, "${consts.service}", got)
}

func TestEvalStringValueResolutionErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		ctx       context.Context
		input     string
		errChecks []string
		want      string
	}{
		{
			name:      "RejectsShorthand",
			ctx:       valueResolutionContext(&core.DAG{Name: "value-resolution"}, nil),
			input:     "$consts.service",
			errChecks: []string{"$consts.service", "invalid Dagu-looking reference"},
		},
		{
			name:      "RejectsMalformedReference",
			ctx:       valueResolutionContext(&core.DAG{Name: "value-resolution", Consts: map[string]any{"service": "api"}}, nil),
			input:     "${consts.service",
			errChecks: []string{"malformed Dagu reference", "${consts.service"},
		},
		{
			name:      "RejectsEmptyReference",
			ctx:       valueResolutionContext(&core.DAG{Name: "value-resolution"}, nil),
			input:     "${}",
			errChecks: []string{"malformed Dagu reference", "${}"},
		},
		{
			name:      "RejectsNamespaceOnlyReference",
			ctx:       valueResolutionContext(&core.DAG{Name: "value-resolution"}, nil),
			input:     "${params}",
			errChecks: []string{"params references", "${params.<name>}"},
		},
		{
			name:      "RejectsMissingConstsDAG",
			ctx:       runtime.WithEnv(context.Background(), runtime.Env{}),
			input:     "${consts.service}",
			errChecks: []string{"unknown consts reference"},
		},
		{
			name:      "RejectsMissingConst",
			ctx:       valueResolutionContext(&core.DAG{Name: "value-resolution", Consts: map[string]any{"service": "api"}}, nil),
			input:     "${consts.missing}",
			errChecks: []string{"unknown consts reference", "missing"},
		},
		{
			name:      "RejectsInvalidConstShape",
			ctx:       valueResolutionContext(&core.DAG{Name: "value-resolution"}, nil),
			input:     "${consts.service.name}",
			errChecks: []string{"consts references", "${consts.<name>}"},
		},
		{
			name:      "RejectsInvalidPathSegment",
			ctx:       valueResolutionContext(&core.DAG{Name: "value-resolution"}, nil),
			input:     "${params.1environment}",
			errChecks: []string{"path segment", "1environment"},
		},
		{
			name:      "RejectsMissingParamWithNilScope",
			ctx:       runtime.WithEnv(context.Background(), runtime.Env{Context: runtime.Context{DAG: &core.DAG{Name: "value-resolution"}}}),
			input:     "${params.environment}",
			errChecks: []string{"unknown params reference", "environment"},
		},
		{
			name:  "ResolvesParamFromScope",
			ctx:   valueResolutionParamScopeContext("environment", "staging"),
			input: "${params.environment}",
			want:  "staging",
		},
		{
			name:      "RejectsParamFromNonParamScope",
			ctx:       valueResolutionDAGEnvScopeContext("environment", "staging"),
			input:     "${params.environment}",
			errChecks: []string{"unknown params reference", "environment"},
		},
		{
			name:      "RejectsMissingStep",
			ctx:       valueResolutionContext(&core.DAG{Name: "value-resolution"}, nil),
			input:     "${steps.build.outputs.image}",
			errChecks: []string{"unknown steps output reference", "step build is unavailable"},
		},
		{
			name:      "RejectsStepWithoutOutputs",
			ctx:       valueResolutionContext(&core.DAG{Name: "value-resolution"}, map[string]eval.StepInfo{"build": {}}),
			input:     "${steps.build.outputs.image}",
			errChecks: []string{"step build has no published outputs"},
		},
		{
			name:      "RejectsInvalidStepOutputsJSON",
			ctx:       valueResolutionContext(&core.DAG{Name: "value-resolution"}, map[string]eval.StepInfo{"build": {Outputs: stringPtr("{")}}),
			input:     "${steps.build.outputs.image}",
			errChecks: []string{"outputs are not a JSON object"},
		},
		{
			name:      "RejectsMissingStepOutput",
			ctx:       valueResolutionContext(&core.DAG{Name: "value-resolution"}, map[string]eval.StepInfo{"build": {Outputs: stringPtr(`{"tag":"v1"}`)}}),
			input:     "${steps.build.outputs.image}",
			errChecks: []string{"output image is unavailable"},
		},
		{
			name:      "RejectsInvalidStepsShape",
			ctx:       valueResolutionContext(&core.DAG{Name: "value-resolution"}, nil),
			input:     "${steps.build.stdout}",
			errChecks: []string{"steps references", "outputs"},
		},
		{
			name:  "FormatsNonFiniteConstAsEmpty",
			ctx:   valueResolutionContext(&core.DAG{Name: "value-resolution", Consts: map[string]any{"bad": math.NaN()}}, nil),
			input: "x${consts.bad}y",
			want:  "xy",
		},
		{
			name:  "FormatsUnmarshalableConstWithFallback",
			ctx:   valueResolutionContext(&core.DAG{Name: "value-resolution", Consts: map[string]any{"ch": make(chan int)}}, nil),
			input: "x${consts.ch}y",
			want:  "x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := runtime.EvalString(tt.ctx, tt.input)
			if len(tt.errChecks) > 0 {
				require.Error(t, err)
				for _, check := range tt.errChecks {
					assert.Contains(t, err.Error(), check)
				}
				return
			}
			require.NoError(t, err)
			if tt.name == "FormatsUnmarshalableConstWithFallback" {
				assert.Contains(t, got, tt.want)
				assert.Contains(t, got, "0x")
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func valueResolutionContext(dag *core.DAG, steps map[string]eval.StepInfo) context.Context {
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "dag.log")
	env := runtime.NewEnv(ctx, core.Step{Name: "test"})
	env.StepMap = steps
	return runtime.WithEnv(ctx, env)
}

func valueResolutionParamScopeContext(name string, value string) context.Context {
	ctx := runtime.NewContext(context.Background(), &core.DAG{Name: "value-resolution"}, "run-1", "dag.log")
	env := runtime.NewEnv(ctx, core.Step{Name: "test"})
	env.Scope = env.Scope.WithEntry(name, value, eval.EnvSourceParam)
	return runtime.WithEnv(ctx, env)
}

func valueResolutionDAGEnvScopeContext(name string, value string) context.Context {
	ctx := runtime.NewContext(context.Background(), &core.DAG{Name: "value-resolution"}, "run-1", "dag.log")
	env := runtime.NewEnv(ctx, core.Step{Name: "test"})
	env.Scope = env.Scope.WithEntry(name, value, eval.EnvSourceDAGEnv)
	return runtime.WithEnv(ctx, env)
}

func valueResolutionConsts() map[string]any {
	return map[string]any{
		"service": "api",
		"i":       int(1),
		"i8":      int8(2),
		"i16":     int16(3),
		"i32":     int32(4),
		"i64":     int64(5),
		"u":       uint(6),
		"u8":      uint8(7),
		"u16":     uint16(8),
		"u32":     uint32(9),
		"u64":     uint64(10),
		"f32":     float32(1.25),
		"f64":     float64(2.5),
		"number":  json.Number("3.75"),
		"object":  map[string]string{"kind": "api"},
	}
}

func stringPtr(value string) *string {
	return &value
}
