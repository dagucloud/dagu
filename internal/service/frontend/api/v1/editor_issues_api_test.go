// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validateIssues posts spec to the validate endpoint and returns the response.
func validateIssues(t *testing.T, server test.Server, spec string) api.ValidateDAGSpec200JSONResponse {
	t.Helper()
	name := "issues_api"
	var body api.ValidateDAGSpec200JSONResponse
	server.Client().Post("/api/v1/dags/validate", api.ValidateDAGSpecJSONRequestBody{
		Name: &name, Spec: spec,
	}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &body)
	return body
}

// errorIssues returns the error issues of body and checks that their messages
// are the errors entries, in order.
func errorIssues(t *testing.T, body api.ValidateDAGSpec200JSONResponse) []api.ValidationIssue {
	t.Helper()
	var issues []api.ValidationIssue
	var messages []string
	for _, issue := range body.Issues {
		if issue.Severity == api.ValidationIssueSeverityError {
			issues = append(issues, issue)
			messages = append(messages, issue.Message)
		}
	}
	require.NotEmpty(t, issues)
	require.Equal(t, body.Errors, messages)
	return issues
}

func TestEditorIssuesBadDepends(t *testing.T) {
	server := test.SetupServer(t)
	body := validateIssues(t, server,
		"steps:\n  - name: a\n    command: echo a\n  - name: b\n    command: echo b\n    depends: zz\n")

	require.False(t, body.Valid)
	issues := errorIssues(t, body)
	require.Len(t, issues, 1)
	issue := issues[0]
	assert.Equal(t, api.ValidationIssueSourceBuild, issue.Source)
	require.NotNil(t, issue.StepName)
	assert.Equal(t, "b", *issue.StepName)
	require.NotNil(t, issue.StepPath)
	assert.Equal(t, "steps[1]", *issue.StepPath)
	require.NotNil(t, issue.FieldPath)
	assert.Equal(t, "steps[1].depends", *issue.FieldPath)
}

func TestEditorIssuesSyntaxPosition(t *testing.T) {
	server := test.SetupServer(t)
	body := validateIssues(t, server, "steps:\n  - name: a\n   command: echo\n  x: [\n")

	require.False(t, body.Valid)
	issues := errorIssues(t, body)
	require.Len(t, issues, 1)
	require.NotNil(t, issues[0].Line)
	require.NotNil(t, issues[0].Column)
	assert.Equal(t, 3, *issues[0].Line)
	assert.Equal(t, 4, *issues[0].Column)
	assert.Nil(t, issues[0].StepName)
}

// The build stops at a failing step and returns no steps, so the path comes from the raw YAML.
func TestEditorIssuesBuildStageStep(t *testing.T) {
	server := test.SetupServer(t)
	body := validateIssues(t, server,
		"steps:\n  - name: a\n    command: echo a\n  - name: b\n    action: http.request\n    with:\n      url: 3\n      bogus: x\n")

	require.False(t, body.Valid)
	issues := errorIssues(t, body)
	require.Len(t, issues, 1)
	require.NotNil(t, issues[0].StepName)
	assert.Equal(t, "b", *issues[0].StepName)
	require.NotNil(t, issues[0].StepPath)
	assert.Equal(t, "steps[1]", *issues[0].StepPath)
	require.NotNil(t, issues[0].FieldPath)
	assert.Equal(t, "steps[1].with", *issues[0].FieldPath)
}

func TestEditorIssuesHandlerStep(t *testing.T) {
	server := test.SetupServer(t)
	body := validateIssues(t, server,
		"steps:\n  - name: a\n    command: echo a\nhandler_on:\n  failure:\n    type: bogus\n")

	require.False(t, body.Valid)
	issues := errorIssues(t, body)
	require.Len(t, issues, 1)
	require.NotNil(t, issues[0].StepPath)
	assert.Equal(t, "handler_on.failure", *issues[0].StepPath)
}

func TestEditorIssuesNoticeStep(t *testing.T) {
	server := test.SetupServer(t)
	body := validateIssues(t, server,
		"steps:\n  - name: a\n    command: echo a\n  - name: b\n    command: echo ${steps.nope.outputs.x}\n")

	require.True(t, body.Valid)
	var found bool
	for _, issue := range body.Issues {
		if issue.Source != api.ValidationIssueSourceValueReference {
			continue
		}
		found = true
		require.NotNil(t, issue.StepPath)
		assert.Equal(t, "steps[1]", *issue.StepPath)
		require.NotNil(t, issue.StepName)
		assert.Equal(t, "b", *issue.StepName)
	}
	assert.True(t, found)
}

// One errors entry gives one issue, also when the entry holds a list of errors.
func TestEditorIssuesOnePerEntry(t *testing.T) {
	server := test.SetupServer(t)
	body := validateIssues(t, server,
		"steps:\n  - name: a\n    command: echo a\n    depends: [x1, x2]\n")

	require.False(t, body.Valid)
	issues := errorIssues(t, body)
	assert.Len(t, issues, len(body.Errors))
}

// A calendar error is about one field of the document, so its issue sits on
// that field, as the errors list says.
func TestEditorIssuesCalendarPosition(t *testing.T) {
	server := test.SetupServer(t)
	body := validateIssues(t, server,
		"calendar: nope\nschedule: \"0 9 * * *\"\nsteps:\n  - run: echo hi\n")

	require.False(t, body.Valid)
	issues := errorIssues(t, body)
	require.Len(t, issues, 1)
	require.NotNil(t, issues[0].Line)
	require.NotNil(t, issues[0].Column)
	assert.Equal(t, 1, *issues[0].Line)
	assert.Equal(t, 11, *issues[0].Column)
}
