// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/clicontext"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/controller"
	persisfile "github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControllerCommandTree(t *testing.T) {
	t.Parallel()

	command := Controller()
	children := map[string]*cobra.Command{}
	for _, child := range command.Commands() {
		children[child.Name()] = child
	}

	require.ElementsMatch(t, []string{"list", "show", "start", "prompt", "stop"}, mapKeys(children))
	assert.NotNil(t, children["list"].Flags().Lookup("format"))
	assert.NotNil(t, children["show"].Flags().Lookup("format"))
	assert.Nil(t, children["start"].Flags().Lookup("format"))
	assert.NotNil(t, children["start"].Flags().Lookup("prompt"))
	assert.NotNil(t, children["prompt"].Flags().Lookup("prompt"))
	assert.Nil(t, children["stop"].Flags().Lookup("prompt"))
}

func mapKeys(values map[string]*cobra.Command) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestControllerSubcommandsUseControllerContextScope(t *testing.T) {
	t.Parallel()

	command := Controller()
	for _, child := range command.Commands() {
		assert.Equal(t, "controller", commandFamilyName(child))
		assert.Equal(t, commandScopeContextAware, scopeForCommand(commandFamilyName(child)))
	}
}

func TestControllerMutationInputPreservesPrompt(t *testing.T) {
	t.Parallel()

	command := controllerStartCommand()
	prompt := "  'quoted'\\value\nsecond line  "
	require.NoError(t, command.Flags().Set("prompt", prompt))
	ctx := &Context{Command: command}

	id, gotPrompt, err := controllerMutationInput(ctx, []string{"ctrl_aaaaaaaaaaaaaaaa"})

	require.NoError(t, err)
	assert.Equal(t, "ctrl_aaaaaaaaaaaaaaaa", id)
	assert.Equal(t, prompt, gotPrompt)
}

func TestRenderControllerListJSONUsesStatusLabel(t *testing.T) {
	t.Parallel()

	output := new(bytes.Buffer)
	command := controllerListCommand()
	command.SetOut(output)
	ctx := &Context{Command: command}
	updatedAt := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)

	err := renderControllerList(ctx, "json", []api.ControllerSummary{{
		Id:                "ctrl_aaaaaaaaaaaaaaaa",
		Name:              "Incident flow",
		Workspace:         "ops",
		Status:            api.Status(7),
		StatusLabel:       api.StatusLabel("waiting"),
		CurrentState:      "needs_input",
		ResourceUpdatedAt: updatedAt,
	}})

	require.NoError(t, err)
	var result []controllerListJSONItem
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, "waiting", result[0].Status)
	assert.Equal(t, "needs_input", result[0].CurrentState)
}

func TestRenderControllerListTableEscapesTerminalControls(t *testing.T) {
	t.Parallel()

	output := new(bytes.Buffer)
	command := controllerListCommand()
	command.SetOut(output)
	ctx := &Context{Command: command}

	err := renderControllerList(ctx, "table", []api.ControllerSummary{{
		Id:                "ctrl_aaaaaaaaaaaa\taaa",
		Name:              "Incident\nflow",
		Workspace:         "ops\x1b[2J",
		StatusLabel:       api.StatusLabel("running\u0085"),
		CurrentState:      "needs\u2028input",
		ResourceUpdatedAt: time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC),
	}})

	require.NoError(t, err)
	assert.Contains(t, output.String(), `Incident\nflow`)
	assert.Contains(t, output.String(), `ctrl_aaaaaaaaaaaa\taaa`)
	assert.Contains(t, output.String(), `ops\x1b[2J`)
	assert.Contains(t, output.String(), `running\u0085`)
	assert.Contains(t, output.String(), `needs\u2028input`)
	assert.NotContains(t, output.String(), "\x1b")
	assert.NotContains(t, output.String(), "\u0085")
	assert.NotContains(t, output.String(), "\u2028")
}

func TestRenderControllerDetailWritesWarningsToStderr(t *testing.T) {
	t.Parallel()

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	command := controllerShowCommand()
	command.SetOut(stdout)
	command.SetErr(stderr)
	ctx := &Context{Command: command}

	err := renderControllerDetail(ctx, "json", &api.ControllerDetail{
		Id: "ctrl_aaaaaaaaaaaaaaaa",
		Definition: api.ControllerDefinition{
			Name:     "Incident flow",
			MaxTurns: 100,
			Labels:   []string{},
		},
		Runtime: api.ControllerRuntime{
			StatusLabel: api.StatusLabel("not_started"),
			DagRunRefs:  []api.ControllerDAGRunRef{},
			Context:     []api.ControllerContextMessage{},
		},
		Warnings: []api.ControllerWarning{{
			Code:    "unreachable_state",
			Path:    "states.orphaned",
			Message: "State is unreachable\nfrom default",
		}},
		ResourceUpdatedAt: time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC),
	})

	require.NoError(t, err)
	assert.Contains(t, stdout.String(), `"id": "ctrl_aaaaaaaaaaaaaaaa"`)
	assert.Equal(t, "Warning [unreachable_state] states.orphaned: State is unreachable\\nfrom default\n", stderr.String())
}

func TestRemoteControllerStartUsesDirectControllerEndpoint(t *testing.T) {
	t.Parallel()

	type requestBody struct {
		Prompt string `json:"prompt"`
	}
	var received requestBody
	client := &remoteClient{
		baseURL: "http://dagu.test",
		apiKey:  "test-key",
		client: &http.Client{Transport: controllerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			assert.Equal(t, "/controllers/ctrl_aaaaaaaaaaaaaaaa/start", request.URL.Path)
			assert.Empty(t, request.URL.Query().Get("remoteNode"))
			require.NoError(t, json.NewDecoder(request.Body).Decode(&received))
			return &http.Response{
				StatusCode: http.StatusAccepted,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"id":"ctrl_aaaaaaaaaaaaaaaa"}`)),
				Request:    request,
			}, nil
		})},
	}
	prompt := "  literal\\prompt\n  "

	err := client.startController(context.Background(), "ctrl_aaaaaaaaaaaaaaaa", prompt)

	require.NoError(t, err)
	assert.Equal(t, prompt, received.Prompt)
}

type controllerRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn controllerRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestLocalControllerClientUsesSharedApplicationService(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	stores := controller.NewFileStores(dataDir)
	service := controller.NewService(
		stores.Definitions,
		stores.Runtimes,
		stores.Locker,
		controller.NewValidator(nil),
		controller.WithIDGenerator(func() (string, error) { return "ctrl_aaaaaaaaaaaaaaaa", nil }),
	)
	created, err := service.Create(context.Background(), []byte(`type: controller
version: 1
name: Incident flow
description: Route incident work safely.
labels:
  - workspace=ops
llm:
  provider: openai
  model: gpt-4o
dags: []
states:
  default:
    description: Initial routing state.
    transitions:
      - to: completed
        when: Work is complete.
  completed:
    description: Work completed successfully.
    terminal: succeeded
`))
	require.NoError(t, err)

	ctx := &Context{
		Context: context.Background(),
		Config: &config.Config{
			Server: config.Server{Audit: config.AuditConfig{Enabled: true}},
			Paths: config.PathsConfig{
				DataDir:      dataDir,
				AdminLogsDir: filepath.Join(dataDir, "admin-logs"),
			},
		},
	}
	client := newLocalControllerCommandClient(ctx)

	items, err := client.listControllers(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, created.Definition.ID, items[0].Id)

	prompt := "  investigate literally  "
	require.NoError(t, client.startController(context.Background(), created.Definition.ID, prompt))
	detail, err := client.getController(context.Background(), created.Definition.ID)
	require.NoError(t, err)
	assert.Equal(t, api.StatusLabel("running"), detail.Runtime.StatusLabel)
	require.Len(t, detail.Runtime.Context, 1)
	require.NotNil(t, detail.Runtime.Context[0].Content)
	assert.Equal(t, prompt, *detail.Runtime.Context[0].Content)

	auditStore, err := persisfile.NewAuditStore(ctx.Config)
	require.NoError(t, err)
	require.NotNil(t, auditStore)
	t.Cleanup(func() { require.NoError(t, auditStore.Close()) })
	auditResult, err := auditStore.Query(context.Background(), audit.QueryFilter{
		Category:   audit.Category("controller"),
		Action:     "start",
		ResourceID: created.Definition.ID,
	})
	require.NoError(t, err)
	require.Len(t, auditResult.Entries, 1)
	assert.Equal(t, "cli", auditResult.Entries[0].Source)
	assert.Equal(t, "success", auditResult.Entries[0].Result)
	assert.Equal(t, "ops", auditResult.Entries[0].Workspace)
}

func TestControllerTableTextEscapesTerminalControls(t *testing.T) {
	t.Parallel()

	result := terminalSafeControllerText("Need\nregion\x1b[2J\t\u0085\u061c\u200e\u200f\u202a\u202e\u2066\u2069")

	assert.Equal(t, `Need\nregion\x1b[2J\t\u0085\u061c\u200e\u200f\u202a\u202e\u2066\u2069`, result)
	assert.NotContains(t, result, "\x1b")
	assert.NotContains(t, result, "\n")
	assert.NotContains(t, result, "\u061c")
	assert.NotContains(t, result, "\u200e")
	assert.NotContains(t, result, "\u200f")
	assert.NotContains(t, result, "\u202a")
	assert.NotContains(t, result, "\u202e")
	assert.NotContains(t, result, "\u2066")
	assert.NotContains(t, result, "\u2069")
}

func TestControllerMutationErrorMarksAmbiguousOutcomes(t *testing.T) {
	t.Parallel()

	ctx := &Context{
		ContextName: "remote",
		CLIContext:  &clicontext.Context{Name: "remote"},
		Remote:      &remoteClient{},
	}

	assert.Contains(t, controllerMutationError(ctx, errors.New("connection reset")).Error(), "outcome unknown; do not retry automatically")
	assert.NotContains(t, controllerMutationError(ctx, &remoteError{StatusCode: http.StatusConflict, Message: "conflict"}).Error(), "outcome unknown")
	assert.Contains(t, controllerMutationError(ctx, &remoteError{StatusCode: http.StatusInternalServerError, Message: "internal error"}).Error(), "outcome unknown; do not retry automatically")
}
