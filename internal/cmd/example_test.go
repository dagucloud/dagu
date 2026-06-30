// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runExampleCmd(args ...string) (string, error) {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(cmd.Example())

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs(args)

	err := root.Execute()
	return buf.String(), err
}

func extractExampleYAML(out string) string {
	lines := strings.Split(out, "\n")
	start := 0
	for start < len(lines) {
		line := lines[start]
		if line == "" || strings.HasPrefix(line, "#") {
			start++
			continue
		}
		break
	}
	return strings.Join(lines[start:], "\n")
}

func TestExampleCommand(t *testing.T) {
	t.Run("ListAll", func(t *testing.T) {
		out, err := runExampleCmd("example")
		require.NoError(t, err)
		assert.Contains(t, out, "parallel-steps")
		assert.Contains(t, out, "custom-action")
	})

	t.Run("ShowByID", func(t *testing.T) {
		out, err := runExampleCmd("example", "1")
		require.NoError(t, err)
		assert.Contains(t, out, "type: graph")
	})

	t.Run("InvalidID", func(t *testing.T) {
		_, err := runExampleCmd("example", "99")
		require.Error(t, err)
		assert.Contains(t, err.Error(), fmt.Sprintf("between 1 and %d", cmd.ExampleCount()))
	})

	t.Run("AllExamplesLoadYAML", func(t *testing.T) {
		for i := 1; i <= cmd.ExampleCount(); i++ {
			out, err := runExampleCmd("example", fmt.Sprintf("%d", i))
			require.NoError(t, err, "example %d failed", i)
			_, err = spec.LoadYAML(context.Background(), []byte(extractExampleYAML(out)), spec.WithoutEval())
			require.NoError(t, err, "example %d failed to load", i)
		}
	})
}
