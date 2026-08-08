// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSimpleParams(t *testing.T) {
	data := map[string]string{"key": "value", "count": "42"}
	params := NewSimpleParams(data)

	assert.Equal(t, ParamTypeString, params.Type())
	assert.Equal(t, data, params.Simple)
	assert.Nil(t, params.Rich)
	assert.Nil(t, params.Raw)
}

func TestNewRichParams(t *testing.T) {
	data := map[string]any{"key": "value", "count": 42, "enabled": true}
	params := NewRichParams(data)

	assert.Equal(t, ParamTypeAny, params.Type())
	assert.Equal(t, data, params.Rich)
	assert.Nil(t, params.Simple)
	assert.Nil(t, params.Raw)
}

func TestNewRawParams(t *testing.T) {
	raw := json.RawMessage(`{"key":"value","count":42}`)
	params := NewRawParams(raw)

	assert.Equal(t, ParamTypeRaw, params.Type())
	assert.Equal(t, raw, params.Raw)
	assert.Nil(t, params.Simple)
	assert.Nil(t, params.Rich)
}

func TestParseParams(t *testing.T) {
	t.Run("Nil", func(t *testing.T) {
		params, err := ParseParams(nil)
		require.NoError(t, err)
		assert.True(t, params.IsEmpty())
	})

	t.Run("MapStringString", func(t *testing.T) {
		input := map[string]string{"key": "value"}
		params, err := ParseParams(input)
		require.NoError(t, err)
		assert.Equal(t, ParamTypeString, params.Type())
		assert.Equal(t, input, params.Simple)
	})

	t.Run("MapStringAny_AllStrings", func(t *testing.T) {
		input := map[string]any{"key": "value", "name": "test"}
		params, err := ParseParams(input)
		require.NoError(t, err)
		// Should optimize to Simple
		assert.Equal(t, ParamTypeString, params.Type())
		assert.Equal(t, "value", params.Simple["key"])
	})

	t.Run("MapStringAny_MixedTypes", func(t *testing.T) {
		input := map[string]any{"key": "value", "count": 42, "enabled": true}
		params, err := ParseParams(input)
		require.NoError(t, err)
		assert.Equal(t, ParamTypeAny, params.Type())
		assert.Equal(t, input, params.Rich)
	})

	t.Run("RawMessage", func(t *testing.T) {
		raw := json.RawMessage(`{"key":"value"}`)
		params, err := ParseParams(raw)
		require.NoError(t, err)
		assert.Equal(t, ParamTypeRaw, params.Type())
		assert.Equal(t, raw, params.Raw)
	})

	t.Run("ByteSlice", func(t *testing.T) {
		input := []byte(`{"key":"value"}`)
		params, err := ParseParams(input)
		require.NoError(t, err)
		assert.Equal(t, ParamTypeRaw, params.Type())
	})

	t.Run("JSONString", func(t *testing.T) {
		input := `{"key":"value","count":42}`
		params, err := ParseParams(input)
		require.NoError(t, err)
		// String input is parsed as JSON, mixed types become Rich
		assert.Equal(t, ParamTypeAny, params.Type())
		assert.Equal(t, "value", params.Rich["key"])
		assert.Equal(t, float64(42), params.Rich["count"])
	})

	t.Run("Params", func(t *testing.T) {
		input := NewSimpleParams(map[string]string{"key": "value"})
		params, err := ParseParams(input)
		require.NoError(t, err)
		assert.Equal(t, input, params)
	})

	t.Run("InvalidString", func(t *testing.T) {
		input := "not a json string"
		_, err := ParseParams(input)
		assert.Error(t, err)
	})
}

func TestParams_Type(t *testing.T) {
	tests := []struct {
		name   string
		params Params
		want   ParamType
	}{
		{
			name:   "Empty",
			params: Params{},
			want:   ParamTypeUnknown,
		},
		{
			name:   "Simple",
			params: NewSimpleParams(map[string]string{"key": "value"}),
			want:   ParamTypeString,
		},
		{
			name:   "Rich",
			params: NewRichParams(map[string]any{"key": "value"}),
			want:   ParamTypeAny,
		},
		{
			name:   "Raw",
			params: NewRawParams(json.RawMessage(`{}`)),
			want:   ParamTypeRaw,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.params.Type())
		})
	}
}

func TestParams_AsStringMap(t *testing.T) {
	t.Run("Simple", func(t *testing.T) {
		data := map[string]string{"key": "value"}
		params := NewSimpleParams(data)
		m, err := params.AsStringMap()
		require.NoError(t, err)
		assert.Equal(t, data, m)
	})

	t.Run("Rich", func(t *testing.T) {
		params := NewRichParams(map[string]any{"key": "value", "count": 42, "enabled": true})
		m, err := params.AsStringMap()
		require.NoError(t, err)
		assert.Equal(t, "value", m["key"])
		assert.Equal(t, "42", m["count"])
		assert.Equal(t, "true", m["enabled"])
	})

	t.Run("Raw", func(t *testing.T) {
		raw := json.RawMessage(`{"key":"value","count":42}`)
		params := NewRawParams(raw)
		m, err := params.AsStringMap()
		require.NoError(t, err)
		assert.Equal(t, "value", m["key"])
		assert.Equal(t, "42", m["count"])
	})

	t.Run("Empty", func(t *testing.T) {
		params := Params{}
		m, err := params.AsStringMap()
		require.NoError(t, err)
		assert.Empty(t, m)
	})
}

func TestParams_JSON(t *testing.T) {
	t.Run("Marshal_Simple", func(t *testing.T) {
		params := NewSimpleParams(map[string]string{"key": "value"})
		data, err := json.Marshal(&params)
		require.NoError(t, err)
		assert.JSONEq(t, `{"key":"value"}`, string(data))
	})

	t.Run("Marshal_Rich", func(t *testing.T) {
		params := NewRichParams(map[string]any{"key": "value", "count": 42})
		data, err := json.Marshal(&params)
		require.NoError(t, err)
		assert.JSONEq(t, `{"key":"value","count":42}`, string(data))
	})

	t.Run("Unmarshal_AllStrings", func(t *testing.T) {
		data := []byte(`{"key":"value","name":"test"}`)
		var params Params
		err := json.Unmarshal(data, &params)
		require.NoError(t, err)
		assert.Equal(t, ParamTypeString, params.Type())
		assert.Equal(t, "value", params.Simple["key"])
	})

	t.Run("Unmarshal_MixedTypes", func(t *testing.T) {
		data := []byte(`{"key":"value","count":42,"enabled":true}`)
		var params Params
		err := json.Unmarshal(data, &params)
		require.NoError(t, err)
		assert.Equal(t, ParamTypeAny, params.Type())
		assert.Equal(t, "value", params.Rich["key"])
		assert.Equal(t, float64(42), params.Rich["count"])
		assert.Equal(t, true, params.Rich["enabled"])
	})

	t.Run("Unmarshal_Null", func(t *testing.T) {
		data := []byte(`null`)
		var params Params
		err := json.Unmarshal(data, &params)
		require.NoError(t, err)
		assert.True(t, params.IsEmpty())
	})

	t.Run("RoundTrip", func(t *testing.T) {
		original := NewRichParams(map[string]any{"key": "value", "count": 42})
		data, err := json.Marshal(&original)
		require.NoError(t, err)

		var restored Params
		err = json.Unmarshal(data, &restored)
		require.NoError(t, err)

		assert.Equal(t, original.Type(), restored.Type())
		assert.Equal(t, "value", restored.Rich["key"])
		assert.Equal(t, float64(42), restored.Rich["count"])
	})
}

func TestParams_IsEmpty(t *testing.T) {
	tests := []struct {
		name   string
		params Params
		want   bool
	}{
		{
			name:   "Empty",
			params: Params{},
			want:   true,
		},
		{
			name:   "Simple",
			params: NewSimpleParams(map[string]string{"key": "value"}),
			want:   false,
		},
		{
			name:   "Rich",
			params: NewRichParams(map[string]any{"key": "value"}),
			want:   false,
		},
		{
			name:   "Raw",
			params: NewRawParams(json.RawMessage(`{}`)),
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.params.IsEmpty())
		})
	}
}
