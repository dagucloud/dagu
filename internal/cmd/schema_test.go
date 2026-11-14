// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runSchemaCmd(args ...string) (string, error) {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(cmd.Schema())

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs(args)

	err := root.Execute()
	return buf.String(), err
}

func TestSchemaCommand(t *testing.T) {
	t.Run("DAGRoot", func(t *testing.T) {
		out, err := runSchemaCmd("schema", "dag")
		require.NoError(t, err)
		assert.Contains(t, out, "steps")
		assert.Contains(t, out, "schedule")
	})

	t.Run("ConfigRoot", func(t *testing.T) {
		out, err := runSchemaCmd("schema", "config")
		require.NoError(t, err)
		assert.Contains(t, out, "server")
		assert.Contains(t, out, "auth")
		assert.Contains(t, out, "license")
	})

	t.Run("CalendarHolidays", func(t *testing.T) {
		out, err := runSchemaCmd("schema", "calendar", "holidays")
		require.NoError(t, err)
		assert.Contains(t, out, "inclusive range")
	})

	t.Run("DAGSteps", func(t *testing.T) {
		out, err := runSchemaCmd("schema", "dag", "steps")
		require.NoError(t, err)
		assert.Contains(t, out, "name")
		assert.Contains(t, out, "command")
	})

	t.Run("DAGStepContainer", func(t *testing.T) {
		out, err := runSchemaCmd("schema", "dag", "steps.container")
		require.NoError(t, err)
		assert.Contains(t, out, `"pull_policy"`)
	})

	t.Run("DAGStepContainerField", func(t *testing.T) {
		out, err := runSchemaCmd("schema", "dag", "steps.container.image")
		require.NoError(t, err)
		assert.Contains(t, out, "Container image to use")
	})

	t.Run("ConfigReferencedSection", func(t *testing.T) {
		out, err := runSchemaCmd("schema", "config", "auth")
		require.NoError(t, err)
		assert.Contains(t, out, `"oidc"`)

		out, err = runSchemaCmd("schema", "config", "auth.mode")
		require.NoError(t, err)
		assert.Contains(t, out, "Authentication mode")
	})

	// Every example in the help text names a path that resolves.
	t.Run("HelpExamples", func(t *testing.T) {
		for line := range strings.Lines(cmd.Schema().Example) {
			command, _, _ := strings.Cut(strings.TrimSpace(line), "  ")
			args := strings.Fields(command)[1:]
			_, err := runSchemaCmd(args...)
			assert.NoError(t, err, command)
		}
	})

	t.Run("InvalidSchema", func(t *testing.T) {
		_, err := runSchemaCmd("schema", "invalid")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "available")
	})

	t.Run("InvalidPath", func(t *testing.T) {
		_, err := runSchemaCmd("schema", "dag", "nonexistent.path")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("NoArgs", func(t *testing.T) {
		_, err := runSchemaCmd("schema")
		require.Error(t, err)
	})
}
