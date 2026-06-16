// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/expand"
)

func TestExpandEnvContext(t *testing.T) {
	t.Run("WithScopeInContext", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("MY_VAR", "scope_value", EnvSourceDAGEnv)

		ctx := WithEnvScope(context.Background(), scope)
		result := ExpandEnvContext(ctx, "Value is $MY_VAR")
		assert.Equal(t, "Value is scope_value", result)
	})

	t.Run("ScopeTakesPrecedenceOverOSEnv", func(t *testing.T) {
		key := "TEST_EXPAND_PRECEDENCE"
		t.Setenv(key, "os_value")

		scope := NewEnvScope(nil, false).
			WithEntry(key, "scope_value", EnvSourceDAGEnv)

		ctx := WithEnvScope(context.Background(), scope)
		result := ExpandEnvContext(ctx, "Value is $"+key)
		assert.Equal(t, "Value is scope_value", result)
	})

	t.Run("WithoutScopeFallsBackToOSExpandEnv", func(t *testing.T) {
		key := "TEST_EXPAND_OS_FALLBACK"
		t.Setenv(key, "os_value")

		result := ExpandEnvContext(context.Background(), "Value is $"+key)
		assert.Equal(t, "Value is os_value", result)
	})

	t.Run("NilContextFallsBackToOSExpandEnv", func(t *testing.T) {
		key := "TEST_EXPAND_NIL_CTX"
		t.Setenv(key, "nil_ctx_value")

		result := ExpandEnvContext(nil, "Value is $"+key) //nolint:staticcheck // testing nil context handling
		assert.Equal(t, "Value is nil_ctx_value", result)
	})

	t.Run("VariableNotFoundPreserved", func(t *testing.T) {
		scope := NewEnvScope(nil, false)
		ctx := WithEnvScope(context.Background(), scope)

		assert.Equal(t, "Hello $NONEXISTENT!", ExpandEnvContext(ctx, "Hello $NONEXISTENT!"))
		assert.Equal(t, "Hello ${NONEXISTENT}!", ExpandEnvContext(ctx, "Hello ${NONEXISTENT}!"))
	})

	t.Run("BracedVariableSyntax", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("VAR", "value", EnvSourceDAGEnv)

		ctx := WithEnvScope(context.Background(), scope)
		result := ExpandEnvContext(ctx, "${VAR}Suffix")
		assert.Equal(t, "valueSuffix", result)
	})

	t.Run("MultipleVariables", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("FIRST", "Hello", EnvSourceDAGEnv).
			WithEntry("SECOND", "World", EnvSourceDAGEnv)

		ctx := WithEnvScope(context.Background(), scope)
		result := ExpandEnvContext(ctx, "$FIRST, $SECOND!")
		assert.Equal(t, "Hello, World!", result)
	})

	t.Run("NoVariablesToExpand", func(t *testing.T) {
		scope := NewEnvScope(nil, false)
		ctx := WithEnvScope(context.Background(), scope)

		result := ExpandEnvContext(ctx, "plain text without variables")
		assert.Equal(t, "plain text without variables", result)
	})

	t.Run("EmptyString", func(t *testing.T) {
		scope := NewEnvScope(nil, false)
		ctx := WithEnvScope(context.Background(), scope)

		result := ExpandEnvContext(ctx, "")
		assert.Equal(t, "", result)
	})
}

func TestExpandWithShellContext(t *testing.T) {
	t.Run("ShellDisabledEnvEnabled", func(t *testing.T) {
		t.Setenv("MYVAR", "myval")

		opts := newOptions()
		opts.ExpandShell = false
		opts.ExpandEnv = true
		opts.ExpandOS = true

		result, err := expandWithShellContext(context.Background(), "$MYVAR", opts)
		require.NoError(t, err)
		assert.Equal(t, "myval", result)
	})

	t.Run("ShellDisabledEnvDisabled", func(t *testing.T) {
		opts := newOptions()
		opts.ExpandShell = false
		opts.ExpandEnv = false

		result, err := expandWithShellContext(context.Background(), "$MYVAR", opts)
		require.NoError(t, err)
		assert.Equal(t, "$MYVAR", result)
	})

	t.Run("EmptyInput", func(t *testing.T) {
		result, err := expandWithShellContext(context.Background(), "", newOptions())
		require.NoError(t, err)
		assert.Equal(t, "", result)
	})

	t.Run("CommandSubstitutionPreserved", func(t *testing.T) {
		t.Setenv("KEEP", "kept")

		opts := newOptions()
		opts.ExpandOS = true

		// $(command) is not a variable expression, so it's preserved as literal text.
		// $KEEP is resolved via OS env.
		result, err := expandWithShellContext(context.Background(), "$(echo x) $KEEP", opts)
		require.NoError(t, err)
		assert.Equal(t, "$(echo x) kept", result)
	})

	t.Run("MalformedInputPreserved", func(t *testing.T) {
		opts := newOptions()
		opts.ExpandOS = true

		// Unclosed ${ doesn't match the variable regex, so it's returned as-is
		result, err := expandWithShellContext(context.Background(), "${", opts)
		require.NoError(t, err)
		assert.Equal(t, "${", result)
	})

	t.Run("NonUnexpectedCommandError", func(t *testing.T) {
		opts := newOptions()
		opts.ExpandOS = true

		// ${UNSET_VAR_ABC:?msg} triggers a non-UnexpectedCommand error from expand.Literal
		_, err := expandWithShellContext(context.Background(), "${UNSET_VAR_ABC:?required}", opts)
		assert.Error(t, err)
	})

	t.Run("UndefinedSimpleVarWithExpandOS", func(t *testing.T) {
		opts := newOptions()
		opts.ExpandOS = true

		// Undefined simple $VAR with ExpandOS=true resolves to empty string
		result, err := expandWithShellContext(context.Background(), "prefix $UNDEF_XYZ suffix", opts)
		require.NoError(t, err)
		assert.Equal(t, "prefix  suffix", result)
	})

	t.Run("DefinedPOSIXWithoutExpandOS", func(t *testing.T) {
		opts := newOptions()
		opts.Variables = []map[string]string{{"VAR": "HelloWorld"}}

		// Defined var with POSIX op works even without ExpandOS
		result, err := expandWithShellContext(context.Background(), "${VAR:0:5}", opts)
		require.NoError(t, err)
		assert.Equal(t, "Hello", result)
	})

	t.Run("SingleQuotedPreserved", func(t *testing.T) {
		opts := newOptions()
		opts.Variables = []map[string]string{{"VAR": "value"}}

		result, err := expandWithShellContext(context.Background(), "'${VAR}'", opts)
		require.NoError(t, err)
		assert.Equal(t, "'${VAR}'", result)
	})

	t.Run("VarFollowedBySingleQuote", func(t *testing.T) {
		opts := newOptions()
		opts.Variables = []map[string]string{{"VAR": "value"}}

		result, err := expandWithShellContext(context.Background(), "${VAR}'", opts)
		require.NoError(t, err)
		assert.Equal(t, "value'", result)
	})

	t.Run("SimpleVarFollowedBySingleQuote", func(t *testing.T) {
		opts := newOptions()
		opts.Variables = []map[string]string{{"VAR": "value"}}

		result, err := expandWithShellContext(context.Background(), "$VAR'", opts)
		require.NoError(t, err)
		assert.Equal(t, "value'", result)
	})

	t.Run("MissingVarFollowedBySingleQuoteWithoutExpandOS", func(t *testing.T) {
		opts := newOptions()
		opts.ExpandOS = false

		result, err := expandWithShellContext(context.Background(), "${MISSING}'", opts)
		require.NoError(t, err)
		assert.Equal(t, "${MISSING}'", result)
	})

	t.Run("SingleQuotedSimplePreserved", func(t *testing.T) {
		opts := newOptions()
		opts.Variables = []map[string]string{{"VAR": "value"}}

		result, err := expandWithShellContext(context.Background(), "'$VAR'", opts)
		require.NoError(t, err)
		assert.Equal(t, "'$VAR'", result)
	})
}

func TestShellEnviron(t *testing.T) {
	t.Run("EachIsNoOp", func(t *testing.T) {
		env := &shellEnviron{resolver: &resolver{}}
		called := false
		env.Each(func(_ string, _ expand.Variable) bool {
			called = true
			return true
		})
		assert.False(t, called)
	})

	t.Run("GetDefined", func(t *testing.T) {
		r := &resolver{variables: []map[string]string{{"FOO": "bar"}}}
		env := &shellEnviron{resolver: r}
		v := env.Get("FOO")
		assert.True(t, v.Set)
		assert.Equal(t, "bar", v.Str)
	})

	t.Run("GetUndefined", func(t *testing.T) {
		env := &shellEnviron{resolver: &resolver{}}
		v := env.Get("MISSING")
		assert.False(t, v.Set)
	})
}

func TestExtractPOSIXVarName(t *testing.T) {
	tests := []struct {
		inner string
		want  string
	}{
		{"VAR", "VAR"},
		{"VAR:0:3", "VAR"},
		{"VAR:-default", "VAR"},
		{"VAR:+alt", "VAR"},
		{"VAR:=val", "VAR"},
		{"VAR:?msg", "VAR"},
		{"#VAR", "VAR"},
		{"VAR#pattern", "VAR"},
		{"VAR%pattern", "VAR"},
		{"VAR/old/new", "VAR"},
		{"VAR.path.field", "VAR"},
		{"VAR-default", "VAR"},
		{"VAR+alt", "VAR"},
		{"VAR=val", "VAR"},
		{"VAR?msg", "VAR"},
	}
	for _, tt := range tests {
		t.Run(tt.inner, func(t *testing.T) {
			assert.Equal(t, tt.want, extractPOSIXVarName(tt.inner))
		})
	}
}

func TestExpandPOSIXExpression(t *testing.T) {
	t.Run("EmptyInput", func(t *testing.T) {
		env := &shellEnviron{resolver: &resolver{}}
		result, err := expandPOSIXExpression("", env)
		require.NoError(t, err)
		assert.Equal(t, "", result)
	})
}

func TestExpandEnvScopeOnly(t *testing.T) {
	t.Run("WithScope", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("DAG_VAR", "dag_value", EnvSourceDAGEnv)
		ctx := WithEnvScope(context.Background(), scope)

		result := expandEnvScopeOnly(ctx, "Value is $DAG_VAR")
		assert.Equal(t, "Value is dag_value", result)
	})

	t.Run("NilScope", func(t *testing.T) {
		result := expandEnvScopeOnly(context.Background(), "Value is $DAG_VAR")
		assert.Equal(t, "Value is $DAG_VAR", result)
	})

	t.Run("OSEntriesSkipped", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("OS_VAR", "os_value", EnvSourceOS).
			WithEntry("DAG_VAR", "dag_value", EnvSourceDAGEnv)
		ctx := WithEnvScope(context.Background(), scope)

		result := expandEnvScopeOnly(ctx, "$OS_VAR and $DAG_VAR")
		assert.Equal(t, "$OS_VAR and dag_value", result)
	})
}
