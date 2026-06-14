// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValueResolutionConstsBuildsMapAndListForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		yaml      string
		want      map[string]any
		errChecks []string
	}{
		{
			name: "MapLiterals",
			yaml: `
consts:
  service: api
  enabled: true
  count: 3
  ratio: 1.5
steps:
  - id: ok
    run: echo ok
`,
			want: map[string]any{
				"service": "api",
				"enabled": true,
				"count":   uint64(3),
				"ratio":   1.5,
			},
		},
		{
			name: "OrderedListReferencesEarlierConsts",
			yaml: `
consts:
  - service: api
  - enabled: true
  - count: 3
  - ratio: 1.5
  - endpoint: http://localhost/${consts.service}/${consts.enabled}/${consts.count}/${consts.ratio}
steps:
  - id: ok
    run: echo ok
`,
			want: map[string]any{
				"service":  "api",
				"enabled":  true,
				"count":    uint64(3),
				"ratio":    1.5,
				"endpoint": "http://localhost/api/true/3/1.5",
			},
		},
		{
			name: "RejectsUnsupportedRootType",
			yaml: `
consts: true
steps:
  - id: ok
    run: echo ok
`,
			errChecks: []string{"consts", "mapping", "ordered list"},
		},
		{
			name: "RejectsMapReference",
			yaml: `
consts:
  service: api
  endpoint: ${consts.service}
steps:
  - id: ok
    run: echo ok
`,
			errChecks: []string{"consts.endpoint", "literal"},
		},
		{
			name: "RejectsInvalidMapKey",
			yaml: `
consts:
  1service: api
steps:
  - id: ok
    run: echo ok
`,
			errChecks: []string{"consts key", "1service"},
		},
		{
			name: "RejectsInvalidValueType",
			yaml: `
consts:
  service:
    name: api
steps:
  - id: ok
    run: echo ok
`,
			errChecks: []string{"consts.service", "literal string"},
		},
		{
			name: "RejectsNonFiniteFloat",
			yaml: `
consts:
  bad: .inf
steps:
  - id: ok
    run: echo ok
`,
			errChecks: []string{"consts.bad", "finite"},
		},
		{
			name: "RejectsListNonMappingItem",
			yaml: `
consts:
  - api
steps:
  - id: ok
    run: echo ok
`,
			errChecks: []string{"consts[0]", "single-entry mapping"},
		},
		{
			name: "RejectsListMultiKeyEntry",
			yaml: `
consts:
  - service: api
    endpoint: http://localhost
steps:
  - id: ok
    run: echo ok
`,
			errChecks: []string{"consts[0]", "exactly one key"},
		},
		{
			name: "RejectsDuplicateListKey",
			yaml: `
consts:
  - service: api
  - service: web
steps:
  - id: ok
    run: echo ok
`,
			errChecks: []string{"consts.service", "more than once"},
		},
		{
			name: "RejectsInvalidListKey",
			yaml: `
consts:
  - 1service: api
steps:
  - id: ok
    run: echo ok
`,
			errChecks: []string{"consts key", "1service"},
		},
		{
			name: "RejectsListShorthandReference",
			yaml: `
consts:
  - service: api
  - endpoint: $consts.service
steps:
  - id: ok
    run: echo ok
`,
			errChecks: []string{"consts.endpoint", "invalid Dagu-looking reference"},
		},
		{
			name: "RejectsListParamsReference",
			yaml: `
consts:
  - endpoint: ${params.environment}
steps:
  - id: ok
    run: echo ok
`,
			errChecks: []string{"consts.endpoint", "earlier consts"},
		},
		{
			name: "RejectsListLaterReference",
			yaml: `
consts:
  - endpoint: ${consts.service}
  - service: api
steps:
  - id: ok
    run: echo ok
`,
			errChecks: []string{"consts.endpoint", "unknown or later consts.service"},
		},
		{
			name: "RejectsListInvalidReferenceSegment",
			yaml: `
consts:
  - endpoint: ${consts.1service}
steps:
  - id: ok
    run: echo ok
`,
			errChecks: []string{"consts.endpoint", "earlier consts"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dag, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			if len(tt.errChecks) > 0 {
				require.Error(t, err)
				for _, check := range tt.errChecks {
					assert.Contains(t, err.Error(), check)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, dag.Consts)
		})
	}
}

func TestValueResolutionConstsHelpersCoverScalarTypes(t *testing.T) {
	t.Parallel()

	t.Run("EmptyForms", func(t *testing.T) {
		t.Parallel()

		got, err := spec.BuildConstsForTest(map[string]any{})
		require.NoError(t, err)
		assert.Nil(t, got)

		got, err = spec.BuildConstsForTest([]any{})
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("ResolveListStringWithoutReferences", func(t *testing.T) {
		t.Parallel()

		got, err := spec.ResolveConstListStringValueForTest("service", "api", nil)
		require.NoError(t, err)
		assert.Equal(t, "api", got)
	})

	validateCases := []struct {
		name  string
		value any
	}{
		{name: "String", value: "api"},
		{name: "Bool", value: true},
		{name: "Int", value: int(1)},
		{name: "Int8", value: int8(2)},
		{name: "Int16", value: int16(3)},
		{name: "Int32", value: int32(4)},
		{name: "Int64", value: int64(5)},
		{name: "Uint", value: uint(6)},
		{name: "Uint8", value: uint8(7)},
		{name: "Uint16", value: uint16(8)},
		{name: "Uint32", value: uint32(9)},
		{name: "Uint64", value: uint64(10)},
		{name: "Float32", value: float32(1.25)},
		{name: "Float64", value: float64(2.5)},
		{name: "JSONNumber", value: json.Number("3.75")},
	}
	for _, tc := range validateCases {
		t.Run("Validate"+tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := spec.ValidateConstValueForTest("value", tc.value)
			require.NoError(t, err)
			assert.Equal(t, tc.value, got)
		})
	}

	errorCases := []struct {
		name      string
		value     any
		errChecks []string
	}{
		{name: "Float32NaN", value: float32(math.NaN()), errChecks: []string{"finite"}},
		{name: "Float64Inf", value: math.Inf(1), errChecks: []string{"finite"}},
		{name: "BadJSONNumber", value: json.Number("not-number"), errChecks: []string{"finite"}},
	}
	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.ValidateConstValueForTest("value", tc.value)
			require.Error(t, err)
			for _, check := range tc.errChecks {
				assert.Contains(t, err.Error(), check)
			}
		})
	}

	formatCases := []struct {
		name  string
		value any
		want  string
	}{
		{name: "String", value: "api", want: "api"},
		{name: "Bool", value: true, want: "true"},
		{name: "Int", value: int(1), want: "1"},
		{name: "Int8", value: int8(2), want: "2"},
		{name: "Int16", value: int16(3), want: "3"},
		{name: "Int32", value: int32(4), want: "4"},
		{name: "Int64", value: int64(5), want: "5"},
		{name: "Uint", value: uint(6), want: "6"},
		{name: "Uint8", value: uint8(7), want: "7"},
		{name: "Uint16", value: uint16(8), want: "8"},
		{name: "Uint32", value: uint32(9), want: "9"},
		{name: "Uint64", value: uint64(10), want: "10"},
		{name: "Float32", value: float32(1.25), want: "1.25"},
		{name: "Float64", value: float64(2.5), want: "2.5"},
		{name: "JSONNumber", value: json.Number("3.75"), want: "3.75"},
		{name: "Default", value: []string{"api"}, want: "[api]"},
	}
	for _, tc := range formatCases {
		t.Run("Format"+tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, spec.FormatConstValueForTest(tc.value))
		})
	}
}
