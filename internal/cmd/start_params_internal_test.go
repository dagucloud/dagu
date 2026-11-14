// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// pipeStdin replaces process stdin with a pipe holding input until the test
// ends. Not parallel-safe: it swaps the process-global os.Stdin.
func pipeStdin(t *testing.T, input string) {
	t.Helper()

	stdin, writer, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = original
		require.NoError(t, stdin.Close())
	})
	_, err = writer.WriteString(input)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
}

func TestStdinHasParamsInput(t *testing.T) {
	command := &cobra.Command{}
	initFlags(command, paramsStdinFlag)
	require.NoError(t, command.Flags().Set(paramsStdinFlag.name, "true"))
	ctx := &Context{Command: command}

	t.Run("Pipe", func(t *testing.T) {
		pipeStdin(t, "P1=foo")
		hasInput, err := stdinHasParamsInput(ctx)
		require.NoError(t, err)
		require.True(t, hasInput)
	})

	t.Run("RedirectedFile", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "params.txt")
		require.NoError(t, os.WriteFile(path, []byte("P1=foo"), 0o600))
		file, err := os.Open(path)
		require.NoError(t, err)
		original := os.Stdin
		os.Stdin = file
		t.Cleanup(func() {
			os.Stdin = original
			require.NoError(t, file.Close())
		})
		hasInput, err := stdinHasParamsInput(ctx)
		require.NoError(t, err)
		require.True(t, hasInput)
	})

	t.Run("CharacterDevice", func(t *testing.T) {
		// A character device (terminal or /dev/null) is never params input, so
		// interactive runs and runs detached from stdin are not read.
		devNull, err := os.Open(os.DevNull)
		require.NoError(t, err)
		original := os.Stdin
		os.Stdin = devNull
		t.Cleanup(func() {
			os.Stdin = original
			require.NoError(t, devNull.Close())
		})
		hasInput, err := stdinHasParamsInput(ctx)
		require.NoError(t, err)
		require.False(t, hasInput)
	})
}

func TestReadStdinParams(t *testing.T) {
	t.Run("TrimsWhitespace", func(t *testing.T) {
		pipeStdin(t, "  P1=foo P2=bar\n")
		params, err := readStdinParams()
		require.NoError(t, err)
		require.Equal(t, "P1=foo P2=bar", params)
	})

	t.Run("Empty", func(t *testing.T) {
		pipeStdin(t, "")
		params, err := readStdinParams()
		require.NoError(t, err)
		require.Empty(t, params)
	})

	t.Run("ExceedsLimit", func(t *testing.T) {
		// A redirected file larger than the limit is rejected instead of being
		// fully buffered in memory.
		path := filepath.Join(t.TempDir(), "params.txt")
		require.NoError(t, os.WriteFile(path, make([]byte, maxStdinParamsSize+1), 0o600))
		file, err := os.Open(path)
		require.NoError(t, err)
		original := os.Stdin
		os.Stdin = file
		t.Cleanup(func() {
			os.Stdin = original
			require.NoError(t, file.Close())
		})

		_, err = readStdinParams()
		require.ErrorContains(t, err, "exceed the")
	})
}
