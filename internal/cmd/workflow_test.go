// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/stretchr/testify/require"
)

func TestWorkflowValidateCommand(t *testing.T) {
	dir := t.TempDir()
	workflowFile := filepath.Join(dir, "workflow.yaml")
	require.NoError(t, os.WriteFile(workflowFile, []byte(`
steps:
  - name: hello
    run: echo hello
`), 0600))

	command := cmd.Workflow()
	command.SetArgs([]string{"validate", workflowFile})

	require.NoError(t, command.Execute())
}
