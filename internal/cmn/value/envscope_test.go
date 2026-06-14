// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEnvScope(t *testing.T) {
	t.Run("NoParentNoOS", func(t *testing.T) {
		scope := NewEnvScope(nil, false)
		require.NotNil(t, scope)
		assert.Empty(t, scope.entries)
		assert.Nil(t, scope.parent)
	})

	t.Run("NoParentWithOS", func(t *testing.T) {
		// Set a test environment variable
		key := "TEST_ENVSCOPE_VAR"
		value := "test_value"
		require.NoError(t, os.Setenv(key, value))
		defer func() { _ = os.Unsetenv(key) }()

		scope := NewEnvScope(nil, true)
		require.NotNil(t, scope)
		assert.NotEmpty(t, scope.entries)

		// Verify our test variable is present
		got, ok := scope.Get(key)
		assert.True(t, ok)
		assert.Equal(t, value, got)
	})

	t.Run("WithParent", func(t *testing.T) {
		parent := NewEnvScope(nil, false).
			WithEntry("PARENT_VAR", "parent_value", EnvSourceDAGEnv)

		child := NewEnvScope(parent, false)
		require.NotNil(t, child)
		assert.Equal(t, parent, child.parent)

		// Child should be able to access parent's variables
		got, ok := child.Get("PARENT_VAR")
		assert.True(t, ok)
		assert.Equal(t, "parent_value", got)
	})
}

func TestEnvScope_WithEntry(t *testing.T) {
	t.Run("ImmutableOriginal", func(t *testing.T) {
		original := NewEnvScope(nil, false)
		modified := original.WithEntry("NEW_KEY", "new_value", EnvSourceDAGEnv)

		// Original should be unchanged
		_, ok := original.Get("NEW_KEY")
		assert.False(t, ok, "original scope should not have the new key")

		// Modified should have the value
		got, ok := modified.Get("NEW_KEY")
		assert.True(t, ok)
		assert.Equal(t, "new_value", got)
	})

	t.Run("ChainedEntries", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("KEY1", "value1", EnvSourceDAGEnv).
			WithEntry("KEY2", "value2", EnvSourceParam)

		val1, ok := scope.Get("KEY1")
		assert.True(t, ok)
		assert.Equal(t, "value1", val1)

		val2, ok := scope.Get("KEY2")
		assert.True(t, ok)
		assert.Equal(t, "value2", val2)
	})

	t.Run("OverrideWithDifferentSource", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("KEY", "original", EnvSourceDAGEnv).
			WithEntry("KEY", "updated", EnvSourceOutput)

		got, ok := scope.Get("KEY")
		assert.True(t, ok)
		assert.Equal(t, "updated", got)

		// Verify source was updated too
		entry, ok := scope.GetEntry("KEY")
		assert.True(t, ok)
		assert.Equal(t, EnvSourceOutput, entry.Source)
	})

	t.Run("DifferentSources", func(t *testing.T) {
		scope := NewEnvScope(nil, false)
		sources := []EnvSource{
			EnvSourceOS,
			EnvSourceDAGEnv,
			EnvSourceDotEnv,
			EnvSourceParam,
			EnvSourceOutput,
			EnvSourceSecret,
			EnvSourceStepEnv,
		}

		for _, src := range sources {
			key := "KEY_" + string(src)
			scope = scope.WithEntry(key, "value", src)
		}

		for _, src := range sources {
			key := "KEY_" + string(src)
			entry, ok := scope.GetEntry(key)
			assert.True(t, ok, "source %s", src)
			assert.Equal(t, src, entry.Source, "source %s", src)
		}
	})
}

func TestEnvScope_WithEntries(t *testing.T) {
	t.Run("AddMultipleEntries", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntries(map[string]string{
				"A": "1",
				"B": "2",
				"C": "3",
			}, EnvSourceDAGEnv)

		val, ok := scope.Get("A")
		require.True(t, ok, "expected A to be present")
		assert.Equal(t, "1", val)

		val, ok = scope.Get("B")
		require.True(t, ok, "expected B to be present")
		assert.Equal(t, "2", val)

		val, ok = scope.Get("C")
		require.True(t, ok, "expected C to be present")
		assert.Equal(t, "3", val)
	})

	t.Run("EmptyMapReturnsOriginal", func(t *testing.T) {
		original := NewEnvScope(nil, false)
		result := original.WithEntries(map[string]string{}, EnvSourceDAGEnv)
		assert.Same(t, original, result, "empty entries should return same scope")
	})
}

func TestEnvScope_WithStepOutputs(t *testing.T) {
	t.Run("AddsOutputsWithOrigin", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithStepOutputs(map[string]string{
				"RESULT": "42",
			}, "step1")

		entry, ok := scope.GetEntry("RESULT")
		assert.True(t, ok)
		assert.Equal(t, "42", entry.Value)
		assert.Equal(t, EnvSourceOutput, entry.Source)
		assert.Equal(t, "step1", entry.Origin)
	})

	t.Run("EmptyMapReturnsOriginal", func(t *testing.T) {
		original := NewEnvScope(nil, false)
		result := original.WithStepOutputs(map[string]string{}, "step1")
		assert.Same(t, original, result, "empty outputs should return same scope")
	})
}

func TestEnvScope_Get(t *testing.T) {
	t.Run("KeyInCurrentScope", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("LOCAL_KEY", "local_value", EnvSourceDAGEnv)

		got, ok := scope.Get("LOCAL_KEY")
		assert.True(t, ok)
		assert.Equal(t, "local_value", got)
	})

	t.Run("KeyInParentScope", func(t *testing.T) {
		parent := NewEnvScope(nil, false).
			WithEntry("PARENT_KEY", "parent_value", EnvSourceDAGEnv)

		child := NewEnvScope(parent, false)

		got, ok := child.Get("PARENT_KEY")
		assert.True(t, ok)
		assert.Equal(t, "parent_value", got)
	})

	t.Run("KeyNotFound", func(t *testing.T) {
		scope := NewEnvScope(nil, false)

		got, ok := scope.Get("NONEXISTENT")
		assert.False(t, ok)
		assert.Empty(t, got)
	})

	t.Run("ChildOverridesParent", func(t *testing.T) {
		parent := NewEnvScope(nil, false).
			WithEntry("KEY", "parent_value", EnvSourceDAGEnv)

		child := NewEnvScope(parent, false).
			WithEntry("KEY", "child_value", EnvSourceOutput)

		got, ok := child.Get("KEY")
		assert.True(t, ok)
		assert.Equal(t, "child_value", got)

		// Parent should still have original value
		parentGot, ok := parent.Get("KEY")
		assert.True(t, ok)
		assert.Equal(t, "parent_value", parentGot)
	})
}

func TestEnvScope_GetEntry(t *testing.T) {
	t.Run("EntryInCurrentScope", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("KEY", "value", EnvSourceSecret)

		entry, ok := scope.GetEntry("KEY")
		assert.True(t, ok)
		assert.Equal(t, "KEY", entry.Key)
		assert.Equal(t, "value", entry.Value)
		assert.Equal(t, EnvSourceSecret, entry.Source)
	})

	t.Run("EntryInParentScope", func(t *testing.T) {
		parent := NewEnvScope(nil, false).
			WithEntry("PARENT_KEY", "parent_value", EnvSourceDotEnv)

		child := NewEnvScope(parent, false)

		entry, ok := child.GetEntry("PARENT_KEY")
		assert.True(t, ok)
		assert.Equal(t, "PARENT_KEY", entry.Key)
		assert.Equal(t, "parent_value", entry.Value)
		assert.Equal(t, EnvSourceDotEnv, entry.Source)
	})

	t.Run("EntryNotFound", func(t *testing.T) {
		scope := NewEnvScope(nil, false)

		entry, ok := scope.GetEntry("NONEXISTENT")
		assert.False(t, ok)
		assert.Empty(t, entry.Key)
		assert.Empty(t, entry.Value)
	})
}

func TestEnvScope_ToSlice(t *testing.T) {
	t.Run("EmptyScope", func(t *testing.T) {
		scope := NewEnvScope(nil, false)
		result := scope.ToSlice()
		assert.Empty(t, result)
	})

	t.Run("WithEntries", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("A", "1", EnvSourceDAGEnv).
			WithEntry("B", "2", EnvSourceDAGEnv)

		result := scope.ToSlice()
		assert.Len(t, result, 2)
		assert.Contains(t, result, "A=1")
		assert.Contains(t, result, "B=2")
	})

	t.Run("WithParentOverride", func(t *testing.T) {
		parent := NewEnvScope(nil, false).
			WithEntry("SHARED", "parent", EnvSourceDAGEnv).
			WithEntry("PARENT_ONLY", "p", EnvSourceDAGEnv)

		child := NewEnvScope(parent, false).
			WithEntry("SHARED", "child", EnvSourceOutput).
			WithEntry("CHILD_ONLY", "c", EnvSourceOutput)

		result := child.ToSlice()

		// Convert to map for easier checking
		resultMap := make(map[string]string)
		for _, s := range result {
			if k, v, ok := strings.Cut(s, "="); ok {
				resultMap[k] = v
			}
		}

		assert.Equal(t, "child", resultMap["SHARED"], "child should override parent")
		assert.Equal(t, "p", resultMap["PARENT_ONLY"])
		assert.Equal(t, "c", resultMap["CHILD_ONLY"])
	})
}

func TestEnvScope_ToMap(t *testing.T) {
	t.Run("EmptyScope", func(t *testing.T) {
		scope := NewEnvScope(nil, false)
		result := scope.ToMap()
		assert.Empty(t, result)
	})

	t.Run("WithEntries", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("X", "10", EnvSourceDAGEnv).
			WithEntry("Y", "20", EnvSourceDAGEnv)

		result := scope.ToMap()
		assert.Len(t, result, 2)
		assert.Equal(t, "10", result["X"])
		assert.Equal(t, "20", result["Y"])
	})

	t.Run("WithParent", func(t *testing.T) {
		parent := NewEnvScope(nil, false).
			WithEntry("A", "1", EnvSourceDAGEnv).
			WithEntry("B", "2", EnvSourceDAGEnv)

		child := NewEnvScope(parent, false).
			WithEntry("B", "overridden", EnvSourceOutput).
			WithEntry("C", "3", EnvSourceOutput)

		result := child.ToMap()
		assert.Len(t, result, 3)
		assert.Equal(t, "1", result["A"])
		assert.Equal(t, "overridden", result["B"])
		assert.Equal(t, "3", result["C"])
	})
}

func TestEnvScope_Expand(t *testing.T) {
	t.Run("VariableExists", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("NAME", "World", EnvSourceDAGEnv)

		result := scope.Expand("Hello, $NAME!")
		assert.Equal(t, "Hello, World!", result)
	})

	t.Run("VariableWithBraces", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("NAME", "World", EnvSourceDAGEnv)

		result := scope.Expand("Hello, ${NAME}!")
		assert.Equal(t, "Hello, World!", result)
	})

	t.Run("VariableNotFoundPreserved", func(t *testing.T) {
		scope := NewEnvScope(nil, false)

		// $VAR syntax preserved
		result := scope.Expand("Hello, $UNKNOWN!")
		assert.Equal(t, "Hello, $UNKNOWN!", result)

		// ${VAR} syntax preserved
		result = scope.Expand("Hello, ${UNKNOWN}!")
		assert.Equal(t, "Hello, ${UNKNOWN}!", result)
	})

	t.Run("MixedKnownAndUnknown", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("KNOWN", "found", EnvSourceDAGEnv)

		result := scope.Expand("$KNOWN and $UNKNOWN")
		assert.Equal(t, "found and $UNKNOWN", result)

		result = scope.Expand("${KNOWN} and ${UNKNOWN}")
		assert.Equal(t, "found and ${UNKNOWN}", result)
	})

	t.Run("MultipleVariables", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("FIRST", "Hello", EnvSourceDAGEnv).
			WithEntry("SECOND", "World", EnvSourceDAGEnv)

		result := scope.Expand("$FIRST, $SECOND!")
		assert.Equal(t, "Hello, World!", result)
	})

	t.Run("VariableFromParent", func(t *testing.T) {
		parent := NewEnvScope(nil, false).
			WithEntry("PARENT_VAR", "from_parent", EnvSourceDAGEnv)

		child := NewEnvScope(parent, false).
			WithEntry("CHILD_VAR", "from_child", EnvSourceOutput)

		result := child.Expand("$PARENT_VAR and $CHILD_VAR")
		assert.Equal(t, "from_parent and from_child", result)
	})

	t.Run("NoVariables", func(t *testing.T) {
		scope := NewEnvScope(nil, false)
		result := scope.Expand("plain text")
		assert.Equal(t, "plain text", result)
	})
}

func TestEnvScope_Debug(t *testing.T) {
	t.Run("EmptyScope", func(t *testing.T) {
		scope := NewEnvScope(nil, false)
		result := scope.Debug()
		assert.Contains(t, result, "EnvScope{")
		assert.Contains(t, result, "}")
	})

	t.Run("WithEntries", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("KEY", "value", EnvSourceDAGEnv)

		result := scope.Debug()
		assert.Contains(t, result, "EnvScope{")
		assert.Contains(t, result, "KEY")
		assert.Contains(t, result, "value")
		assert.Contains(t, result, "dag_env")
		assert.Contains(t, result, "}")
	})

	t.Run("WithParent", func(t *testing.T) {
		parent := NewEnvScope(nil, false)
		child := NewEnvScope(parent, false)

		result := child.Debug()
		assert.Contains(t, result, "parent: <yes>")
	})

	t.Run("WithoutParent", func(t *testing.T) {
		scope := NewEnvScope(nil, false)
		result := scope.Debug()
		assert.NotContains(t, result, "parent:")
	})
}

func TestWithEnvScope(t *testing.T) {
	scope := NewEnvScope(nil, false).
		WithEntry("TEST", "value", EnvSourceDAGEnv)

	ctx := context.Background()
	ctxWithScope := WithEnvScope(ctx, scope)

	retrieved := GetEnvScope(ctxWithScope)
	require.NotNil(t, retrieved)

	got, ok := retrieved.Get("TEST")
	assert.True(t, ok)
	assert.Equal(t, "value", got)
}

func TestGetEnvScope(t *testing.T) {
	t.Run("NilContext", func(t *testing.T) {
		result := GetEnvScope(nil) //nolint:staticcheck // testing nil context handling
		assert.Nil(t, result)
	})

	t.Run("ContextWithoutScope", func(t *testing.T) {
		ctx := context.Background()
		result := GetEnvScope(ctx)
		assert.Nil(t, result)
	})

	t.Run("ContextWithScope", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("KEY", "value", EnvSourceDAGEnv)

		ctx := WithEnvScope(context.Background(), scope)
		result := GetEnvScope(ctx)

		require.NotNil(t, result)
		got, ok := result.Get("KEY")
		assert.True(t, ok)
		assert.Equal(t, "value", got)
	})
}

func TestEnvScope_AllBySource(t *testing.T) {
	t.Run("SingleSource", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("SECRET1", "val1", EnvSourceSecret).
			WithEntry("SECRET2", "val2", EnvSourceSecret).
			WithEntry("DAG_VAR", "dag_val", EnvSourceDAGEnv)

		secrets := scope.AllBySource(EnvSourceSecret)
		assert.Len(t, secrets, 2)
		assert.Equal(t, "val1", secrets["SECRET1"])
		assert.Equal(t, "val2", secrets["SECRET2"])

		dagVars := scope.AllBySource(EnvSourceDAGEnv)
		assert.Len(t, dagVars, 1)
		assert.Equal(t, "dag_val", dagVars["DAG_VAR"])
	})

	t.Run("WithParentScopes", func(t *testing.T) {
		parent := NewEnvScope(nil, false).
			WithEntry("PARENT_SECRET", "parent_val", EnvSourceSecret)

		child := NewEnvScope(parent, false).
			WithEntry("CHILD_SECRET", "child_val", EnvSourceSecret)

		secrets := child.AllBySource(EnvSourceSecret)
		assert.Len(t, secrets, 2)
		assert.Equal(t, "parent_val", secrets["PARENT_SECRET"])
		assert.Equal(t, "child_val", secrets["CHILD_SECRET"])
	})

	t.Run("ChildOverridesParent", func(t *testing.T) {
		parent := NewEnvScope(nil, false).
			WithEntry("KEY", "parent", EnvSourceSecret)

		child := NewEnvScope(parent, false).
			WithEntry("KEY", "child", EnvSourceSecret)

		secrets := child.AllBySource(EnvSourceSecret)
		assert.Len(t, secrets, 1)
		assert.Equal(t, "child", secrets["KEY"])
	})

	t.Run("NoMatchingSource", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("KEY", "value", EnvSourceDAGEnv)

		secrets := scope.AllBySource(EnvSourceSecret)
		assert.Empty(t, secrets)
	})
}

func TestEnvScope_AllSecrets(t *testing.T) {
	scope := NewEnvScope(nil, false).
		WithEntry("DB_PASSWORD", "secret123", EnvSourceSecret).
		WithEntry("API_KEY", "key456", EnvSourceSecret).
		WithEntry("NORMAL_VAR", "not_secret", EnvSourceDAGEnv)

	secrets := scope.AllSecrets()
	assert.Len(t, secrets, 2)
	assert.Equal(t, "secret123", secrets["DB_PASSWORD"])
	assert.Equal(t, "key456", secrets["API_KEY"])
	assert.NotContains(t, secrets, "NORMAL_VAR")
}

func TestEnvScope_AllUserEnvs(t *testing.T) {
	t.Run("ExcludesOSEnv", func(t *testing.T) {
		// Create scope with OS env
		scope := NewEnvScope(nil, true).
			WithEntry("USER_VAR", "user_value", EnvSourceDAGEnv).
			WithEntry("SECRET_VAR", "secret", EnvSourceSecret)

		userEnvs := scope.AllUserEnvs()
		assert.Contains(t, userEnvs, "USER_VAR")
		assert.Contains(t, userEnvs, "SECRET_VAR")
		// Should not contain any OS env vars in the result
		for k := range userEnvs {
			entry, ok := scope.GetEntry(k)
			if ok {
				assert.NotEqual(t, EnvSourceOS, entry.Source, "OS env var %s should not be in user envs", k)
			}
		}
	})

	t.Run("EmptyScope", func(t *testing.T) {
		scope := NewEnvScope(nil, false)
		userEnvs := scope.AllUserEnvs()
		assert.Empty(t, userEnvs)
	})

	t.Run("WithParentScopes", func(t *testing.T) {
		parent := NewEnvScope(nil, false).
			WithEntry("PARENT_VAR", "parent", EnvSourceParam)

		child := NewEnvScope(parent, false).
			WithEntry("CHILD_VAR", "child", EnvSourceDAGEnv)

		userEnvs := child.AllUserEnvs()
		assert.Len(t, userEnvs, 2)
		assert.Equal(t, "parent", userEnvs["PARENT_VAR"])
		assert.Equal(t, "child", userEnvs["CHILD_VAR"])
	})
}

func TestEnvScope_Provenance(t *testing.T) {
	t.Run("WithOrigin", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithStepOutputs(map[string]string{"RESULT": "42"}, "step1")

		prov := scope.Provenance("RESULT")
		assert.Contains(t, prov, "output")
		assert.Contains(t, prov, "step1")
	})

	t.Run("WithoutOrigin", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("KEY", "value", EnvSourceDAGEnv)

		prov := scope.Provenance("KEY")
		assert.Equal(t, "dag_env", prov)
	})

	t.Run("KeyNotFound", func(t *testing.T) {
		scope := NewEnvScope(nil, false)
		prov := scope.Provenance("NONEXISTENT")
		assert.Empty(t, prov)
	})
}

func TestEnvSourceStep_Alias(t *testing.T) {
	// EnvSourceStep should be an alias for EnvSourceOutput
	assert.Equal(t, EnvSourceOutput, EnvSourceStep)
}

func TestEnvScope_Provenance_FullChain(t *testing.T) {
	// Build a scope chain with all source types to test full provenance tracking
	scope := NewEnvScope(nil, false).
		WithEntries(map[string]string{"DOTENV_VAR": "dotenv"}, EnvSourceDotEnv).
		WithEntries(map[string]string{"DAG_VAR": "dag"}, EnvSourceDAGEnv).
		WithEntries(map[string]string{"PARAM_VAR": "param"}, EnvSourceParam).
		WithStepOutputs(map[string]string{"OUTPUT_VAR": "output"}, "step1").
		WithEntries(map[string]string{"SECRET_VAR": "secret"}, EnvSourceSecret).
		WithEntries(map[string]string{"STEP_VAR": "step"}, EnvSourceStepEnv)

	// Verify each provenance returns correct source name
	assert.Equal(t, "dotenv", scope.Provenance("DOTENV_VAR"))
	assert.Equal(t, "dag_env", scope.Provenance("DAG_VAR"))
	assert.Equal(t, "param", scope.Provenance("PARAM_VAR"))
	assert.Contains(t, scope.Provenance("OUTPUT_VAR"), "output")
	assert.Contains(t, scope.Provenance("OUTPUT_VAR"), "step1")
	assert.Equal(t, "secret", scope.Provenance("SECRET_VAR"))
	assert.Equal(t, "step_env", scope.Provenance("STEP_VAR"))

	// Verify unknown key returns empty
	assert.Empty(t, scope.Provenance("UNKNOWN_VAR"))
}

func TestEnvScope_Immutability_Concurrent(t *testing.T) {
	original := NewEnvScope(nil, false).
		WithEntry("KEY", "original", EnvSourceDAGEnv)

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each goroutine creates its own branch - should not affect original
			_ = original.WithEntry("KEY", fmt.Sprintf("modified-%d", i), EnvSourceStepEnv)
		}(i)
	}
	wg.Wait()

	// Original must be unchanged
	val, ok := original.Get("KEY")
	assert.True(t, ok)
	assert.Equal(t, "original", val)
}

func TestEnvScope_NilReceiverHandling(t *testing.T) {
	t.Run("Get", func(t *testing.T) {
		var scope *EnvScope
		val, ok := scope.Get("KEY")
		assert.False(t, ok)
		assert.Empty(t, val)
	})

	t.Run("GetEntry", func(t *testing.T) {
		var scope *EnvScope
		entry, ok := scope.GetEntry("KEY")
		assert.False(t, ok)
		assert.Empty(t, entry.Key)
	})

	t.Run("ToSlice", func(t *testing.T) {
		var scope *EnvScope
		result := scope.ToSlice()
		assert.Nil(t, result)
	})

	t.Run("ToMap", func(t *testing.T) {
		var scope *EnvScope
		result := scope.ToMap()
		assert.NotNil(t, result, "should return empty map, not nil")
		assert.Empty(t, result)
	})

	t.Run("Expand", func(t *testing.T) {
		var scope *EnvScope
		result := scope.Expand("$FOO")
		assert.Equal(t, "$FOO", result, "nil scope should return input unchanged")
	})

	t.Run("AllBySource", func(t *testing.T) {
		var scope *EnvScope
		result := scope.AllBySource(EnvSourceSecret)
		assert.NotNil(t, result, "should return empty map, not nil")
		assert.Empty(t, result)
	})

	t.Run("AllSecrets", func(t *testing.T) {
		var scope *EnvScope
		result := scope.AllSecrets()
		assert.NotNil(t, result, "should return empty map, not nil")
		assert.Empty(t, result)
	})

	t.Run("AllUserEnvs", func(t *testing.T) {
		var scope *EnvScope
		result := scope.AllUserEnvs()
		assert.NotNil(t, result, "should return empty map, not nil")
		assert.Empty(t, result)
	})

	t.Run("Provenance", func(t *testing.T) {
		var scope *EnvScope
		result := scope.Provenance("KEY")
		assert.Empty(t, result)
	})
}

func TestEnvScope_AllSecrets_EmptyAndNil(t *testing.T) {
	t.Run("NoSecrets", func(t *testing.T) {
		scope := NewEnvScope(nil, false).
			WithEntry("DAG_VAR", "value", EnvSourceDAGEnv).
			WithEntry("STEP_VAR", "value", EnvSourceStepEnv)

		secrets := scope.AllSecrets()
		assert.Empty(t, secrets, "no secrets should return empty map")
	})

	t.Run("SecretsInParent", func(t *testing.T) {
		parent := NewEnvScope(nil, false).
			WithEntry("PARENT_SECRET", "parent_secret_val", EnvSourceSecret)

		child := NewEnvScope(parent, false).
			WithEntry("CHILD_VAR", "val", EnvSourceDAGEnv)

		secrets := child.AllSecrets()
		assert.Len(t, secrets, 1)
		assert.Equal(t, "parent_secret_val", secrets["PARENT_SECRET"])
	})
}

func TestExpandWithLookup_SingleQuoted(t *testing.T) {
	lookup := func(key string) (string, bool) {
		if key == "FOO" {
			return "bar", true
		}
		return "", false
	}
	result := expandWithLookup("'$FOO' stays", lookup)
	assert.Equal(t, "'$FOO' stays", result)
}

func TestExpandWithLookup_QuoteAdjacentCases(t *testing.T) {
	lookup := func(key string) (string, bool) {
		switch key {
		case "FOO":
			return "bar", true
		case "BAR":
			return "baz", true
		default:
			return "", false
		}
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "BracedVarFollowedBySingleQuote",
			input: "${FOO}'",
			want:  "bar'",
		},
		{
			name:  "SimpleVarFollowedBySingleQuote",
			input: "$FOO'",
			want:  "bar'",
		},
		{
			name:  "SingleQuotedBracedPreserved",
			input: "'${FOO}'",
			want:  "'${FOO}'",
		},
		{
			name:  "SingleQuotedSimplePreserved",
			input: "'$FOO'",
			want:  "'$FOO'",
		},
		{
			name:  "MissingBracedVarFollowedBySingleQuote",
			input: "${MISSING}'",
			want:  "${MISSING}'",
		},
		{
			name:  "MultipleVarsWithQuoteAdjacency",
			input: "${FOO}' + $BAR'",
			want:  "bar' + baz'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := expandWithLookup(tt.input, lookup)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEnvScope_Debug_NoParent(t *testing.T) {
	scope := NewEnvScope(nil, true)
	debug := scope.Debug()
	assert.Contains(t, debug, "EnvScope{")
	assert.NotContains(t, debug, "parent: <yes>")
}

func TestEnvScope_Debug_NilScope(t *testing.T) {
	var scope *EnvScope
	debug := scope.Debug()
	assert.Equal(t, "EnvScope{nil}", debug)
}

func TestCollectBySource_NilScope(t *testing.T) {
	var scope *EnvScope
	result := scope.AllBySource(EnvSourceDAGEnv)
	assert.Empty(t, result)
}

func TestCollectBySource_WithParent(t *testing.T) {
	parent := NewEnvScope(nil, false).
		WithEntry("PKEY", "pval", EnvSourceDAGEnv)
	child := NewEnvScope(parent, false).
		WithEntry("CKEY", "cval", EnvSourceDAGEnv)

	result := child.AllBySource(EnvSourceDAGEnv)
	assert.Equal(t, "pval", result["PKEY"])
	assert.Equal(t, "cval", result["CKEY"])
}

func TestEnvScope_ExcludesInternalTransportVars(t *testing.T) {
	t.Setenv("_DAGU_PRESOLVED_SECRET_FOO", "bar")
	t.Setenv("_DAGU_PRESOLVED_BUILD_ENV_FILE", "/tmp/buildenv.json")
	t.Setenv("NORMAL_VAR", "visible")

	scope := NewEnvScope(nil, true)

	_, exists := scope.Get("_DAGU_PRESOLVED_SECRET_FOO")
	require.False(t, exists, "presolved secret vars must not enter scope")
	_, exists = scope.Get("_DAGU_PRESOLVED_BUILD_ENV_FILE")
	require.False(t, exists, "presolved build env transport vars must not enter scope")

	val, exists := scope.Get("NORMAL_VAR")
	require.True(t, exists)
	require.Equal(t, "visible", val)
}
