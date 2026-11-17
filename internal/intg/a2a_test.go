// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	a2aToken     = "a2a-intg-token"
	a2aExampleID = "https://agents.example.invalid/diagnostics"
	a2aReport    = "it's $(echo pwned) \"quoted\"\nline two; `id`"
)

var trustOnce sync.Once

// trustTestCertificate makes this process trust the certificate that every
// httptest TLS server shares. Go loads its root pool once, from SSL_CERT_FILE,
// at the first certificate verification; the pool is loaded here, from a
// private temporary file, and the file and variable are removed again. It runs
// only when an a2a test starts, so other tests keep the system roots unless
// they verified a certificate earlier in the process, in which case the a2a
// tests fail instead of passing unnoticed.
func trustTestCertificate(t *testing.T, srv *httptest.Server) {
	t.Helper()
	trustOnce.Do(func() {
		dir, err := os.MkdirTemp("", "dagu-a2a-ca-")
		require.NoError(t, err)
		defer func() { _ = os.RemoveAll(dir) }()
		path := filepath.Join(dir, "ca.pem")
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
		require.NoError(t, os.WriteFile(path, pemBytes, 0o600))
		require.NoError(t, os.Setenv("SSL_CERT_FILE", path))
		defer func() { _ = os.Unsetenv("SSL_CERT_FILE") }()
		_, err = x509.SystemCertPool()
		require.NoError(t, err)
	})
}

type a2aAgent struct {
	srv   *httptest.Server
	sends atomic.Int32
	gets  atomic.Int32

	mu    sync.Mutex
	reply map[string]any
	state func(poll int32) string
	// artifact is the text of the task artifact.
	artifact string
}

func newA2AAgent(t *testing.T) *a2aAgent {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("trusting the test certificate needs SSL_CERT_FILE, which only Linux honors")
	}
	a := &a2aAgent{artifact: a2aReport}
	a.srv = httptest.NewTLSServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.srv.Close)
	trustTestCertificate(t, a.srv)
	return a
}

func (a *a2aAgent) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+a2aToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if strings.HasSuffix(r.URL.Path, "agent-card.json") {
		rpc := "https://" + r.Host + "/rpc"
		_ = json.NewEncoder(w).Encode(map[string]any{"supportedInterfaces": []map[string]any{
			{"url": rpc, "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}}})
		return
	}
	var req struct {
		ID     string         `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	a.mu.Lock()
	reply, state := a.reply, a.state
	a.mu.Unlock()
	result := reply
	if req.Method == "GetTask" {
		result = map[string]any{
			"id": req.Params["id"], "contextId": "ctx-1",
			"status":    map[string]any{"state": "TASK_STATE_" + state(a.gets.Add(1))},
			"artifacts": []map[string]any{{"artifactId": "a", "parts": []map[string]any{{"text": a.artifact}}}},
		}
	} else {
		a.sends.Add(1)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

func (a *a2aAgent) replyTask() {
	a.reply = map[string]any{"task": map[string]any{
		"id": "task-1", "contextId": "ctx-1", "status": map[string]any{"state": "TASK_STATE_WORKING"}}}
}

func (a *a2aAgent) replyMessage() { a.replyMessageText(a2aReport) }

func (a *a2aAgent) replyMessageText(text string) {
	a.reply = map[string]any{"message": map[string]any{
		"messageId": "m-1", "role": "ROLE_AGENT", "parts": []map[string]any{{"text": text}}}}
}

// a2aExample loads an example DAG pointed at the agent, with a file-backed
// secret and a short polling interval.
func a2aExample(t *testing.T, th test.Helper, file, url string, limit string) test.DAG {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", file))
	require.NoError(t, err)
	secret := th.TempFile(t, "a2a-token.txt", []byte(a2aToken))
	spec := string(raw)
	for old, repl := range map[string]string{
		a2aExampleID:                        url,
		"provider: env\n    key: A2A_TOKEN": "provider: file\n    key: " + secret,
		"interval_sec: 10":                  "interval_sec: 1",
		"limit: 360":                        "limit: " + limit,
	} {
		if strings.Contains(spec, old) {
			spec = strings.ReplaceAll(spec, old, repl)
		}
	}
	require.NotContains(t, spec, "example.invalid")
	return th.DAG(t, spec)
}

func stdoutOf(t *testing.T, th test.Helper, dag test.DAG, id string) string {
	t.Helper()
	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	for _, n := range status.Nodes {
		if n.Step.ID != id {
			continue
		}
		out, err := os.ReadFile(n.Stdout)
		require.NoError(t, err)
		errOut, err := os.ReadFile(n.Stderr)
		require.NoError(t, err)
		return string(out) + string(errOut)
	}
	t.Fatalf("no step %s", id)
	return ""
}

func nodeStatus(t *testing.T, th test.Helper, dag test.DAG, id string) ir.NodeStatus {
	t.Helper()
	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	for _, n := range status.Nodes {
		if n.Step.ID == id {
			return n.Status
		}
	}
	t.Fatalf("no step %s", id)
	return 0
}

func TestA2ATaskPath(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)
	agent := newA2AAgent(t)
	agent.replyTask()
	agent.state = func(poll int32) string {
		if poll < 3 {
			return "WORKING"
		}
		return "COMPLETED"
	}
	dag := a2aExample(t, th, "a2a-delegate.yaml", agent.srv.URL, "20")

	dag.Agent().RunSuccess(t)

	assert.Equal(t, int32(1), agent.sends.Load())
	assert.Equal(t, int32(3), agent.gets.Load())
	assert.Equal(t, ir.NodeSkipped, nodeStatus(t, th, dag, "use_message"))
	verify := stdoutOf(t, th, dag, "verify")
	assert.Contains(t, verify, "report: "+a2aReport)
	assert.NotContains(t, stdoutOf(t, th, dag, "send"), a2aToken)
}

func TestA2AMessagePath(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)
	agent := newA2AAgent(t)
	agent.replyMessage()
	dag := a2aExample(t, th, "a2a-delegate.yaml", agent.srv.URL, "20")

	dag.Agent().RunSuccess(t)

	assert.Equal(t, int32(1), agent.sends.Load())
	assert.Zero(t, agent.gets.Load(), "a direct message needs no polling")
	assert.Equal(t, ir.NodeSkipped, nodeStatus(t, th, dag, "wait_for_task"))
	assert.Contains(t, stdoutOf(t, th, dag, "use_message"), "agent replied: "+a2aReport)
}

// A credential the agent echoes in a task artifact or a direct message is
// masked in the captured output and in what later steps print.
func TestA2AEchoedCredentialMasked(t *testing.T) {
	t.Parallel()
	echo := "echo " + a2aToken + " end"

	t.Run("task", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)
		agent := newA2AAgent(t)
		agent.replyTask()
		agent.state = func(int32) string { return "COMPLETED" }
		agent.artifact = echo
		dag := a2aExample(t, th, "a2a-delegate.yaml", agent.srv.URL, "20")

		dag.Agent().RunSuccess(t)

		poll := stdoutOf(t, th, dag, "wait_for_task")
		assert.Contains(t, poll, "echo *** end")
		assert.NotContains(t, poll, a2aToken)
		verify := stdoutOf(t, th, dag, "verify")
		assert.Contains(t, verify, "report: echo *** end")
		assert.NotContains(t, verify, a2aToken)
	})

	t.Run("message", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)
		agent := newA2AAgent(t)
		agent.replyMessageText(echo)
		dag := a2aExample(t, th, "a2a-delegate.yaml", agent.srv.URL, "20")

		dag.Agent().RunSuccess(t)

		send := stdoutOf(t, th, dag, "send")
		assert.Contains(t, send, "echo *** end")
		assert.NotContains(t, send, a2aToken)
		reply := stdoutOf(t, th, dag, "use_message")
		assert.Contains(t, reply, "agent replied: echo *** end")
		assert.NotContains(t, reply, a2aToken)
	})
}

func TestA2AInterruptedAndExhausted(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ state, want string }{
		"input required": {"INPUT_REQUIRED", "agent needs a person: input-required"},
		"auth required":  {"AUTH_REQUIRED", "agent needs a person: auth-required"},
		"failed":         {"FAILED", "task ended as 'failed'"},
		"limit":          {"WORKING", "still working when the repeat limit was reached"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			th := test.Setup(t)
			agent := newA2AAgent(t)
			agent.replyTask()
			agent.state = func(int32) string { return tc.state }
			dag := a2aExample(t, th, "a2a-delegate.yaml", agent.srv.URL, "2")

			dag.Agent().RunError(t)

			assert.Contains(t, stdoutOf(t, th, dag, "verify"), tc.want)
		})
	}
}

// A new run that starts at a2a.get_task fetches the task a previous run
// created, and sends nothing.
func TestA2ARecovery(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)
	agent := newA2AAgent(t)
	agent.state = func(int32) string { return "COMPLETED" }
	dag := a2aExample(t, th, "a2a-recover.yaml", agent.srv.URL, "20")

	dag.Agent().RunSuccess(t)

	assert.Zero(t, agent.sends.Load())
	assert.Equal(t, int32(1), agent.gets.Load())
}

// A result over the capture limit fails the step with a named error instead
// of a truncated capture.
func TestA2AOutputLimit(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)
	agent := newA2AAgent(t)
	agent.state = func(int32) string { return "COMPLETED" }
	agent.artifact = strings.Repeat("x", 2<<20)
	secret := th.TempFile(t, "a2a-token.txt", []byte(a2aToken))
	dag := th.DAG(t, `
secrets:
  - name: A2A_TOKEN
    provider: file
    key: `+secret+`

steps:
  - id: poll
    action: a2a.get_task
    with:
      agent_url: `+agent.srv.URL+`
      task_id: task-1
      headers:
        Authorization: Bearer ${A2A_TOKEN}
    output: TASK
`)

	dag.Agent().RunError(t)

	assert.Contains(t, stdoutOf(t, th, dag, "poll"), `"kind":"output_limit"`)
}
