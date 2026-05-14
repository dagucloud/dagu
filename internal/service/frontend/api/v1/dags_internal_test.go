// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	openapi "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	localapi "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

type stubSchedulerStateStore struct {
	state *scheduler.SchedulerState
}

func (s stubSchedulerStateStore) Load(context.Context) (*scheduler.SchedulerState, error) {
	return s.state, nil
}

func (stubSchedulerStateStore) Save(context.Context, *scheduler.SchedulerState) error {
	return nil
}

var errLoadSpecFatal = errors.New("load spec fatal")

type loadSpecErrorDAGStore struct {
	exec.DAGStore
	updateCalled bool
}

func (s *loadSpecErrorDAGStore) GetDetails(context.Context, string, ...spec.LoadOption) (*core.DAG, error) {
	return &core.DAG{Name: "load-spec-error"}, nil
}

func (s *loadSpecErrorDAGStore) LoadSpec(context.Context, []byte, ...spec.LoadOption) (*core.DAG, error) {
	return nil, errLoadSpecFatal
}

func (s *loadSpecErrorDAGStore) UpdateSpec(context.Context, string, []byte) error {
	s.updateCalled = true
	return nil
}

func TestListDAGsDataPreservesNextRunAcrossSSEPath(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	scheduledAt := time.Now().UTC().Truncate(time.Minute).Add(-5 * time.Minute)
	dag := helper.DAG(t, fmt.Sprintf(`
name: sse-next-run-dag
schedule:
  - at: "%s"
steps:
  - run: echo hi
`, scheduledAt.Format(time.RFC3339)))

	state := &scheduler.SchedulerState{
		Version: scheduler.SchedulerStateVersion,
		DAGs: map[string]scheduler.DAGWatermark{
			dag.Name: {
				OneOffs: map[string]scheduler.OneOffScheduleState{
					dag.Schedule[0].Fingerprint(): {
						ScheduledTime: scheduledAt,
						Status:        scheduler.OneOffStatusPending,
					},
				},
			},
		},
	}

	api := localapi.New(
		helper.DAGStore,
		helper.DAGRunStore,
		helper.QueueStore,
		helper.ProcStore,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
		localapi.WithSchedulerStateStore(stubSchedulerStateStore{state: state}),
	)

	name := dag.Name
	listRespObj, err := api.ListDAGs(context.Background(), openapi.ListDAGsRequestObject{
		Params: openapi.ListDAGsParams{Name: &name},
	})
	require.NoError(t, err)

	listResp, ok := listRespObj.(*openapi.ListDAGs200JSONResponse)
	require.True(t, ok)
	require.Len(t, listResp.Dags, 1)
	require.NotNil(t, listResp.Dags[0].NextRun)
	require.True(t, scheduledAt.Equal(*listResp.Dags[0].NextRun))

	sseRespAny, err := api.GetDAGsListData(context.Background(), "name="+name)
	require.NoError(t, err)

	sseResp, ok := sseRespAny.(openapi.ListDAGs200JSONResponse)
	require.True(t, ok)
	require.Len(t, sseResp.Dags, 1)
	require.NotNil(t, sseResp.Dags[0].NextRun)
	require.True(t, listResp.Dags[0].NextRun.Equal(*sseResp.Dags[0].NextRun))
}

func TestGetDAGsListDataUsesConfiguredListDefaults(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	helper.Config.UI.DAGs.SortField = "name"
	helper.Config.UI.DAGs.SortOrder = "desc"
	helper.DAG(t, `
name: sse-sort-alpha
steps:
  - run: echo alpha
`)
	helper.DAG(t, `
name: sse-sort-zulu
steps:
  - run: echo zulu
`)

	api := localapi.New(
		helper.DAGStore,
		helper.DAGRunStore,
		helper.QueueStore,
		helper.ProcStore,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	listRespObj, err := api.ListDAGs(context.Background(), openapi.ListDAGsRequestObject{
		Params: openapi.ListDAGsParams{},
	})
	require.NoError(t, err)

	listResp, ok := listRespObj.(*openapi.ListDAGs200JSONResponse)
	require.True(t, ok)
	require.Len(t, listResp.Dags, 2)
	require.Equal(t, "sse-sort-zulu", listResp.Dags[0].Dag.Name)

	sseRespAny, err := api.GetDAGsListData(context.Background(), "")
	require.NoError(t, err)

	sseResp, ok := sseRespAny.(openapi.ListDAGs200JSONResponse)
	require.True(t, ok)
	require.Len(t, sseResp.Dags, 2)
	require.Equal(t, listResp.Dags[0].Dag.Name, sseResp.Dags[0].Dag.Name)
	require.Equal(t, listResp.Dags[1].Dag.Name, sseResp.Dags[1].Dag.Name)
}

func TestGetDAGDetails_InvalidYAML_Returns200WithErrors(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())

	// Write an invalid YAML file directly to the DAGs directory
	invalidYAML := `this is not valid yaml: [unterminated`
	dagFile := helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "invalid-dag", []byte(invalidYAML))
	fileName := filepath.Base(dagFile)
	// Strip .yaml extension to match how the API resolves filenames
	fileName = fileName[:len(fileName)-len(".yaml")]

	api := localapi.New(
		helper.DAGStore,
		helper.DAGRunStore,
		helper.QueueStore,
		helper.ProcStore,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	respObj, err := api.GetDAGDetails(context.Background(), openapi.GetDAGDetailsRequestObject{
		FileName: fileName,
	})
	// Should NOT return an error (which would become a 404/500)
	require.NoError(t, err)

	resp, ok := respObj.(openapi.GetDAGDetails200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", respObj)

	// Should contain build errors describing the YAML parse failure
	require.NotEmpty(t, resp.Errors, "expected build errors for invalid YAML")

	// File path should still be set
	require.NotNil(t, resp.FilePath)
	require.NotEmpty(t, *resp.FilePath)
}

func TestUpdateDAGSpec_AllowsLegacyDefinitionRuntimeVariableInput(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "legacy-definition-runtime-save", []byte(`
name: legacy-definition-runtime-save
steps:
  - run: echo original
`))

	api := localapi.New(
		helper.DAGStore,
		helper.DAGRunStore,
		helper.QueueStore,
		helper.ProcStore,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	specText := `
name: legacy-definition-runtime-save
type: graph
step_types:
  repeat:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message, count]
      properties:
        message:
          type: string
        count:
          type: integer
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
          - {$input: count}
steps:
  - id: produce
    run: echo 3
    output: COUNT
  - id: consume
    depends: [produce]
    type: repeat
    with:
      message: runtime value
      count: ${COUNT}
`

	respObj, err := api.UpdateDAGSpec(context.Background(), openapi.UpdateDAGSpecRequestObject{
		FileName: "legacy-definition-runtime-save",
		Body: &openapi.UpdateDAGSpecJSONRequestBody{
			Spec: specText,
		},
	})
	require.NoError(t, err)

	resp, ok := respObj.(openapi.UpdateDAGSpec200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", respObj)
	require.Empty(t, resp.Errors)
}

func TestUpdateDAGSpec_StepConfigAliasCompatibility(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "step-config-alias-api", []byte(`
name: step-config-alias-api
steps:
  - run: echo original
`))

	api := localapi.New(
		helper.DAGStore,
		helper.DAGRunStore,
		helper.QueueStore,
		helper.ProcStore,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	respObj, err := api.UpdateDAGSpec(context.Background(), openapi.UpdateDAGSpecRequestObject{
		FileName: "step-config-alias-api",
		Body: &openapi.UpdateDAGSpecJSONRequestBody{
			Spec: `
name: step-config-alias-api
steps:
  - name: request
    type: http
    command: GET https://example.com
    config:
      timeout: 30
`,
		},
	})
	require.NoError(t, err)

	resp, ok := respObj.(openapi.UpdateDAGSpec200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", respObj)
	require.Empty(t, resp.Errors)
}

func TestUpdateDAGSpec_RejectsStepWithAndLegacyConfigTogether(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "step-mixed-config-api", []byte(`
name: step-mixed-config-api
steps:
  - run: echo original
`))

	api := localapi.New(
		helper.DAGStore,
		helper.DAGRunStore,
		helper.QueueStore,
		helper.ProcStore,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	respObj, err := api.UpdateDAGSpec(context.Background(), openapi.UpdateDAGSpecRequestObject{
		FileName: "step-mixed-config-api",
		Body: &openapi.UpdateDAGSpecJSONRequestBody{
			Spec: `
name: step-mixed-config-api
steps:
  - name: request
    type: http
    command: GET https://example.com
    with:
      timeout: 30
    config:
      timeout: 60
`,
		},
	})
	require.NoError(t, err)

	resp, ok := respObj.(openapi.UpdateDAGSpec200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", respObj)
	require.NotEmpty(t, resp.Errors)
	require.Contains(t, resp.Errors[0], `fields "with" and "config" cannot be used together`)
}

func TestUpdateDAGSpec_ReturnsFatalLoadSpecError(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	dagStore := &loadSpecErrorDAGStore{}
	api := localapi.New(
		dagStore,
		helper.DAGRunStore,
		helper.QueueStore,
		helper.ProcStore,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	respObj, err := api.UpdateDAGSpec(context.Background(), openapi.UpdateDAGSpecRequestObject{
		FileName: "load-spec-error",
		Body: &openapi.UpdateDAGSpecJSONRequestBody{
			Spec: "steps:\n  - command: echo updated\n",
		},
	})

	require.ErrorIs(t, err, errLoadSpecFatal)
	require.Nil(t, respObj)
	require.False(t, dagStore.updateCalled)
}

func TestUpdateDAGSpec_NotifiesDAGMutation(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "dag-update-notify", []byte(`
name: dag-update-notify
schedule: "34 * * * *"
steps:
  - run: echo original
`))

	var notified []string
	api := localapi.New(
		helper.DAGStore,
		helper.DAGRunStore,
		helper.QueueStore,
		helper.ProcStore,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
		localapi.WithDAGMutationNotifier(func(fileName string) {
			notified = append(notified, fileName)
		}),
	)

	respObj, err := api.UpdateDAGSpec(context.Background(), openapi.UpdateDAGSpecRequestObject{
		FileName: "dag-update-notify",
		Body: &openapi.UpdateDAGSpecJSONRequestBody{
			Spec: `
name: dag-update-notify
schedule: "43 * * * *"
steps:
  - run: echo updated
`,
		},
	})
	require.NoError(t, err)

	resp, ok := respObj.(openapi.UpdateDAGSpec200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", respObj)
	require.Empty(t, resp.Errors)
	require.Equal(t, []string{"dag-update-notify"}, notified)
}

func TestUpdateDAGSuspensionState_NotifiesDAGMutation(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	dag := helper.DAG(t, `
name: dag-suspend-notify
schedule: "43 * * * *"
steps:
  - run: echo original
`)

	var notified []string
	api := localapi.New(
		helper.DAGStore,
		helper.DAGRunStore,
		helper.QueueStore,
		helper.ProcStore,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
		localapi.WithDAGMutationNotifier(func(fileName string) {
			notified = append(notified, fileName)
		}),
	)

	respObj, err := api.UpdateDAGSuspensionState(context.Background(), openapi.UpdateDAGSuspensionStateRequestObject{
		FileName: dag.FileName(),
		Body: &openapi.UpdateDAGSuspensionStateJSONRequestBody{
			Suspend: true,
		},
	})
	require.NoError(t, err)

	_, ok := respObj.(openapi.UpdateDAGSuspensionState200Response)
	require.True(t, ok, "expected 200 response, got %T", respObj)
	require.Equal(t, []string{dag.FileName()}, notified)
}

func TestGetDAGDetails_EditorHintsIncludeInheritedLegacyDefinitions(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	require.NoError(t, os.WriteFile(helper.Config.Paths.BaseConfig, []byte(`
step_types:
  greet:
    type: command
    description: Send a greeting
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: echo
        args:
          - {$input: message}
actions:
  slack.notify:
    description: Send Slack notification
    input_schema:
      type: object
      additionalProperties: false
      required: [text]
      properties:
        text:
          type: string
    template:
      action: http.request
      with:
        method: POST
        url: ${SLACK_WEBHOOK_URL}
        body: {$input: text}
`), 0o600))

	dag := helper.DAG(t, `
name: inherited-editor-hints
steps:
  - run: echo hi
`)

	api := localapi.New(
		helper.DAGStore,
		helper.DAGRunStore,
		helper.QueueStore,
		helper.ProcStore,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	respObj, err := api.GetDAGDetails(context.Background(), openapi.GetDAGDetailsRequestObject{
		FileName: dag.FileName(),
	})
	require.NoError(t, err)

	resp, ok := respObj.(openapi.GetDAGDetails200JSONResponse)
	require.True(t, ok)
	require.NotNil(t, resp.EditorHints)
	require.Len(t, resp.EditorHints.InheritedLegacyDefinitions, 1)
	require.NotNil(t, resp.EditorHints.InheritedCustomActions)
	require.Len(t, *resp.EditorHints.InheritedCustomActions, 1)

	hint := resp.EditorHints.InheritedLegacyDefinitions[0]
	require.Equal(t, "greet", hint.Name)
	require.Equal(t, "command", hint.TargetType)
	require.NotNil(t, hint.Description)
	require.Equal(t, "Send a greeting", *hint.Description)

	properties, ok := hint.InputSchema["properties"].(map[string]any)
	require.True(t, ok)
	message, ok := properties["message"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "string", message["type"])

	actionHint := (*resp.EditorHints.InheritedCustomActions)[0]
	require.Equal(t, "slack.notify", actionHint.Name)
	require.NotNil(t, actionHint.Description)
	require.Equal(t, "Send Slack notification", *actionHint.Description)

	actionProperties, ok := actionHint.InputSchema["properties"].(map[string]any)
	require.True(t, ok)
	text, ok := actionProperties["text"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "string", text["type"])
}

func TestGetDAGDetails_EditorHintsKeepDistinctDescriptions(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	require.NoError(t, os.WriteFile(helper.Config.Paths.BaseConfig, []byte(`
step_types:
  greet:
    type: command
    description: First description
    input_schema:
      type: object
      properties: {}
    template:
      exec:
        command: echo
  wave:
    type: command
    description: Second description
    input_schema:
      type: object
      properties: {}
    template:
      exec:
        command: echo
`), 0o600))

	dag := helper.DAG(t, `
name: inherited-editor-hint-descriptions
steps:
  - run: echo hi
`)

	api := localapi.New(
		helper.DAGStore,
		helper.DAGRunStore,
		helper.QueueStore,
		helper.ProcStore,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	respObj, err := api.GetDAGDetails(context.Background(), openapi.GetDAGDetailsRequestObject{
		FileName: dag.FileName(),
	})
	require.NoError(t, err)

	resp, ok := respObj.(openapi.GetDAGDetails200JSONResponse)
	require.True(t, ok)
	require.NotNil(t, resp.EditorHints)
	require.Len(t, resp.EditorHints.InheritedLegacyDefinitions, 2)

	require.NotNil(t, resp.EditorHints.InheritedLegacyDefinitions[0].Description)
	require.NotNil(t, resp.EditorHints.InheritedLegacyDefinitions[1].Description)
	require.Equal(t, "First description", *resp.EditorHints.InheritedLegacyDefinitions[0].Description)
	require.Equal(t, "Second description", *resp.EditorHints.InheritedLegacyDefinitions[1].Description)
}

func TestGetDAGDetails_InvalidYAMLStillReturnsEditorHints(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	require.NoError(t, os.WriteFile(helper.Config.Paths.BaseConfig, []byte(`
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: echo
        args:
          - {$input: message}
`), 0o600))

	invalidYAML := `this is not valid yaml: [unterminated`
	dagFile := helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "invalid-hints-dag", []byte(invalidYAML))
	fileName := filepath.Base(dagFile)
	fileName = fileName[:len(fileName)-len(".yaml")]

	api := localapi.New(
		helper.DAGStore,
		helper.DAGRunStore,
		helper.QueueStore,
		helper.ProcStore,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	respObj, err := api.GetDAGDetails(context.Background(), openapi.GetDAGDetailsRequestObject{
		FileName: fileName,
	})
	require.NoError(t, err)

	resp, ok := respObj.(openapi.GetDAGDetails200JSONResponse)
	require.True(t, ok)
	require.NotEmpty(t, resp.Errors)
	require.NotNil(t, resp.EditorHints)
	require.Len(t, resp.EditorHints.InheritedLegacyDefinitions, 1)
	require.Equal(t, "greet", resp.EditorHints.InheritedLegacyDefinitions[0].Name)
}

func TestGetDAGDetails_NonExistent_Returns404(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())

	api := localapi.New(
		helper.DAGStore,
		helper.DAGRunStore,
		helper.QueueStore,
		helper.ProcStore,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	_, err := api.GetDAGDetails(context.Background(), openapi.GetDAGDetailsRequestObject{
		FileName: "does-not-exist",
	})
	var apiErr *localapi.Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, 404, apiErr.HTTPStatus)
	require.Equal(t, openapi.ErrorCodeNotFound, apiErr.Code)
}

func TestGetDAGDetailsAndSpecIncludeNextRun(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	scheduledAt := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	dag := helper.DAG(t, fmt.Sprintf(`
name: dag-details-next-run
schedule:
  - at: "%s"
steps:
  - run: echo hi
`, scheduledAt.Format(time.RFC3339)))

	state := &scheduler.SchedulerState{
		Version: scheduler.SchedulerStateVersion,
		DAGs: map[string]scheduler.DAGWatermark{
			dag.Name: {
				OneOffs: map[string]scheduler.OneOffScheduleState{
					dag.Schedule[0].Fingerprint(): {
						ScheduledTime: scheduledAt,
						Status:        scheduler.OneOffStatusPending,
					},
				},
			},
		},
	}

	api := localapi.New(
		helper.DAGStore,
		helper.DAGRunStore,
		helper.QueueStore,
		helper.ProcStore,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
		localapi.WithSchedulerStateStore(stubSchedulerStateStore{state: state}),
	)

	detailsRespObj, err := api.GetDAGDetails(context.Background(), openapi.GetDAGDetailsRequestObject{
		FileName: dag.FileName(),
	})
	require.NoError(t, err)

	detailsResp, ok := detailsRespObj.(openapi.GetDAGDetails200JSONResponse)
	require.True(t, ok)
	require.NotNil(t, detailsResp.Dag)
	require.NotNil(t, detailsResp.Dag.NextRun)
	require.True(t, scheduledAt.Equal(*detailsResp.Dag.NextRun))

	specRespObj, err := api.GetDAGSpec(context.Background(), openapi.GetDAGSpecRequestObject{
		FileName: dag.FileName(),
	})
	require.NoError(t, err)

	specResp, ok := specRespObj.(*openapi.GetDAGSpec200JSONResponse)
	if !ok {
		valueResp, valueOK := specRespObj.(openapi.GetDAGSpec200JSONResponse)
		require.True(t, valueOK)
		specResp = &valueResp
	}
	require.NotNil(t, specResp.Dag)
	require.NotNil(t, specResp.Dag.NextRun)
	require.True(t, scheduledAt.Equal(*specResp.Dag.NextRun))
}
