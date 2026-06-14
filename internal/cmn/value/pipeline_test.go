// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func commandSubstitutionTestContext() context.Context {
	ctx := context.Background()
	if runtime.GOOS == "windows" {
		scope := NewEnvScope(nil, true).WithEntry("SHELL", "cmd", EnvSourceOS)
		ctx = WithEnvScope(ctx, scope)
	}
	return ctx
}

func TestExpandQuotedRefs_SimpleVariable(t *testing.T) {
	ctx := context.Background()
	opts := NewOptions()
	WithVariables(map[string]string{"VAR": "hello"})(opts)

	result, err := expandQuotedRefs(ctx, `{"key": "${VAR}"}`, opts)
	require.NoError(t, err)
	assert.Equal(t, `{"key": "hello"}`, result)
}

func TestExpandQuotedRefs_JSONPathRef(t *testing.T) {
	ctx := context.Background()
	vars := map[string]string{"DATA": `{"name":"alice"}`}
	opts := NewOptions()
	WithVariables(vars)(opts)

	result, err := expandQuotedRefs(ctx, `{"val": "${DATA.name}"}`, opts)
	require.NoError(t, err)
	assert.Equal(t, `{"val": "alice"}`, result)
}

func TestExpandQuotedRefs_NotFound(t *testing.T) {
	ctx := context.Background()
	opts := NewOptions()

	result, err := expandQuotedRefs(ctx, `{"val": "${MISSING}"}`, opts)
	require.NoError(t, err)
	assert.Equal(t, `{"val": "${MISSING}"}`, result)
}

func TestExpandQuotedRefs_JSONPathNotFound(t *testing.T) {
	ctx := context.Background()
	opts := NewOptions()

	result, err := expandQuotedRefs(ctx, `{"val": "${MISSING.path}"}`, opts)
	require.NoError(t, err)
	assert.Equal(t, `{"val": "${MISSING.path}"}`, result)
}

func TestExpandQuotedRefs_NoMatch(t *testing.T) {
	ctx := context.Background()
	opts := NewOptions()

	result, err := expandQuotedRefs(ctx, `no refs here`, opts)
	require.NoError(t, err)
	assert.Equal(t, `no refs here`, result)
}

func TestExpandQuotedRefs_WithStepRef(t *testing.T) {
	ctx := context.Background()
	opts := NewOptions()
	WithStepMap(map[string]StepInfo{
		"step1": {Stdout: "output_val", ExitCode: "0"},
	})(opts)

	result, err := expandQuotedRefs(ctx, `{"out": "${step1.stdout}"}`, opts)
	require.NoError(t, err)
	assert.Equal(t, `{"out": "output_val"}`, result)
}

func TestShellExpandPhase_FallbackOnError(t *testing.T) {
	ctx := context.Background()
	opts := NewOptions()
	opts.ExpandOS = true
	t.Setenv("TESTVAR", "value123")

	result, err := shellExpandPhase(ctx, "$(echo hello) $TESTVAR", opts)
	require.NoError(t, err)
	assert.Contains(t, result, "value123")
}

func TestShellExpandPhase_NonCommandError(t *testing.T) {
	ctx := context.Background()
	opts := NewOptions()
	opts.ExpandOS = true

	result, err := shellExpandPhase(ctx, "${UNSET_XYZ_99:?required}", opts)
	require.NoError(t, err)
	assert.Contains(t, result, "UNSET_XYZ_99")
}

func TestShellExpandPhase_ShellDisabledWithExpandOS(t *testing.T) {
	t.Setenv("SHELL_TEST_VAR", "os_val")
	ctx := context.Background()
	opts := NewOptions()
	opts.ExpandShell = false
	opts.ExpandOS = true

	result, err := shellExpandPhase(ctx, "$SHELL_TEST_VAR", opts)
	require.NoError(t, err)
	assert.Equal(t, "os_val", result)
}

func TestShellExpandPhase_ErrorFallbackWithoutExpandOS(t *testing.T) {
	ctx := context.Background()
	opts := NewOptions()
	opts.Variables = []map[string]string{{"VAR": ""}}

	// :? treats empty as unset, triggering an error that falls back to expandEnvScopeOnly
	result, err := shellExpandPhase(ctx, "${VAR:?required}", opts)
	require.NoError(t, err)
	assert.Equal(t, "${VAR:?required}", result)
}

func TestPipeline_DisabledPhases(t *testing.T) {
	ctx := context.Background()
	t.Setenv("PVAR", "pval")

	result, err := String(ctx, "`echo nope` $PVAR",
		WithoutSubstitute(),
		WithoutExpandEnv(),
		WithVariables(map[string]string{"X": "y"}),
	)
	require.NoError(t, err)
	assert.Contains(t, result, "`echo nope`")
	assert.Contains(t, result, "$PVAR")
}

func TestString_ShellExpandFallback(t *testing.T) {
	t.Setenv("FBVAR", "fbval")
	ctx := context.Background()

	result, err := String(ctx, "$(echo x) $FBVAR", WithOSExpansion())
	require.NoError(t, err)
	assert.Contains(t, result, "fbval")
}

func TestString(t *testing.T) {
	t.Setenv("TEST_ENV", "test_value")
	t.Setenv("TEST_JSON", `{"key": "value"}`)

	tests := []struct {
		name    string
		input   string
		opts    []Option
		want    string
		wantErr bool
	}{
		{
			name:  "EmptyString",
			input: "",
			want:  "",
		},
		{
			name:  "EnvVarExpansion",
			input: "$TEST_ENV",
			opts:  []Option{WithOSExpansion()},
			want:  "test_value",
		},
		{
			name:  "CommandSubstitution",
			input: "`echo hello`",
			want:  "hello",
		},
		{
			name:  "CombinedEnvAndCommand",
			input: "$TEST_ENV and `echo world`",
			opts:  []Option{WithOSExpansion()},
			want:  "test_value and world",
		},
		{
			name:  "WithVariables",
			input: "${FOO} and ${BAR}",
			opts:  []Option{WithVariables(map[string]string{"FOO": "foo", "BAR": "bar"})},
			want:  "foo and bar",
		},
		{
			name:  "WithoutEnvExpansion",
			input: "$TEST_ENV",
			opts:  []Option{WithoutExpandEnv()},
			want:  "$TEST_ENV",
		},
		{
			name:  "WithoutSubstitution",
			input: "`echo hello`",
			opts:  []Option{WithoutSubstitute()},
			want:  "`echo hello`",
		},
		{
			name:  "ShellSubstringExpansion",
			input: "prefix ${UID:0:5} suffix",
			opts:  []Option{WithVariables(map[string]string{"UID": "HBL01_22OCT2025_0536"})},
			want:  "prefix HBL01 suffix",
		},
		{
			name:  "OnlyReplaceVars",
			input: "$TEST_ENV and `echo hello` and ${FOO}",
			opts:  []Option{OnlyReplaceVars(), WithVariables(map[string]string{"FOO": "foo"})},
			want:  "$TEST_ENV and `echo hello` and foo",
		},
		{
			name:    "InvalidCommandSubstitution",
			input:   "`invalid_command_that_does_not_exist`",
			wantErr: true,
		},
		{
			name:  "JSONReference",
			input: "${TEST_JSON.key}",
			opts:  []Option{WithVariables(map[string]string{"TEST_JSON": `{"key": "value"}`})},
			want:  "value",
		},
		{
			name:  "MultipleVariableSets",
			input: "${FOO} ${BAR}",
			opts: []Option{
				WithVariables(map[string]string{"FOO": "first"}),
				WithVariables(map[string]string{"BAR": "second"}),
			},
			want: "first second",
		},
		{
			name:  "QuotedJSONVariableEscaping",
			input: `params: aJson="${ITEM}"`,
			opts:  []Option{WithVariables(map[string]string{"ITEM": `{"file": "file1.txt", "config": "prod"}`})},
			want:  `params: aJson=` + strconv.Quote(`{"file": "file1.txt", "config": "prod"}`),
		},
		{
			name:  "QuotedFilePathWithSpaces",
			input: `path: "FILE=\"${ITEM}\""`,
			opts:  []Option{WithVariables(map[string]string{"ITEM": "/path/to/my file.txt"})},
			want:  `path: "FILE=\"/path/to/my file.txt\""`,
		},
		{
			name:  "QuotedStringWithInternalQuotes",
			input: `value: "VAR=\"${ITEM}\""`,
			opts:  []Option{WithVariables(map[string]string{"ITEM": `say "hello"`})},
			want:  `value: "VAR=\"say "hello"\""`,
		},
		{
			name:  "MixedQuotedAndUnquotedVariables",
			input: `unquoted ${ITEM} and quoted "value=\"${ITEM}\""`,
			opts:  []Option{WithVariables(map[string]string{"ITEM": `{"test": "value"}`})},
			want:  `unquoted {"test": "value"} and quoted "value=\"{"test": "value"}\""`,
		},
		{
			name:  "QuotedEmptyString",
			input: `empty: "VAL=\"${EMPTY}\""`,
			opts:  []Option{WithVariables(map[string]string{"EMPTY": ""})},
			want:  `empty: "VAL=\"\""`,
		},
		{
			name:  "QuotedJSONPathReference",
			input: `config: "file=\"${CONFIG.file}\""`,
			opts:  []Option{WithVariables(map[string]string{"CONFIG": `{"file": "/path/to/config.json", "env": "prod"}`})},
			want:  `config: "file=\"/path/to/config.json\""`,
		},
		{
			name:  "QuotedJSONPathWithSpaces",
			input: `path: "value=\"${DATA.path}\""`,
			opts:  []Option{WithVariables(map[string]string{"DATA": `{"path": "/my dir/file name.txt"}`})},
			want:  `path: "value=\"/my dir/file name.txt\""`,
		},
		{
			name:  "QuotedNestedJSONPath",
			input: `nested: "result=\"${OBJ.nested.deep}\""`,
			opts:  []Option{WithVariables(map[string]string{"OBJ": `{"nested": {"deep": "found it"}}`})},
			want:  `nested: "result=\"found it\""`,
		},
		{
			name:  "QuotedJSONPathWithQuotesInValue",
			input: `msg: "text=\"${MSG.content}\""`,
			opts:  []Option{WithVariables(map[string]string{"MSG": `{"content": "He said \"hello\""}`})},
			want:  `msg: "text=\"He said "hello"\""`,
		},
		{
			name:  "MixedQuotedJSONPathAndSimpleVariable",
			input: `params: "${SIMPLE}" and config="file=\"${CONFIG.file}\""`,
			opts: []Option{WithVariables(map[string]string{
				"SIMPLE": "value",
				"CONFIG": `{"file": "app.conf"}`,
			})},
			want: `params: "value" and config="file=\"app.conf\""`,
		},
		{
			name:  "QuotedNonExistentJSONPath",
			input: `missing: "val=\"${CONFIG.missing}\""`,
			opts:  []Option{WithVariables(map[string]string{"CONFIG": `{"file": "app.conf"}`})},
			want:  `missing: "val=\"<nil>\""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := commandSubstitutionTestContext()
			got, err := String(ctx, tt.input, tt.opts...)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIntString(t *testing.T) {
	t.Setenv("TEST_INT", "42")

	tests := []struct {
		name    string
		input   string
		opts    []Option
		want    int
		wantErr bool
	}{
		{
			name:  "SimpleInteger",
			input: "123",
			want:  123,
		},
		{
			name:  "EnvVarInteger",
			input: "$TEST_INT",
			opts:  []Option{WithOSExpansion()},
			want:  42,
		},
		{
			name:  "CommandSubstitutionInteger",
			input: "`echo 100`",
			want:  100,
		},
		{
			name:  "WithVariables",
			input: "${NUM}",
			opts:  []Option{WithVariables(map[string]string{"NUM": "999"})},
			want:  999,
		},
		{
			name:    "InvalidInteger",
			input:   "not_a_number",
			wantErr: true,
		},
		{
			name:    "InvalidCommand",
			input:   "`invalid_command`",
			wantErr: true,
		},
		{
			name:  "WithoutSubstitute_SkipsCommandSubstitution",
			input: "123",
			opts:  []Option{WithoutSubstitute()},
			want:  123,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := commandSubstitutionTestContext()
			got, err := IntString(ctx, tt.input, tt.opts...)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestString_WithStepMap(t *testing.T) {
	tests := []struct {
		name  string
		input string
		opts  []Option
		want  string
	}{
		{
			name:  "StepReferenceWithNoVariables",
			input: "Output: ${step1.stdout}",
			opts: []Option{
				WithStepMap(map[string]StepInfo{
					"step1": {Stdout: "/tmp/output.txt"},
				}),
			},
			want: "Output: /tmp/output.txt",
		},
		{
			name:  "StepReferenceWithVariables",
			input: "Var: ${VAR}, Step: ${step1.exit_code}",
			opts: []Option{
				WithVariables(map[string]string{"VAR": "value"}),
				WithStepMap(map[string]StepInfo{
					"step1": {ExitCode: "0"},
				}),
			},
			want: "Var: value, Step: 0",
		},
		{
			name:  "StepStdoutSlice",
			input: "Slice: ${step1.stdout:0:3}",
			opts: []Option{
				WithStepMap(map[string]StepInfo{
					"step1": {Stdout: "HBL01_22OCT2025_0536"},
				}),
			},
			want: "Slice: HBL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got, err := String(ctx, tt.input, tt.opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIntString_WithStepMap(t *testing.T) {
	tests := []struct {
		name  string
		input string
		opts  []Option
		want  int
	}{
		{
			name:  "StepExitCodeAsInteger",
			input: "${step1.exit_code}",
			opts: []Option{
				WithStepMap(map[string]StepInfo{
					"step1": {ExitCode: "42"},
				}),
			},
			want: 42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got, err := IntString(ctx, tt.input, tt.opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStringWithSteps(t *testing.T) {
	ctx := context.Background()

	stepMap := map[string]StepInfo{
		"download": {
			Stdout:   "/var/log/download.stdout",
			Stderr:   "/var/log/download.stderr",
			ExitCode: "0",
		},
		"process": {
			Stdout:   "/var/log/process.stdout",
			Stderr:   "/var/log/process.stderr",
			ExitCode: "1",
		},
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "StdoutReference",
			input: "cat ${download.stdout}",
			want:  "cat /var/log/download.stdout",
		},
		{
			name:  "StderrReference",
			input: "tail -20 ${process.stderr}",
			want:  "tail -20 /var/log/process.stderr",
		},
		{
			name:  "ExitCodeReference",
			input: "if [ ${process.exit_code} -ne 0 ]; then echo failed; fi",
			want:  "if [ 1 -ne 0 ]; then echo failed; fi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := String(ctx, tt.input, WithStepMap(stepMap))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- OS expansion behavior ---

func TestString_OSExpansion(t *testing.T) {
	tests := []struct {
		name  string
		env   map[string]string
		input string
		opts  []Option
		want  string
	}{
		{
			name:  "DefaultNoOSExpansion",
			env:   map[string]string{"HOME": "/home/testuser"},
			input: "${HOME}",
			want:  "${HOME}",
		},
		{
			name:  "WithOSExpansion",
			env:   map[string]string{"TEST_OS_VAR": "resolved_value"},
			input: "${TEST_OS_VAR}",
			opts:  []Option{WithOSExpansion()},
			want:  "resolved_value",
		},
		{
			name:  "ExplicitVarsWorkWithoutOS",
			input: "${MY_VAR}",
			opts:  []Option{WithVariables(map[string]string{"MY_VAR": "hello"})},
			want:  "hello",
		},
		{
			name:  "OSEnvUsedWithOSExpansion",
			env:   map[string]string{"REAL_OS_VAR": "real_os_value"},
			input: "${REAL_OS_VAR}",
			opts:  []Option{WithOSExpansion()},
			want:  "real_os_value",
		},
		{
			name:  "POSIXDefaultExpanded",
			input: "${UNDEFINED:-default}",
			opts:  []Option{WithOSExpansion()},
			want:  "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			result, err := String(context.Background(), tt.input, tt.opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestString_ScopeNonOSEntriesWorkWithoutOSExpansion(t *testing.T) {
	scope := NewEnvScope(nil, false).
		WithEntry("SCOPE_VAR", "scope_value", EnvSourceDAGEnv)
	ctx := WithEnvScope(context.Background(), scope)

	result, err := String(ctx, "${SCOPE_VAR}")
	require.NoError(t, err)
	assert.Equal(t, "scope_value", result)
}

func TestString_ScopeOSEntriesSkippedWithoutOSExpansion(t *testing.T) {
	scope := NewEnvScope(nil, false).
		WithEntry("OS_VAR", "os_value", EnvSourceOS)
	ctx := WithEnvScope(context.Background(), scope)

	result, err := String(ctx, "${OS_VAR}")
	require.NoError(t, err)
	assert.Equal(t, "${OS_VAR}", result)
}

// --- POSIX syntax preservation (no options, no OS env) ---

func TestString_POSIXSyntaxPreserved(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"Default", "${UNDEFINED:-default}"},
		{"Assign", "${VAR:=value}"},
		{"Alternate", "${VAR:+alt}"},
		{"Substring", "${VAR:0:5}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := String(context.Background(), tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.input, result)
		})
	}
}

// --- POSIX expansion with user-defined variables ---

func TestString_POSIXWithUserVariables(t *testing.T) {
	tests := []struct {
		name  string
		input string
		vars  map[string]string
		want  string
	}{
		{
			name:  "SubstringWithUserVar",
			input: "prefix ${UID:0:5} suffix",
			vars:  map[string]string{"UID": "HBL01_22OCT2025_0536"},
			want:  "prefix HBL01 suffix",
		},
		{
			name:  "DefaultWithDefinedVar",
			input: "${VAR:-fallback}",
			vars:  map[string]string{"VAR": "actual"},
			want:  "actual",
		},
		{
			name:  "DefaultWithUndefinedVarPreserved",
			input: "${MISSING:-fallback}",
			want:  "${MISSING:-fallback}",
		},
		{
			name:  "MixedDefinedAndUndefined",
			input: "${UID:0:3} ${MISSING:-kept}",
			vars:  map[string]string{"UID": "ABCDEF"},
			want:  "ABC ${MISSING:-kept}",
		},
		{
			name:  "LengthOperator",
			input: "${#VAR}",
			vars:  map[string]string{"VAR": "HelloWorld"},
			want:  "10",
		},
		{
			name:  "EmptyVarWithDefault",
			input: "${VAR:-fallback}",
			vars:  map[string]string{"VAR": ""},
			want:  "fallback",
		},
		{
			name:  "MixedWithKnownVars",
			input: "${KNOWN} ${UNDEFINED:-default}",
			vars:  map[string]string{"KNOWN": "value"},
			want:  "value ${UNDEFINED:-default}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.vars != nil {
				opts = append(opts, WithVariables(tt.vars))
			}
			result, err := String(context.Background(), tt.input, opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestString_POSIXWithScope(t *testing.T) {
	scope := NewEnvScope(nil, false).
		WithEntry("SCOPE_VAR", "HelloWorld", EnvSourceDAGEnv)
	ctx := WithEnvScope(context.Background(), scope)

	result, err := String(ctx, "${SCOPE_VAR:0:5}")
	require.NoError(t, err)
	assert.Equal(t, "Hello", result)
}

func TestStringFields_DefaultNoOSExpansion(t *testing.T) {
	t.Setenv("SF_VAR", "should_not_appear")

	type S struct {
		Field string
	}
	ctx := context.Background()
	result, err := StringFields(ctx, S{Field: "${SF_VAR}"})
	require.NoError(t, err)
	assert.Equal(t, "${SF_VAR}", result.Field)
}

func TestObject_NoOSExpansion(t *testing.T) {
	t.Setenv("OBJ_VAR", "obj_value")

	type S struct {
		Field string
	}
	ctx := context.Background()
	result, err := Object(ctx, S{Field: "$OBJ_VAR"}, map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, "$OBJ_VAR", result.Field, "OS vars should be preserved, not expanded")
}

func TestObject_ExplicitVarsStillWork(t *testing.T) {
	type S struct {
		Field string
	}
	ctx := context.Background()
	result, err := Object(ctx, S{Field: "$MY_VAR"}, map[string]string{"MY_VAR": "hello"})
	require.NoError(t, err)
	assert.Equal(t, "hello", result.Field, "Explicit vars map should still expand")
}

func TestObject_ScopeVarsStillWork(t *testing.T) {
	type S struct {
		Field string
	}
	scope := NewEnvScope(nil, false).
		WithEntry("SCOPE_VAR", "scope_value", EnvSourceDAGEnv)
	ctx := WithEnvScope(context.Background(), scope)

	result, err := Object(ctx, S{Field: "${SCOPE_VAR}"}, map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, "scope_value", result.Field, "Non-OS scope entries should still expand")
}

func TestObject_OSScopeEntriesSkipped(t *testing.T) {
	type S struct {
		Field string
	}
	scope := NewEnvScope(nil, false).
		WithEntry("OS_VAR", "os_value", EnvSourceOS)
	ctx := WithEnvScope(context.Background(), scope)

	result, err := Object(ctx, S{Field: "${OS_VAR}"}, map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, "${OS_VAR}", result.Field, "OS-sourced scope entries should be skipped")
}

// --- Integration-style tests: executor config evaluation ---

// TestObject_SSHExecutorNoOSExpansion simulates evaluating an SSH executor config.
// OS variables like $HOME must be preserved for the remote shell,
// while DAG-defined variables must be expanded.
func TestObject_SSHExecutorNoOSExpansion(t *testing.T) {
	t.Setenv("HOME", "/home/localuser")

	type SSHConfig struct {
		Host    string
		Command string
	}

	// REMOTE_HOST is provided as a DAG-scoped variable via the vars map.
	// $HOME is an OS variable that should NOT be expanded.
	vars := map[string]string{"REMOTE_HOST": "remotehost.example.com"}
	cfg := SSHConfig{
		Host:    "${REMOTE_HOST}",
		Command: "tar czf $HOME/backup.tar.gz /data",
	}

	result, err := Object(context.Background(), cfg, vars)
	require.NoError(t, err)
	assert.Equal(t, "remotehost.example.com", result.Host, "DAG var should be expanded")
	assert.Equal(t, "tar czf $HOME/backup.tar.gz /data", result.Command, "$HOME should be preserved for remote shell")
}

// TestObject_DockerExecutorNoOSExpansion simulates evaluating a Docker executor config.
// OS variables like $HOME in container env should be preserved as literal text,
// while DAG-defined variables like REGISTRY should be expanded.
func TestObject_DockerExecutorNoOSExpansion(t *testing.T) {
	t.Setenv("HOME", "/home/localuser")

	type DockerConfig struct {
		Image string
		Env   []string
	}

	vars := map[string]string{"REGISTRY": "myregistry.com"}
	cfg := DockerConfig{
		Image: "${REGISTRY}/app",
		Env:   []string{"WORKDIR=$HOME/app", "TAG=${REGISTRY}/latest"},
	}

	result, err := Object(context.Background(), cfg, vars)
	require.NoError(t, err)
	assert.Equal(t, "myregistry.com/app", result.Image, "DAG var should be expanded in image")
	assert.Equal(t, "WORKDIR=$HOME/app", result.Env[0], "$HOME should be preserved for container env")
	assert.Equal(t, "TAG=myregistry.com/latest", result.Env[1], "DAG var should be expanded in env")
}

// TestObject_ExplicitOSImportStillWorks verifies that when an OS variable like HOME
// is explicitly imported into the DAG env scope, it gets expanded even through Object().
func TestObject_ExplicitOSImportStillWorks(t *testing.T) {
	t.Setenv("HOME", "/home/testuser")

	type SSHConfig struct {
		Command string
	}

	// Simulate a DAG that explicitly imports HOME via env: block.
	// At DAG load time, HOME="${HOME}" would have been expanded with WithOSExpansion(),
	// resulting in the vars map containing the resolved value.
	vars := map[string]string{"HOME": "/home/testuser"}
	cfg := SSHConfig{
		Command: "echo ${HOME}",
	}

	result, err := Object(context.Background(), cfg, vars)
	require.NoError(t, err)
	assert.Equal(t, "echo /home/testuser", result.Command, "Explicitly imported OS var should be expanded")
}

func TestString_CommandLikeStringWithSingleQuoteAfterVar(t *testing.T) {
	t.Parallel()

	scope := NewEnvScope(nil, false).WithEntry("MY_VALUE", "hello", EnvSourceDAGEnv)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "BracedVar",
			input: `nu -c "print $'got: ${MY_VALUE}'"`,
			want:  `nu -c "print $'got: hello'"`,
		},
		{
			name:  "SimpleVar",
			input: `nu -c "print $'got: $MY_VALUE'"`,
			want:  `nu -c "print $'got: hello'"`,
		},
		{
			name:  "MultipleVars",
			input: `nu -c "print $'bucket: ${BUCKET_PREFIX}${PROJECT_BUCKET}'"`,
			want:  `nu -c "print $'bucket: gs://my-bucket'"`,
		},
		{
			name:  "MissingVarPreserved",
			input: `nu -c "print $'got: ${MISSING}'"`,
			want:  `nu -c "print $'got: ${MISSING}'"`,
		},
	}

	scope = scope.
		WithEntry("BUCKET_PREFIX", "gs://", EnvSourceDAGEnv).
		WithEntry("PROJECT_BUCKET", "my-bucket", EnvSourceDAGEnv)
	ctx := WithEnvScope(context.Background(), scope)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := String(ctx, tt.input, WithoutExpandEnv(), WithoutDollarEscape())
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// --- DeferShellVars integration tests (full pipeline via String) ---

func TestString_OnlyReplaceVars_DefersScopeVars(t *testing.T) {
	// Scope vars (params, env) should be deferred to the shell.
	scope := NewEnvScope(nil, false).
		WithEntry("TOPIC", "my-topic", EnvSourceDAGEnv)
	ctx := WithEnvScope(context.Background(), scope)

	result, err := String(ctx, "echo $TOPIC", OnlyReplaceVars())
	require.NoError(t, err)
	assert.Equal(t, "echo $TOPIC", result, "scope var should be deferred")
}

func TestString_OnlyReplaceVars_ExpandsExplicitVars(t *testing.T) {
	result, err := String(context.Background(), "echo ${FOO}",
		OnlyReplaceVars(),
		WithVariables(map[string]string{"FOO": "bar"}),
	)
	require.NoError(t, err)
	assert.Equal(t, "echo bar", result, "explicit WithVariables should be expanded")
}

func TestString_OnlyReplaceVars_BackticksNotExecuted(t *testing.T) {
	result, err := String(context.Background(), "`echo injected`",
		OnlyReplaceVars(),
	)
	require.NoError(t, err)
	assert.Equal(t, "`echo injected`", result, "backtick substitution should be disabled")
}

func TestString_OnlyReplaceVars_ScopeVarWithBackticksDeferred(t *testing.T) {
	// Value containing backticks in scope: should be deferred, not expanded.
	scope := NewEnvScope(nil, false).
		WithEntry("MSG", "say `hello`", EnvSourceDAGEnv)
	ctx := WithEnvScope(context.Background(), scope)

	result, err := String(ctx, "echo $MSG", OnlyReplaceVars())
	require.NoError(t, err)
	assert.Equal(t, "echo $MSG", result, "scope var with backticks should be deferred")
}

func TestString_OnlyReplaceVars_StepRefsStillExpanded(t *testing.T) {
	result, err := String(context.Background(), "cat ${step1.stdout}",
		OnlyReplaceVars(),
		WithStepMap(map[string]StepInfo{
			"step1": {Stdout: "/tmp/out.txt"},
		}),
	)
	require.NoError(t, err)
	assert.Equal(t, "cat /tmp/out.txt", result, "step references should still be expanded")
}

func TestString_OnlyReplaceVars_JSONPathStillExpanded(t *testing.T) {
	result, err := String(context.Background(), "val: ${DATA.key}",
		OnlyReplaceVars(),
		WithVariables(map[string]string{"DATA": `{"key":"resolved"}`}),
	)
	require.NoError(t, err)
	assert.Equal(t, "val: resolved", result, "JSON path refs should still be expanded")
}

func TestString_OnlyReplaceVars_MixedDeferredAndExpanded(t *testing.T) {
	scope := NewEnvScope(nil, false).
		WithEntry("PARAM", "param_val", EnvSourceDAGEnv)
	ctx := WithEnvScope(context.Background(), scope)

	result, err := String(ctx, "${EXPLICIT} and $PARAM and ${step1.stdout}",
		OnlyReplaceVars(),
		WithVariables(map[string]string{"EXPLICIT": "explicit_val"}),
		WithStepMap(map[string]StepInfo{
			"step1": {Stdout: "/tmp/out"},
		}),
	)
	require.NoError(t, err)
	assert.Equal(t, "explicit_val and $PARAM and /tmp/out", result)
}

func TestString_OnlyReplaceVars_OSEnvNotExpanded(t *testing.T) {
	t.Setenv("HOME", "/home/user")
	result, err := String(context.Background(), "echo $HOME", OnlyReplaceVars())
	require.NoError(t, err)
	assert.Equal(t, "echo $HOME", result, "OS env should not be expanded")
}

func TestString_OnlyReplaceVars_MultipleStepSources(t *testing.T) {
	scope := NewEnvScope(nil, false).
		WithEntry("DEFERRED", "should-defer", EnvSourceStepEnv)
	ctx := WithEnvScope(context.Background(), scope)

	result, err := String(ctx, "${step1.exit_code} $DEFERRED ${EXPLICIT}",
		OnlyReplaceVars(),
		WithVariables(map[string]string{"EXPLICIT": "yes"}),
		WithStepMap(map[string]StepInfo{
			"step1": {ExitCode: "0"},
		}),
	)
	require.NoError(t, err)
	assert.Equal(t, "0 $DEFERRED yes", result)
}

func TestString_MultipleVariablesWithStepMapOnLast(t *testing.T) {
	ctx := context.Background()

	stepMap := map[string]StepInfo{
		"deploy": {Stdout: "/logs/deploy.out"},
	}

	tests := []struct {
		name     string
		input    string
		varSets  []map[string]string
		expected string
	}{
		{
			name:  "StepReferencesProcessedWithLastVariableSet",
			input: "${X} and ${Y} with log at ${deploy.stdout}",
			varSets: []map[string]string{
				{"X": "1", "Y": "2"},
				{"Z": "3"},
			},
			expected: "1 and 2 with log at /logs/deploy.out",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			for _, vars := range tt.varSets {
				opts = append(opts, WithVariables(vars))
			}
			opts = append(opts, WithStepMap(stepMap))

			result, err := String(ctx, tt.input, opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
