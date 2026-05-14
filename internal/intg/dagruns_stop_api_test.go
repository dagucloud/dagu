// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestAPITerminateLocalRun_DoesNotRequireCoordinator(t *testing.T) {
	server := test.SetupServer(t)

	const dagName = "intg_local_stop_regression"
	releaseFile := t.TempDir() + "/release"
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
	})
	spec := fmt.Sprintf(`steps:
  - name: hold
    run: |
%s
`, indentTestScript(waitForFileCommand(releaseFile), 6))

	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	startResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dags/%s/start", dagName),
		api.ExecuteDAGJSONRequestBody{},
	).ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	require.NotEmpty(t, startBody.DagRunId)

	waitForAPIRunStatus(t, server, dagName, startBody.DagRunId, []core.Status{core.Running}, intgTestTimeout(10*time.Second), false)

	server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/stop", dagName, startBody.DagRunId),
		nil,
	).ExpectStatus(http.StatusOK).Send(t)

	waitForAPIRunStatus(t, server, dagName, startBody.DagRunId, []core.Status{core.Aborted, core.Failed}, intgTestTimeout(30*time.Second), true)
}

func waitForAPIRunStatus(
	t *testing.T,
	server test.Server,
	dagName, runID string,
	expected []core.Status,
	timeout time.Duration,
	allowNotFound bool,
) {
	t.Helper()

	require.Eventually(t, func() bool {
		resp := server.Client().Get(
			fmt.Sprintf("/api/v1/dag-runs/%s/%s", dagName, runID),
		).Send(t)

		switch resp.Response.StatusCode() {
		case http.StatusOK:
		case http.StatusNotFound:
			return allowNotFound
		default:
			return false
		}

		var body api.GetDAGRunDetails200JSONResponse
		resp.Unmarshal(t, &body)
		for _, status := range expected {
			if body.DagRunDetails.Status == api.Status(status) {
				return true
			}
		}
		return false
	}, timeout, 200*time.Millisecond, "run %s should reach one of %v", runID, expected)
}
