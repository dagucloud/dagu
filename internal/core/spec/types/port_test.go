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

func TestPortValue_UnmarshalYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        string
		wantErr      bool
		errContains  string
		wantString   string
		checkNotZero bool
	}{
		{
			name:         "Integer",
			input:        "22",
			wantString:   "22",
			checkNotZero: true,
		},
		{
			name:       "String",
			input:      `"8080"`,
			wantString: "8080",
		},
		{
			name:       "LargePortNumber",
			input:      "65535",
			wantString: "65535",
		},
		{
			name:        "InvalidTypeArray",
			input:       "[22, 80]",
			wantErr:     true,
			errContains: "must be string or number",
		},
		{
			name:        "InvalidTypeMap",
			input:       "{port: 22}",
			wantErr:     true,
			errContains: "must be string or number",
		},
		{
			name:        "FloatWithDecimal",
			input:       "22.5",
			wantErr:     true,
			errContains: "port must be an integer",
		},
		{
			name:       "FloatWholeNumber",
			input:      "22.0",
			wantString: "22",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var p types.PortValue
			err := yaml.Unmarshal([]byte(tt.input), &p)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantString, p.String())
			if tt.checkNotZero {
				assert.False(t, p.IsZero())
			}
		})
	}

	t.Run("ZeroValue", func(t *testing.T) {
		t.Parallel()
		var p types.PortValue
		assert.True(t, p.IsZero())
		assert.Equal(t, "", p.String())
	})
}

func TestPortValue_InStruct(t *testing.T) {
	t.Parallel()

	type SSHConfig struct {
		Host string          `yaml:"host"`
		Port types.PortValue `yaml:"port"`
		User string          `yaml:"user"`
	}

	sshTests := []struct {
		name       string
		input      string
		wantHost   string
		wantPort   string
		wantUser   string
		wantIsZero bool
	}{
		{
			name: "PortAsInteger",
			input: `
host: example.com
port: 22
user: admin
`,
			wantHost: "example.com",
			wantPort: "22",
			wantUser: "admin",
		},
		{
			name: "PortAsString",
			input: `
host: example.com
port: "2222"
user: admin
`,
			wantPort: "2222",
		},
		{
			name: "PortNotSet",
			input: `
host: example.com
user: admin
`,
			wantIsZero: true,
		},
	}

	for _, tt := range sshTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var cfg SSHConfig
			err := yaml.Unmarshal([]byte(tt.input), &cfg)
			require.NoError(t, err)
			if tt.wantHost != "" {
				assert.Equal(t, tt.wantHost, cfg.Host)
			}
			if tt.wantPort != "" {
				assert.Equal(t, tt.wantPort, cfg.Port.String())
			}
			if tt.wantUser != "" {
				assert.Equal(t, tt.wantUser, cfg.User)
			}
			if tt.wantIsZero {
				assert.True(t, cfg.Port.IsZero())
			}
		})
	}

	t.Run("SMTPPort", func(t *testing.T) {
		t.Parallel()
		type SMTPConfig struct {
			Host     string          `yaml:"host"`
			Port     types.PortValue `yaml:"port"`
			Username string          `yaml:"username"`
		}
		var cfg SMTPConfig
		err := yaml.Unmarshal([]byte(`
host: smtp.example.com
port: 587
username: user
`), &cfg)
		require.NoError(t, err)
		assert.Equal(t, "587", cfg.Port.String())
	})
}

func TestPortValue_AdditionalCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		input           string
		wantValueNotNil bool
		wantValue       any
		wantIsZero      bool
		wantString      string
	}{
		{
			name:            "ValueReturnsRawInt",
			input:           "22",
			wantValueNotNil: true,
		},
		{
			name:      "ValueReturnsRawString",
			input:     `"22"`,
			wantValue: "22",
		},
		{
			name:       "NullValueSetsIsZeroFalse",
			input:      "null",
			wantIsZero: true,
		},
		{
			name:       "LargeInteger",
			input:      "99999",
			wantString: "99999",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var p types.PortValue
			err := yaml.Unmarshal([]byte(tt.input), &p)
			require.NoError(t, err)
			if tt.wantValueNotNil {
				assert.NotNil(t, p.Value())
			}
			if tt.wantValue != nil {
				assert.Equal(t, tt.wantValue, p.Value())
			}
			if tt.wantIsZero {
				assert.True(t, p.IsZero())
			}
			if tt.wantString != "" {
				assert.Equal(t, tt.wantString, p.String())
			}
		})
	}
}
