// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	_ "github.com/dagucloud/dagu/v2/internal/llm/providers/openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// responsesServer answers each Responses API request with the next scripted
// response body and records the request bodies.
type responsesServer struct {
	mu        sync.Mutex
	responses []string
	requests  []map[string]any
}

func (s *responsesServer) start(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/responses", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var request map[string]any
		require.NoError(t, json.Unmarshal(body, &request))
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests = append(s.requests, request)
		response := s.responses[0]
		s.responses = s.responses[1:]
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func newComputerSession(t *testing.T, providerType llm.ProviderType, server *responsesServer) (computeruse.Session, error) {
	t.Helper()
	provider, err := llm.NewProvider(providerType, llm.Config{APIKey: "test-key", BaseURL: server.start(t)})
	require.NoError(t, err)
	return computeruse.New(providerType, provider, computeruse.ModeNative, computeruse.Options{
		Model:  "gpt-5.6-sol",
		Task:   "Rename the file",
		System: "The computer runs Windows.",
	})
}

func testScreen(label string) computeruse.Screen {
	return computeruse.Screen{Image: llm.Image{MediaType: "image/png", Data: []byte(label)}, Width: 1440, Height: 900}
}

func TestComputerSession(t *testing.T) {
	t.Parallel()

	server := &responsesServer{responses: []string{
		`{"id":"resp_1","status":"completed","output":[
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Opening the file."}]},
			{"type":"computer_call","call_id":"call_1","actions":[
				{"type":"click","button":"right","x":10,"y":20},
				{"type":"double_click","x":30,"y":40},
				{"type":"drag","path":[{"x":1,"y":2},{"x":3,"y":4}]},
				{"type":"scroll","x":5,"y":6,"scroll_x":0,"scroll_y":-300},
				{"type":"keypress","keys":["CTRL","S"]},
				{"type":"type","text":"report.txt"},
				{"type":"wait"},
				{"type":"screenshot"}
			],"pending_safety_checks":[{"id":"sc_1","code":"sensitive_domain","message":"Check the file name."}]}
		],"usage":{"input_tokens":50,"output_tokens":10,"total_tokens":60}}`,
		`{"id":"resp_2","status":"completed","output":[
			{"type":"function_call","call_id":"call_2","name":"done","arguments":"{\"success\":true,\"summary\":\"Renamed\"}"}
		]}`,
	}}
	session, err := newComputerSession(t, llm.ProviderOpenAI, server)
	require.NoError(t, err)

	turn, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.NoError(t, err)
	assert.Equal(t, "Opening the file.", turn.Text)
	assert.Equal(t, "Check the file name.", turn.Confirmation)
	assert.Equal(t, llm.Usage{PromptTokens: 50, CompletionTokens: 10, TotalTokens: 60}, turn.Usage)
	require.Len(t, turn.Actions, 8)
	assert.Equal(t, computeruse.Action{CallID: "call_1", Kind: computeruse.KindClick, Point: &computeruse.Point{X: 10, Y: 20}, Button: computeruse.ButtonRight, Count: 1}, turn.Actions[0])
	assert.Equal(t, 2, turn.Actions[1].Count)
	assert.Equal(t, []computeruse.Point{{X: 1, Y: 2}, {X: 3, Y: 4}}, turn.Actions[2].Path)
	assert.Equal(t, -3, turn.Actions[3].ScrollY)
	assert.Equal(t, []string{"CTRL", "S"}, turn.Actions[4].Keys)
	assert.Equal(t, computeruse.KindScreenshot, turn.Actions[7].Kind)

	first := server.requests[0]
	assert.Equal(t, "gpt-5.6-sol", first["model"])
	assert.NotContains(t, first, "previous_response_id")
	assert.Contains(t, first["instructions"], "The computer runs Windows.")
	assert.Equal(t, map[string]any{"type": "computer"}, first["tools"].([]any)[0])
	content := first["input"].([]any)[0].(map[string]any)["content"].([]any)
	assert.Contains(t, content[0].(map[string]any)["text"], "Task: Rename the file")
	assert.Equal(t, "data:image/png;base64,Zmlyc3Q=", content[1].(map[string]any)["image_url"])

	turn, err = session.Next(context.Background(), computeruse.Observation{
		Screen:       testScreen("second"),
		Acknowledged: true,
		Results:      []computeruse.Result{{CallID: "call_1"}, {CallID: "call_1", Error: "window closed"}},
	})
	require.NoError(t, err)
	assert.Equal(t, &computeruse.Done{Success: true, Summary: "Renamed"}, turn.Done)

	second := server.requests[1]
	assert.Equal(t, "resp_1", second["previous_response_id"])
	input := second["input"].([]any)
	require.Len(t, input, 2)
	assert.Equal(t, map[string]any{
		"type":    "computer_call_output",
		"call_id": "call_1",
		"output": map[string]any{
			"type":      "computer_screenshot",
			"image_url": "data:image/png;base64,c2Vjb25k",
			"detail":    "original",
		},
		"acknowledged_safety_checks": []any{map[string]any{"id": "sc_1", "code": "sensitive_domain", "message": "Check the file name."}},
	}, input[0])
	assert.Contains(t, input[1].(map[string]any)["content"].([]any)[0].(map[string]any)["text"], "Action 2 failed: window closed")
}

func TestComputerSessionRefusal(t *testing.T) {
	t.Parallel()

	server := &responsesServer{responses: []string{
		`{"id":"resp_1","status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"I can't help with that."}]}]}`,
	}}
	session, err := newComputerSession(t, llm.ProviderOpenAI, server)
	require.NoError(t, err)

	_, err = session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.ErrorContains(t, err, "I can't help with that.")
}

// OpenCode shares the OpenAI provider but has no Responses API computer tool.
func TestComputerSessionNotNativeForOpenCode(t *testing.T) {
	t.Parallel()

	_, err := newComputerSession(t, llm.ProviderOpenCode, &responsesServer{})
	require.ErrorContains(t, err, "no native computer use")
}
