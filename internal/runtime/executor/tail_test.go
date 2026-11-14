// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"bytes"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"golang.org/x/text/encoding/japanese"
)

func TestTailWriter_RollingBufferSimple(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	tw := NewTailWriter(&buf, 100)

	input := "line1\nline2\n"
	n, err := tw.Write([]byte(input))
	assert.NoError(t, err)
	assert.Equal(t, len(input), n)

	// Underlying receives full content
	assert.Equal(t, input, buf.String())
	// Tail returns recent output (not just the last line)
	assert.Equal(t, input, tw.Tail())
}

func TestTailWriter_AcrossWritesAndLimit(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	limit := 10
	tw := NewTailWriter(&buf, limit)

	_, _ = tw.Write([]byte("one\nlin")) // "one\nlin"
	assert.True(t, len(tw.Tail()) <= limit)
	assert.True(t, bytes.HasSuffix([]byte(tw.Tail()), []byte("one\nlin")))

	_, _ = tw.Write([]byte("e2\npar")) // "one\nline2\npar"
	assert.True(t, len(tw.Tail()) <= limit)
	assert.True(t, bytes.HasSuffix([]byte(tw.Tail()), []byte("e2\npar")))

	_, _ = tw.Write([]byte("tial")) // "one\nline2\npartial"
	assert.True(t, len(tw.Tail()) <= limit)
	assert.True(t, bytes.HasSuffix([]byte(tw.Tail()), []byte("partial")))
}

func TestTailWriter_NoNewlineStillIncluded(t *testing.T) {
	t.Parallel()
	tw := NewTailWriter(&bytes.Buffer{}, 50)

	_, _ = tw.Write([]byte("no newline yet"))
	// Now tail contains the partial line
	assert.Equal(t, "no newline yet", tw.Tail())

	_, _ = tw.Write([]byte("\n"))
	assert.Equal(t, "no newline yet\n", tw.Tail())
}

func TestTailWriter_TrimToLimit(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	tw := NewTailWriter(&buf, 5)

	_, _ = tw.Write([]byte("abcdef"))
	// Only last 5 bytes should remain
	assert.Equal(t, "bcdef", tw.Tail())
}

func TestTailWriter_DecodesConfiguredEncoding(t *testing.T) {
	t.Parallel()
	tw := NewTailWriterWithEncoding(&bytes.Buffer{}, 100, "shift_jis")

	sjis, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte("発生場所 s:1"))
	assert.NoError(t, err)
	_, _ = tw.Write(sjis)

	assert.Equal(t, "発生場所 s:1", tw.Tail())
}

// A shell can switch its output to UTF-8 regardless of the configured
// encoding, so output that is already UTF-8 must not be decoded again.
func TestTailWriter_KeepsUTF8WithConfiguredEncoding(t *testing.T) {
	t.Parallel()
	tw := NewTailWriterWithEncoding(&bytes.Buffer{}, 100, "shift_jis")

	_, _ = tw.Write([]byte("Get-Item: ここ"))

	assert.Equal(t, "Get-Item: ここ", tw.Tail())
}

// The rolling limit can cut a multi-byte rune at the start of the tail; the
// rest of the UTF-8 output must still be returned as is.
func TestTailWriter_TrimSplitsUTF8Rune(t *testing.T) {
	t.Parallel()
	tw := NewTailWriterWithEncoding(&bytes.Buffer{}, 10, "shift_jis")

	_, _ = tw.Write([]byte("発生場所"))

	assert.Equal(t, "生場所", tw.Tail())
}

func TestTailWriter_UndecodableOutputIsValidUTF8(t *testing.T) {
	t.Parallel()
	tw := NewTailWriter(&bytes.Buffer{}, 100)

	_, _ = tw.Write([]byte{0x94, 0xad, 0x90, 0xb6, ' ', 's', ':', '1'})

	tail := tw.Tail()
	assert.True(t, utf8.ValidString(tail))
	assert.Contains(t, tail, " s:1")
}
