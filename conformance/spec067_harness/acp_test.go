// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec067_harness_test

import (
	"bufio"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestACPFixture serves the independent ACP peer used by binary conformance tests.
func TestACPFixture(_ *testing.T) {
	if !slices.Contains(os.Args, "--acp-fixture") {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	send := func(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }
	for scanner.Scan() {
		var msg struct {
			ID     any
			Method string
			Params struct {
				Cwd    string
				Prompt []struct{ Text string }
			}
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			os.Exit(2)
		}
		result := map[string]any{}
		switch msg.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}
		case "session/new":
			if !filepath.IsAbs(msg.Params.Cwd) {
				os.Exit(3)
			}
			result["sessionId"] = "fixture"
		case "session/prompt":
			if len(msg.Params.Prompt) == 0 {
				os.Exit(4)
			}
			for _, part := range []string{"ACP:", msg.Params.Prompt[0].Text} {
				send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
					"sessionId": "fixture", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": part}},
				}})
			}
			result["stopReason"] = "end_turn"
		}
		send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
	}
	os.Exit(0)
}

// TestHarnessACP verifies that ACP text output reaches a dependent workflow step.
func TestHarnessACP(t *testing.T) {
	dagu := harness.NewRunner(t)
	binary, err := os.Executable()
	require.NoError(t, err)
	dagu.WriteFile("acp.yaml", fmt.Sprintf(`harnesses:
  adapter:
    protocol: acp
    binary: %q
    prefix_args: ["-test.run=^TestACPFixture$", "--", "--acp-fixture"]
steps:
  - id: ask
    action: harness.run
    with:
      provider: adapter
      prompt: hello
      stdin: context
    output: ANSWER
  - id: use
    depends: ask
    action: harness.run
    with:
      provider: adapter
      prompt: "${ANSWER}"
`, binary))
	dagu.Run("validate", "acp.yaml").ExpectExitCode(0)
	result := dagu.Run("start", "acp.yaml")
	result.ExpectExitCode(0)
	require.Equal(t, "ACP:hello\n\ncontext", stepStdout(t, result.Stdout(), 0))
	require.Equal(t, "ACP:ACP:hello\n\ncontext", stepStdout(t, result.Stdout(), 1))
}

// An empty prompt must produce an explicit fixture failure instead of a panic.
func TestACPFixtureEmptyPrompt(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestACPFixture$", "--", "--acp-fixture")
	cmd.Stdin = strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"session/prompt\",\"params\":{\"prompt\":[]}}\n")
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, string(output))
	require.Equal(t, 4, exitErr.ExitCode(), string(output))
}

// TestHarnessACPRemote verifies remote configuration and remote-to-stdio output propagation.
func TestHarnessACPRemote(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("TLS=%t", secure), func(t *testing.T) { runHarnessACPRemote(t, secure) })
	}
}

// runHarnessACPRemote verifies the same workflow with cleartext or trusted TLS.
func runHarnessACPRemote(t *testing.T, secure bool) {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		socket, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"acp.v1"}})
		if !assert.NoError(t, err) {
			return
		}
		defer func() { _ = socket.CloseNow() }()
		for {
			_, data, err := socket.Read(t.Context())
			if err != nil {
				return
			}
			var msg struct {
				ID     any
				Method string
				Params struct {
					Cwd    string
					Prompt []struct{ Text string }
				}
			}
			if !assert.NoError(t, json.Unmarshal(data, &msg)) {
				return
			}
			result := map[string]any{}
			switch msg.Method {
			case "initialize":
				result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}
			case "session/new":
				assert.Equal(t, "/srv/remote-project", msg.Params.Cwd)
				result["sessionId"] = "remote"
			case "session/prompt":
				if !assert.Len(t, msg.Params.Prompt, 1) {
					return
				}
				update, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
					"sessionId": "remote", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "remote:" + msg.Params.Prompt[0].Text}},
				}})
				assert.NoError(t, socket.Write(t.Context(), websocket.MessageText, update))
				result["stopReason"] = "end_turn"
			}
			response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
			assert.NoError(t, socket.Write(t.Context(), websocket.MessageText, response))
		}
	}))
	caFile := ""
	if secure {
		server.StartTLS()
		caFile = filepath.Join(t.TempDir(), "ca.pem")
		require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.TLS.Certificates[0].Certificate[0]}), 0o600))
	} else {
		server.Start()
	}
	defer server.Close()
	dagu := harness.NewRunner(t)
	binary, err := os.Executable()
	require.NoError(t, err)
	caConfig := ""
	if secure {
		caConfig = fmt.Sprintf("    ca_file: %q\n", caFile)
	}
	// The remote provider is chosen by a variable, so the DAG-level model default
	// is filtered only after it resolves.
	dagu.WriteFile("remote.yaml", fmt.Sprintf(`env:
  ACP_ENDPOINT: %q
  ACP_TOKEN: test-token
  AGENT_CWD: /srv/remote-project
  SELECTED_PROVIDER: remote
harness:
  provider: local
  model: sonnet
harnesses:
  remote:
    protocol: acp
    transport: websocket
    url: ${ACP_ENDPOINT}
%s    cwd: ${AGENT_CWD}
    headers:
      Authorization: Bearer ${ACP_TOKEN}
  local:
    protocol: acp
    binary: %q
    prefix_args: ["-test.run=^TestACPFixture$", "--", "--acp-fixture"]
steps:
  - id: ask
    action: harness.run
    with:
      provider: ${SELECTED_PROVIDER}
      prompt: hello
      stdin: context
    output: ANSWER
  - id: use
    depends: ask
    action: harness.run
    with:
      provider: local
      prompt: "${ANSWER}"
`, strings.Replace(server.URL, "http", "ws", 1), caConfig, binary))
	dagu.Run("validate", "remote.yaml").ExpectExitCode(0)
	result := dagu.Run("start", "remote.yaml")
	result.ExpectExitCode(0)
	require.Equal(t, "remote:hello\n\ncontext", stepStdout(t, result.Stdout(), 0))
	require.Equal(t, "ACP:remote:hello\n\ncontext", stepStdout(t, result.Stdout(), 1))
}
