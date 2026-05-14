// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileActions(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	workDirForYAML := filepath.ToSlash(workDir)

	th := test.Setup(t)
	dag := th.DAG(t, `
working_dir: "`+workDirForYAML+`"
type: graph
steps:
  - id: make_dir
    action: file.mkdir
    with:
      path: data

  - id: write_file
    depends: [make_dir]
    action: file.write
    with:
      path: data/input.txt
      content: alpha

  - id: copy_file
    depends: [write_file]
    action: file.copy
    with:
      source: data/input.txt
      destination: data/copy.txt

  - id: list_files
    depends: [copy_file]
    action: file.list
    with:
      path: data
      recursive: true
      pattern: "**/*.txt"
    output: FILES

  - id: read_file
    depends: [list_files]
    action: file.read
    with:
      path: data/copy.txt
    output: CONTENT

  - id: delete_source
    depends: [read_file]
    action: file.delete
    with:
      path: data/input.txt
`)

	agent := dag.Agent()
	agent.RunSuccess(t)

	dag.AssertLatestStatus(t, core.Succeeded)
	dag.AssertOutputs(t, map[string]any{
		"FILES":   test.Contains("copy.txt"),
		"CONTENT": "alpha",
	})

	assert.NoFileExists(t, filepath.Join(workDir, "data", "input.txt"))
	content, err := os.ReadFile(filepath.Join(workDir, "data", "copy.txt"))
	require.NoError(t, err)
	assert.Equal(t, "alpha", string(content))
}
