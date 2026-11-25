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

// A run of an unsaved edit that names its DAG file in draftOf takes the
// default profile of that file, as a start of the file would. Without
// draftOf, an omitted profile runs without one.
func TestDraftTakesTheDefaultProfileOfItsFile(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	const name = "draft-profile-dag"
	spec := "steps:\n  - command: echo \"draft=$DRAFT_VAR\"\n"
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: api.DAGName(name),
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)
	server.Client().Post("/api/v1/profiles", api.CreateRuntimeProfileRequest{Name: "drafted"}).
		ExpectStatus(http.StatusCreated).Send(t)
	setVariable(t, server, "", "drafted", "DRAFT_VAR", "from-profile")
	profile := api.RuntimeProfileName("drafted")
	server.Client().Put("/api/v1/dags/"+name+"/settings", api.UpdateDAGSettingsJSONRequestBody{
		Profile: &profile,
	}).ExpectStatus(http.StatusOK).Send(t)

	run := func(draftOf *string) string {
		var started struct {
			DagRunID string `json:"dagRunId"`
		}
		server.Client().Post("/api/v1/dag-runs", api.ExecuteDAGRunFromSpecJSONRequestBody{
			Spec:    spec,
			Name:    new(name),
			DraftOf: draftOf,
		}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &started)
		details := waitForRun(t, server, "", name, started.DagRunID)
		require.Len(t, details.Nodes, 1)
		content, err := os.ReadFile(details.Nodes[0].Stdout)
		require.NoError(t, err)
		return string(content)
	}

	require.Contains(t, run(new(name)), "draft=from-profile")
	require.Contains(t, run(nil), "draft=\n")
}
