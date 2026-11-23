// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec070_profiles_test

import (
	"net/http"
	"os"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// Global and workspace default profile layers, and webhook profile
// selection, are managed only through the HTTP API (no CLI command reaches
// them). These tests exercise that API directly against a real in-process
// server, the same way conformance/spec034_wiki_page_format and
// conformance/mcptest already do for behavior a CLI process cannot reach.

// withAuth adds a bearer token to a request when one is given. The
// default-config server used for the layered-defaults test runs with no
// auth configured, so an empty token is a no-op.
func withAuth(r *test.Request, token string) *test.Request {
	if token == "" {
		return r
	}
	return r.WithBearerToken(token)
}

// setVariable sets a runtime profile (or inherited defaults layer) variable
// through the REST API and requires the call to succeed. path is the
// profile-path segment: a plain profile name, "_global", or
// "_workspaces/<name>".
func setVariable(t *testing.T, server test.Server, token, path, key, value string) {
	t.Helper()

	withAuth(server.Client().Put("/api/v1/profiles/"+path+"/variables/"+key,
		api.SetRuntimeProfileVariableRequest{Value: value}), token).
		ExpectStatus(http.StatusOK).Send(t)
}

// setSecret sets a runtime profile secret through the REST API and requires
// the call to succeed.
func setSecret(t *testing.T, server test.Server, token, profileName, key, value string) {
	t.Helper()

	withAuth(server.Client().Put("/api/v1/profiles/"+profileName+"/secrets/"+key,
		api.SetRuntimeProfileSecretRequest{Value: &value}), token).
		ExpectStatus(http.StatusOK).Send(t)
}

// runInlineSpec creates and immediately executes a DAG from a raw spec
// string, optionally selecting a runtime profile and workspace labels, and
// returns the content of its single step's stdout log file.
func runInlineSpec(t *testing.T, server test.Server, token, spec, name, profile string, labels []string) string {
	t.Helper()

	body := api.ExecuteDAGRunFromSpecJSONRequestBody{
		Spec: spec,
		Name: &name,
	}
	if profile != "" {
		override := api.RuntimeProfileOverride(profile)
		body.Profile = &override
	}
	if len(labels) > 0 {
		apiLabels := api.Labels(labels)
		body.Labels = &apiLabels
	}

	startResp := withAuth(server.Client().Post("/api/v1/dag-runs", body), token).
		ExpectStatus(http.StatusOK).Send(t)
	var started struct {
		DagRunID string `json:"dagRunId"`
	}
	startResp.Unmarshal(t, &started)

	details := waitForRun(t, server, token, name, started.DagRunID)
	require.Len(t, details.Nodes, 1)

	content, err := os.ReadFile(details.Nodes[0].Stdout)
	require.NoError(t, err)
	return string(content)
}

// waitForRun waits for asynchronous execution before reading its final output.
func waitForRun(t *testing.T, server test.Server, token, name, runID string) api.DAGRunDetails {
	t.Helper()

	var details api.GetDAGRunDetails200JSONResponse
	require.Eventually(t, func() bool {
		response := withAuth(server.Client().Get("/api/v1/dag-runs/"+name+"/"+runID), token).
			ExpectStatus(http.StatusOK).Send(t)
		response.Unmarshal(t, &details)
		switch details.DagRunDetails.StatusLabel {
		case api.StatusLabelSucceeded, api.StatusLabelPartiallySucceeded,
			api.StatusLabelFailed, api.StatusLabelAborted, api.StatusLabelRejected:
			return true
		case api.StatusLabelNotStarted, api.StatusLabelQueued,
			api.StatusLabelRunning, api.StatusLabelWaiting:
			return false
		}
		return false
	}, harness.WaitTimeout(t), 50*time.Millisecond)
	require.Equal(t, api.StatusLabelSucceeded, details.DagRunDetails.StatusLabel)
	return details.DagRunDetails
}

// setupBuiltinAuthServer starts a server with builtin auth and webhook
// management enabled (webhooks require both), creates the admin account, and
// returns the server and an admin bearer token.
func setupBuiltinAuthServer(t *testing.T) (test.Server, string) {
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

	loginResp := server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)
	var login api.LoginResponse
	loginResp.Unmarshal(t, &login)
	require.NotEmpty(t, login.Token)

	return server, login.Token
}
