// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateStartArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hasDash bool
		args    []string
		wantErr string
	}{
		{
			name: "NoExtraArgs",
			args: []string{"dag.yaml"},
		},
		{
			name:    "HasDashSeparator",
			hasDash: true,
			args:    []string{"dag.yaml", "p1"},
		},
		{
			name:    "ExtraArgsWithoutDash",
			args:    []string{"dag.yaml", "p1"},
			wantErr: "use '--' before parameters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateStartArgs(tt.hasDash, tt.args)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidateStartParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		defaultParams string
		input         StartParamInput
		wantErr       string
	}{
		{
			name:          "NoDeclaredParamsAllowsPositional",
			defaultParams: "",
			input:         StartParamInput{DashArgs: []string{"success"}},
		},
		{
			name:          "NoDeclaredParamsAllowsNamedPairs",
			defaultParams: "",
			input:         StartParamInput{DashArgs: []string{"key1=value1", "key2=value2"}},
		},
		{
			name:          "AllowsNoProvidedParamsWhenDeclared",
			defaultParams: "p1 p2",
			input:         StartParamInput{},
		},
		{
			name:          "AllowsFewerThanDeclaredPositional",
			defaultParams: "p1 p2",
			input:         StartParamInput{DashArgs: []string{"only-one"}},
		},
		{
			name:          "RejectsMoreThanDeclaredPositional",
			defaultParams: "p1 p2",
			input:         StartParamInput{DashArgs: []string{"one", "two", "three"}},
			wantErr:       "too many positional params: expected at most 2, got 3",
		},
		{
			name:          "NamedOnlyBypassesPositionalCount",
			defaultParams: "p1 p2",
			input:         StartParamInput{RawParams: "KEY1=value1 KEY2=value2"},
		},
		{
			name:          "JSONRawBypassesValidation",
			defaultParams: "p1 p2",
			input:         StartParamInput{RawParams: `{"k":"v"}`},
		},
		{
			name:          "JSONDashBypassesValidation",
			defaultParams: "p1 p2",
			input:         StartParamInput{DashArgs: []string{`{"k":"v"}`}},
		},
		{
			name:          "NamedDeclaredParamCountsAsOneExpected",
			defaultParams: "ITEM=default",
			input:         StartParamInput{DashArgs: []string{"server1"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateStartParams(tt.defaultParams, tt.input)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestParseParamTokens_Matrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []paramToken
	}{
		{
			name:  "NamedBacktickWithInnerDoubleQuotes",
			input: `cmd=` + "`echo \"hello world\"`",
			expected: []paramToken{
				{Name: "cmd", Value: "`echo \"hello world\"`"},
			},
		},
		{
			name:  "PositionalBacktickToken",
			input: "`echo \"hello\"`",
			expected: []paramToken{
				{Name: "", Value: "`echo \"hello\"`"},
			},
		},
		{
			name:  "MixedNamedBacktickQuotedAndPositional",
			input: `A=1 cmd=` + "`echo \"x\"`" + ` B="y z" bare`,
			expected: []paramToken{
				{Name: "A", Value: "1"},
				{Name: "cmd", Value: "`echo \"x\"`"},
				{Name: "B", Value: "y z"},
				{Name: "", Value: "bare"},
			},
		},
		{
			name:  "EscapedQuotesInDoubleQuotedToken",
			input: `X="a \"b\"" Y=2`,
			expected: []paramToken{
				{Name: "X", Value: `a "b"`},
				{Name: "Y", Value: "2"},
			},
		},
		{
			name:  "MultilineQuotedValue",
			input: `msg="line1\nline2"`,
			expected: []paramToken{
				{Name: "msg", Value: "line1\nline2"},
			},
		},
		{
			name:  "EscapedBackslash",
			input: `path="C:\\Users"`,
			expected: []paramToken{
				{Name: "path", Value: `C:\Users`},
			},
		},
		{
			name:     "EmptyInput",
			input:    "",
			expected: []paramToken{},
		},
		{
			name:     "WhitespaceInput",
			input:    "   ",
			expected: []paramToken{},
		},
		{
			name:  "UnterminatedDoubleQuoteFallback",
			input: `A="unterminated B=2`,
			expected: []paramToken{
				{Name: "", Value: "A="},
				{Name: "", Value: "unterminated"},
				{Name: "B", Value: "2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseParamTokens(tt.input)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestShouldSkipDashArgsPositionalValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want bool
	}{
		{
			name: "SingleJSONObject",
			args: []string{`{"KEY":"value"}`},
			want: true,
		},
		{
			name: "SingleJSONArray",
			args: []string{`["a","b"]`},
			want: true,
		},
		{
			name: "SingleQuotedJSONObject",
			args: []string{`'{"KEY":"value"}'`},
			want: false,
		},
		{
			name: "SingleNonJSON",
			args: []string{"key=value"},
			want: false,
		},
		{
			name: "MultipleArgs",
			args: []string{"key=value", "key2=value2"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := shouldSkipDashArgsPositionalValidation(tt.args)
			require.Equal(t, tt.want, got)
		})
	}
}
