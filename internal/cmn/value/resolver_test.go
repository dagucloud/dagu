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

func TestResolverConstLoadResolvesConstsAndPreservesRuntimeBindings(t *testing.T) {
	ctx := context.Background()
	resolver := value.NewResolver(
		value.StaticScope{Consts: value.Values{"service": "api"}},
		value.RuntimeScope{Consts: value.Values{"service": "api"}},
	)

	got, err := resolver.String(ctx, "${consts.service}", value.ConstLoadField("consts.image"))
	require.NoError(t, err)
	assert.Equal(t, "api", got)

	err = resolver.Validate("${params.environment}", value.ConstLoadField("consts.bad"))
	require.NoError(t, err)

	got, err = resolver.String(ctx, "${params.environment}", value.ConstLoadField("consts.bad"))
	require.NoError(t, err)
	assert.Equal(t, "${params.environment}", got)
}

func TestResolverUnresolvedStrictReferencesWarnAndPreserve(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(
		value.StaticScope{
			Consts: value.Values{"service": "api"},
			Params: value.Values{"environment": nil},
		},
		value.RuntimeScope{
			Consts: value.Values{"service": "api"},
		},
	)

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "unknown const", raw: "${consts.missing}", want: "${consts.missing}"},
		{name: "missing param value", raw: "${params.environment}", want: "${params.environment}"},
		{name: "missing env value", raw: "${env.RUNTIME_ONLY}", want: "${env.RUNTIME_ONLY}"},
		{name: "missing step output", raw: "${steps.build.outputs.image}", want: "${steps.build.outputs.image}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolver.String(context.Background(), tt.raw, value.WorkflowField("run"))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolverWarningsReportsDedupedStrictMisses(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(
		value.StaticScope{
			Consts: value.Values{"service": "api"},
			Params: value.Values{"environment": nil},
		},
		value.RuntimeScope{
			Consts: value.Values{"service": "api"},
			Env:    value.NewEnvScope(nil, false),
			Steps:  map[string]value.StepInfo{},
		},
	)

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "unknown const", raw: "${consts.missing} ${consts.missing}", want: "${consts.missing}"},
		{name: "missing param value", raw: "${params.environment}", want: "${params.environment}"},
		{name: "missing env value", raw: "${env.RUNTIME_ONLY}", want: "${env.RUNTIME_ONLY}"},
		{name: "missing step output", raw: "${steps.build.outputs.image}", want: "${steps.build.outputs.image}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := resolver.Warnings(tt.raw, value.WorkflowField("steps[0].run"))
			require.Len(t, warnings, 1)
			assert.Contains(t, warnings[0], "steps[0].run")
			assert.Contains(t, warnings[0], tt.want)
			assert.Contains(t, warnings[0], "preserving literal text")
		})
	}
}

func TestResolverWarningsTreatsUnsupportedSyntaxAsOrdinaryContent(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})

	tests := []string{
		"${params...}",
		"$params.name",
		"${steps.build.output.image}",
	}

	for _, raw := range tests {
		warnings := resolver.Warnings(raw, value.WorkflowField("steps[0].run"))
		assert.Empty(t, warnings)
	}
}

func TestResolverStaticValidationResolvesParamsAndLeavesOtherNamespacesUnresolved(t *testing.T) {
	ctx := context.Background()
	output := `{"image":"repo/app:1"}`
	resolver := value.NewResolver(
		value.StaticScope{
			Consts: value.Values{"service": "api"},
			Params: value.Values{"name": nil},
		},
		value.RuntimeScope{
			Consts: value.Values{"service": "api"},
			Params: value.Values{"name": "prod"},
			Steps: map[string]value.StepInfo{
				"build": {Outputs: &output},
			},
		},
	)

	tests := []struct {
		raw  string
		want string
	}{
		{raw: "${params.name}", want: "prod"},
		{raw: "${env.NAME}", want: "${env.NAME}"},
		{raw: "${steps.build.outputs.image}", want: "repo/app:1"},
		{raw: "$params.name", want: "$params.name"},
		{raw: "$env.NAME", want: "$env.NAME"},
		{raw: "$steps.build", want: "$steps.build"},
		{raw: "$consts.service", want: "$consts.service"},
	}

	for _, tt := range tests {
		require.NoError(t, resolver.Validate(tt.raw, value.StaticValidationField("field")))
		got, err := resolver.String(ctx, tt.raw, value.StaticValidationField("field"))
		require.NoError(t, err)
		assert.Equal(t, tt.want, got)
	}

	err := resolver.Validate("${params.missing}", value.StaticValidationField("field"))
	require.NoError(t, err)
}

func TestResolverParamDeclarationsAreNotRuntimeValues(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(
		value.StaticScope{Params: value.Values{"name": nil}},
		value.RuntimeScope{},
	)

	got, err := resolver.String(context.Background(), "${params.name}", value.StepDirField("working_dir"))

	require.NoError(t, err)
	assert.Equal(t, "${params.name}", got)
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

	got, err := resolver.String(ctx, "$SCOPED:$OS_SCOPED:${build.stdout}:${build.outputs.image}:${steps.build.outputs.image}", value.WorkflowField("steps[0].command"))
	require.NoError(t, err)
	assert.Equal(t, "from-scope:$OS_SCOPED:stdout.txt:repo/app:1:repo/app:1", got)
}

func TestResolverDirectCommandFieldUsesDirectOSFallback(t *testing.T) {
	ctx := context.Background()
	t.Setenv("VALUE_RESOLUTION_DIRECT_OS", "from-os")
	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})

	got, err := resolver.String(ctx, "$VALUE_RESOLUTION_DIRECT_OS", value.DirectCommandField("steps[0].command", value.CommandContext{}))
	require.NoError(t, err)
	assert.Equal(t, "from-os", got)
}

func TestResolverDockerDirectCommandFieldPreservesTargetEnv(t *testing.T) {
	ctx := context.Background()
	t.Setenv("VALUE_RESOLUTION_DOCKER_TARGET", "from-host")
	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})

	got, err := resolver.String(
		ctx,
		"$VALUE_RESOLUTION_DOCKER_TARGET",
		value.DirectCommandField("container.command[0]", value.CommandContext{Target: value.CommandTargetDocker}),
	)
	require.NoError(t, err)
	assert.Equal(t, "$VALUE_RESOLUTION_DOCKER_TARGET", got)
}

func TestResolverSSHCommandFieldsPreserveRemoteEnv(t *testing.T) {
	ctx := context.Background()
	t.Setenv("VALUE_RESOLUTION_SSH_REMOTE", "from-host")
	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})
	command := value.CommandContext{Target: value.CommandTargetSSH}

	for _, tt := range []struct {
		name  string
		field value.Field
	}{
		{name: "direct command", field: value.DirectCommandField("steps[0].command", command)},
		{name: "shell command", field: value.ShellCommandField("steps[0].run", command)},
		{name: "command script", field: value.CommandScriptField("steps[0].run", command)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolver.String(ctx, "$VALUE_RESOLUTION_SSH_REMOTE", tt.field)
			require.NoError(t, err)
			assert.Equal(t, "$VALUE_RESOLUTION_SSH_REMOTE", got)
		})
	}
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

func TestResolverShellCommandFieldDefersScopeVarsAndExpandsStepRefs(t *testing.T) {
	ctx := context.Background()
	scope := value.NewEnvScope(nil, false).WithEntry("ROOT_VALUE", "${params.environment}", value.EnvSourceDAGEnv)
	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			Env: scope,
			Steps: map[string]value.StepInfo{
				"prep": {Stdout: "ready"},
			},
		},
	)

	got, err := resolver.String(ctx, `printf '%s\n' "$ROOT_VALUE" ${prep.stdout}`, value.ShellCommandField("steps[0].run", value.CommandContext{
		Target:          value.CommandTargetLocal,
		Shell:           []string{"/bin/sh"},
		ShellConfigured: true,
	}))

	require.NoError(t, err)
	assert.Equal(t, `printf '%s\n' "$ROOT_VALUE" ready`, got)
}

func TestResolverPowerShellCommandFieldExpandsDaguScopeVars(t *testing.T) {
	ctx := context.Background()
	scope := value.NewEnvScope(nil, false).
		WithEntry("TEXT", "hello", value.EnvSourceDAGEnv).
		WithEntry("DEV_PCENT", "90", value.EnvSourceDAGEnv)
	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{Env: scope})
	command := value.CommandContext{
		Target:          value.CommandTargetLocal,
		Shell:           []string{"powershell"},
		ShellConfigured: true,
	}

	got, err := resolver.String(
		ctx,
		`echo "action says ${TEXT}"; if ($env:DEV_PCENT) { exit 0 }`,
		value.ShellCommandField("steps[0].run", command),
	)

	require.NoError(t, err)
	assert.Equal(t, `echo "action says hello"; if ($env:DEV_PCENT) { exit 0 }`, got)
}

func TestResolverDotenvPathPreservesMissingParamReference(t *testing.T) {
	ctx := context.Background()
	resolver := value.NewResolver(
		value.StaticScope{
			Params: value.Values{"environment": ""},
		},
		value.RuntimeScope{},
	)

	got, err := resolver.String(ctx, ".env.${params.environment}", value.DotenvPathField("dotenv[0]"))

	require.NoError(t, err)
	assert.Equal(t, ".env.${params.environment}", got)
}

func TestResolverStepArtifactOutputExpandsVarAfterWindowsSeparator(t *testing.T) {
	ctx := context.Background()
	scope := value.NewEnvScope(nil, false).
		WithEntry("LOG_NAME", "prepared-output", value.EnvSourceStepEnv)
	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{Env: scope})

	got, err := resolver.String(ctx, `C:\tmp\${LOG_NAME}.log`, value.StepArtifactOutputField("stdout"))

	require.NoError(t, err)
	assert.Equal(t, `C:\tmp\prepared-output.log`, got)
}

func TestResolverPowerShellCommandFieldPreservesEnvMemberAccess(t *testing.T) {
	ctx := context.Background()
	scope := value.NewEnvScope(nil, false).
		WithEntry("DEV_PCENT", "90", value.EnvSourceDAGEnv).
		WithEntry("DEV_ALERT", "80", value.EnvSourceDAGEnv)
	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{Env: scope})
	command := value.CommandContext{
		Target:          value.CommandTargetLocal,
		Shell:           []string{"powershell"},
		ShellConfigured: true,
	}

	got, err := resolver.String(
		ctx,
		"if ([int]($env:DEV_PCENT) -ge [int]($env:DEV_ALERT)) { exit 0 } else { exit 1 }",
		value.ConditionCommandField("preconditions[0].condition", command),
	)

	require.NoError(t, err)
	assert.Equal(t, "if ([int]($env:DEV_PCENT) -ge [int]($env:DEV_ALERT)) { exit 0 } else { exit 1 }", got)
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

func TestResolverDynamicParamEvalDoesNotSubstituteOSFallbackValue(t *testing.T) {
	ctx := context.Background()
	t.Setenv("VALUE_RESOLUTION_DYNAMIC_OS", "`printf unsafe`")
	scope := value.NewEnvScope(nil, true)
	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{Env: scope})

	got, err := resolver.String(ctx, "$VALUE_RESOLUTION_DYNAMIC_OS", value.DynamicParamEvalField("params"))
	require.NoError(t, err)
	assert.Equal(t, "`printf unsafe`", got)
}

func TestResolverEnvEntryFieldsRunCommandSubstitution(t *testing.T) {
	ctx := context.Background()
	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})

	stepValue, err := resolver.String(ctx, "`printf step`", value.StepEnvField("steps[0].env[0]"))
	require.NoError(t, err)
	assert.Equal(t, "step", stepValue)

	containerValue, err := resolver.String(ctx, "`printf container`", value.ContainerEnvField("container.env.VALUE"))
	require.NoError(t, err)
	assert.Equal(t, "container", containerValue)
}

func TestResolverDAGEnvAndRuntimeDAGEnvHaveDistinctSubstitutionPolicy(t *testing.T) {
	ctx := context.Background()
	t.Setenv("VALUE_RESOLUTION_DAG_ENV_OS", "from-os")
	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})

	dagValue, err := resolver.String(ctx, "`printf dag`", value.DAGEnvField("env.VALUE"))
	require.NoError(t, err)
	assert.Equal(t, "dag", dagValue)

	dagOSValue, err := resolver.String(ctx, "$VALUE_RESOLUTION_DAG_ENV_OS", value.DAGEnvField("env.VALUE"))
	require.NoError(t, err)
	assert.Equal(t, "from-os", dagOSValue)

	runtimeValue, err := resolver.String(ctx, "`printf runtime`", value.RuntimeDAGEnvField("env.VALUE"))
	require.NoError(t, err)
	assert.Equal(t, "`printf runtime`", runtimeValue)

	runtimeOSValue, err := resolver.String(ctx, "$VALUE_RESOLUTION_DAG_ENV_OS", value.RuntimeDAGEnvField("env.VALUE"))
	require.NoError(t, err)
	assert.Equal(t, "$VALUE_RESOLUTION_DAG_ENV_OS", runtimeOSValue)
}

func TestStepOutputReferencesKeepsNarrowGrammar(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantExpr string
		wantStep string
		wantPath []string
	}{
		{
			name:     "braced dotted output path",
			raw:      "${build.output.image.tag}",
			wantExpr: "${build.output.image.tag}",
			wantStep: "build",
			wantPath: []string{"image", "tag"},
		},
		{
			name:     "hyphenated step name",
			raw:      "${build-step.output.image}",
			wantExpr: "${build-step.output.image}",
			wantStep: "build-step",
			wantPath: []string{"image"},
		},
		{
			name: "unbraced output reference ignored",
			raw:  "$build.output.image",
		},
		{
			name:     "strict plural outputs reference",
			raw:      "${steps.build.outputs.image}",
			wantExpr: "${steps.build.outputs.image}",
			wantStep: "build",
			wantPath: []string{"image"},
		},
		{
			name:     "strict plural outputs reference with output step id",
			raw:      "${steps.output.outputs.image}",
			wantExpr: "${steps.output.outputs.image}",
			wantStep: "output",
			wantPath: []string{"image"},
		},
		{
			name: "plural outputs reference without steps prefix ignored",
			raw:  "${build.outputs.image}",
		},
		{
			name: "invalid output path segment ignored",
			raw:  "${build.output.9bad}",
		},
		{
			name: "array-like output path ignored",
			raw:  "${build.output.items[0]}",
		},
		{
			name: "extra prefix does not become step output",
			raw:  "${bad.step.output.x}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := value.StepOutputReferences(tt.raw)
			if tt.wantExpr == "" {
				require.Empty(t, refs)
				return
			}
			require.Len(t, refs, 1)
			assert.Equal(t, tt.wantExpr, refs[0].Expression)
			assert.Equal(t, tt.wantStep, refs[0].StepName)
			assert.Equal(t, tt.wantPath, refs[0].Path)
		})
	}
}
