// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core/spec/types"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShellValue_UnmarshalYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        string
		wantErr      bool
		errContains  string
		wantCommand  string
		wantArgs     []string
		wantIsArray  bool
		checkNotZero bool
		checkIsZero  bool
	}{
		{
			name:         "StringWithoutArgs",
			input:        "bash",
			wantCommand:  "bash",
			wantArgs:     nil,
			wantIsArray:  false,
			checkNotZero: true,
		},
		{
			name:        "StringWithArgsInline",
			input:       `"bash -e"`,
			wantCommand: "bash -e",
			wantArgs:    nil,
		},
		{
			name:        "ArrayFormInline",
			input:       `["bash", "-e", "-x"]`,
			wantCommand: "bash",
			wantArgs:    []string{"-e", "-x"},
			wantIsArray: true,
		},
		{
			name:        "MultilineArrayForm",
			input:       "- bash\n- -e\n- -x",
			wantCommand: "bash",
			wantArgs:    []string{"-e", "-x"},
		},
		{
			name:         "EmptyString",
			input:        `""`,
			wantCommand:  "",
			checkNotZero: true,
		},
		{
			name:        "EmptyArray",
			input:       "[]",
			wantCommand: "",
			wantArgs:    nil,
		},
		{
			name:        "InvalidTypeMap",
			input:       "{key: value}",
			wantErr:     true,
			errContains: "must be string or array",
		},
		{
			name:        "ShellWithEnvVariableSyntax",
			input:       `"${SHELL}"`,
			wantCommand: "${SHELL}",
		},
		{
			name:        "NixShellExample",
			input:       `["nix-shell", "-p", "python3"]`,
			wantCommand: "nix-shell",
			wantArgs:    []string{"-p", "python3"},
		},
		{
			name:        "NullValue",
			input:       "null",
			checkIsZero: true,
		},
		{
			name:        "ArrayWithNonStringItems",
			input:       "[123, true]",
			wantCommand: "123",
			wantArgs:    []string{"true"},
		},
		{
			name:        "SingleElementArray",
			input:       `["bash"]`,
			wantCommand: "bash",
			wantArgs:    nil,
			wantIsArray: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s types.ShellValue
			err := yaml.Unmarshal([]byte(tt.input), &s)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			if tt.checkIsZero {
				assert.True(t, s.IsZero())
				return
			}
			if tt.checkNotZero {
				assert.False(t, s.IsZero())
			}
			assert.Equal(t, tt.wantCommand, s.Command())
			if tt.wantArgs != nil {
				assert.Equal(t, tt.wantArgs, s.Arguments())
			} else {
				assert.Empty(t, s.Arguments())
			}
			if tt.wantIsArray {
				assert.True(t, s.IsArray())
			}
		})
	}

	t.Run("ZeroValue", func(t *testing.T) {
		t.Parallel()
		var s types.ShellValue
		assert.True(t, s.IsZero())
	})
}

func TestShellValue_InStruct(t *testing.T) {
	t.Parallel()

	type Config struct {
		Shell types.ShellValue `yaml:"shell"`
		Name  string           `yaml:"name"`
	}

	tests := []struct {
		name         string
		input        string
		wantName     string
		wantCommand  string
		wantIsZero   bool
		checkNotZero bool
	}{
		{
			name: "ShellSet",
			input: `
name: test
shell: bash
`,
			wantName:     "test",
			wantCommand:  "bash",
			checkNotZero: true,
		},
		{
			name:       "ShellNotSet",
			input:      "name: test",
			wantIsZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var cfg Config
			err := yaml.Unmarshal([]byte(tt.input), &cfg)
			require.NoError(t, err)
			if tt.wantName != "" {
				assert.Equal(t, tt.wantName, cfg.Name)
			}
			if tt.wantCommand != "" {
				assert.Equal(t, tt.wantCommand, cfg.Shell.Command())
			}
			if tt.wantIsZero {
				assert.True(t, cfg.Shell.IsZero())
			}
			if tt.checkNotZero {
				assert.False(t, cfg.Shell.IsZero())
			}
		})
	}
}

func TestShellValue_AdditionalCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        string
		wantErr      bool
		errContains  string
		wantValue    any
		checkIsArray bool
		wantIsArray  bool
		checkIsZero  bool
	}{
		{
			name:      "ValueReturnsRawString",
			input:     "bash",
			wantValue: "bash",
		},
		{
			name:        "InvalidTypeNumber",
			input:       "123",
			wantErr:     true,
			errContains: "must be string or array",
		},
		{
			name:         "IsArrayReturnsFalseForString",
			input:        `"bash -e"`,
			checkIsArray: true,
			wantIsArray:  false,
		},
		{
			name:         "IsArrayReturnsFalseForNil",
			input:        "null",
			checkIsArray: true,
			wantIsArray:  false,
			checkIsZero:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s types.ShellValue
			err := yaml.Unmarshal([]byte(tt.input), &s)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			if tt.wantValue != nil {
				assert.Equal(t, tt.wantValue, s.Value())
			}
			if tt.checkIsArray {
				assert.Equal(t, tt.wantIsArray, s.IsArray())
			}
			if tt.checkIsZero {
				assert.True(t, s.IsZero())
			}
		})
	}

	t.Run("ValueReturnsRawArray", func(t *testing.T) {
		t.Parallel()
		var s types.ShellValue
		err := yaml.Unmarshal([]byte(`["bash", "-e"]`), &s)
		require.NoError(t, err)
		val, ok := s.Value().([]any)
		require.True(t, ok)
		assert.Len(t, val, 2)
	})
}
