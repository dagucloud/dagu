// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec102_projects_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// TestAPIChoosesProject proves Spec 102 "Choosing a Project", API: a DAG
// path addresses the project its project parameter names, without regard
// to case, else the default project; a run started there runs in that
// project and is read only through it; a missing project gets 404; and a
// request for a DAG the project lacks gets 404 naming the projects that
// have one.
func TestAPIChoosesProject(t *testing.T) {
	t.Parallel()
	in := newProjectsInstall(t)
	port := harness.FreePort(t)
	server := in.dagu.StartWithEnv(in.env, "server", "--host=127.0.0.1", fmt.Sprintf("--port=%d", port))
	defer server.Stop()
	base := fmt.Sprintf("http://127.0.0.1:%d/api/v1", port)
	waitForHealth(t, server, base, func(body string) bool {
		return strings.Contains(body, `"status":"healthy"`)
	})
	// The server opens each project's stores once it has read the registry.
	require.Eventually(t, func() bool {
		code, _ := get(t, base+"/dags?project=day-ahead")
		return code == http.StatusOK
	}, harness.WaitTimeout(t), 50*time.Millisecond)

	require.Equal(t, []string{"forecast"}, listedDAGs(t, base+"/dags?project=Intraday"))
	require.Contains(t, listedDAGs(t, base+"/dags"), "cleanup")
	require.NotContains(t, listedDAGs(t, base+"/dags"), "forecast")

	code, body := get(t, base+"/dags?project=nowhere")
	require.Equal(t, http.StatusNotFound, code, body)
	code, body = get(t, base+"/dags/forecast")
	require.Equal(t, http.StatusNotFound, code, body)
	require.Contains(t, body, "projects with a DAG of that name: intraday")

	code, body = post(t, base+"/dags/forecast/start?project=intraday", "", "{}")
	require.Equal(t, http.StatusOK, code, body)
	var started struct {
		DagRunID string `json:"dagRunId"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &started))
	run := base + "/dag-runs/forecast/" + started.DagRunID
	require.Eventually(t, func() bool {
		code, body := get(t, run+"?project=intraday")
		return code == http.StatusOK && strings.Contains(body, `"statusLabel":"succeeded"`)
	}, harness.WaitTimeout(t), 100*time.Millisecond)
	require.Equal(t, "intraday", in.projectOf(t, "forecast"))
	code, body = get(t, run)
	require.Equal(t, http.StatusNotFound, code, body)
}

// listedDAGs returns the file names of the DAGs that a DAG list answers.
func listedDAGs(t *testing.T, url string) []string {
	t.Helper()
	code, body := get(t, url)
	require.Equal(t, http.StatusOK, code, body)
	var list struct {
		DAGs []struct {
			FileName string `json:"fileName"`
		} `json:"dags"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &list))
	names := make([]string, 0, len(list.DAGs))
	for _, dag := range list.DAGs {
		names = append(names, dag.FileName)
	}
	return names
}
