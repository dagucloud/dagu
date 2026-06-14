// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStringFields_PointerFields(t *testing.T) {
	t.Setenv("PTR_VAR", "pointer_value")

	type PointerNested struct {
		Value string
	}

	type PointerStruct struct {
		Token  *string
		Nested *PointerNested
		Items  []*PointerNested
	}

	ctx := context.Background()
	token := "$PTR_VAR"
	input := PointerStruct{
		Token:  &token,
		Nested: &PointerNested{Value: "${PTR_VAR}"},
		Items: []*PointerNested{{
			Value: "$PTR_VAR",
		}},
	}

	result, err := StringFields(ctx, input, WithOSExpansion())
	require.NoError(t, err)
	require.NotNil(t, result.Token)
	assert.Equal(t, "pointer_value", *result.Token)
	require.NotNil(t, result.Nested)
	assert.Equal(t, "pointer_value", result.Nested.Value)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "pointer_value", result.Items[0].Value)
}

func TestStringFields_NilPointerFields(t *testing.T) {
	t.Setenv("NIL_TEST_VAR", "value")

	type Nested struct {
		Value string
	}

	type StructWithNilPointers struct {
		NilString *string
		NilStruct *Nested
		NilMap    *map[string]string
		NilSlice  *[]string
		Regular   string
	}

	ctx := context.Background()
	input := StructWithNilPointers{
		NilString: nil,
		NilStruct: nil,
		NilMap:    nil,
		NilSlice:  nil,
		Regular:   "$NIL_TEST_VAR",
	}

	result, err := StringFields(ctx, input, WithOSExpansion())
	require.NoError(t, err)
	assert.Nil(t, result.NilString)
	assert.Nil(t, result.NilStruct)
	assert.Nil(t, result.NilMap)
	assert.Nil(t, result.NilSlice)
	assert.Equal(t, "value", result.Regular)
}

func TestStringFields_PointerToMap(t *testing.T) {
	t.Setenv("MAP_PTR_VAR", "map_ptr_value")

	type StructWithPtrMap struct {
		Config *map[string]string
	}

	ctx := context.Background()

	t.Run("PointerToMapWithEnvVars", func(t *testing.T) {
		mapVal := map[string]string{
			"key1": "$MAP_PTR_VAR",
			"key2": "${MAP_PTR_VAR}",
		}
		input := StructWithPtrMap{Config: &mapVal}

		result, err := StringFields(ctx, input, WithOSExpansion())
		require.NoError(t, err)
		require.NotNil(t, result.Config)
		assert.Equal(t, "map_ptr_value", (*result.Config)["key1"])
		assert.Equal(t, "map_ptr_value", (*result.Config)["key2"])
	})

	t.Run("PointerToNilMap", func(t *testing.T) {
		input := StructWithPtrMap{Config: nil}

		result, err := StringFields(ctx, input)
		require.NoError(t, err)
		assert.Nil(t, result.Config)
	})
}

func TestStringFields_PointerToSlice(t *testing.T) {
	t.Setenv("SLICE_PTR_VAR", "slice_ptr_value")

	type Nested struct {
		Value string
	}

	type StructWithPtrSlice struct {
		Items   *[]string
		Structs *[]*Nested
	}

	ctx := context.Background()

	t.Run("PointerToSliceOfStrings", func(t *testing.T) {
		items := []string{"$SLICE_PTR_VAR", "${SLICE_PTR_VAR}"}
		input := StructWithPtrSlice{Items: &items}

		result, err := StringFields(ctx, input, WithOSExpansion())
		require.NoError(t, err)
		require.NotNil(t, result.Items)
		require.Len(t, *result.Items, 2)
		assert.Equal(t, "slice_ptr_value", (*result.Items)[0])
		assert.Equal(t, "slice_ptr_value", (*result.Items)[1])
	})

	t.Run("PointerToSliceOfStructPointers", func(t *testing.T) {
		structs := []*Nested{
			{Value: "$SLICE_PTR_VAR"},
			{Value: "${SLICE_PTR_VAR}"},
		}
		input := StructWithPtrSlice{Structs: &structs}

		result, err := StringFields(ctx, input, WithOSExpansion())
		require.NoError(t, err)
		require.NotNil(t, result.Structs)
		require.Len(t, *result.Structs, 2)
		assert.Equal(t, "slice_ptr_value", (*result.Structs)[0].Value)
		assert.Equal(t, "slice_ptr_value", (*result.Structs)[1].Value)
	})

	t.Run("PointerToNilSlice", func(t *testing.T) {
		input := StructWithPtrSlice{Items: nil}

		result, err := StringFields(ctx, input)
		require.NoError(t, err)
		assert.Nil(t, result.Items)
	})
}

func TestStringFields_SliceOfStrings(t *testing.T) {
	t.Setenv("SLICE_STR_VAR", "slice_str_value")

	type StructWithStringSlice struct {
		Tags   []string
		Labels []string
	}

	ctx := context.Background()

	t.Run("SliceWithEnvVars", func(t *testing.T) {
		input := StructWithStringSlice{
			Tags: []string{"$SLICE_STR_VAR", "${SLICE_STR_VAR}", "plain"},
		}

		result, err := StringFields(ctx, input, WithOSExpansion())
		require.NoError(t, err)
		require.Len(t, result.Tags, 3)
		assert.Equal(t, "slice_str_value", result.Tags[0])
		assert.Equal(t, "slice_str_value", result.Tags[1])
		assert.Equal(t, "plain", result.Tags[2])
	})

	t.Run("EmptySlice", func(t *testing.T) {
		input := StructWithStringSlice{
			Tags: []string{},
		}

		result, err := StringFields(ctx, input)
		require.NoError(t, err)
		assert.Empty(t, result.Tags)
	})

	t.Run("NilSlice", func(t *testing.T) {
		input := StructWithStringSlice{
			Tags: nil,
		}

		result, err := StringFields(ctx, input)
		require.NoError(t, err)
		assert.Nil(t, result.Tags)
	})

	t.Run("SliceWithCommandSubstitution", func(t *testing.T) {
		input := StructWithStringSlice{
			Tags: []string{"`echo hello`", "`echo world`"},
		}

		result, err := StringFields(ctx, input, WithOSExpansion())
		require.NoError(t, err)
		require.Len(t, result.Tags, 2)
		assert.Equal(t, "hello", result.Tags[0])
		assert.Equal(t, "world", result.Tags[1])
	})
}

func TestStringFields_SliceOfStructs(t *testing.T) {
	t.Setenv("STRUCT_SLICE_VAR", "struct_slice_value")

	type Item struct {
		Name  string
		Value string
	}

	type StructWithStructSlice struct {
		Items []Item
	}

	ctx := context.Background()

	t.Run("SliceOfStructsWithEnvVars", func(t *testing.T) {
		input := StructWithStructSlice{
			Items: []Item{
				{Name: "$STRUCT_SLICE_VAR", Value: "plain"},
				{Name: "plain", Value: "${STRUCT_SLICE_VAR}"},
			},
		}

		result, err := StringFields(ctx, input, WithOSExpansion())
		require.NoError(t, err)
		require.Len(t, result.Items, 2)
		assert.Equal(t, "struct_slice_value", result.Items[0].Name)
		assert.Equal(t, "plain", result.Items[0].Value)
		assert.Equal(t, "plain", result.Items[1].Name)
		assert.Equal(t, "struct_slice_value", result.Items[1].Value)
	})

	t.Run("EmptySliceOfStructs", func(t *testing.T) {
		input := StructWithStructSlice{
			Items: []Item{},
		}

		result, err := StringFields(ctx, input)
		require.NoError(t, err)
		assert.Empty(t, result.Items)
	})
}

func TestStringFields_SliceOfMaps(t *testing.T) {
	t.Setenv("MAP_SLICE_VAR", "map_slice_value")

	type StructWithMapSlice struct {
		Configs []map[string]string
	}

	ctx := context.Background()

	t.Run("SliceOfMapsWithEnvVars", func(t *testing.T) {
		input := StructWithMapSlice{
			Configs: []map[string]string{
				{"key": "$MAP_SLICE_VAR"},
				{"key": "${MAP_SLICE_VAR}"},
			},
		}

		result, err := StringFields(ctx, input, WithOSExpansion())
		require.NoError(t, err)
		require.Len(t, result.Configs, 2)
		assert.Equal(t, "map_slice_value", result.Configs[0]["key"])
		assert.Equal(t, "map_slice_value", result.Configs[1]["key"])
	})

	t.Run("SliceWithNilMapElement", func(t *testing.T) {
		input := StructWithMapSlice{
			Configs: []map[string]string{
				{"key": "$MAP_SLICE_VAR"},
				nil,
				{"key": "plain"},
			},
		}

		result, err := StringFields(ctx, input, WithOSExpansion())
		require.NoError(t, err)
		require.Len(t, result.Configs, 3)
		assert.Equal(t, "map_slice_value", result.Configs[0]["key"])
		assert.Nil(t, result.Configs[1])
		assert.Equal(t, "plain", result.Configs[2]["key"])
	})
}

func TestStringFields_SliceWithNilPointers(t *testing.T) {
	t.Setenv("NIL_PTR_SLICE_VAR", "nil_ptr_slice_value")

	type Nested struct {
		Value string
	}

	type StructWithPointerSlice struct {
		StringPtrs []*string
		StructPtrs []*Nested
	}

	ctx := context.Background()

	t.Run("SliceWithMixedNilAndNonNilStringPointers", func(t *testing.T) {
		val1 := "$NIL_PTR_SLICE_VAR"
		val2 := "${NIL_PTR_SLICE_VAR}"
		input := StructWithPointerSlice{
			StringPtrs: []*string{&val1, nil, &val2},
		}

		result, err := StringFields(ctx, input, WithOSExpansion())
		require.NoError(t, err)
		require.Len(t, result.StringPtrs, 3)
		require.NotNil(t, result.StringPtrs[0])
		assert.Equal(t, "nil_ptr_slice_value", *result.StringPtrs[0])
		assert.Nil(t, result.StringPtrs[1])
		require.NotNil(t, result.StringPtrs[2])
		assert.Equal(t, "nil_ptr_slice_value", *result.StringPtrs[2])
	})

	t.Run("SliceWithMixedNilAndNonNilStructPointers", func(t *testing.T) {
		input := StructWithPointerSlice{
			StructPtrs: []*Nested{
				{Value: "$NIL_PTR_SLICE_VAR"},
				nil,
				{Value: "${NIL_PTR_SLICE_VAR}"},
			},
		}

		result, err := StringFields(ctx, input, WithOSExpansion())
		require.NoError(t, err)
		require.Len(t, result.StructPtrs, 3)
		require.NotNil(t, result.StructPtrs[0])
		assert.Equal(t, "nil_ptr_slice_value", result.StructPtrs[0].Value)
		assert.Nil(t, result.StructPtrs[1])
		require.NotNil(t, result.StructPtrs[2])
		assert.Equal(t, "nil_ptr_slice_value", result.StructPtrs[2].Value)
	})
}

func TestStringFields_PointerMutation(t *testing.T) {
	t.Setenv("MUT_VAR", "expanded")

	t.Run("PointerToString", func(t *testing.T) {
		type S struct {
			Token *string
		}

		original := "$MUT_VAR"
		input := S{Token: &original}

		result, err := StringFields(context.Background(), input, WithOSExpansion())
		require.NoError(t, err)

		assert.Equal(t, "expanded", *result.Token)
		assert.Equal(t, "$MUT_VAR", original, "BUG: original variable was mutated")
		assert.Equal(t, "$MUT_VAR", *input.Token, "BUG: input struct's pointer target was mutated")
	})

	t.Run("SliceOfStringPointers", func(t *testing.T) {
		type S struct {
			Items []*string
		}

		val1 := "$MUT_VAR"
		val2 := "${MUT_VAR}"
		input := S{Items: []*string{&val1, &val2}}

		result, err := StringFields(context.Background(), input, WithOSExpansion())
		require.NoError(t, err)

		require.Len(t, result.Items, 2)
		assert.Equal(t, "expanded", *result.Items[0])
		assert.Equal(t, "expanded", *result.Items[1])

		assert.Equal(t, "$MUT_VAR", val1, "BUG: val1 was mutated")
		assert.Equal(t, "${MUT_VAR}", val2, "BUG: val2 was mutated")
		assert.Equal(t, "$MUT_VAR", *input.Items[0], "BUG: input.Items[0] target was mutated")
		assert.Equal(t, "${MUT_VAR}", *input.Items[1], "BUG: input.Items[1] target was mutated")
	})

	t.Run("PointerToStruct", func(t *testing.T) {
		type Nested struct {
			Value string
		}
		type S struct {
			Nested *Nested
		}

		input := S{Nested: &Nested{Value: "$MUT_VAR"}}
		originalValue := input.Nested.Value

		result, err := StringFields(context.Background(), input, WithOSExpansion())
		require.NoError(t, err)

		assert.Equal(t, "expanded", result.Nested.Value)
		assert.Equal(t, "$MUT_VAR", originalValue, "BUG: original nested value was mutated")
		assert.Equal(t, "$MUT_VAR", input.Nested.Value, "BUG: input.Nested.Value was mutated")
	})

	t.Run("PointerToSlice", func(t *testing.T) {
		type S struct {
			Items *[]string
		}

		items := []string{"$MUT_VAR", "${MUT_VAR}"}
		input := S{Items: &items}

		result, err := StringFields(context.Background(), input, WithOSExpansion())
		require.NoError(t, err)

		require.NotNil(t, result.Items)
		assert.Equal(t, "expanded", (*result.Items)[0])
		assert.Equal(t, "expanded", (*result.Items)[1])

		assert.Equal(t, "$MUT_VAR", items[0], "BUG: items[0] was mutated")
		assert.Equal(t, "${MUT_VAR}", items[1], "BUG: items[1] was mutated")
	})
}

func TestStringFields_PointerFieldErrors(t *testing.T) {
	type Nested struct {
		Command string
	}

	type StructWithPointerErrors struct {
		StringPtr *string
		StructPtr *Nested
		MapPtr    *map[string]string
	}

	ctx := context.Background()

	t.Run("PointerToStringWithInvalidCommand", func(t *testing.T) {
		input := StructWithPointerErrors{StringPtr: new("`invalid_command_xyz123`")}

		_, err := StringFields(ctx, input)
		assert.Error(t, err)
	})

	t.Run("PointerToStructWithInvalidNestedCommand", func(t *testing.T) {
		input := StructWithPointerErrors{
			StructPtr: &Nested{Command: "`invalid_command_xyz123`"},
		}

		_, err := StringFields(ctx, input)
		assert.Error(t, err)
	})

	t.Run("PointerToMapWithInvalidCommand", func(t *testing.T) {
		mapVal := map[string]string{
			"key": "`invalid_command_xyz123`",
		}
		input := StructWithPointerErrors{MapPtr: &mapVal}

		_, err := StringFields(ctx, input)
		assert.Error(t, err)
	})
}

func TestStringFields_SliceFieldErrors(t *testing.T) {
	type Nested struct {
		Command string
	}

	type StructWithSliceErrors struct {
		Strings    []string
		Structs    []Nested
		StringPtrs []*string
		Maps       []map[string]string
	}

	ctx := context.Background()

	t.Run("SliceStringWithInvalidCommand", func(t *testing.T) {
		input := StructWithSliceErrors{
			Strings: []string{"valid", "`invalid_command_xyz123`"},
		}

		_, err := StringFields(ctx, input)
		assert.Error(t, err)
	})

	t.Run("SliceStructWithInvalidNestedCommand", func(t *testing.T) {
		input := StructWithSliceErrors{
			Structs: []Nested{
				{Command: "valid"},
				{Command: "`invalid_command_xyz123`"},
			},
		}

		_, err := StringFields(ctx, input)
		assert.Error(t, err)
	})

	t.Run("SlicePointerWithInvalidCommand", func(t *testing.T) {
		valid := "valid"
		invalid := "`invalid_command_xyz123`"
		input := StructWithSliceErrors{
			StringPtrs: []*string{&valid, &invalid},
		}

		_, err := StringFields(ctx, input)
		assert.Error(t, err)
	})

	t.Run("SliceMapWithInvalidCommand", func(t *testing.T) {
		input := StructWithSliceErrors{
			Maps: []map[string]string{
				{"key": "valid"},
				{"key": "`invalid_command_xyz123`"},
			},
		}

		_, err := StringFields(ctx, input)
		assert.Error(t, err)
	})
}

func TestStringFields_StructWithMapField(t *testing.T) {
	t.Setenv("TEST_VAR", "env_value")

	type TestStruct struct {
		Name    string
		Config  map[string]string
		Options map[string]any
	}

	ctx := context.Background()
	if runtime.GOOS == "windows" {
		ctx = config.WithConfig(ctx, &config.Config{
			Core: config.Core{DefaultShell: "cmd"},
		})
	}
	input := TestStruct{
		Name: "$TEST_VAR",
		Config: map[string]string{
			"key1": "$TEST_VAR",
			"key2": "config-value",
		},
		Options: map[string]any{
			"enabled": true,
			"command": "`echo option`",
			"nested": map[string]any{
				"value": "$TEST_VAR",
			},
		},
	}

	got, err := StringFields(ctx, input, WithOSExpansion())
	require.NoError(t, err)

	assert.Equal(t, "env_value", got.Name)
	assert.Equal(t, "env_value", got.Config["key1"])
	assert.Equal(t, "config-value", got.Config["key2"])
	assert.Equal(t, true, got.Options["enabled"])
	assert.Equal(t, "option", got.Options["command"])
	assert.Equal(t, "env_value", got.Options["nested"].(map[string]any)["value"])
}

func TestStringFields_MapNilValues(t *testing.T) {
	input := map[string]any{
		"string": "value",
		"nil":    nil,
		"ptr":    (*string)(nil),
		"iface":  any(nil),
	}

	ctx := context.Background()
	got, err := StringFields(ctx, input)
	require.NoError(t, err)

	assert.Equal(t, "value", got["string"])
	assert.Nil(t, got["nil"])
	assert.Nil(t, got["ptr"])
	assert.Nil(t, got["iface"])
}
