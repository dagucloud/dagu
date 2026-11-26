// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package masking

import (
	"regexp"
	"strings"
	"sync"
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
			name: "values overlapping at different offsets",
			sources: SourcedEnvVars{
				Secrets: []string{"USER_PASS=x-t", "TOKEN=token-password"},
			},
			input:    "x-token-password",
			expected: "*******",
		},
		{
			name: "shorter value starting first",
			sources: SourcedEnvVars{
				Secrets: []string{"OLD=db-pass", "NEW=pass-2026-rotated"},
			},
			input:    "PGPASSWORD=db-pass-2026-rotated psql",
			expected: "PGPASSWORD=******* psql",
		},
		{
			name: "values that only touch",
			sources: SourcedEnvVars{
				Secrets: []string{"A=abc", "B=def"},
			},
			input:    "abcdef",
			expected: "**************",
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

// A pattern match and a value that overlap are masked as one span.
func TestNewValueMasker_JoinsPatternMatches(t *testing.T) {
	t.Parallel()

	m := NewValueMasker([]string{"key-tok_"}, regexp.MustCompile(`tok_[a-z]+`))

	assert.Equal(t, "use *******!", m.MaskString("use key-tok_abc!"))
}

// Provider tokens are written split in two, so a secret scanner does not
// take the test data for leaked tokens.
var (
	googleAccessToken  = "ya29." + "a0AfB_byC1x2y3z4ExampleAccessToken-abc"
	googleServiceToken = "ya29." + "c.c0ASRK0GZExampleServiceToken"
	googleRefreshToken = "1//" + "0gExampleRefreshTokenValue-abcdefghijklmnopqrstuvwxyz"
)

func TestNewMasker_ProviderTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "google access token",
			input:    "Authorization: Bearer " + googleAccessToken + "\n",
			expected: "Authorization: Bearer *******\n",
		},
		{
			name:     "google service account token",
			input:    "token=" + googleServiceToken,
			expected: "token=*******",
		},
		{
			name:     "google refresh token in JSON",
			input:    `{"refresh_token":"` + googleRefreshToken + `","expires_in":3599}`,
			expected: `{"refresh_token":"*******","expires_in":3599}`,
		},
		{
			// A doubled slash in a path is too short to be a refresh token.
			name:     "doubled slash in a path",
			input:    "POST /api/v1//license/deactivate",
			expected: "POST /api/v1//license/deactivate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.expected, NewMasker(SourcedEnvVars{}).MaskString(tt.input))
		})
	}
}

func TestMasker_Add(t *testing.T) {
	t.Parallel()

	m := NewValueMasker([]string{"first"})
	m.Add("", `later"value`)

	assert.Equal(t, `******* ******* {"k":"*******"}`, m.MaskString(`first later"value {"k":"later\"value"}`))
}

// Masking goroutines keep hiding the values held before Add while Add runs,
// and the race detector checks that Add is safe beside them.
func TestMasker_AddWhileMasking(t *testing.T) {
	t.Parallel()

	m := NewValueMasker([]string{"first"})
	added := []string{"second", "third", "fourth"}
	text := "first " + strings.Join(added, " ")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				assert.NotContains(t, m.MaskString(text), "first")
			}
		})
	}

	for _, value := range added {
		m.Add(value)
	}
	close(stop)
	wg.Wait()

	assert.Equal(t, "******* ******* ******* *******", m.MaskString(text))
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
