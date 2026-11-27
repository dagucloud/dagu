// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec102_projects_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFirstStartMakesWorkspacesProjects proves Spec 102 "What Moves",
// "Files That Run in Place", and "Webhooks" for an install with a
// workspace. On v2, workspace billing has the DAG invoices, which has run
// once and has a webhook. After the first start with projects on, billing is
// a project, invoices runs in place as billing's with its past run, and its
// v2 webhook URL reaches it there with its v2 token, while the default
// project no longer reaches it. The DAG file is not changed.
func TestFirstStartMakesWorkspacesProjects(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	const invoicesSpec = "labels:\n  - workspace: billing\nsteps:\n  - run: echo invoices\n"
	invoices := in.write(t, "dags/invoices.yaml", invoicesSpec)
	in.write(t, "dags/cleanup.yaml", "steps:\n  - run: echo cleanup\n")
	auth := []string{
		"DAGU_AUTH_MODE=builtin",
		"DAGU_AUTH_BUILTIN_INITIAL_ADMIN_USERNAME=admin",
		"DAGU_AUTH_BUILTIN_INITIAL_ADMIN_PASSWORD=adminpass",
	}

	var tokens map[string]string
	in.serve(t, false, auth, func(base string) {
		session := login(t, base)
		code, body := post(t, base+"/workspaces", session, `{"name":"billing"}`)
		require.Equal(t, http.StatusCreated, code, body)
		tokens = map[string]string{
			"invoices": createWebhook(t, base, session, "invoices"),
			"cleanup":  createWebhook(t, base, session, "cleanup"),
		}
	})
	in.run(false, nil, "start", invoices).ExpectExitCode(0)

	in.serve(t, true, auth, func(base string) {
		// Only the default project's queues are processed yet, so billing
		// refuses the run that the webhook would queue, and names itself:
		// the request reached billing with the token v2 gave the webhook.
		for _, path := range []string{"invoices", "billing/invoices"} {
			code, body := post(t, base+"/webhooks/"+path, tokens["invoices"], `{}`)
			require.Equal(t, http.StatusServiceUnavailable, code, "POST /webhooks/%s: %s", path, body)
			require.Contains(t, body, "project billing cannot queue runs")
		}
		// The webhook moved with the DAG, so the default project has none.
		requireTrigger(t, base, "default/invoices", tokens["invoices"], http.StatusUnauthorized)
		requireTrigger(t, base, "cleanup", tokens["cleanup"], http.StatusOK)
	})

	history := in.run(true, nil, "history", "--project", "billing", "invoices")
	history.ExpectExitCode(0)
	require.Contains(t, history.Stdout(), "Succeeded")
	require.DirExists(t, filepath.Join(in.home, "data", "projects", "billing", "runs", "invoices"))
	require.NoDirExists(t, filepath.Join(in.home, "data", "projects", "default", "runs", "invoices"))
	data, err := os.ReadFile(invoices)
	require.NoError(t, err)
	require.Equal(t, invoicesSpec, string(data))
}
