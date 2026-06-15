// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolverConstLoadResolvesConstsAndRejectsRuntimeBindings(t *testing.T) {
	ctx := context.Background()
	resolver := value.NewResolver(
		value.StaticScope{Consts: value.Values{"service": "api"}},
		value.RuntimeScope{Consts: value.Values{"service": "api"}},
	)

	got, err := resolver.String(ctx, "${consts.service}", value.ConstLoadField("consts.image"))
	require.NoError(t, err)
	assert.Equal(t, "api", got)

	err = resolver.Validate("${params.environment}", value.ConstLoadField("consts.bad"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not available while loading consts")
}

func TestResolverStaticValidationLeavesFutureNamespacesUnresolved(t *testing.T) {
	ctx := context.Background()
	resolver := value.NewResolver(
		value.StaticScope{Consts: value.Values{"service": "api"}},
		value.RuntimeScope{Consts: value.Values{"service": "api"}},
	)

	for _, raw := range []string{
		"${params.name}",
		"${env.NAME}",
		"${steps.build.outputs.image}",
		"$params.name",
		"$env.NAME",
		"$steps.build",
	} {
		require.NoError(t, resolver.Validate(raw, value.StaticValidationField("field")))
		got, err := resolver.String(ctx, raw, value.StaticValidationField("field"))
		require.NoError(t, err)
		assert.Equal(t, raw, got)
	}

	err := resolver.Validate("$consts.service", value.StaticValidationField("field"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid binding shorthand")
}

func TestResolverWorkflowFieldUsesNonOSEnvAndStepRefsOnly(t *testing.T) {
	ctx := context.Background()
	scope := value.NewEnvScope(nil, false).
		WithEntry("SCOPED", "from-scope", value.EnvSourceDAGEnv).
		WithEntry("OS_SCOPED", "from-os-scope", value.EnvSourceOS)
	output := `{"image":"repo/app:1"}`
	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			Env: scope,
			Steps: map[string]value.StepInfo{
				"build": {Stdout: "stdout.txt", Outputs: &output},
			},
		},
	)

	got, err := resolver.String(ctx, "$SCOPED:$OS_SCOPED:${build.stdout}:${build.outputs.image}", value.WorkflowField("steps[0].command"))
	require.NoError(t, err)
	assert.Equal(t, "from-scope:$OS_SCOPED:stdout.txt:repo/app:1", got)
}

func TestResolverDirectCommandFieldUsesDirectOSFallback(t *testing.T) {
	ctx := context.Background()
	t.Setenv("VALUE_RESOLUTION_DIRECT_OS", "from-os")
	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})

	got, err := resolver.String(ctx, "$VALUE_RESOLUTION_DIRECT_OS", value.DirectCommandField("steps[0].command", value.CommandContext{}))
	require.NoError(t, err)
	assert.Equal(t, "from-os", got)
}

func TestResolverCommandScriptFieldDefersScopeVarsAndExpandsStepRefs(t *testing.T) {
	ctx := context.Background()
	scope := value.NewEnvScope(nil, false).WithEntry("SCRIPT_VALUE", "`echo unsafe`", value.EnvSourceDAGEnv)
	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			Env: scope,
			Steps: map[string]value.StepInfo{
				"prep": {Stdout: "ready"},
			},
		},
	)

	got, err := resolver.String(ctx, "echo $SCRIPT_VALUE ${prep.stdout}", value.CommandScriptField("steps[0].run", value.CommandContext{ShellConfigured: true}))
	require.NoError(t, err)
	assert.Equal(t, "echo $SCRIPT_VALUE ready", got)
}

func TestResolverHostConfigObjectUsesScopedEnvWithoutOSFallback(t *testing.T) {
	type config struct {
		Name string
		Home string
	}

	ctx := context.Background()
	t.Setenv("VALUE_RESOLUTION_HOST_OS", "from-os")
	scope := value.NewEnvScope(nil, false).WithEntry("HOST_NAME", "from-scope", value.EnvSourceDAGEnv)
	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{Env: scope})

	gotAny, err := resolver.Object(ctx, config{
		Name: "$HOST_NAME",
		Home: "$VALUE_RESOLUTION_HOST_OS",
	}, value.HostConfigObjectField("smtp"))
	require.NoError(t, err)
	got, ok := gotAny.(config)
	require.True(t, ok)
	assert.Equal(t, "from-scope", got.Name)
	assert.Equal(t, "$VALUE_RESOLUTION_HOST_OS", got.Home)
}

func TestResolverRetryIntegerUsesOSFallback(t *testing.T) {
	ctx := context.Background()
	t.Setenv("VALUE_RESOLUTION_RETRY_LIMIT", "7")
	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})

	got, err := resolver.Int(ctx, "$VALUE_RESOLUTION_RETRY_LIMIT", value.RetryIntegerField("retryPolicy.limit"))
	require.NoError(t, err)
	assert.Equal(t, 7, got)
}

func TestStepOutputReferencesKeepsNarrowGrammar(t *testing.T) {
	refs := value.StepOutputReferences("${build.output.image.tag} $build.output.image ${bad.step.output.x} ${build.outputs.image} ${build.output.9bad}")

	require.Len(t, refs, 1)
	assert.Equal(t, "${build.output.image.tag}", refs[0].Expression)
	assert.Equal(t, "build", refs[0].StepName)
	assert.Equal(t, []string{"image", "tag"}, refs[0].Path)
}
