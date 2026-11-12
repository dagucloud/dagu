// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec070_profiles_test

import (
	"net/http"
	"testing"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// The three profile layers apply in order: global defaults, then workspace
// defaults (overriding global), then the explicitly selected profile
// (overriding workspace). A run only picks up workspace defaults when its
// DAG carries a `workspace` label naming that workspace.
func TestLayeredDefaultsPrecedence(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	const noAuth = ""

	setVariable(t, server, noAuth, "_global", "PRECEDENCE_VAR", "global-value")
	setVariable(t, server, noAuth, "_global", "GLOBAL_ONLY_VAR", "global-only-value")

	withAuth(server.Client().Post("/api/v1/workspaces", api.CreateWorkspaceRequest{Name: "acme"}), noAuth).
		ExpectStatus(http.StatusCreated).Send(t)
	setVariable(t, server, noAuth, "_workspaces/acme", "PRECEDENCE_VAR", "workspace-value")
	setVariable(t, server, noAuth, "_workspaces/acme", "WORKSPACE_ONLY_VAR", "workspace-only-value")

	withAuth(server.Client().Post("/api/v1/profiles", api.CreateRuntimeProfileRequest{Name: "selected"}), noAuth).
		ExpectStatus(http.StatusCreated).Send(t)
	setVariable(t, server, noAuth, "selected", "WORKSPACE_ONLY_VAR", "selected-value")
	setVariable(t, server, noAuth, "selected", "SELECTED_ONLY_VAR", "selected-only-value")

	spec := "steps:\n  - command: echo \"$PRECEDENCE_VAR $GLOBAL_ONLY_VAR $WORKSPACE_ONLY_VAR $SELECTED_ONLY_VAR\"\n"
	output := runInlineSpec(t, server, noAuth, spec, "layered-defaults-dag", "selected", []string{"workspace=acme"})

	require.Contains(t, output, "workspace-value global-only-value selected-value selected-only-value")
}

// A run naming a workspace with no configured defaults, or naming none at
// all, still applies global defaults and the selected profile.
func TestAbsentWorkspaceDefaults(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	const noAuth = ""

	setVariable(t, server, noAuth, "_global", "GLOBAL_VAR", "global-value")

	server.Client().Post("/api/v1/profiles", api.CreateRuntimeProfileRequest{Name: "selected"}).
		ExpectStatus(http.StatusCreated).Send(t)
	setVariable(t, server, noAuth, "selected", "SELECTED_VAR", "selected-value")

	spec := "steps:\n  - command: echo \"$GLOBAL_VAR $SELECTED_VAR\"\n"
	for _, tc := range []struct {
		name   string
		labels []string
	}{
		{"no-workspace", nil},
		{"unconfigured-workspace", []string{"workspace=unconfigured"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := runInlineSpec(t, server, noAuth, spec, tc.name, "selected", tc.labels)
			require.Contains(t, output, "global-value selected-value")
		})
	}
}

// A profile secret entry resolves and is masked in run output the same way a
// DAG-level secret is. Setting a profile secret requires the REST API (the
// CLI's --value-stdin/interactive prompt has no black-box equivalent).
func TestProfileSecretIsMasked(t *testing.T) {
	t.Parallel()

	server, token := setupBuiltinAuthServer(t)

	withAuth(server.Client().Post("/api/v1/profiles", api.CreateRuntimeProfileRequest{Name: "secprof"}), token).
		ExpectStatus(http.StatusCreated).Send(t)
	setSecret(t, server, token, "secprof", "MY_PROF_SECRET", "profile-secret-value")

	spec := "steps:\n  - command: echo \"value is $MY_PROF_SECRET\"\n"
	output := runInlineSpec(t, server, token, spec, "profile-secret-dag", "secprof", nil)

	require.Contains(t, output, "*******")
	require.NotContains(t, output, "profile-secret-value")
}

// A DAG secrets: entry with the env provider reads profile entries: a global
// default secret and a selected profile variable. The run fails before any
// step starts when an env secret's key is unset, so success shows both
// resolved; their values are masked as DAG secrets.
func TestEnvSecretReadsProfileEntries(t *testing.T) {
	t.Parallel()

	server, token := setupBuiltinAuthServer(t)

	setSecret(t, server, token, "_global", "GLOBAL_PROF_SECRET", "global-secret-value")
	withAuth(server.Client().Post("/api/v1/profiles", api.CreateRuntimeProfileRequest{Name: "selected"}), token).
		ExpectStatus(http.StatusCreated).Send(t)
	setVariable(t, server, token, "selected", "SELECTED_VAR", "selected-value")

	spec := `secrets:
  - name: FROM_GLOBAL
    provider: env
    key: GLOBAL_PROF_SECRET
  - name: FROM_SELECTED
    provider: env
    key: SELECTED_VAR
steps:
  - command: echo "values are $FROM_GLOBAL $FROM_SELECTED"
`
	output := runInlineSpec(t, server, token, spec, "env-secret-profile-dag", "selected", nil)

	require.Contains(t, output, "values are ******* *******")
	require.NotContains(t, output, "global-secret-value")
	require.NotContains(t, output, "selected-value")
}
