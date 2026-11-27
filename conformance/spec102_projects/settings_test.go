// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec102_projects_test

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// settingsDAG runs in the default project under the label of workspace ops.
// Each step runs only when its value is the one expected, and prints the
// secret so the test can see it masked.
const settingsDAG = `name: settings
labels: [workspace=ops]
secrets:
  - name: TOKEN
    ref: api/token
steps:
  - name: token
    preconditions:
      - condition: "${TOKEN}"
        expected: "%[1]s-token"
    run: echo "token ${TOKEN}"
  - name: tier
    preconditions:
      - condition: "${TIER}"
        expected: "%[1]s"
    run: echo "tier ${TIER}"
`

// TestRunUsesProjectSettings proves Spec 102 "Settings". The default
// project and workspace ops hold a secret and a profile default of the same
// names with different values. With projects on, a run of a DAG whose label
// names ops reads the project's values, and its output masks the secret.
// With projects off, the same run reads the workspace's, as on v2.
func TestRunUsesProjectSettings(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	for _, tc := range []struct {
		projectsOn bool
		scope      string
	}{
		{projectsOn: true, scope: "project"},
		{projectsOn: false, scope: "workspace"},
	} {
		// Each install keeps its settings where its first start leaves
		// them, so each case seeds its own.
		home := filepath.Join(t.TempDir(), "dagu")
		env := []string{"DAGU_HOME=" + home}
		if tc.projectsOn {
			env = append(env, "DAGU_EXPERIMENTAL_PROJECTS=true")
		}
		seedSettings(t, dagu, env)
		dagFile := filepath.Join(home, "dags", "settings.yaml")
		require.NoError(t, os.WriteFile(dagFile, fmt.Appendf(nil, settingsDAG, tc.scope), 0o600))
		result := dagu.RunWithEnv(env, "start", dagFile)
		result.ExpectExitCode(0)
		require.Contains(t, result.Stdout(), "token *******")
		require.Contains(t, result.Stdout(), "tier "+tc.scope)
		for _, value := range []string{"project-token", "workspace-token"} {
			require.NotContains(t, result.Stdout()+result.Stderr(), value)
		}
	}
}

// seedSettings gives the default project and workspace ops the secret
// api/token and the profile default TIER through the API of a server that
// runs with env.
func seedSettings(t *testing.T, dagu *harness.Runner, env []string) {
	t.Helper()
	port := harness.FreePort(t)
	server := dagu.StartWithEnv(env, "server", "--host=127.0.0.1", fmt.Sprintf("--port=%d", port))
	defer server.Stop()
	base := fmt.Sprintf("http://127.0.0.1:%d/api/v1", port)
	waitForHealth(t, server, base, func(body string) bool {
		return strings.Contains(body, `"status":"healthy"`)
	})

	send(t, http.MethodPost, base+"/workspaces", `{"name":"ops"}`)
	for _, scope := range []string{"project", "workspace"} {
		workspace, defaults := "", "_global"
		if scope == "workspace" {
			workspace, defaults = "ops", "_workspaces/ops"
		}
		send(t, http.MethodPost, base+"/secrets", fmt.Sprintf(
			`{"workspace":%q,"ref":"api/token","providerType":"dagu-managed","value":"%s-token"}`, workspace, scope))
		send(t, http.MethodPut, base+"/profiles/"+defaults+"/variables/TIER", fmt.Sprintf(`{"value":%q}`, scope))
	}
}

// send makes a request with a JSON body and requires a 2xx answer that
// names no secret value.
func send(t *testing.T, method, url, body string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var answer bytes.Buffer
	_, err = answer.ReadFrom(resp.Body)
	require.NoError(t, err)
	require.Less(t, resp.StatusCode, 300, "%s %s: %s", method, url, answer.String())
	require.NotContains(t, answer.String(), "-token")
}

// TestRunUsesProjectBaseConfig proves Spec 102 "Base Config". With projects
// on, a run of weather reads base.yaml in its DAG directory, a run of
// day-ahead, which has none, reads no base config, not even through a
// library child, and a default-project DAG whose label names workspace ops
// reads paths.base_config alone. With projects off, the label's workspace
// base config layers over paths.base_config, as on v2.
func TestRunUsesProjectBaseConfig(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	in.run(true, nil, "project", "create", "day-ahead").ExpectExitCode(0)
	out := t.TempDir()
	// Each DAG records the base config values its run read.
	record := func(name string) string {
		return fmt.Sprintf("steps:\n  - id: record\n    run: printf '%%s' \"${BASE_MARK:-}${DEFAULT_ONLY:-}\" > '%s'\n",
			filepath.ToSlash(filepath.Join(out, name)))
	}
	in.write(t, "base.yaml", "env:\n  - BASE_MARK: default\n  - DEFAULT_ONLY: \"+server\"\n")
	in.write(t, "dags/workspaces/ops/base.yaml", "env:\n  - BASE_MARK: ops\n")
	in.write(t, "projects/weather/base.yaml", "env:\n  - BASE_MARK: weather\n")
	in.write(t, "projects/weather/forecast.yaml", record("forecast"))
	in.write(t, "projects/day-ahead/auction.yaml", callerOf("report"))
	in.write(t, "library/report.yaml", record("report"))
	labelled := in.write(t, "dags/labelled.yaml", "labels: [workspace=ops]\n"+record("labelled"))
	recorded := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(out, name))
		require.NoError(t, err)
		return string(data)
	}

	in.run(true, nil, "start", "--project", "weather", "forecast").ExpectExitCode(0)
	require.Equal(t, "weather", recorded("forecast"))
	in.run(true, nil, "start", "--project", "day-ahead", "auction").ExpectExitCode(0)
	require.Empty(t, recorded("report"))
	in.run(true, nil, "start", labelled).ExpectExitCode(0)
	require.Equal(t, "default+server", recorded("labelled"))
	in.run(false, nil, "start", labelled).ExpectExitCode(0)
	require.Equal(t, "ops+server", recorded("labelled"))
}
