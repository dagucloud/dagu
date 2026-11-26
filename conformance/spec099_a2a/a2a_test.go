// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec099_a2a_test

import (
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

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

const token = "conformance-token"

type agent struct {
	srv   *httptest.Server
	sends atomic.Int32
	gets  atomic.Int32
	// drop closes the connection on SendMessage instead of answering. Tests
	// set drop and message after the server starts, so both are atomic.
	drop atomic.Bool
	// message makes SendMessage answer with a Message instead of a Task.
	message atomic.Bool
	// dropFirst closes the connection on the first SendMessage only, after
	// recording it, as an agent that accepted the message but lost the reply.
	dropFirst atomic.Bool

	mu  sync.Mutex
	ids []string
}

func (a *agent) sentIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.ids...)
}

// newAgent serves an A2A 1.0 JSON-RPC agent over TLS that requires the bearer
// token and echoes it in its task artifact, so masking is observable. The
// returned env makes the Dagu process trust the agent's certificate.
func newAgent(t *testing.T) (*agent, []string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("trusting the test certificate needs SSL_CERT_FILE, which only Linux honors")
	}
	a := &agent{}
	a.srv = httptest.NewTLSServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.srv.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.srv.Certificate().Raw})
	if err := os.WriteFile(ca, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return a, []string{"A2A_URL=" + a.srv.URL, "A2A_TOKEN=" + token, "SSL_CERT_FILE=" + ca}
}

func (a *agent) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if strings.HasSuffix(r.URL.Path, "agent-card.json") {
		_ = json.NewEncoder(w).Encode(map[string]any{"supportedInterfaces": []map[string]any{
			{"url": "https://" + r.Host + "/rpc", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}}})
		return
	}
	var req struct {
		ID     string         `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	result := map[string]any{}
	switch req.Method {
	case "SendMessage":
		a.sends.Add(1)
		if m, ok := req.Params["message"].(map[string]any); ok {
			a.mu.Lock()
			a.ids = append(a.ids, m["messageId"].(string))
			a.mu.Unlock()
		}
		if a.drop.Load() || a.dropFirst.CompareAndSwap(true, false) {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		result = map[string]any{"task": task("task-1", "WORKING", "")}
		if a.message.Load() {
			result = map[string]any{"message": map[string]any{"messageId": "m-1", "role": "ROLE_AGENT",
				"parts": []map[string]any{{"text": "it's done; $(true) via " + token}}}}
		}
	case "GetTask":
		state := "WORKING"
		if a.gets.Add(1) >= 2 {
			state = "COMPLETED"
		}
		result = task(req.Params["id"].(string), state, "it's ready; $(true) via "+token)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

func task(id, state, text string) map[string]any {
	t := map[string]any{"id": id, "contextId": "ctx-1", "status": map[string]any{"state": "TASK_STATE_" + state}}
	if text != "" {
		t["artifacts"] = []map[string]any{{"artifactId": "a", "parts": []map[string]any{{"text": text}}}}
	}
	return t
}

// A task is submitted once, polled with repeat_policy until completed, its
// artifact reaches a later step through the environment unmodified, and the
// echoed credential is masked in the output.
func TestA2ATask(t *testing.T) {
	t.Parallel()
	a, env := newAgent(t)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env, "start", "task.yaml")

	result.ExpectExitCode(0)
	dagu.ExpectFileContains("send.out", `"kind":"task"`, `"task_id":"task-1"`, `"state":"working"`)
	dagu.ExpectFileContains("poll.out", `"state":"completed"`, "***")
	dagu.ExpectFileNotContains("poll.out", token)
	dagu.ExpectFileContains("use.out", "task|it's ready; $(true) via ***")
	if a.sends.Load() != 1 || a.gets.Load() != 2 {
		t.Fatalf("sends=%d gets=%d, want 1 and 2", a.sends.Load(), a.gets.Load())
	}
}

// A direct message ends the flow without any task call.
func TestA2AMessage(t *testing.T) {
	t.Parallel()
	a, env := newAgent(t)
	a.message.Store(true)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env, "start", "message.yaml")

	result.ExpectExitCode(0)
	dagu.ExpectFileContains("send.out", `"kind":"message"`)
	dagu.ExpectFileNotContains("send.out", token)
	dagu.ExpectFileContains("use.out", "message|it's done; $(true) via ***")
	if a.gets.Load() != 0 {
		t.Fatalf("gets=%d, want 0", a.gets.Load())
	}
}

// A response that never arrives fails the step after exactly one submission.
func TestA2ADroppedResponse(t *testing.T) {
	t.Parallel()
	a, env := newAgent(t)
	a.drop.Store(true)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env, "start", "message.yaml")

	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("Outcome unknown")
	if a.sends.Load() != 1 {
		t.Fatalf("sends=%d, want 1", a.sends.Load())
	}
}

// A retry_policy retry after a lost response resends the same messageId, so a
// deduplicating agent sees one logical message.
func TestA2ARetrySameID(t *testing.T) {
	t.Parallel()
	a, env := newAgent(t)
	a.dropFirst.Store(true)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env, "start", "retry.yaml")

	result.ExpectExitCode(0)
	ids := a.sentIDs()
	if len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("message ids=%v, want two equal non-empty IDs", ids)
	}
	dagu.ExpectFileContains("send.out", `"sent_message_id":"`+ids[0]+`"`)
}

// with.message_id resolves references and is sent as provided.
func TestA2AExplicitID(t *testing.T) {
	t.Parallel()
	a, env := newAgent(t)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env, "start", "--run-id", "run-42", "message_id.yaml")

	result.ExpectExitCode(0)
	dagu.ExpectFileContains("send.out", `"sent_message_id":"run-42-delegate"`)
	if ids := a.sentIDs(); len(ids) != 1 || ids[0] != "run-42-delegate" {
		t.Fatalf("message ids=%v, want [run-42-delegate]", ids)
	}
}

// A new run that starts at a2a.get_task reads the task and sends nothing.
func TestA2ARecovery(t *testing.T) {
	t.Parallel()
	a, env := newAgent(t)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(append(env, "TASK_ID=task-9"), "start", "recover.yaml")

	result.ExpectExitCode(0)
	dagu.ExpectFileContains("poll.out", `"task_id":"task-9"`)
	if a.sends.Load() != 0 {
		t.Fatalf("sends=%d, want 0", a.sends.Load())
	}
}

func TestA2AConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ file, message string }{
		{"unknown_field.yaml", "task_id"},
		{"userinfo.yaml", "must not contain credentials"},
		{"query.yaml", "query string"},
		{"reserved_header.yaml", "cannot be overridden"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)
		})
	}
}
