// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantKey string
		wantVal string
		wantStr string
	}{
		{
			name:    "simple label",
			input:   "production",
			wantKey: "production",
			wantVal: "",
			wantStr: "production",
		},
		{
			name:    "key=value label",
			input:   "env=prod",
			wantKey: "env",
			wantVal: "prod",
			wantStr: "env=prod",
		},
		{
			name:    "uppercase normalized to lowercase",
			input:   "ENV=PROD",
			wantKey: "env",
			wantVal: "prod",
			wantStr: "env=prod",
		},
		{
			name:    "spaces trimmed",
			input:   "  env = production  ",
			wantKey: "env",
			wantVal: "production",
			wantStr: "env=production",
		},
		{
			name:    "empty value",
			input:   "env=",
			wantKey: "env",
			wantVal: "",
			wantStr: "env",
		},
		{
			name:    "empty string",
			input:   "",
			wantKey: "",
			wantVal: "",
			wantStr: "",
		},
		{
			name:    "whitespace only",
			input:   "   ",
			wantKey: "",
			wantVal: "",
			wantStr: "",
		},
		{
			name:    "value with equals sign",
			input:   "config=key=value",
			wantKey: "config",
			wantVal: "key=value",
			wantStr: "config=key=value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label := ParseLabel(tt.input)
			assert.Equal(t, tt.wantKey, label.Key)
			assert.Equal(t, tt.wantVal, label.Value)
			if tt.wantKey != "" {
				assert.Equal(t, tt.wantStr, label.String())
			}
		})
	}
}

func TestLabel_IsZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		label Label
		want  bool
	}{
		{"empty label", Label{}, true},
		{"key only", Label{Key: "env"}, false},
		{"key and value", Label{Key: "env", Value: "prod"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.label.IsZero())
		})
	}
}

func TestNewLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []string
		wantLen  int
		wantStrs []string
	}{
		{
			name:     "mixed labels",
			input:    []string{"env=prod", "team=platform", "critical"},
			wantLen:  3,
			wantStrs: []string{"env=prod", "team=platform", "critical"},
		},
		{
			name:     "filters empty strings",
			input:    []string{"env=prod", "", "  ", "critical"},
			wantLen:  2,
			wantStrs: []string{"env=prod", "critical"},
		},
		{
			name:     "nil input",
			input:    nil,
			wantLen:  0,
			wantStrs: []string{},
		},
		{
			name:     "empty input",
			input:    []string{},
			wantLen:  0,
			wantStrs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := NewLabels(tt.input)
			assert.Len(t, labels, tt.wantLen)
			assert.Equal(t, tt.wantStrs, labels.Strings())
		})
	}
}

func TestLabels_Keys(t *testing.T) {
	t.Parallel()

	labels := Labels{
		{Key: "env", Value: "prod"},
		{Key: "env", Value: "staging"},
		{Key: "team", Value: "platform"},
		{Key: "critical", Value: ""},
	}

	keys := labels.Keys()
	assert.Len(t, keys, 3)
	assert.Contains(t, keys, "env")
	assert.Contains(t, keys, "team")
	assert.Contains(t, keys, "critical")
}

func TestLabels_HasKey(t *testing.T) {
	t.Parallel()

	labels := Labels{
		{Key: "env", Value: "prod"},
		{Key: "team", Value: "platform"},
	}

	assert.True(t, labels.HasKey("env"))
	assert.True(t, labels.HasKey("ENV"))
	assert.True(t, labels.HasKey("team"))
	assert.False(t, labels.HasKey("missing"))
}

func TestLabels_Get(t *testing.T) {
	t.Parallel()

	labels := Labels{
		{Key: "env", Value: "prod"},
		{Key: "env", Value: "staging"},
		{Key: "team", Value: "platform"},
		{Key: "critical", Value: ""},
	}

	envValues := labels.Get("env")
	assert.Len(t, envValues, 2)
	assert.Contains(t, envValues, "prod")
	assert.Contains(t, envValues, "staging")

	teamValues := labels.Get("team")
	assert.Len(t, teamValues, 1)
	assert.Equal(t, "platform", teamValues[0])

	criticalValues := labels.Get("critical")
	assert.Len(t, criticalValues, 1)
	assert.Equal(t, "", criticalValues[0])

	missingValues := labels.Get("missing")
	assert.Nil(t, missingValues)
}

func TestLabels_JSON(t *testing.T) {
	t.Parallel()

	t.Run("marshal", func(t *testing.T) {
		labels := Labels{
			{Key: "env", Value: "prod"},
			{Key: "critical", Value: ""},
		}

		data, err := json.Marshal(labels)
		require.NoError(t, err)
		assert.JSONEq(t, `["env=prod","critical"]`, string(data))
	})

	t.Run("unmarshal", func(t *testing.T) {
		data := []byte(`["env=prod","team=platform","critical"]`)

		var labels Labels
		err := json.Unmarshal(data, &labels)
		require.NoError(t, err)

		assert.Len(t, labels, 3)
		assert.Equal(t, "env", labels[0].Key)
		assert.Equal(t, "prod", labels[0].Value)
		assert.Equal(t, "team", labels[1].Key)
		assert.Equal(t, "platform", labels[1].Value)
		assert.Equal(t, "critical", labels[2].Key)
		assert.Equal(t, "", labels[2].Value)
	})

	t.Run("nil labels marshal", func(t *testing.T) {
		var labels Labels
		data, err := json.Marshal(labels)
		require.NoError(t, err)
		assert.Equal(t, "null", string(data))
	})

	t.Run("unmarshal invalid JSON", func(t *testing.T) {
		// Use valid JSON but wrong type - this triggers the error path inside UnmarshalJSON
		data := []byte(`{"key": "value"}`)
		var labels Labels
		err := json.Unmarshal(data, &labels)
		require.Error(t, err)
	})
}

func TestParseLabelFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantType  LabelFilterType
		wantKey   string
		wantValue string
	}{
		{
			name:      "key only",
			input:     "env",
			wantType:  LabelFilterTypeKeyOnly,
			wantKey:   "env",
			wantValue: "",
		},
		{
			name:      "exact match",
			input:     "env=prod",
			wantType:  LabelFilterTypeExact,
			wantKey:   "env",
			wantValue: "prod",
		},
		{
			name:      "negation",
			input:     "!deprecated",
			wantType:  LabelFilterTypeNegation,
			wantKey:   "deprecated",
			wantValue: "",
		},
		{
			name:      "case normalized",
			input:     "ENV=PROD",
			wantType:  LabelFilterTypeExact,
			wantKey:   "env",
			wantValue: "prod",
		},
		{
			name:      "spaces trimmed",
			input:     "  env = prod  ",
			wantType:  LabelFilterTypeExact,
			wantKey:   "env",
			wantValue: "prod",
		},
		{
			name:      "negation with spaces",
			input:     "! deprecated ",
			wantType:  LabelFilterTypeNegation,
			wantKey:   "deprecated",
			wantValue: "",
		},
		{
			name:      "empty string",
			input:     "",
			wantType:  LabelFilterTypeKeyOnly,
			wantKey:   "",
			wantValue: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := ParseLabelFilter(tt.input)
			assert.Equal(t, tt.wantType, filter.Type)
			assert.Equal(t, tt.wantKey, filter.Key)
			assert.Equal(t, tt.wantValue, filter.Value)
		})
	}
}

func TestLabelFilter_MatchesLabels(t *testing.T) {
	t.Parallel()

	labels := Labels{
		{Key: "env", Value: "prod"},
		{Key: "team", Value: "platform"},
		{Key: "critical", Value: ""},
	}

	tests := []struct {
		name   string
		filter string
		want   bool
	}{
		// Key-only filters
		{"key exists", "env", true},
		{"key exists (uppercase)", "ENV", true},
		{"key missing", "missing", false},
		{"key-only label exists", "critical", true},

		// Exact match filters
		{"exact match", "env=prod", true},
		{"exact match (wrong value)", "env=staging", false},
		{"exact match (key-only label)", "critical=", true},
		{"exact match (missing key)", "missing=value", false},

		// Negation filters
		{"negation (key missing)", "!deprecated", true},
		{"negation (key exists)", "!env", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := ParseLabelFilter(tt.filter)
			assert.Equal(t, tt.want, filter.MatchesLabels(labels))
		})
	}
}

func TestLabelFilter_MatchesLabels_InvalidType(t *testing.T) {
	t.Parallel()

	labels := Labels{{Key: "env", Value: "prod"}}
	filter := LabelFilter{Type: LabelFilterType(999), Key: "env"}
	assert.False(t, filter.MatchesLabels(labels))
}

func TestLabels_MatchesFilters(t *testing.T) {
	t.Parallel()

	labels := Labels{
		{Key: "env", Value: "prod"},
		{Key: "team", Value: "platform"},
		{Key: "critical", Value: ""},
	}

	tests := []struct {
		name    string
		filters []string
		want    bool
	}{
		{
			name:    "no filters",
			filters: []string{},
			want:    true,
		},
		{
			name:    "single match",
			filters: []string{"env=prod"},
			want:    true,
		},
		{
			name:    "multiple matches (AND)",
			filters: []string{"env=prod", "team"},
			want:    true,
		},
		{
			name:    "one filter fails (AND)",
			filters: []string{"env=prod", "missing"},
			want:    false,
		},
		{
			name:    "complex filter",
			filters: []string{"env=prod", "team", "!deprecated"},
			want:    true,
		},
		{
			name:    "negation fails",
			filters: []string{"env=prod", "!env"},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filters := make([]LabelFilter, len(tt.filters))
			for i, f := range tt.filters {
				filters[i] = ParseLabelFilter(f)
			}
			assert.Equal(t, tt.want, labels.MatchesFilters(filters))
		})
	}
}

func TestParseLabelFilter_Wildcard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantType  LabelFilterType
		wantKey   string
		wantValue string
	}{
		{
			name:      "key wildcard star",
			input:     "env*",
			wantType:  LabelFilterTypeWildcard,
			wantKey:   "env*",
			wantValue: "",
		},
		{
			name:      "key wildcard question",
			input:     "te?m",
			wantType:  LabelFilterTypeWildcard,
			wantKey:   "te?m",
			wantValue: "",
		},
		{
			name:      "value wildcard",
			input:     "env=prod*",
			wantType:  LabelFilterTypeWildcard,
			wantKey:   "env",
			wantValue: "prod*",
		},
		{
			name:      "both wildcard",
			input:     "env*=prod*",
			wantType:  LabelFilterTypeWildcard,
			wantKey:   "env*",
			wantValue: "prod*",
		},
		{
			name:      "match any value",
			input:     "team=*",
			wantType:  LabelFilterTypeWildcard,
			wantKey:   "team",
			wantValue: "*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := ParseLabelFilter(tt.input)
			assert.Equal(t, tt.wantType, filter.Type)
			assert.Equal(t, tt.wantKey, filter.Key)
			assert.Equal(t, tt.wantValue, filter.Value)
		})
	}
}

func TestLabelFilter_MatchesLabels_Wildcard(t *testing.T) {
	t.Parallel()

	labels := Labels{
		{Key: "env", Value: "prod"},
		{Key: "env", Value: "production"},
		{Key: "env", Value: "prod-us"},
		{Key: "team", Value: "platform"},
		{Key: "critical", Value: ""},
		{Key: "path", Value: "foo/bar/baz"},
	}

	tests := []struct {
		name   string
		filter string
		want   bool
	}{
		// Value wildcard patterns
		{"value prefix match", "env=prod*", true},
		{"value prefix no match", "env=staging*", false},
		{"value question mark", "env=pro?", true},
		{"value question mark no match", "env=pr?", false},
		{"value any", "team=*", true},
		{"value any empty label", "critical=*", true}, // * matches empty string

		// Key wildcard patterns
		{"key prefix match", "env*", true},
		{"key prefix no match", "missing*", false},
		{"key question mark", "te?m", true},
		{"key question mark no match", "te??m", false},

		// Combined wildcards
		{"key and value wildcard", "env*=prod*", true},
		{"key and value wildcard no match", "missing*=*", false},

		// Wildcard with slashes (path.Match would fail these, regex works)
		{"value with slash prefix", "path=foo/*", true},
		{"value with slash any", "path=*", true},
		{"value with slash pattern", "path=*/bar/*", true},
		{"value with slash no match", "path=baz/*", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := ParseLabelFilter(tt.filter)
			assert.Equal(t, tt.want, filter.MatchesLabels(labels), "filter: %s", tt.filter)
		})
	}
}

func TestValidateLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		label   Label
		wantErr bool
		errMsg  string
	}{
		// Valid labels
		{"simple key", Label{Key: "env"}, false, ""},
		{"key with value", Label{Key: "env", Value: "prod"}, false, ""},
		{"key with dash", Label{Key: "my-label"}, false, ""},
		{"key with underscore", Label{Key: "my_tag"}, false, ""},
		{"key with dot", Label{Key: "my.label"}, false, ""},
		{"key starts with number", Label{Key: "1env"}, false, ""},
		{"value with slash", Label{Key: "path", Value: "foo/bar"}, false, ""},

		// Invalid keys
		{"empty key", Label{Key: ""}, true, "label key cannot be empty"},
		{"key starts with dash", Label{Key: "-env"}, true, "invalid characters"},
		{"key starts with underscore", Label{Key: "_env"}, true, "invalid characters"},
		{"key starts with dot", Label{Key: ".env"}, true, "invalid characters"},
		{"key with space", Label{Key: "my env"}, true, "invalid characters"},
		{"key with equals", Label{Key: "my=env"}, true, "invalid characters"},
		{"key with exclamation", Label{Key: "my!env"}, true, "invalid characters"},
		{"key with special char", Label{Key: "my@env"}, true, "invalid characters"},

		// Invalid values
		{"value starts with dash", Label{Key: "env", Value: "-prod"}, true, "invalid characters"},
		{"value with space", Label{Key: "env", Value: "my prod"}, true, "invalid characters"},
		{"value with special char", Label{Key: "env", Value: "prod@test"}, true, "invalid characters"},

		// Length limits
		{"key too long", Label{Key: string(make([]byte, 64))}, true, "exceeds max length"},
		{"value too long", Label{Key: "env", Value: string(make([]byte, 256))}, true, "exceeds max length"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLabel(tt.label)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateLabels(t *testing.T) {
	t.Parallel()

	t.Run("all valid", func(t *testing.T) {
		labels := Labels{
			{Key: "env", Value: "prod"},
			{Key: "team", Value: "platform"},
		}
		err := ValidateLabels(labels)
		require.NoError(t, err)
	})

	t.Run("one invalid", func(t *testing.T) {
		labels := Labels{
			{Key: "env", Value: "prod"},
			{Key: "invalid key", Value: "value"},
		}
		err := ValidateLabels(labels)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid key")
	})

	t.Run("multiple invalid", func(t *testing.T) {
		labels := Labels{
			{Key: "", Value: "empty"},
			{Key: "invalid key", Value: "value"},
		}
		err := ValidateLabels(labels)
		require.Error(t, err)
		// Both errors should be present
		assert.Contains(t, err.Error(), "cannot be empty")
		assert.Contains(t, err.Error(), "invalid characters")
	})

	t.Run("empty labels", func(t *testing.T) {
		err := ValidateLabels(Labels{})
		require.NoError(t, err)
	})
}

func TestMatchGlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		input   string
		want    bool
	}{
		// Star wildcard
		{"prod*", "prod", true},
		{"prod*", "production", true},
		{"prod*", "prod-us", true},
		{"prod*", "dev", false},
		{"*prod", "preprod", true},
		{"*prod", "prod", true},
		{"*", "anything", true},
		{"*", "", true},

		// Question mark wildcard
		{"te?m", "team", true},
		{"te?m", "teem", true},
		{"te?m", "teaam", false},
		{"te?m", "tem", false},
		{"???", "abc", true},
		{"???", "ab", false},

		// Combined
		{"prod-*", "prod-us", true},
		{"prod-*", "prod-", true},
		{"prod-*", "prod", false},
		{"te?m-*", "team-platform", true},

		// Exact match (no wildcards)
		{"exact", "exact", true},
		{"exact", "other", false},

		// Invalid pattern (causes regex compile error)
		{"[invalid", "test", false},

		// Escaped regex metacharacters
		{"test.name", "test.name", true},
		{"test.name", "testXname", false},
		{"test+name", "test+name", true},
		{"test^name", "test^name", true},
		{"test$name", "test$name", true},
		{"test(name)", "test(name)", true},
		{"test[x]", "test[x]", true},
		{"test{name}", "test{name}", true},
		{"test|name", "test|name", true},
		{"test\\name", "test\\name", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, matchGlob(tt.pattern, tt.input))
		})
	}
}
