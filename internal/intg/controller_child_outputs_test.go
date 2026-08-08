// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedModel serves the OpenAI-compatible chat completions API with a fixed
// script of controller decisions, and records the observations it was sent.
type scriptedModel struct {
	mu       sync.Mutex
	calls    int
	tools    []string
	observed []string
}

func (m *scriptedModel) observations() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.observed...)
}

func (m *scriptedModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(body, &req)

	m.mu.Lock()
	m.observed = m.observed[:0]
	for _, msg := range req.Messages {
		if msg.Role == "tool" {
			m.observed = append(m.observed, msg.Content)
		}
	}
	turn := m.calls
	m.calls++
	m.mu.Unlock()

	message := map[string]any{"role": "assistant"}
	finish := "stop"
	if turn < len(m.tools) {
		args := "{}"
		if m.tools[turn] == "set_task_status" {
			args = `{"task":"checked","status":"completed","reason":"check ran"}`
		}
		message["tool_calls"] = []map[string]any{{
			"id":       fmt.Sprintf("call_%d", turn),
			"type":     "function",
			"function": map[string]any{"name": m.tools[turn], "arguments": args},
		}}
		finish = "tool_calls"
	} else {
		message["content"] = "no further actions"
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
}

// controllerWithChild runs a controller whose single action is a sub-workflow,
// and returns what the controller observed when that action finished.
func controllerWithChild(t *testing.T, childTail string) []string {
	t.Helper()

	model := &scriptedModel{tools: []string{"check", "set_task_status"}}
	server := httptest.NewServer(model)
	t.Cleanup(server.Close)

	th := test.Setup(t)
	dag := th.DAG(t, fmt.Sprintf(`
type: controller
llm:
  provider: local
  model: test-model
  base_url: %s
  system: drive the workflow
steps:
  - name: check
    description: check the draft
    action: dag.run
    with:
      dag: check_child
tasks:
  - name: checked
    description: done when check ran
---
name: check_child
steps:
  - id: load
    run: echo scratch-value
    output: SCRATCH
%s
`, server.URL, childTail))

	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, ir.Succeeded)

	return model.observations()
}

func TestControllerObservesDeclaredChildOutputs(t *testing.T) {
	t.Parallel()

	observed := controllerWithChild(t, `  - id: publish
    depends: [load]
    action: outputs.write
    with:
      values:
        verdict: clean`)

	require.NotEmpty(t, observed)
	assert.Contains(t, observed[0], `outputs: {"verdict":"clean"}`)
	// The child declared its surface, so its internal variables are not appended
	// on top of it.
	assert.NotContains(t, observed[0], "SCRATCH")
}

func TestControllerObservesChildVariablesWhenNothingIsDeclared(t *testing.T) {
	t.Parallel()

	observed := controllerWithChild(t, "")

	require.NotEmpty(t, observed)
	assert.Contains(t, observed[0], "SCRATCH=scratch-value")
}
