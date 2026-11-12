// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package masking

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMasker_MaskString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sources  SourcedEnvVars
		input    string
		expected string
	}{
		{
			name: "single value",
			sources: SourcedEnvVars{
				Secrets: []string{"API_KEY=secret123"},
			},
			input:    "The API key is secret123",
			expected: "The API key is *******",
		},
		{
			name: "multiple values",
			sources: SourcedEnvVars{
				Secrets: []string{
					"API_KEY=secret123",
					"PASSWORD=pass456",
				},
			},
			input:    "Key: secret123, Pass: pass456",
			expected: "Key: *******, Pass: *******",
		},
		{
			name: "overlapping values",
			sources: SourcedEnvVars{
				Secrets: []string{"SHORT=abc", "LONG=abcdef"},
			},
			input:    "Value: abcdef",
			expected: "Value: *******",
		},
		{
			name: "no match",
			sources: SourcedEnvVars{
				Secrets: []string{"API_KEY=secret123"},
			},
			input:    "This has no secrets",
			expected: "This has no secrets",
		},
		{
			name: "empty secret value does not mask everything",
			sources: SourcedEnvVars{
				Secrets: []string{
					"EMPTY_SECRET=",
					"REAL_SECRET=actual_value",
				},
			},
			input:    "Testing empty secrets with actual_value here",
			expected: "Testing empty secrets with ******* here",
		},
		{
			name: "only empty secrets",
			sources: SourcedEnvVars{
				Secrets: []string{"EMPTY1=", "EMPTY2="},
			},
			input:    "This should not be masked at all",
			expected: "This should not be masked at all",
		},
		{
			name: "plain secret in JSON text",
			sources: SourcedEnvVars{
				Secrets: []string{"API_KEY=secret123"},
			},
			input:    `{"key":"secret123"}`,
			expected: `{"key":"*******"}`,
		},
		{
			name: "quoted secret escaped in JSON text",
			sources: SourcedEnvVars{
				Secrets: []string{`API_KEY=secret-"quoted"-value`},
			},
			input:    `{"error":"bad key secret-\"quoted\"-value"} secret-"quoted"-value`,
			expected: `{"error":"bad key *******"} *******`,
		},
		{
			name: "backslash secret escaped in JSON text",
			sources: SourcedEnvVars{
				Secrets: []string{`PATH_SECRET=C:\secret\dir`},
			},
			input:    `{"path":"C:\\secret\\dir"}`,
			expected: `{"path":"*******"}`,
		},
		{
			name: "newline secret escaped in JSON text",
			sources: SourcedEnvVars{
				Secrets: []string{"PEM=line one\nline two"},
			},
			input:    `{"pem":"line one\nline two"}`,
			expected: `{"pem":"*******"}`,
		},
		{
			name: "non-ASCII secret as unicode escapes",
			sources: SourcedEnvVars{
				Secrets: []string{"TOKEN=héllo🔑"},
			},
			input:    `{"literal":"héllo🔑","ascii":"h\u00e9llo\ud83d\udd11"}`,
			expected: `{"literal":"*******","ascii":"*******"}`,
		},
		{
			name: "HTML characters as unicode escapes",
			sources: SourcedEnvVars{
				Secrets: []string{"TOKEN=a<b&c>"},
			},
			input:    `{"literal":"a<b&c>","go":"a\u003cb\u0026c\u003e"}`,
			expected: `{"literal":"*******","go":"*******"}`,
		},
		{
			name: "non-ASCII and HTML characters as unicode escapes",
			sources: SourcedEnvVars{
				Secrets: []string{"TOKEN=héllo<🔑>"},
			},
			input:    `{"ascii_html":"h\u00e9llo\u003c\ud83d\udd11\u003e","html":"héllo\u003c🔑\u003e","ascii":"h\u00e9llo<\ud83d\udd11>"}`,
			expected: `{"ascii_html":"*******","html":"*******","ascii":"*******"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := NewMasker(tt.sources)
			output := m.MaskString(tt.input)

			assert.Equal(t, tt.expected, output)
		})
	}
}

func TestMasker_MaskBytes(t *testing.T) {
	t.Parallel()

	sources := SourcedEnvVars{
		Secrets: []string{"API_KEY=secret123"},
	}

	m := NewMasker(sources)
	input := []byte("The API key is secret123")
	output := m.MaskBytes(input)

	assert.Equal(t, []byte("The API key is *******"), output)
}

func TestSplitEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantKey string
		wantVal string
	}{
		{
			name:    "valid env var",
			input:   "API_KEY=secret123",
			wantKey: "API_KEY",
			wantVal: "secret123",
		},
		{
			name:    "value with equals sign",
			input:   "BASE64=YWJjZGVm==",
			wantKey: "BASE64",
			wantVal: "YWJjZGVm==",
		},
		{
			name:    "no equals sign",
			input:   "INVALID",
			wantKey: "",
			wantVal: "",
		},
		{
			name:    "empty value",
			input:   "EMPTY=",
			wantKey: "EMPTY",
			wantVal: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			key, val := splitEnv(tt.input)

			assert.Equal(t, tt.wantKey, key)
			assert.Equal(t, tt.wantVal, val)
		})
	}
}
