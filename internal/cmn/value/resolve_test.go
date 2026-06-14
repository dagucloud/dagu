// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- resolveForShell coverage ---

func TestResolveForShell_FromScope(t *testing.T) {
	scope := NewEnvScope(nil, false)
	scope = scope.WithEntry("SCOPEVAR", "scopeval", EnvSourceDAGEnv)
	r := &resolver{scope: scope}

	val, ok := r.resolveForShell("SCOPEVAR")
	require.True(t, ok)
	require.Equal(t, "scopeval", val)
}

func TestResolveForShell_SkipsOSScope(t *testing.T) {
	t.Setenv("OSVAR", "live_os_value")
	scope := NewEnvScope(nil, false)
	scope = scope.WithEntry("OSVAR", "frozen_value", EnvSourceOS)
	r := &resolver{scope: scope, expandOS: true}

	val, ok := r.resolveForShell("OSVAR")
	require.True(t, ok)
	require.Equal(t, "live_os_value", val)
}

func TestResolveForShell_OSEnvFallback(t *testing.T) {
	t.Setenv("TESTOSVAR", "osval")
	r := &resolver{expandOS: true}

	val, ok := r.resolveForShell("TESTOSVAR")
	require.True(t, ok)
	require.Equal(t, "osval", val)
}

func TestResolveForShell_NotFound(t *testing.T) {
	r := &resolver{}
	_, ok := r.resolveForShell("DEFINITELY_NOT_SET_EVER_12345")
	assert.False(t, ok)
}

// --- resolveJSONSource coverage ---

func TestResolveJSONSource_FromScope(t *testing.T) {
	scope := NewEnvScope(nil, false)
	scope = scope.WithEntry("JSONVAR", `{"a":1}`, EnvSourceDAGEnv)
	r := &resolver{scope: scope}

	val, ok := r.resolveJSONSource("JSONVAR")
	require.True(t, ok)
	require.Equal(t, `{"a":1}`, val)
}

func TestResolveJSONSource_FromScopeWithExpandOS(t *testing.T) {
	scope := NewEnvScope(nil, false)
	scope = scope.WithEntry("JSONVAR", `{"a":1}`, EnvSourceDAGEnv)
	r := &resolver{scope: scope, expandOS: true}

	val, ok := r.resolveJSONSource("JSONVAR")
	require.True(t, ok)
	require.Equal(t, `{"a":1}`, val)
}

func TestResolveJSONSource_FromOSEnv(t *testing.T) {
	t.Setenv("JSONOSVAR", `{"b":2}`)
	r := &resolver{expandOS: true}

	val, ok := r.resolveJSONSource("JSONOSVAR")
	require.True(t, ok)
	require.Equal(t, `{"b":2}`, val)
}

func TestResolveJSONSource_NotFound(t *testing.T) {
	r := &resolver{}
	_, ok := r.resolveJSONSource("NOPE_NOT_HERE_12345")
	assert.False(t, ok)
}

// --- resolver.resolve from scope ---

func TestResolver_Resolve_FromScope(t *testing.T) {
	scope := NewEnvScope(nil, false)
	scope = scope.WithEntry("SCOPEVAR", "val", EnvSourceDAGEnv)
	r := &resolver{scope: scope}

	val, ok := r.resolve("SCOPEVAR")
	require.True(t, ok)
	require.Equal(t, "val", val)
}

func TestResolver_Resolve_SkipsOSSource(t *testing.T) {
	scope := NewEnvScope(nil, false)
	scope = scope.WithEntry("OSONLY", "frozen", EnvSourceOS)
	r := &resolver{scope: scope}

	_, ok := r.resolve("OSONLY")
	assert.False(t, ok)
}

// --- resolveReference coverage ---

func TestResolveReference_JSONFromScope(t *testing.T) {
	scope := NewEnvScope(nil, false)
	scope = scope.WithEntry("JDATA", `{"x":"y"}`, EnvSourceDAGEnv)
	r := &resolver{scope: scope}

	val, ok := r.resolveReference(context.Background(), "JDATA", ".x")
	require.True(t, ok)
	require.Equal(t, "y", val)
}

func TestResolveReference_JSONFromOSEnv(t *testing.T) {
	t.Setenv("OSJSON", `{"a":"b"}`)
	r := &resolver{expandOS: true}

	val, ok := r.resolveReference(context.Background(), "OSJSON", ".a")
	require.True(t, ok)
	require.Equal(t, "b", val)
}

func TestResolveReference_NotFound(t *testing.T) {
	r := &resolver{}
	_, ok := r.resolveReference(context.Background(), "NOPE12345", ".x")
	assert.False(t, ok)
}

// --- ExpandOS resolver tests ---

func TestResolveForShell_WithoutExpandOS(t *testing.T) {
	t.Setenv("SHELL_TEST_VAR", "os_value")
	r := &resolver{expandOS: false}

	_, ok := r.resolveForShell("SHELL_TEST_VAR")
	assert.False(t, ok, "should not find OS var when expandOS=false")
}

func TestResolveForShell_WithExpandOS(t *testing.T) {
	t.Setenv("SHELL_TEST_VAR", "os_value")
	r := &resolver{expandOS: true}

	val, ok := r.resolveForShell("SHELL_TEST_VAR")
	require.True(t, ok)
	require.Equal(t, "os_value", val)
}

func TestResolveJSONSource_WithoutExpandOS(t *testing.T) {
	t.Setenv("JSON_OS_VAR", `{"a":1}`)

	// OS env should not be found
	r := &resolver{expandOS: false}
	_, ok := r.resolveJSONSource("JSON_OS_VAR")
	assert.False(t, ok)

	// OS-sourced scope entries should not be found
	scope := NewEnvScope(nil, false).
		WithEntry("SCOPE_OS", `{"b":2}`, EnvSourceOS)
	r2 := &resolver{scope: scope, expandOS: false}
	_, ok = r2.resolveJSONSource("SCOPE_OS")
	assert.False(t, ok)

	// Non-OS scope entries should still be found
	scope2 := NewEnvScope(nil, false).
		WithEntry("SCOPE_DAG", `{"c":3}`, EnvSourceDAGEnv)
	r3 := &resolver{scope: scope2, expandOS: false}
	val, ok := r3.resolveJSONSource("SCOPE_DAG")
	require.True(t, ok)
	require.Equal(t, `{"c":3}`, val)
}

// --- resolveForReplace coverage ---

func TestResolveForReplace_ExplicitVarsAlwaysExpanded(t *testing.T) {
	r := &resolver{
		variables:      []map[string]string{{"FOO": "bar"}},
		deferShellVars: true,
	}
	val, ok := r.resolveForReplace("FOO")
	require.True(t, ok)
	require.Equal(t, "bar", val)
}

func TestResolveForReplace_ExplicitVarsWithoutDefer(t *testing.T) {
	r := &resolver{
		variables:      []map[string]string{{"FOO": "bar"}},
		deferShellVars: false,
	}
	val, ok := r.resolveForReplace("FOO")
	require.True(t, ok)
	require.Equal(t, "bar", val)
}

func TestResolveForReplace_ScopeVarDeferredWhenDeferTrue(t *testing.T) {
	scope := NewEnvScope(nil, false).WithEntry("PARAM", "value", EnvSourceDAGEnv)
	r := &resolver{
		scope:          scope,
		deferShellVars: true,
	}
	_, ok := r.resolveForReplace("PARAM")
	assert.False(t, ok, "scope var should be deferred when deferShellVars=true")
}

func TestResolveForReplace_ScopeVarExpandedWhenDeferFalse(t *testing.T) {
	scope := NewEnvScope(nil, false).WithEntry("PARAM", "value", EnvSourceDAGEnv)
	r := &resolver{
		scope:          scope,
		deferShellVars: false,
	}
	val, ok := r.resolveForReplace("PARAM")
	require.True(t, ok)
	require.Equal(t, "value", val)
}

func TestResolveForReplace_OSSourcedScopeSkippedEvenWithoutDefer(t *testing.T) {
	scope := NewEnvScope(nil, false).WithEntry("HOME", "/home/user", EnvSourceOS)
	r := &resolver{
		scope:          scope,
		deferShellVars: false,
	}
	_, ok := r.resolveForReplace("HOME")
	assert.False(t, ok, "OS-sourced scope should be skipped by lookupScopeNonOS")
}

func TestResolveForReplace_NotFoundReturnsEmpty(t *testing.T) {
	r := &resolver{deferShellVars: true}
	_, ok := r.resolveForReplace("NONEXISTENT")
	assert.False(t, ok)
}

func TestResolveForReplace_ExplicitVarTakesPrecedenceOverScope(t *testing.T) {
	scope := NewEnvScope(nil, false).WithEntry("VAR", "scope_val", EnvSourceDAGEnv)
	r := &resolver{
		variables:      []map[string]string{{"VAR": "explicit_val"}},
		scope:          scope,
		deferShellVars: false,
	}
	val, ok := r.resolveForReplace("VAR")
	require.True(t, ok)
	require.Equal(t, "explicit_val", val)
}

// --- replaceVars with deferShellVars ---

func TestReplaceVars_DeferShellVars_ExplicitVarExpanded(t *testing.T) {
	r := &resolver{
		variables:      []map[string]string{{"FOO": "bar"}},
		deferShellVars: true,
	}
	got := r.replaceVars("echo $FOO")
	require.Equal(t, "echo bar", got)
}

func TestReplaceVars_DeferShellVars_ScopeVarPreserved(t *testing.T) {
	scope := NewEnvScope(nil, false).WithEntry("TOPIC", "my-topic", EnvSourceDAGEnv)
	r := &resolver{
		scope:          scope,
		deferShellVars: true,
	}
	got := r.replaceVars("echo $TOPIC")
	require.Equal(t, "echo $TOPIC", got)
}

func TestReplaceVars_DeferShellVars_MixedExplicitAndScope(t *testing.T) {
	scope := NewEnvScope(nil, false).WithEntry("PARAM", "param_val", EnvSourceDAGEnv)
	r := &resolver{
		variables:      []map[string]string{{"EXPLICIT": "explicit_val"}},
		scope:          scope,
		deferShellVars: true,
	}
	got := r.replaceVars("$EXPLICIT and $PARAM")
	require.Equal(t, "explicit_val and $PARAM", got)
}

func TestReplaceVars_DeferShellVars_BracedSyntaxScopePreserved(t *testing.T) {
	scope := NewEnvScope(nil, false).WithEntry("TOPIC", "my-topic", EnvSourceDAGEnv)
	r := &resolver{
		scope:          scope,
		deferShellVars: true,
	}
	got := r.replaceVars("echo ${TOPIC}")
	require.Equal(t, "echo ${TOPIC}", got)
}

func TestReplaceVars_DeferShellVars_BackticksInValuePreserved(t *testing.T) {
	// Simulates the scenario where a scope var has backticks in its value.
	// With deferShellVars=true, the var is NOT expanded, so the backticks
	// never appear in the template output.
	scope := NewEnvScope(nil, false).WithEntry("MSG", "say `hello`", EnvSourceDAGEnv)
	r := &resolver{
		scope:          scope,
		deferShellVars: true,
	}
	got := r.replaceVars("echo $MSG")
	require.Equal(t, "echo $MSG", got, "scope var with backticks should be deferred")
}

// --- deferShellVars with numeric (positional) vars ---

func TestResolveForReplace_NumericVarNotDeferredEvenWithDefer(t *testing.T) {
	scope := NewEnvScope(nil, false).WithEntry("1", "A", EnvSourceDAGEnv)
	r := &resolver{
		scope:          scope,
		deferShellVars: true,
	}
	val, ok := r.resolveForReplace("1")
	require.True(t, ok, "$1 should be expanded even with deferShellVars")
	require.Equal(t, "A", val)
}

func TestReplaceVars_DeferShellVars_PositionalParamsExpanded(t *testing.T) {
	scope := NewEnvScope(nil, false).
		WithEntry("1", "A", EnvSourceDAGEnv).
		WithEntry("2", "B", EnvSourceDAGEnv).
		WithEntry("NAMED", "deferred", EnvSourceDAGEnv)
	r := &resolver{
		scope:          scope,
		deferShellVars: true,
	}
	got := r.replaceVars("echo $1 $2 $NAMED")
	require.Equal(t, "echo A B $NAMED", got)
}

func TestIsNumericVar(t *testing.T) {
	assert.True(t, isNumericVar("1"))
	assert.True(t, isNumericVar("42"))
	assert.True(t, isNumericVar("0"))
	assert.False(t, isNumericVar(""))
	assert.False(t, isNumericVar("a"))
	assert.False(t, isNumericVar("1a"))
	assert.False(t, isNumericVar("FOO"))
}

// --- expandReferences unaffected by deferShellVars ---

func TestExpandReferences_StillWorksWithDeferShellVars(t *testing.T) {
	ctx := context.Background()
	r := &resolver{
		stepMap: map[string]StepInfo{
			"step1": {Stdout: "/tmp/out.txt"},
		},
		deferShellVars: true,
	}
	got := r.expandReferences(ctx, "cat ${step1.stdout}")
	require.Equal(t, "cat /tmp/out.txt", got)
}

func TestExpandReferences_JSONPathWithDeferShellVars(t *testing.T) {
	ctx := context.Background()
	r := &resolver{
		variables:      []map[string]string{{"DATA": `{"key":"val"}`}},
		deferShellVars: true,
	}
	got := r.expandReferences(ctx, "result: ${DATA.key}")
	require.Equal(t, "result: val", got)
}

// --- expandReferences short submatch ---

func TestExpandReferences_ShortSubmatch(t *testing.T) {
	ctx := context.Background()
	dataMap := map[string]string{
		"DATA": `{"key":"val"}`,
	}
	result := ExpandReferences(ctx, "$DATA.key", dataMap)
	require.Equal(t, "val", result)
}

// --- replaceVars ---

func TestReplaceVars(t *testing.T) {
	tests := []struct {
		name     string
		template string
		vars     map[string]string
		want     string
	}{
		{
			name:     "BasicSubstitution",
			template: "${FOO}",
			vars:     map[string]string{"FOO": "BAR"},
			want:     "BAR",
		},
		{
			name:     "ShortSyntax",
			template: "$FOO",
			vars:     map[string]string{"FOO": "BAR"},
			want:     "BAR",
		},
		{
			name:     "NoSubstitution",
			template: "$FOO_",
			vars:     map[string]string{"FOO": "BAR"},
			want:     "$FOO_",
		},
		{
			name:     "InMiddleOfString",
			template: "prefix $FOO suffix",
			vars:     map[string]string{"FOO": "BAR"},
			want:     "prefix BAR suffix",
		},
		{
			name:     "InMiddleOfStringAndNoSubstitution",
			template: "prefix $FOO1 suffix",
			vars:     map[string]string{"FOO": "BAR"},
			want:     "prefix $FOO1 suffix",
		},
		{
			name:     "MissingVar",
			template: "${MISSING}",
			vars:     map[string]string{"FOO": "BAR"},
			want:     "${MISSING}",
		},
		{
			name:     "MultipleVars",
			template: "$FOO ${BAR} $BAZ",
			vars: map[string]string{
				"FOO": "1",
				"BAR": "2",
				"BAZ": "3",
			},
			want: "1 2 3",
		},
		{
			name:     "NestedVarsNotSupported",
			template: "${FOO${BAR}}",
			vars:     map[string]string{"FOO": "1", "BAR": "2"},
			want:     "${FOO${BAR}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &resolver{variables: []map[string]string{tt.vars}}
			got := r.replaceVars(tt.template)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestReplaceVars_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		template string
		vars     map[string]string
		want     string
	}{
		{
			name:     "SingleQuotesPreserved",
			template: "'$FOO'",
			vars:     map[string]string{"FOO": "bar"},
			want:     "'$FOO'",
		},
		{
			name:     "SingleQuotesPreservedWithBraces",
			template: "'${FOO}'",
			vars:     map[string]string{"FOO": "bar"},
			want:     "'${FOO}'",
		},
		{
			name:     "EmptyVariableName",
			template: "${}",
			vars:     map[string]string{"": "value"},
			want:     "${}",
		},
		{
			name:     "UnderscoreInVarName",
			template: "${FOO_BAR}",
			vars:     map[string]string{"FOO_BAR": "value"},
			want:     "value",
		},
		{
			name:     "NumberInVarName",
			template: "${FOO123}",
			vars:     map[string]string{"FOO123": "value"},
			want:     "value",
		},
		{
			name:     "VarFollowedBySingleQuote",
			template: "${FOO}'",
			vars:     map[string]string{"FOO": "bar"},
			want:     "bar'",
		},
		{
			name:     "SimpleVarFollowedBySingleQuote",
			template: "$FOO'",
			vars:     map[string]string{"FOO": "bar"},
			want:     "bar'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &resolver{variables: []map[string]string{tt.vars}}
			got := r.replaceVars(tt.template)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestReplaceVarsWithScope(t *testing.T) {
	tests := []struct {
		name     string
		template string
		scope    *EnvScope
		want     string
	}{
		{
			name:     "UserDefinedVarExpanded",
			template: "Hello ${NAME}",
			scope:    NewEnvScope(nil, false).WithEntry("NAME", "World", EnvSourceDAGEnv),
			want:     "Hello World",
		},
		{
			name:     "OSVarNotExpanded",
			template: "Path is $PATH",
			scope:    NewEnvScope(nil, true),
			want:     "Path is $PATH",
		},
		{
			name:     "JSONPathSkipped",
			template: "${step.stdout}",
			scope:    NewEnvScope(nil, false).WithEntry("step", "ignored", EnvSourceDAGEnv),
			want:     "${step.stdout}",
		},
		{
			name:     "SingleQuotedSkipped",
			template: "'${NAME}' stays",
			scope:    NewEnvScope(nil, false).WithEntry("NAME", "World", EnvSourceDAGEnv),
			want:     "'${NAME}' stays",
		},
		{
			name:     "MixedExpansion",
			template: "User ${USER} env $HOME",
			scope:    NewEnvScope(nil, true).WithEntry("USER", "alice", EnvSourceDAGEnv),
			want:     "User alice env $HOME",
		},
		{
			name:     "StepEnvExpanded",
			template: "${STEP_VAR}",
			scope:    NewEnvScope(nil, false).WithEntry("STEP_VAR", "step_value", EnvSourceStepEnv),
			want:     "step_value",
		},
		{
			name:     "SecretExpanded",
			template: "key=${SECRET}",
			scope:    NewEnvScope(nil, false).WithEntry("SECRET", "s3cr3t", EnvSourceSecret),
			want:     "key=s3cr3t",
		},
		{
			name:     "NilScope",
			template: "${VAR}",
			scope:    nil,
			want:     "${VAR}",
		},
		{
			name:     "MissingVar",
			template: "${MISSING}",
			scope:    NewEnvScope(nil, false).WithEntry("OTHER", "value", EnvSourceDAGEnv),
			want:     "${MISSING}",
		},
		{
			name:     "MultipleVars",
			template: "${A} ${B} ${C}",
			scope: NewEnvScope(nil, false).
				WithEntry("A", "1", EnvSourceDAGEnv).
				WithEntry("B", "2", EnvSourceStepEnv).
				WithEntry("C", "3", EnvSourceSecret),
			want: "1 2 3",
		},
		{
			name:     "ShortAndBracedSyntax",
			template: "$FOO and ${BAR}",
			scope: NewEnvScope(nil, false).
				WithEntry("FOO", "foo", EnvSourceDAGEnv).
				WithEntry("BAR", "bar", EnvSourceDAGEnv),
			want: "foo and bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &resolver{scope: tt.scope}
			got := r.replaceVars(tt.template)
			require.Equal(t, tt.want, got)
		})
	}
}

// --- ExpandReferences ---

func TestExpandReferences(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		dataMap map[string]string
		want    string
	}{
		{
			name:  "BasicReplacementWithCurlyBraces",
			input: "Hello: ${FOO.bar}",
			dataMap: map[string]string{
				"FOO": `{"bar": "World"}`,
			},
			want: "Hello: World",
		},
		{
			name:  "BasicReplacementWithSingleDollarSign",
			input: "Output => $FOO.value",
			dataMap: map[string]string{
				"FOO": `{"value": "SingleDollarWorks"}`,
			},
			want: "Output => SingleDollarWorks",
		},
		{
			name:  "MissingKeyInDataMap",
			input: "Hello: ${BAR.xyz}",
			dataMap: map[string]string{
				"FOO": `{"bar":"zzz"}`,
			},
			want: "Hello: ${BAR.xyz}",
		},
		{
			name:  "InvalidJSONInDataMap",
			input: "Test => ${FOO.bar}",
			dataMap: map[string]string{
				"FOO": `{"bar":`,
			},
			want: "Test => ${FOO.bar}",
		},
		{
			name:  "NestedSubPathExtraction",
			input: "Deep => ${FOO.level1.level2}",
			dataMap: map[string]string{
				"FOO": `{"level1": {"level2":"DeepValue"}}`,
			},
			want: "Deep => DeepValue",
		},
		{
			name:  "NonExistentSubPathInValidJSON",
			input: "Data => ${FOO.bar.baz}",
			dataMap: map[string]string{
				"FOO": `{"bar":"NotAnObject"}`,
			},
			want: "Data => ${FOO.bar.baz}",
		},
		{
			name:  "MultiplePlaceholdersIncludingSingleDollarForm",
			input: "Multi: ${FOO.one}, $FOO.two , and ${FOO.three}",
			dataMap: map[string]string{
				"FOO": `{
								"one": "1",
								"two": "2",
								"three": "3"
						}`,
			},
			want: "Multi: 1, 2 , and 3",
		},
		{
			name:    "LookupFromEnvironmentNotExpanded",
			input:   "${TEST_JSON_VAR.bar}",
			dataMap: map[string]string{},
			want:    "${TEST_JSON_VAR.bar}",
		},
	}

	t.Setenv("TEST_JSON_VAR", `{"bar": "World"}`)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got := ExpandReferences(ctx, tt.input, tt.dataMap)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestExpandReferencesWithSteps(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		dataMap map[string]string
		stepMap map[string]StepInfo
		want    string
	}{
		{
			name:    "BasicStepIDStdoutReference",
			input:   "Log file is at ${download.stdout}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"download": {
					Stdout:   "/tmp/logs/download.out",
					Stderr:   "/tmp/logs/download.err",
					ExitCode: "0",
				},
			},
			want: "Log file is at /tmp/logs/download.out",
		},
		{
			name:    "StepIDStderrReference",
			input:   "Check errors at ${build.stderr}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"build": {
					Stdout:   "/tmp/logs/build.out",
					Stderr:   "/tmp/logs/build.err",
					ExitCode: "1",
				},
			},
			want: "Check errors at /tmp/logs/build.err",
		},
		{
			name:    "StepIDExitCodeReference",
			input:   "Build exited with code ${build.exit_code}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"build": {
					Stdout:   "/tmp/logs/build.out",
					Stderr:   "/tmp/logs/build.err",
					ExitCode: "1",
				},
			},
			want: "Build exited with code 1",
		},
		{
			name:    "MultipleStepReferences",
			input:   "Download log: ${download.stdout}, Build errors: ${build.stderr}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"download": {
					Stdout: "/tmp/logs/download.out",
				},
				"build": {
					Stderr: "/tmp/logs/build.err",
				},
			},
			want: "Download log: /tmp/logs/download.out, Build errors: /tmp/logs/build.err",
		},
		{
			name:    "UnknownStepIDLeavesAsIs",
			input:   "Unknown step: ${unknown.stdout}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"known": {
					Stdout: "/tmp/logs/known.out",
				},
			},
			want: "Unknown step: ${unknown.stdout}",
		},
		{
			name:    "UnknownPropertyLeavesAsIs",
			input:   "Unknown prop: ${download.unknown}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"download": {
					Stdout: "/tmp/logs/download.out",
				},
			},
			want: "Unknown prop: ${download.unknown}",
		},
		{
			name:  "StepIDPrecedenceOverVariable",
			input: "Value: ${download.stdout}",
			dataMap: map[string]string{
				"download": `{"stdout": "from-variable"}`,
			},
			stepMap: map[string]StepInfo{
				"download": {
					Stdout: "/tmp/logs/download.out",
				},
			},
			want: "Value: /tmp/logs/download.out",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got := ExpandReferencesWithSteps(ctx, tt.input, tt.dataMap, tt.stepMap)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestExpandReferencesWithSteps_Extended(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		dataMap map[string]string
		stepMap map[string]StepInfo
		want    string
	}{
		{
			name:    "StepStdoutReference",
			input:   "The output is at ${step1.stdout}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"step1": {
					Stdout: "/tmp/step1.out",
				},
			},
			want: "The output is at /tmp/step1.out",
		},
		{
			name:    "StepStderrReference",
			input:   "Errors at ${step1.stderr}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"step1": {
					Stderr: "/tmp/step1.err",
				},
			},
			want: "Errors at /tmp/step1.err",
		},
		{
			name:    "StepExitCodeReference",
			input:   "Exit code: ${step1.exit_code}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"step1": {
					ExitCode: "0",
				},
			},
			want: "Exit code: 0",
		},
		{
			name:    "MissingStepReference",
			input:   "Missing: ${missing_step.stdout}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"step1": {
					Stdout: "/tmp/step1.out",
				},
			},
			want: "Missing: ${missing_step.stdout}",
		},
		{
			name:    "EmptyStepProperty",
			input:   "Empty: ${step1.stdout}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"step1": {
					Stdout: "",
				},
			},
			want: "Empty: ${step1.stdout}",
		},
		{
			name:    "NilStepMap",
			input:   "No steps: ${step1.stdout}",
			dataMap: map[string]string{},
			stepMap: nil,
			want:    "No steps: ${step1.stdout}",
		},
		{
			name:  "RegularVariableTakesPrecedence",
			input: "Value: ${step1.field}",
			dataMap: map[string]string{
				"step1": `{"field": "from_var"}`,
			},
			stepMap: map[string]StepInfo{
				"step1": {
					Stdout: "/tmp/step1.out",
				},
			},
			want: "Value: from_var",
		},
		{
			name:    "DollarSignWithoutBraces",
			input:   "Path: $step1.stdout",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"step1": {
					Stdout: "/tmp/out",
				},
			},
			want: "Path: /tmp/out",
		},
		{
			name:    "MultipleStepReferences",
			input:   "Out: ${step1.stdout}, Err: ${step1.stderr}, Code: ${step1.exit_code}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"step1": {
					Stdout:   "/tmp/out",
					Stderr:   "/tmp/err",
					ExitCode: "1",
				},
			},
			want: "Out: /tmp/out, Err: /tmp/err, Code: 1",
		},
		{
			name:    "StepStdoutReferenceWithSlice",
			input:   "Slice: ${step1.stdout:0:4}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"step1": {
					Stdout: "abcdef",
				},
			},
			want: "Slice: abcd",
		},
		{
			name:    "StepStdoutReferenceWithOffsetOnly",
			input:   "Tail: ${step1.stdout:3}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"step1": {
					Stdout: "abcdef",
				},
			},
			want: "Tail: def",
		},
		{
			name:    "StepStdoutReferenceWithSliceBeyondLength",
			input:   "Beyond: ${step1.stdout:10:3}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"step1": {
					Stdout: "abc",
				},
			},
			want: "Beyond: ",
		},
		{
			name:    "StepStdoutReferenceWithInvalidSlice",
			input:   "Invalid: ${step1.stdout:-1:2}",
			dataMap: map[string]string{},
			stepMap: map[string]StepInfo{
				"step1": {
					Stdout: "abcdef",
				},
			},
			want: "Invalid: ${step1.stdout:-1:2}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got := ExpandReferencesWithSteps(ctx, tt.input, tt.dataMap, tt.stepMap)
			require.Equal(t, tt.want, got)
		})
	}
}
