// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStringFields(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping backtick tests on Windows")
	}
	t.Setenv("TEST_VAR", "test_value")
	t.Setenv("NESTED_VAR", "nested_value")

	type Nested struct {
		NestedField   string
		NestedCommand string
		unexported    string
	}

	type TestStruct struct {
		SimpleField  string
		EnvField     string
		CommandField string
		MultiField   string
		EmptyField   string
		unexported   string
		NestedStruct Nested
	}

	tests := []struct {
		name    string
		input   TestStruct
		want    TestStruct
		wantErr bool
	}{
		{
			name: "BasicSubstitution",
			input: TestStruct{
				SimpleField:  "hello",
				EnvField:     "$TEST_VAR",
				CommandField: "`echo hello`",
				MultiField:   "$TEST_VAR and `echo command`",
				EmptyField:   "",
				NestedStruct: Nested{
					NestedField:   "$NESTED_VAR",
					NestedCommand: "`echo nested`",
					unexported:    "should not change",
				},
				unexported: "should not change",
			},
			want: TestStruct{
				SimpleField:  "hello",
				EnvField:     "test_value",
				CommandField: "hello",
				MultiField:   "test_value and command",
				EmptyField:   "",
				NestedStruct: Nested{
					NestedField:   "nested_value",
					NestedCommand: "nested",
					unexported:    "should not change",
				},
				unexported: "should not change",
			},
		},
		{
			name: "InvalidCommand",
			input: TestStruct{
				CommandField: "`invalid_command_that_does_not_exist`",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got, err := stringFields(ctx, tt.input, withOSExpansion())
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStringFields_AnonymousStruct(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping backtick tests on Windows")
	}
	ctx := context.Background()
	obj, err := stringFields(ctx, struct {
		Field string
	}{
		Field: "`echo hello`",
	})
	require.NoError(t, err)
	require.Equal(t, "hello", obj.Field)
}

func TestStringFields_NonStruct(t *testing.T) {
	ctx := context.Background()
	_, err := stringFields(ctx, "not a struct")
	assert.Error(t, err)
}

func TestStringFields_NestedStructs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping backtick tests on Windows")
	}
	type DeepNested struct {
		Field string
	}

	type Nested struct {
		Field      string
		DeepNested DeepNested
	}

	type Root struct {
		Field  string
		Nested Nested
	}

	input := Root{
		Field: "$TEST_VAR",
		Nested: Nested{
			Field: "`echo nested`",
			DeepNested: DeepNested{
				Field: "$NESTED_VAR",
			},
		},
	}

	t.Setenv("TEST_VAR", "test_value")
	t.Setenv("NESTED_VAR", "deep_nested_value")

	want := Root{
		Field: "test_value",
		Nested: Nested{
			Field: "nested",
			DeepNested: DeepNested{
				Field: "deep_nested_value",
			},
		},
	}

	ctx := context.Background()
	got, err := stringFields(ctx, input, withOSExpansion())
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestStringFields_EmptyStruct(t *testing.T) {
	type Empty struct{}

	input := Empty{}
	ctx := context.Background()
	got, err := stringFields(ctx, input)
	require.NoError(t, err)
	assert.Equal(t, input, got)
}

func TestStringFields_Map(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping backtick tests on Windows")
	}
	t.Setenv("MAP_ENV", "map_value")

	tests := []struct {
		name    string
		input   map[string]any
		opts    []option
		want    map[string]any
		wantErr bool
	}{
		{
			name: "SimpleMapWithStringValues",
			input: map[string]any{
				"key1": "$MAP_ENV",
				"key2": "`echo hello`",
				"key3": "plain",
			},
			opts: []option{withOSExpansion()},
			want: map[string]any{
				"key1": "map_value",
				"key2": "hello",
				"key3": "plain",
			},
		},
		{
			name: "NestedMap",
			input: map[string]any{
				"outer": map[string]any{
					"inner": "$MAP_ENV",
				},
			},
			opts: []option{withOSExpansion()},
			want: map[string]any{
				"outer": map[string]any{
					"inner": "map_value",
				},
			},
		},
		{
			name: "MapWithNonStringValues",
			input: map[string]any{
				"string": "$MAP_ENV",
				"int":    42,
				"bool":   true,
				"nil":    nil,
			},
			opts: []option{withOSExpansion()},
			want: map[string]any{
				"string": "map_value",
				"int":    42,
				"bool":   true,
				"nil":    nil,
			},
		},
		{
			name: "MapWithStructValue",
			input: map[string]any{
				"struct": struct {
					Field string
				}{
					Field: "$MAP_ENV",
				},
			},
			opts: []option{withOSExpansion()},
			want: map[string]any{
				"struct": struct {
					Field string
				}{
					Field: "map_value",
				},
			},
		},
		{
			name: "WithVariablesOption",
			input: map[string]any{
				"key": "${VAR}",
			},
			opts: []option{withVariables(map[string]string{"VAR": "value"})},
			want: map[string]any{
				"key": "value",
			},
		},
		{
			name: "MapWithPointerValues",
			input: map[string]any{
				"ptr": new("$MAP_ENV"),
			},
			opts: []option{withOSExpansion()},
			want: map[string]any{
				"ptr": "map_value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got, err := stringFields(ctx, tt.input, tt.opts...)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestProcessStructFields_WithStepMap(t *testing.T) {
	type TestStruct struct {
		StepOutput string
		StepError  string
	}

	input := TestStruct{
		StepOutput: "${step1.stdout}",
		StepError:  "${step1.stderr}",
	}

	ctx := context.Background()
	got, err := stringFields(ctx, input,
		withStepMap(map[string]StepInfo{
			"step1": {
				Stdout: "/tmp/out.txt",
				Stderr: "/tmp/err.txt",
			},
		}),
	)

	require.NoError(t, err)
	assert.Equal(t, "/tmp/out.txt", got.StepOutput)
	assert.Equal(t, "/tmp/err.txt", got.StepError)
}

func TestProcessMap_WithStepMap(t *testing.T) {
	input := map[string]any{
		"output": "${step1.stdout}",
		"nested": map[string]any{
			"exit_code": "${step1.exit_code}",
		},
	}

	ctx := context.Background()
	got, err := stringFields(ctx, input,
		withStepMap(map[string]StepInfo{
			"step1": {
				Stdout:   "/tmp/output",
				ExitCode: "0",
			},
		}),
	)

	require.NoError(t, err)
	assert.Equal(t, "/tmp/output", got["output"])
	nested, ok := got["nested"].(map[string]any)
	require.True(t, ok, "Expected nested to be map[string]any, got %T", got["nested"])
	assert.Equal(t, "0", nested["exit_code"])
}

func TestStringFields_MultipleVariablesWithStepMapOnLast(t *testing.T) {
	type TestStruct struct {
		Field1 string
		Field2 string
		Field3 string
		Field4 string
	}

	stepMap := map[string]StepInfo{
		"build": {
			Stdout:   "/logs/build.out",
			Stderr:   "/logs/build.err",
			ExitCode: "0",
		},
		"test": {
			Stdout: "/logs/test.out",
		},
	}

	tests := []struct {
		name     string
		input    TestStruct
		varSets  []map[string]string
		expected TestStruct
	}{
		{
			name: "ThreeVariableSetsWithStepReferences",
			input: TestStruct{
				Field1: "${A}",
				Field2: "${B}",
				Field3: "${C}",
				Field4: "${build.stderr}",
			},
			varSets: []map[string]string{
				{"A": "alpha"},
				{"B": "beta"},
				{"C": "gamma"},
			},
			expected: TestStruct{
				Field1: "alpha",
				Field2: "beta",
				Field3: "gamma",
				Field4: "/logs/build.err",
			},
		},
		{
			name: "StepReferencesOnlyOnLastVariableSet",
			input: TestStruct{
				Field1: "${VAR1}",
				Field2: "${VAR2}",
				Field3: "${test.stdout}",
				Field4: "${VAR3}",
			},
			varSets: []map[string]string{
				{"VAR1": "first"},
				{"VAR2": "second"},
				{"VAR3": "third"},
			},
			expected: TestStruct{
				Field1: "first",
				Field2: "second",
				Field3: "/logs/test.out",
				Field4: "third",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			var opts []option
			for _, vars := range tt.varSets {
				opts = append(opts, withVariables(vars))
			}
			opts = append(opts, withStepMap(stepMap))

			result, err := stringFields(ctx, tt.input, opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStringFields_ErrorCases(t *testing.T) {
	t.Run("UnsupportedType", func(t *testing.T) {
		ch := make(chan int)
		_, err := stringFields(context.Background(), ch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "input must be a struct or map")
	})

	t.Run("StructWithInvalidCommand", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Skipping backtick tests on Windows")
		}
		type TestStruct struct {
			Field string
		}
		_, err := stringFields(context.Background(), TestStruct{
			Field: "`invalid_command_xyz`",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to execute command")
	})

	t.Run("MapWithInvalidCommand", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Skipping backtick tests on Windows")
		}
		_, err := stringFields(context.Background(), map[string]any{
			"key": "`invalid_command_xyz`",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to execute command")
	})
}

func TestObject_String(t *testing.T) {
	ctx := context.Background()
	vars := map[string]string{
		"NAME":  "World",
		"VALUE": "42",
	}

	t.Run("SimpleVariableSubstitution", func(t *testing.T) {
		input := "Hello, $NAME!"
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Equal(t, "Hello, World!", result)
	})

	t.Run("MultipleVariables", func(t *testing.T) {
		input := "$NAME has value $VALUE"
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Equal(t, "World has value 42", result)
	})

	t.Run("BracedVariable", func(t *testing.T) {
		input := "${NAME}Test"
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Equal(t, "WorldTest", result)
	})

	t.Run("NoVariables", func(t *testing.T) {
		input := "plain text"
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Equal(t, "plain text", result)
	})

	t.Run("EmptyString", func(t *testing.T) {
		input := ""
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Equal(t, "", result)
	})
}

func TestObject_Struct(t *testing.T) {
	ctx := context.Background()
	vars := map[string]string{
		"HOST": "localhost",
		"PORT": "8080",
	}

	type Config struct {
		Host    string
		Port    string
		Timeout int
	}

	t.Run("StructWithStringFields", func(t *testing.T) {
		input := Config{
			Host:    "$HOST",
			Port:    "$PORT",
			Timeout: 30,
		}
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Equal(t, "localhost", result.Host)
		assert.Equal(t, "8080", result.Port)
		assert.Equal(t, 30, result.Timeout)
	})

	t.Run("StructWithNoVariables", func(t *testing.T) {
		input := Config{
			Host:    "example.com",
			Port:    "443",
			Timeout: 60,
		}
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Equal(t, "example.com", result.Host)
		assert.Equal(t, "443", result.Port)
		assert.Equal(t, 60, result.Timeout)
	})
}

func TestObject_Map(t *testing.T) {
	ctx := context.Background()
	vars := map[string]string{
		"KEY":   "resolved_key",
		"VALUE": "resolved_value",
	}

	t.Run("SimpleMapWithStringValues", func(t *testing.T) {
		input := map[string]string{
			"key1": "$KEY",
			"key2": "$VALUE",
		}
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Equal(t, "resolved_key", result["key1"])
		assert.Equal(t, "resolved_value", result["key2"])
	})

	t.Run("MapWithInterfaceValues", func(t *testing.T) {
		input := map[string]any{
			"str":    "$KEY",
			"number": 42,
		}
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Equal(t, "resolved_key", result["str"])
		assert.Equal(t, 42, result["number"])
	})

	t.Run("NestedMap", func(t *testing.T) {
		input := map[string]any{
			"outer": map[string]any{
				"inner": "$VALUE",
			},
		}
		result, err := object(ctx, input, vars)
		require.NoError(t, err)

		outerMap, ok := result["outer"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "resolved_value", outerMap["inner"])
	})

	t.Run("EmptyMap", func(t *testing.T) {
		input := map[string]string{}
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

func TestObject_Slice(t *testing.T) {
	ctx := context.Background()
	vars := map[string]string{
		"ITEM1": "first",
		"ITEM2": "second",
	}

	t.Run("SliceOfStrings", func(t *testing.T) {
		input := []string{"$ITEM1", "$ITEM2", "literal"}
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Equal(t, []string{"first", "second", "literal"}, result)
	})

	t.Run("SliceOfInterface", func(t *testing.T) {
		input := []any{"$ITEM1", 42, "$ITEM2"}
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Equal(t, "first", result[0])
		assert.Equal(t, 42, result[1])
		assert.Equal(t, "second", result[2])
	})

	t.Run("EmptySlice", func(t *testing.T) {
		input := []string{}
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("SliceInMap", func(t *testing.T) {
		input := map[string]any{
			"items": []string{"$ITEM1", "$ITEM2"},
		}
		result, err := object(ctx, input, vars)
		require.NoError(t, err)

		items, ok := result["items"].([]string)
		require.True(t, ok)
		assert.Equal(t, []string{"first", "second"}, items)
	})
}

func TestObject_Primitives(t *testing.T) {
	ctx := context.Background()
	vars := map[string]string{"VAR": "value"}

	t.Run("IntegerPassthrough", func(t *testing.T) {
		input := 42
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Equal(t, 42, result)
	})

	t.Run("FloatPassthrough", func(t *testing.T) {
		input := 3.14
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Equal(t, 3.14, result)
	})

	t.Run("BoolPassthrough", func(t *testing.T) {
		input := true
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.True(t, result)
	})

	t.Run("NilPassthrough", func(t *testing.T) {
		var input *string = nil
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		assert.Nil(t, result)
	})
}

func TestObject_ComplexScenarios(t *testing.T) {
	ctx := context.Background()
	vars := map[string]string{
		"HOST":     "api.example.com",
		"PORT":     "443",
		"PROTOCOL": "https",
	}

	type Endpoint struct {
		URL  string
		Name string
	}

	t.Run("StructInMap", func(t *testing.T) {
		input := map[string]any{
			"endpoint": Endpoint{
				URL:  "$PROTOCOL://$HOST:$PORT",
				Name: "main",
			},
		}
		result, err := object(ctx, input, vars)
		require.NoError(t, err)

		endpoint, ok := result["endpoint"].(Endpoint)
		require.True(t, ok)
		assert.Equal(t, "https://api.example.com:443", endpoint.URL)
		assert.Equal(t, "main", endpoint.Name)
	})

	t.Run("DeeplyNestedStructure", func(t *testing.T) {
		input := map[string]any{
			"level1": map[string]any{
				"level2": map[string]any{
					"value": "$HOST",
				},
			},
		}
		result, err := object(ctx, input, vars)
		require.NoError(t, err)

		level1, ok := result["level1"].(map[string]any)
		require.True(t, ok)
		level2, ok := level1["level2"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "api.example.com", level2["value"])
	})

	t.Run("SliceOfStructs", func(t *testing.T) {
		input := []Endpoint{
			{URL: "$PROTOCOL://$HOST", Name: "endpoint1"},
			{URL: "$PROTOCOL://$HOST:$PORT", Name: "endpoint2"},
		}
		result, err := object(ctx, input, vars)
		require.NoError(t, err)
		require.Len(t, result, 2)
		assert.Equal(t, "https://api.example.com", result[0].URL)
		assert.Equal(t, "https://api.example.com:443", result[1].URL)
	})
}

func TestObject_EmptyVars(t *testing.T) {
	result, err := object(context.Background(), "Hello, $UNDEFINED!", map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, "Hello, $UNDEFINED!", result)
}

func TestObject_NilVars(t *testing.T) {
	result, err := object(context.Background(), "Hello, World!", nil)
	require.NoError(t, err)
	assert.Equal(t, "Hello, World!", result)
}

func TestObject_ErrorPropagation(t *testing.T) {
	ctx := context.Background()
	type S struct {
		Field string
	}
	input := S{Field: "`nonexistent_cmd_abc123`"}
	_, err := object(ctx, input, map[string]string{})
	assert.Error(t, err)
}
