// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package masking

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaskingWriter_BasicWrites(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		secrets  []string
		input    []byte
		expected string
	}{
		{
			name:     "SingleWrite",
			secrets:  []string{"API_KEY=secret123"},
			input:    []byte("The key is secret123\n"),
			expected: "The key is *******\n",
		},
		{
			name:     "MultipleLines",
			secrets:  []string{"API_KEY=secret123"},
			input:    []byte("Line 1: secret123\nLine 2: secret123\n"),
			expected: "Line 1: *******\nLine 2: *******\n",
		},
		{
			name:     "MultipleSecrets",
			secrets:  []string{"API_KEY=secret123", "PASSWORD=pass456"},
			input:    []byte("API: secret123, PWD: pass456\n"),
			expected: "API: *******, PWD: *******\n",
		},
		{
			name:     "EmptyWrite",
			secrets:  []string{"API_KEY=secret123"},
			input:    []byte{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			sources := SourcedEnvVars{Secrets: tt.secrets}
			masker := NewMasker(sources)
			writer := NewMaskingWriter(&buf, masker)

			n, err := writer.Write(tt.input)
			require.NoError(t, err)
			assert.Equal(t, len(tt.input), n)
			assert.Equal(t, tt.expected, buf.String())
		})
	}
}

func TestMaskingWriter_Buffering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		secrets     []string
		writes      [][]byte
		flushAfter  bool
		expectedMid string // Expected output after writes but before flush
		expectedEnd string // Expected output after flush
	}{
		{
			name:        "SplitAcrossWrites",
			secrets:     []string{"API_KEY=secret123"},
			writes:      [][]byte{[]byte("The key is "), []byte("secret123\n")},
			expectedMid: "",
			expectedEnd: "The key is *******\n",
		},
		{
			name:        "SecretSplitAcrossWrites",
			secrets:     []string{"API_KEY=secret123"},
			writes:      [][]byte{[]byte("The key is sec"), []byte("ret123\n")},
			expectedMid: "",
			expectedEnd: "The key is *******\n",
		},
		{
			name:        "NoNewline",
			secrets:     []string{"API_KEY=secret123"},
			writes:      [][]byte{[]byte("The key is secret123")},
			flushAfter:  true,
			expectedMid: "",
			expectedEnd: "The key is *******",
		},
		{
			name:        "PartialLineBuffer",
			secrets:     []string{"API_KEY=secret123"},
			writes:      [][]byte{[]byte("Complete line\n"), []byte("Partial secret123")},
			flushAfter:  true,
			expectedMid: "Complete line\n",
			expectedEnd: "Complete line\nPartial *******",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			sources := SourcedEnvVars{Secrets: tt.secrets}
			masker := NewMasker(sources)
			writer := NewMaskingWriter(&buf, masker)

			// Perform writes
			for i, write := range tt.writes {
				_, err := writer.Write(write)
				require.NoError(t, err)

				// Check intermediate state after first write if specified
				if i == 0 && tt.expectedMid != "" {
					assert.Equal(t, tt.expectedMid, buf.String())
				}
			}

			// Check state before flush
			if !tt.flushAfter && tt.expectedMid != "" {
				assert.Equal(t, tt.expectedMid, buf.String())
			}

			// Flush if needed
			if tt.flushAfter {
				err := writer.Flush()
				require.NoError(t, err)
			}

			assert.Equal(t, tt.expectedEnd, buf.String())
		})
	}
}

func TestMaskingWriter_FlushAndClose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		secrets  []string
		input    []byte
		testFunc func(t *testing.T, writer *MaskingWriter, buf *bytes.Buffer)
	}{
		{
			name:    "Flush",
			secrets: []string{"API_KEY=secret123"},
			input:   []byte("secret123"),
			testFunc: func(t *testing.T, writer *MaskingWriter, buf *bytes.Buffer) {
				// First flush
				err := writer.Flush()
				require.NoError(t, err)
				assert.Equal(t, "*******", buf.String())

				// Second flush (should be no-op)
				err = writer.Flush()
				require.NoError(t, err)
				assert.Equal(t, "*******", buf.String())
			},
		},
		{
			name:    "Close",
			secrets: []string{"API_KEY=secret123"},
			input:   []byte("secret123"),
			testFunc: func(t *testing.T, writer *MaskingWriter, buf *bytes.Buffer) {
				err := writer.Close()
				require.NoError(t, err)
				assert.Equal(t, "*******", buf.String())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			sources := SourcedEnvVars{Secrets: tt.secrets}
			masker := NewMasker(sources)
			writer := NewMaskingWriter(&buf, masker)

			_, err := writer.Write(tt.input)
			require.NoError(t, err)
			assert.Empty(t, buf.String()) // Buffered, not written yet

			tt.testFunc(t, writer, &buf)
		})
	}
}

func TestMaskingWriter_NilMasker(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writer := NewMaskingWriter(&buf, nil)

	input := "The key is secret123\n"
	_, err := writer.Write([]byte(input))
	require.NoError(t, err)

	// Should pass through without masking
	assert.Equal(t, input, buf.String())
}

// A value added to the masker after the writer wraps it is masked in the
// lines written from then on.
func TestMaskingWriter_AddedValue(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	masker := NewValueMasker([]string{"first"})
	writer := NewMaskingWriter(&buf, masker)

	_, err := writer.Write([]byte("first later\n"))
	require.NoError(t, err)
	masker.Add("later")
	_, err = writer.Write([]byte("first later\n"))
	require.NoError(t, err)

	assert.Equal(t, "******* later\n******* *******\n", buf.String())
}

func TestMaskingWriter_CloseWithCloser(t *testing.T) {
	t.Parallel()

	closerWriter := &mockCloser{buf: &bytes.Buffer{}}
	sources := SourcedEnvVars{Secrets: []string{"API_KEY=secret123"}}
	masker := NewMasker(sources)
	writer := NewMaskingWriter(closerWriter, masker)

	_, err := writer.Write([]byte("secret123"))
	require.NoError(t, err)

	// Close should flush and call underlying Close
	err = writer.Close()
	require.NoError(t, err)
	assert.True(t, closerWriter.closed)
	assert.Equal(t, "*******", closerWriter.buf.String())
}

func TestMaskingWriter_LargeWrite(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	sources := SourcedEnvVars{Secrets: []string{"API_KEY=secret123"}}
	masker := NewMasker(sources)
	writer := NewMaskingWriter(&buf, masker)

	// Generate large input with secrets scattered throughout
	var input strings.Builder
	for i := range 1000 {
		input.WriteString("Line ")
		input.WriteString(string(rune('0' + (i % 10))))
		input.WriteString(": The secret is secret123\n")
	}

	_, err := writer.Write([]byte(input.String()))
	require.NoError(t, err)

	// Verify all secrets are masked
	output := buf.String()
	assert.NotContains(t, output, "secret123")
	assert.Contains(t, output, "*******")
}

// mockCloser is a mock writer that implements io.WriteCloser
type mockCloser struct {
	buf    *bytes.Buffer
	closed bool
}

func (m *mockCloser) Write(p []byte) (n int, err error) {
	return m.buf.Write(p)
}

func (m *mockCloser) Close() error {
	m.closed = true
	return nil
}

var _ io.WriteCloser = (*mockCloser)(nil)
