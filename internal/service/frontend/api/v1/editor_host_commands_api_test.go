// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// missingCommand returns an absolute path that names no file.
func missingCommand(t *testing.T) string {
	t.Helper()
	return filepath.ToSlash(filepath.Join(t.TempDir(), "does-not-exist"))
}

// hostIssues returns the issues of body that depend on the server host.
func hostIssues(body api.ValidateDAGSpec200JSONResponse) []api.ValidationIssue {
	var issues []api.ValidationIssue
	for _, issue := range body.Issues {
		if issue.Source == api.ValidationIssueSourceHost {
			issues = append(issues, issue)
		}
	}
	return issues
}

func TestEditorIssuesMissingCommand(t *testing.T) {
	server := test.SetupServer(t)
	command := missingCommand(t)
	body := validateIssues(t, server,
		fmt.Sprintf("steps:\n  - name: a\n    command: echo a\n  - name: b\n    command: %s --flag\n", command))

	// The warning blocks neither saving nor running.
	require.True(t, body.Valid)
	require.Empty(t, body.Errors)

	issues := hostIssues(body)
	require.Len(t, issues, 1)
	issue := issues[0]
	assert.Equal(t, api.ValidationIssueSeverityWarning, issue.Severity)
	assert.Contains(t, issue.Message, command)
	assert.Contains(t, body.Warnings, issue.Message)
	require.NotNil(t, issue.StepName)
	assert.Equal(t, "b", *issue.StepName)
	require.NotNil(t, issue.StepPath)
	assert.Equal(t, "steps[1]", *issue.StepPath)
	require.NotNil(t, issue.FieldPath)
	assert.Equal(t, "steps[1].command", *issue.FieldPath)
}

func TestEditorIssuesExistingCommand(t *testing.T) {
	server := test.SetupServer(t)
	executable, err := os.Executable()
	require.NoError(t, err)

	body := validateIssues(t, server,
		fmt.Sprintf("steps:\n  - name: a\n    command: %s\n", filepath.ToSlash(executable)))

	require.True(t, body.Valid)
	assert.Empty(t, hostIssues(body))
}

// The server host says nothing about these commands, so they get no warning.
func TestEditorIssuesUnknowableCommand(t *testing.T) {
	server := test.SetupServer(t)
	command := missingCommand(t)

	specs := map[string]string{
		"Variable":  "steps:\n  - name: a\n    command: ${BIN}/tool\n",
		"Container": fmt.Sprintf("steps:\n  - name: a\n    container:\n      image: alpine\n    command: %s\n", command),
		"SSH": fmt.Sprintf(
			"steps:\n  - name: a\n    type: ssh\n    with:\n      user: u\n      host: example.com\n      key: /tmp/key\n    command: %s\n",
			command),
	}
	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			body := validateIssues(t, server, spec)
			require.True(t, body.Valid, "errors: %v", body.Errors)
			assert.Empty(t, hostIssues(body))
		})
	}
}

// The warning reveals which paths exist on the server, so only a caller that
// may edit DAGs gets it.
func TestEditorIssuesMissingCommandNeedsWrite(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.Permissions[config.PermissionWriteDAGs] = false
	}))

	body := validateIssues(t, server,
		fmt.Sprintf("steps:\n  - name: a\n    command: %s\n", missingCommand(t)))

	require.True(t, body.Valid)
	assert.Empty(t, hostIssues(body))
	assert.Empty(t, body.Warnings)
}

// A worker runs the steps of a distributed DAG on its own host.
func TestEditorIssuesMissingCommandOnWorker(t *testing.T) {
	server := test.SetupServer(t, test.WithCoordinatorEnabled())

	body := validateIssues(t, server,
		fmt.Sprintf("worker_selector:\n  gpu: \"true\"\nsteps:\n  - name: a\n    command: %s\n", missingCommand(t)))

	require.True(t, body.Valid, "errors: %v", body.Errors)
	assert.Empty(t, hostIssues(body))
}
