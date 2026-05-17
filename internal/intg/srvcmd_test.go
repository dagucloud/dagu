// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestServer_StartWithConfig(t *testing.T) {
	testCases := []struct {
		name       string
		setupFunc  func(t *testing.T) (string, string) // returns configFile and dagPath
		envVarName string
	}{
		{
			name: "GlobalLogDir",
			setupFunc: func(t *testing.T) (string, string) {
				tempDir := t.TempDir()
				configFile := filepath.Join(tempDir, "config.yaml")
				configContent := `log_dir: ${TMP_LOGS_DIR}/logs`
				require.NoError(t, os.WriteFile(configFile, []byte(configContent), 0600))

				// Create DAG with inline YAML
				th := test.Setup(t)
				dagContent := `steps:
  - name: step1
    run: echo "Hello, world!"
`
				dag := th.DAG(t, dagContent)

				return configFile, dag.Location
			},
			envVarName: "TMP_LOGS_DIR",
		},
		{
			name: "DAGLocalLogDir",
			setupFunc: func(t *testing.T) (string, string) {
				// Create DAG with inline YAML
				th := test.Setup(t)
				dagContent := `
log_dir: ${DAG_TMP_LOGS_DIR}/logs
steps:
  - name: step1
    run: echo "Hello, world!"
`
				dag := th.DAG(t, dagContent)
				return "", dag.Location
			},
			envVarName: "DAG_TMP_LOGS_DIR",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup test case
			configFile, dagPath := tc.setupFunc(t)
			tempDir := t.TempDir()
			_ = os.Setenv(tc.envVarName, tempDir)

			// Run command
			th := test.SetupCommand(t)
			args := []string{"start"}
			if tc.name == "GlobalLogDir" && configFile != "" {
				args = append(args, "--config", configFile)
			}
			args = append(args, dagPath)

			th.RunCommand(t, cmd.Start(), test.CmdTest{
				Args:        args,
				ExpectedOut: []string{"DAG run finished"},
			})
		})
	}
}

// TestServer_BasePath verifies that when BasePath is set in the configuration,
// the API endpoints are served under that base path and not on the root.
func TestServer_BasePath(t *testing.T) {
	port := findPort(t)
	configFile := writeServerConfig(t, port, "/dagu", false)
	stopServer := startServer(t, configFile, port, "/dagu")

	requireHealthy(t, fmt.Sprintf("http://127.0.0.1:%s/dagu/api/v1/health", port))

	stopServer()
}

// TestServer_RemoteNode verifies that remote node health checks work with and without a base path.
func TestServer_RemoteNode(t *testing.T) {
	testCases := []struct {
		name     string
		basePath string
	}{
		{name: "root", basePath: ""},
		{name: "with base path", basePath: "/dagu"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			port := findPort(t)
			configFile := writeServerConfig(t, port, tc.basePath, true)
			stopServer := startServer(t, configFile, port, tc.basePath)

			url := fmt.Sprintf("http://127.0.0.1:%s%s/api/v1/health?remoteNode=dev", port, tc.basePath)
			requireHealthy(t, url)

			stopServer()
		})
	}
}

func writeServerConfig(t *testing.T, port, basePath string, includeRemoteNodes bool) string {
	t.Helper()
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")

	configContent := fmt.Sprintf(`host: "127.0.0.1"
port: %s
base_path: "%s"
auth:
  mode: none
`, port, basePath)

	if includeRemoteNodes {
		configContent += fmt.Sprintf(`remote_nodes:
  - name: "dev"
    api_base_url: "http://127.0.0.1:%s%s/api/v1"
`, port, basePath)
	}

	require.NoError(t, os.WriteFile(configFile, []byte(configContent), 0o600))
	return configFile
}

func startServer(t *testing.T, configFile, port, basePath string) func() {
	t.Helper()
	th := test.SetupCommand(t)

	done := make(chan struct{})
	go func() {
		th.RunCommand(t, cmd.Server(), test.CmdTest{
			Args:        []string{"server", "--config", configFile, "--port=" + port},
			ExpectedOut: []string{"Server is starting"},
		})
		close(done)
	}()

	waitForServer(t, fmt.Sprintf("http://127.0.0.1:%s%s/api/v1/health", port, basePath))

	return func() {
		th.Cancel()
		<-done
	}
}

func waitForServer(t *testing.T, url string) {
	t.Helper()
	client := http.Client{Timeout: 200 * time.Millisecond}
	require.Eventually(t, func() bool {
		resp, err := client.Get(url)
		if err != nil {
			return false
		}
		defer func() {
			_ = resp.Body.Close()
		}()
		return resp.StatusCode == http.StatusOK
	}, serverStartupTimeout(), 50*time.Millisecond, "server did not become healthy at %s", url)
}

func serverStartupTimeout() time.Duration {
	// CI coverage builds can spend several seconds initializing the server before it binds.
	return 10 * time.Second
}

func requireHealthy(t *testing.T, url string) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode, "Response: %s", string(body))

	var healthResp struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(body, &healthResp))
	require.Equal(t, "healthy", healthResp.Status)
}

// findPort finds an available port.
func findPort(t *testing.T) string {
	t.Helper()
	tcpListener, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := tcpListener.Addr().(*net.TCPAddr).Port
	_ = tcpListener.Close()
	return strconv.Itoa(port)
}
