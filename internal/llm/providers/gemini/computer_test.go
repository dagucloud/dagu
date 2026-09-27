// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gemini_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	_ "github.com/dagucloud/dagu/v2/internal/llm/providers/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateServer answers each generateContent request with the next
// scripted response body and records the request bodies.
type generateServer struct {
	mu        sync.Mutex
	responses []string
	requests  []map[string]any
}

func (s *generateServer) start(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/models/gemini-3.8-flash:generateContent", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-goog-api-key"))
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

func newComputerSession(t *testing.T, server *generateServer) computeruse.Session {
	t.Helper()
	provider, err := llm.NewProvider(llm.ProviderGemini, llm.Config{APIKey: "test-key", BaseURL: server.start(t)})
	require.NoError(t, err)
	session, err := computeruse.New(llm.ProviderGemini, provider, computeruse.ModeAuto, computeruse.Options{
		Model:  "gemini-3.8-flash",
		Task:   "Fill in the form",
		System: "The computer runs Windows.",
	})
	require.NoError(t, err)
	return session
}

func testScreen(label string) computeruse.Screen {
	return computeruse.Screen{Image: llm.Image{MediaType: "image/png", Data: []byte(label)}, Width: 1000, Height: 500}
}

func TestComputerSession(t *testing.T) {
	t.Parallel()

	model := `{"role":"model","parts":[
		{"text":"reasoning","thought":true},
		{"text":"Filling in."},
		{"functionCall":{"id":"f1","name":"click","args":{"x":500,"y":500,"intent":"focus"}},"thoughtSignature":"sig"},
		{"functionCall":{"id":"f2","name":"type_text_at","args":{"x":100,"y":200,"text":"Ada","clear_before_typing":true,"press_enter":true,"safety_decision":{"decision":"require_confirmation","explanation":"Submits a form."}}}},
		{"functionCall":{"name":"hotkey","args":{"keys":["control","s"]}}},
		{"functionCall":{"name":"scroll","args":{"x":0,"y":0,"direction":"down","magnitude_in_pixels":400}}}
	]}`
	server := &generateServer{responses: []string{
		`{"candidates":[{"content":` + model + `}],"usageMetadata":{"promptTokenCount":30,"candidatesTokenCount":5,"totalTokenCount":35}}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"f5","name":"done","args":{"success":false,"summary":"Form locked"}}}]}}]}`,
	}}
	session := newComputerSession(t, server)
	assert.Equal(t, 1440, session.ImageLimit().LongEdge)

	turn, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.NoError(t, err)
	assert.Equal(t, "Filling in.", turn.Text)
	assert.Equal(t, "Submits a form.", turn.Confirmation)
	assert.Equal(t, 35, turn.Usage.TotalTokens)
	assert.Equal(t, []computeruse.Action{
		{CallID: "f1", Kind: computeruse.KindClick, Point: &computeruse.Point{X: 500, Y: 250}, Button: computeruse.ButtonLeft, Count: 1},
		{CallID: "f2", Kind: computeruse.KindClick, Point: &computeruse.Point{X: 100, Y: 100}, Button: computeruse.ButtonLeft, Count: 3},
		{CallID: "f2", Kind: computeruse.KindType, Text: "Ada"},
		{CallID: "f2", Kind: computeruse.KindKey, Keys: []string{"Return"}},
		{CallID: "call-4", Kind: computeruse.KindKey, Keys: []string{"control", "s"}},
		{CallID: "call-5", Kind: computeruse.KindScroll, Point: &computeruse.Point{}, ScrollY: 4},
	}, turn.Actions)

	first := server.requests[0]
	tools := first["tools"].([]any)
	assert.Equal(t, map[string]any{"computerUse": map[string]any{
		"environment":                 "ENVIRONMENT_DESKTOP",
		"excludedPredefinedFunctions": []any{"navigate", "go_back", "go_forward", "search", "open_web_browser"},
	}}, tools[0])
	parts := first["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	assert.Contains(t, parts[0].(map[string]any)["text"], "Task: Fill in the form")
	assert.Equal(t, map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": "Zmlyc3Q="}}, parts[1])

	turn, err = session.Next(context.Background(), computeruse.Observation{
		Screen:       testScreen("second"),
		Acknowledged: true,
		Results: []computeruse.Result{
			{CallID: "f1"}, {CallID: "f2"}, {CallID: "f2", Error: "field disabled"}, {CallID: "f2", Skipped: true},
			{CallID: "call-4", Skipped: true}, {CallID: "call-5", Skipped: true},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, &computeruse.Done{Success: false, Summary: "Form locked"}, turn.Done)

	contents := server.requests[1]["contents"].([]any)
	require.Len(t, contents, 3)
	var sent any
	require.NoError(t, json.Unmarshal([]byte(model), &sent))
	assert.Equal(t, sent, contents[1], "the model turn is sent back unchanged")

	responses := contents[2].(map[string]any)["parts"].([]any)
	require.Len(t, responses, 4)
	assert.Equal(t, map[string]any{"functionResponse": map[string]any{"id": "f1", "name": "click", "response": map[string]any{}}}, responses[0])
	assert.Equal(t, map[string]any{"functionResponse": map[string]any{
		"id": "f2", "name": "type_text_at",
		"response": map[string]any{"error": "field disabled", "safety_acknowledgement": "true"},
	}}, responses[1])
	last := responses[3].(map[string]any)["functionResponse"].(map[string]any)
	assert.NotContains(t, last, "id", "calls without an ID are answered without one")
	assert.Equal(t, []any{map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": "c2Vjb25k"}}}, last["parts"])
}

func TestComputerSessionWaitAndBlocked(t *testing.T) {
	t.Parallel()

	server := &generateServer{responses: []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"wait","args":{"seconds":1.5}}}]}}]}`,
		`{"promptFeedback":{"blockReason":"SAFETY"}}`,
	}}
	session := newComputerSession(t, server)

	turn, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.NoError(t, err)
	assert.Equal(t, 1500*time.Millisecond, turn.Actions[0].Duration)

	_, err = session.Next(context.Background(), computeruse.Observation{Screen: testScreen("second")})
	require.ErrorContains(t, err, "blocked: SAFETY")
}
