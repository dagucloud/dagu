// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlushableMultiWriter_Write(t *testing.T) {
	tests := []struct {
		name    string
		writers []io.Writer
		input   []byte
		wantErr bool
	}{
		{
			name:    "WriteToSingleWriter",
			writers: []io.Writer{&bytes.Buffer{}},
			input:   []byte("hello world"),
			wantErr: false,
		},
		{
			name:    "WriteToMultipleWriters",
			writers: []io.Writer{&bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{}},
			input:   []byte("test data"),
			wantErr: false,
		},
		{
			name:    "EmptyWrite",
			writers: []io.Writer{&bytes.Buffer{}},
			input:   []byte{},
			wantErr: false,
		},
		{
			name:    "WriteWithError",
			writers: []io.Writer{&errorWriter{err: errors.New("write failed")}},
			input:   []byte("data"),
			wantErr: true,
		},
		{
			name:    "WriteWithShortWrite",
			writers: []io.Writer{&shortWriter{}},
			input:   []byte("data"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fw := newFlushableMultiWriter(tt.writers...)
			n, err := fw.Write(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, len(tt.input), n)

				// Verify all writers received the data
				for _, w := range tt.writers {
					if buf, ok := w.(*bytes.Buffer); ok {
						assert.Equal(t, string(tt.input), buf.String())
					}
				}
			}
		})
	}
}

func TestFlushableMultiWriter_Flush(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() (*flushableMultiWriter, func())
		wantErr bool
	}{
		{
			name: "FlushBufferedWriter",
			setup: func() (*flushableMultiWriter, func()) {
				var buf bytes.Buffer
				bw := bufio.NewWriter(&buf)
				fw := newFlushableMultiWriter(bw)

				// Write some data that will be buffered
				_, err := fw.Write([]byte("buffered data"))
				require.NoError(t, err)

				return fw, func() {
					// Check that data was flushed to underlying buffer
					assert.Equal(t, "buffered data", buf.String())
				}
			},
			wantErr: false,
		},
		{
			name: "FlushMultipleWriters",
			setup: func() (*flushableMultiWriter, func()) {
				var buf1, buf2, buf3 bytes.Buffer
				bw1 := bufio.NewWriter(&buf1)
				bw2 := bufio.NewWriter(&buf2)
				fw := newFlushableMultiWriter(bw1, &buf3, bw2)

				// Write data
				_, err := fw.Write([]byte("test"))
				require.NoError(t, err)

				return fw, func() {
					// Both buffered writers should be flushed
					assert.Equal(t, "test", buf1.String())
					assert.Equal(t, "test", buf2.String())
					// Regular buffer gets data immediately
					assert.Equal(t, "test", buf3.String())
				}
			},
			wantErr: false,
		},
		{
			name: "FlushWithFlushableInterface",
			setup: func() (*flushableMultiWriter, func()) {
				f := &flushableWriter{flushed: false}
				fw := newFlushableMultiWriter(f)
				return fw, func() {
					assert.True(t, f.flushed, "flushable writer should be flushed")
				}
			},
			wantErr: false,
		},
		{
			name: "FlushWithSyncableInterface",
			setup: func() (*flushableMultiWriter, func()) {
				s := &syncableWriter{synced: false}
				fw := newFlushableMultiWriter(s)
				return fw, func() {
					assert.True(t, s.synced, "syncable writer should be synced")
				}
			},
			wantErr: false,
		},
		{
			name: "FlushWithError",
			setup: func() (*flushableMultiWriter, func()) {
				f := &flushableWriter{err: errors.New("flush failed")}
				fw := newFlushableMultiWriter(f)
				return fw, func() {}
			},
			wantErr: true,
		},
		{
			name: "FlushWithNoFlushableWriters",
			setup: func() (*flushableMultiWriter, func()) {
				fw := newFlushableMultiWriter(&bytes.Buffer{})
				return fw, func() {}
			},
			wantErr: false,
		},
		{
			name: "FlushWithMixedWriterTypes",
			setup: func() (*flushableMultiWriter, func()) {
				var buf bytes.Buffer
				bw := bufio.NewWriter(&buf)
				f := &flushableWriter{flushed: false}
				s := &syncableWriter{synced: false}

				fw := newFlushableMultiWriter(bw, &bytes.Buffer{}, f, s)
				_, err := fw.Write([]byte("mixed"))
				require.NoError(t, err)

				return fw, func() {
					assert.Equal(t, "mixed", buf.String())
					assert.True(t, f.flushed)
					assert.True(t, s.synced)
				}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fw, verify := tt.setup()
			err := fw.Flush()

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			verify()
		})
	}
}

func TestFlushableMultiWriter_Integration(t *testing.T) {
	// Test the full flow with pipes similar to how it's used in node.go
	t.Run("PipeWithBufferedWriter", func(t *testing.T) {
		pr, pw := io.Pipe()
		defer func() { _ = pr.Close() }()
		defer func() { _ = pw.Close() }()

		var captured bytes.Buffer
		done := make(chan struct{})

		// Start reading from pipe
		go func() {
			defer close(done)
			_, _ = io.Copy(&captured, pr)
		}()

		// Create flushable multi writer with buffered writer
		var logBuffer bytes.Buffer
		logWriter := bufio.NewWriter(&logBuffer)
		fw := newFlushableMultiWriter(pw, logWriter)

		// Write data
		data := "test data for pipe"
		n, err := fw.Write([]byte(data))
		require.NoError(t, err)
		require.Equal(t, len(data), n)

		// Data should be in pipe but not yet in logBuffer
		assert.Empty(t, logBuffer.String(), "data should still be buffered")

		// Flush the writer
		err = fw.Flush()
		require.NoError(t, err)

		// Now data should be in logBuffer
		assert.Equal(t, data, logBuffer.String())

		// Close pipe and wait for reader
		err = pw.Close()
		require.NoError(t, err)
		<-done

		// Verify captured data
		assert.Equal(t, data, captured.String())
	})
}

// A periodic flush must not write a partial line, or a secret split across
// writes reaches the log unmasked. Flush ends the attempt and writes it all.
func TestSafeBufferedWriter_MaskedPartialLine(t *testing.T) {
	var out bytes.Buffer
	masker := masking.NewMasker(masking.SourcedEnvVars{Secrets: []string{"TOKEN=s3cr3t"}})
	w := newSafeBufferedWriter(masking.NewMaskingWriter(&out, masker))

	_, err := w.Write([]byte("s3c"))
	require.NoError(t, err)
	require.NoError(t, w.FlushIfDue())
	assert.Empty(t, out.String())

	_, err = w.Write([]byte("r3t"))
	require.NoError(t, err)
	require.NoError(t, w.Flush())
	assert.Equal(t, "*******", out.String())
}

// Helper types for testing

type errorWriter struct {
	err error
}

func (e *errorWriter) Write(_ []byte) (n int, err error) {
	return 0, e.err
}

type shortWriter struct{}

func (s *shortWriter) Write(p []byte) (n int, err error) {
	if len(p) > 0 {
		return len(p) - 1, nil
	}
	return 0, nil
}

type flushableWriter struct {
	bytes.Buffer
	flushed bool
	err     error
}

func (f *flushableWriter) Flush() error {
	f.flushed = true
	return f.err
}

type syncableWriter struct {
	bytes.Buffer
	synced bool
	err    error
}

func (s *syncableWriter) Sync() error {
	s.synced = true
	return s.err
}
