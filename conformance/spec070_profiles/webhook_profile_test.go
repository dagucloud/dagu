// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec070_profiles_test

import (
	"net/http"
	"os"
	"testing"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// A webhook caller selects a runtime profile with the X-Dagu-Profile header,
// restricted to the profiles ConfigureDAGWebhookProfileSelection allowed for
// that webhook.
func TestWebhookProfile(t *testing.T) {
	t.Parallel()

	server, adminToken := setupBuiltinAuthServer(t)

	const dagName = "webhook-profile-dag"
	spec := "steps:\n  - command: echo \"$WEBHOOK_VAR\"\n"
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	server.Client().Post("/api/v1/profiles", api.CreateRuntimeProfileRequest{Name: "webhookprof"}).
		WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)
	setVariable(t, server, adminToken, "webhookprof", "WEBHOOK_VAR", "from-webhook-profile")

	createHookResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)
	var hookCreate api.WebhookCreateResponse
	createHookResp.Unmarshal(t, &hookCreate)

	server.Client().Put("/api/v1/dags/"+dagName+"/webhook/profile-selection",
		api.WebhookProfileSelectionRequest{AllowedProfiles: []api.RuntimeProfileName{"webhookprof"}}).
		WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	triggerResp := server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{}).
		WithBearerToken(hookCreate.Token).
		WithHeader("X-Dagu-Profile", "webhookprof").
		ExpectStatus(http.StatusOK).Send(t)
	var trigger api.WebhookResponse
	triggerResp.Unmarshal(t, &trigger)

	test.ProcessQueuedInlineRun(t, server, dagName)

	details := waitForRun(t, server, adminToken, dagName, string(trigger.DagRunId))
	require.NotNil(t, details.ProfileName)
	require.Equal(t, api.RuntimeProfileName("webhookprof"), *details.ProfileName)
	require.Len(t, details.Nodes, 1)
	output, err := os.ReadFile(details.Nodes[0].Stdout)
	require.NoError(t, err)
	require.Contains(t, string(output), "from-webhook-profile")
}

// A webhook caller naming a profile outside the configured allow-list is
// rejected before any DAG-run is created.
func TestDisallowedWebhookProfile(t *testing.T) {
	t.Parallel()

	server, adminToken := setupBuiltinAuthServer(t)

	const dagName = "webhook-profile-reject-dag"
	spec := "steps:\n  - command: echo hi\n"
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	server.Client().Post("/api/v1/profiles", api.CreateRuntimeProfileRequest{Name: "allowedprof"}).
		WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	server.Client().Post("/api/v1/profiles", api.CreateRuntimeProfileRequest{Name: "disallowedprof"}).
		WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	createHookResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)
	var hookCreate api.WebhookCreateResponse
	createHookResp.Unmarshal(t, &hookCreate)

	server.Client().Put("/api/v1/dags/"+dagName+"/webhook/profile-selection",
		api.WebhookProfileSelectionRequest{AllowedProfiles: []api.RuntimeProfileName{"allowedprof"}}).
		WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	rejectResp := server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{}).
		WithBearerToken(hookCreate.Token).
		WithHeader("X-Dagu-Profile", "disallowedprof").
		ExpectStatus(http.StatusForbidden).Send(t)
	require.Contains(t, rejectResp.Body, "runtime profile selection is not allowed for this webhook")

	response := server.Client().Get("/api/v1/dag-runs/" + dagName).
		WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
	var runs api.DAGRunsPageResponse
	response.Unmarshal(t, &runs)
	require.Empty(t, runs.DagRuns)
}

// A webhook profile token always runs its bound profile. A header naming a
// different profile, or a revoked token, is rejected without creating a run.
func TestWebhookProfileToken(t *testing.T) {
	t.Parallel()

	server, adminToken := setupBuiltinAuthServer(t)

	const dagName = "webhook-profile-token-dag"
	spec := "steps:\n  - command: echo \"$WEBHOOK_VAR\"\n"
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	for _, name := range []string{"tokenprof", "otherprof"} {
		server.Client().Post("/api/v1/profiles", api.CreateRuntimeProfileRequest{Name: api.RuntimeProfileName(name)}).
			WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)
	}
	setVariable(t, server, adminToken, "tokenprof", "WEBHOOK_VAR", "from-profile-token")

	server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	createTokenResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook/profile-tokens",
		api.WebhookProfileTokenCreateRequest{Name: "caller-a", Profile: "tokenprof"}).
		WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)
	var tokenCreate api.WebhookCreateResponse
	createTokenResp.Unmarshal(t, &tokenCreate)
	require.NotNil(t, tokenCreate.Webhook.ProfileTokens)
	require.Len(t, *tokenCreate.Webhook.ProfileTokens, 1)
	profileTokenID := (*tokenCreate.Webhook.ProfileTokens)[0].Id

	triggerResp := server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{}).
		WithBearerToken(tokenCreate.Token).
		ExpectStatus(http.StatusOK).Send(t)
	var trigger api.WebhookResponse
	triggerResp.Unmarshal(t, &trigger)

	test.ProcessQueuedInlineRun(t, server, dagName)

	details := waitForRun(t, server, adminToken, dagName, string(trigger.DagRunId))
	require.NotNil(t, details.ProfileName)
	require.Equal(t, api.RuntimeProfileName("tokenprof"), *details.ProfileName)
	require.Len(t, details.Nodes, 1)
	output, err := os.ReadFile(details.Nodes[0].Stdout)
	require.NoError(t, err)
	require.Contains(t, string(output), "from-profile-token")

	server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{}).
		WithBearerToken(tokenCreate.Token).
		WithHeader("X-Dagu-Profile", "otherprof").
		ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Delete("/api/v1/dags/" + dagName + "/webhook/profile-tokens/" + profileTokenID).
		WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
	server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{}).
		WithBearerToken(tokenCreate.Token).
		ExpectStatus(http.StatusUnauthorized).Send(t)

	response := server.Client().Get("/api/v1/dag-runs/" + dagName).
		WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
	var runs api.DAGRunsPageResponse
	response.Unmarshal(t, &runs)
	require.Len(t, runs.DagRuns, 1)
}
