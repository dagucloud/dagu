// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	exec1 "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/service/frontend"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

// TestWebhookPayloadEnv tests that WEBHOOK_PAYLOAD environment variable
// is properly passed to DAG steps when triggered via webhook.
// Note: The actual webhook API integration tests require builtin auth mode
// with webhook store and are tested separately.
func TestWebhookPayloadEnv(t *testing.T) {
	t.Parallel()

	t.Run("AsEnvVar", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)

		// Create a DAG that uses WEBHOOK_PAYLOAD
		// Note: The shell echo command doesn't preserve JSON quotes,
		// so we just check that the env var content is passed correctly
		dag := th.DAG(t, `
name: webhook-payload-test

params:
  - WEBHOOK_PAYLOAD: 'test-payload-value'

steps:
  - name: process-webhook
    run: echo "$WEBHOOK_PAYLOAD"
    with:
      shell: bash
    output: PAYLOAD_OUTPUT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"PAYLOAD_OUTPUT": "test-payload-value",
		})
	})

	t.Run("WithJsonContent", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)

		// Create a DAG that processes JSON payload using shell command
		// This simulates how a real webhook would work
		dag := th.DAG(t, `
name: webhook-json-test

params:
  - WEBHOOK_PAYLOAD: '{"event":"push","repository":"dagu"}'

steps:
  - name: check-payload-contains-event
    run: |
      if echo "$WEBHOOK_PAYLOAD" | grep -q "event"; then
        echo "found"
      else
        echo "not-found"
      fi
    with:
      shell: bash
    output: CHECK_RESULT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"CHECK_RESULT": "found",
		})
	})
}

func TestWebhookHMACOnlyTriggerExecutesSignedRequest(t *testing.T) {
	server := setupWebhookBuiltinAuthServer(t)
	adminToken := getWebhookBuiltinAdminToken(t, server)

	const (
		dagName = "intg_webhook_hmac_only_signed"
		runID   = "signed_hmac_run"
	)

	payloadFile := t.TempDir() + "/webhook-payload.json"
	spec := fmt.Sprintf(`name: %s
params:
  - name: idea
    type: string
    default: existing-default
steps:
  - name: capture-payload
%s    command: |
%s
`, dagName, webhookCaptureShellYAML(), indentTestScript(writeWebhookPayloadCommand(payloadFile), 6))

	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusCreated).Send(t)

	enforcementMode := api.WebhookHMACEnforcementModeStrict
	enableResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook/hmac/enable", api.WebhookHMACConfigureRequest{
		AuthMode:        api.WebhookHMACConfigureRequestAuthModeHmacOnly,
		EnforcementMode: &enforcementMode,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	var enableBody api.WebhookHMACSecretResponse
	enableResp.Unmarshal(t, &enableBody)
	require.NotEmpty(t, enableBody.HmacSecret)

	payload := map[string]any{
		"event":      "push",
		"repository": "dagu",
	}
	dagRunID := api.DAGRunId(runID)
	body := api.WebhookRequest{
		DagRunId: &dagRunID,
		Payload:  &payload,
	}
	signature := signWebhookRequestBody(t, enableBody.HmacSecret, body)

	triggerResp := server.Client().Post("/api/v1/webhooks/"+dagName, body).
		WithHeader("X-Dagu-Signature", signature).
		ExpectStatus(http.StatusOK).Send(t)

	var triggerBody api.WebhookResponse
	triggerResp.Unmarshal(t, &triggerBody)
	require.Equal(t, dagName, triggerBody.DagName)
	require.Equal(t, api.DAGRunId(runID), triggerBody.DagRunId)

	// Webhook triggers enqueue DAG runs; execute the queued item in this
	// server-only integration harness before asserting the stored status.
	test.ProcessQueuedInlineRun(t, server, dagName)
	waitForWebhookRunStatus(t, server, dagName, runID, core.Succeeded)

	expectedPayload, err := json.Marshal(payload)
	require.NoError(t, err)

	actualPayload, err := os.ReadFile(payloadFile)
	require.NoError(t, err)
	require.Equal(t, string(expectedPayload), string(actualPayload))
}

func setupWebhookBuiltinAuthServer(t *testing.T) test.Server {
	t.Helper()

	server := test.SetupServer(t,
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Server.Auth.Mode = config.AuthModeBuiltin
			cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-key"
			cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
		}),
		test.WithServerOptions(
			frontend.WithLicenseManager(
				license.NewTestManager(license.FeatureRBAC, license.FeatureAudit),
			),
		),
	)

	server.Client().Post("/api/v1/auth/setup", api.SetupRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)

	return server
}

func getWebhookBuiltinAdminToken(t *testing.T, server test.Server) string {
	t.Helper()

	resp := server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)

	var body api.LoginResponse
	resp.Unmarshal(t, &body)
	require.NotEmpty(t, body.Token)
	return body.Token
}

func webhookCaptureShellYAML() string {
	return test.ForOS("    shell: /bin/sh\n", "    shell: powershell\n")
}

func writeWebhookPayloadCommand(path string) string {
	return test.ForOS(
		test.JoinLines(
			`test -n "${WEBHOOK_PAYLOAD}"`,
			fmt.Sprintf(`printf '%%s' "$WEBHOOK_PAYLOAD" > %s`, test.PosixQuote(path)),
		),
		test.JoinLines(
			"if ($null -eq $env:WEBHOOK_PAYLOAD) { throw 'WEBHOOK_PAYLOAD not set' }",
			fmt.Sprintf("[System.IO.File]::WriteAllText(%s, $env:WEBHOOK_PAYLOAD)", test.PowerShellQuote(path)),
		),
	)
}

func signWebhookRequestBody(t *testing.T, secret string, body any) string {
	t.Helper()

	raw, err := json.Marshal(body)
	require.NoError(t, err)

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(raw)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func waitForWebhookRunStatus(
	t *testing.T,
	server test.Server,
	dagName, runID string,
	expected core.Status,
) {
	t.Helper()

	require.Eventually(t, func() bool {
		attempt, err := server.DAGRunStore.FindAttempt(
			server.Context,
			exec1.NewDAGRunRef(dagName, runID),
		)
		if err != nil {
			return false
		}

		status, err := attempt.ReadStatus(server.Context)
		if err != nil {
			return false
		}

		return status.Status == expected
	}, intgTestTimeout(30*time.Second), 200*time.Millisecond)
}
