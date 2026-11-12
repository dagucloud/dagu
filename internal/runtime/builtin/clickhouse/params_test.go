// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package clickhouse

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatParam(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"String", "hello", "hello"},
		{"StringKeepsQuote", "it's", "it's"},
		{"StringEscapes", "a\\b\tc\nd\re\x00", `a\\b\tc\nd\re\0`},
		{"Int", 42, "42"},
		{"Int64", int64(-7), "-7"},
		{"Uint64", uint64(18446744073709551615), "18446744073709551615"},
		{"Float", 1.5, "1.5"},
		{"WholeFloat", float64(5), "5"},
		{"LargeFloat", 1e21, "1000000000000000000000"},
		{"True", true, "true"},
		{"False", false, "false"},
		{"Null", nil, `\N`},
		{"IntList", []any{1, 2, 3}, "[1,2,3]"},
		{"StringList", []any{"a", "it's", `b\c`, "t\tn\n"}, `['a','it\'s','b\\c','t\tn\n']`},
		{"MixedListWithNull", []any{"a", nil, 2.5, true}, "['a',NULL,2.5,true]"},
		{"EmptyList", []any{}, "[]"},
		{"NestedList", []any{[]any{1, 2}, []any{}}, "[[1,2],[]]"},
		{"Map", map[string]any{"b": 2, "a": "x"}, "{'a':'x','b':2}"},
		{"MapKeyEscaped", map[string]any{"it's": 1}, `{'it\'s':1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := formatParam(tt.value)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("UnsupportedType", func(t *testing.T) {
		t.Parallel()
		_, err := formatParam(time.Now())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported value type")

		_, err = formatParam([]any{struct{}{}})
		require.Error(t, err)
	})
}

func TestFormatSetting(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		value any
		want  string
	}{
		{1, "1"},
		{"strict", "strict"},
		{true, "1"},
		{false, "0"},
		{float64(30), "30"},
		{0.5, "0.5"},
	} {
		got, err := formatSetting(tt.value)
		require.NoError(t, err)
		assert.Equal(t, tt.want, got)
	}

	_, err := formatSetting([]any{1})
	require.Error(t, err)
	_, err = formatSetting(nil)
	require.Error(t, err)
}
