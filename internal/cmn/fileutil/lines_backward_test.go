// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type backwardLine struct {
	text       string
	terminated bool
}

// linesBackward collects every line ReadLinesBackward reports for data.
func linesBackward(t *testing.T, data string) []backwardLine {
	t.Helper()
	var lines []backwardLine
	err := ReadLinesBackward(context.Background(), strings.NewReader(data), int64(len(data)), func(line []byte, terminated bool) (bool, error) {
		lines = append(lines, backwardLine{string(line), terminated})
		return false, nil
	})
	require.NoError(t, err)
	return lines
}

func TestReadLinesBackward(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 3*backwardChunkSize+17)
	tests := []struct {
		name string
		data string
		want []backwardLine
	}{
		{name: "empty", data: "", want: nil},
		{name: "terminated lines", data: "a\nb\n", want: []backwardLine{{"b", true}, {"a", true}}},
		{name: "unterminated last line", data: "a\nb", want: []backwardLine{{"b", false}, {"a", true}}},
		{name: "only a newline", data: "\n", want: []backwardLine{{"", true}}},
		{name: "blank lines", data: "a\n\nb\n", want: []backwardLine{{"b", true}, {"", true}, {"a", true}}},
		{name: "carriage returns kept", data: "a\r\nb\r\n", want: []backwardLine{{"b\r", true}, {"a\r", true}}},
		{name: "line longer than a chunk", data: "first\n" + long + "\nlast\n", want: []backwardLine{{"last", true}, {long, true}, {"first", true}}},
		{name: "unterminated line longer than a chunk", data: "first\n" + long, want: []backwardLine{{long, false}, {"first", true}}},
		// The last chunk starts right after the first newline.
		{name: "newline ending a chunk", data: "x\n" + strings.Repeat("y", backwardChunkSize-1) + "\n", want: []backwardLine{{strings.Repeat("y", backwardChunkSize-1), true}, {"x", true}}},
		// The last chunk starts with the first newline.
		{name: "newline starting a chunk", data: "x\n" + strings.Repeat("y", backwardChunkSize-2) + "\n", want: []backwardLine{{strings.Repeat("y", backwardChunkSize-2), true}, {"x", true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, linesBackward(t, tt.data))
		})
	}
}

// offsetReader records the lowest offset read.
type offsetReader struct {
	*bytes.Reader
	lowest int64
}

func (r *offsetReader) ReadAt(p []byte, off int64) (int, error) {
	r.lowest = min(r.lowest, off)
	return r.Reader.ReadAt(p, off)
}

func TestReadLinesBackwardStopsReadingWhereItIsDone(t *testing.T) {
	t.Parallel()

	data := strings.Repeat(strings.Repeat("e", 1000)+"\n", 1000) + "last\n"
	r := &offsetReader{Reader: bytes.NewReader([]byte(data)), lowest: int64(len(data))}
	var got string
	err := ReadLinesBackward(context.Background(), r, int64(len(data)), func(line []byte, _ bool) (bool, error) {
		got = string(line)
		return true, nil
	})
	require.NoError(t, err)
	require.Equal(t, "last", got)
	require.Equal(t, int64(len(data)-backwardChunkSize), r.lowest, "only the last chunk is read")
}

func TestReadLinesBackwardReportsErrors(t *testing.T) {
	t.Parallel()

	data := "a\nb\n"
	stop := errors.New("stop")
	err := ReadLinesBackward(context.Background(), strings.NewReader(data), int64(len(data)), func([]byte, bool) (bool, error) {
		return false, stop
	})
	require.ErrorIs(t, err, stop)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = ReadLinesBackward(ctx, strings.NewReader(data), int64(len(data)), func([]byte, bool) (bool, error) {
		t.Fatal("a canceled read reported a line")
		return false, nil
	})
	require.ErrorIs(t, err, context.Canceled)

	// A size past the end of r, as when a file shrinks after its size is taken.
	err = ReadLinesBackward(context.Background(), strings.NewReader(data), int64(len(data))+8, func([]byte, bool) (bool, error) {
		t.Fatal("a short read reported a line")
		return false, nil
	})
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}
