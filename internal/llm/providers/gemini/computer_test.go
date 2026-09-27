// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gemini_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/llm/llmtest"
	_ "github.com/dagucloud/dagu/v2/internal/llm/providers/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateServer returns a generateContent server that answers with the scripted
// response bodies.
func generateServer(responses ...string) *llmtest.Server {
	return &llmtest.Server{Path: "/models/gemini-3.8-flash:generateContent", Headers: map[string]string{"x-goog-api-key": "test-key"}, Responses: responses}
}

func newComputerSession(t *testing.T, server *llmtest.Server) computeruse.Session {
	t.Helper()
	provider, err := llm.NewProvider(llm.ProviderGemini, llm.Config{APIKey: "test-key", BaseURL: server.Start(t)})
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
	server := generateServer(
		`{"candidates":[{"content":`+model+`}],"usageMetadata":{"promptTokenCount":30,"candidatesTokenCount":5,"totalTokenCount":35}}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"f5","name":"done","args":{"success":false,"summary":"Form locked"}}}]}}]}`,
	)
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

	first := server.Requests()[0]
	tools := first["tools"].([]any)
	assert.Equal(t, map[string]any{"computerUse": map[string]any{
		"environment":                 "ENVIRONMENT_DESKTOP",
		"excludedPredefinedFunctions": []any{"key_down", "key_up"},
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

	contents := server.Requests()[1]["contents"].([]any)
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

	server := generateServer(
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"wait","args":{"seconds":1.5}}}]}}]}`,
		`{"promptFeedback":{"blockReason":"SAFETY"}}`,
	)
	session := newComputerSession(t, server)

	turn, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.NoError(t, err)
	assert.Equal(t, 1500*time.Millisecond, turn.Actions[0].Duration)

	_, err = session.Next(context.Background(), computeruse.Observation{Screen: testScreen("second")})
	require.ErrorContains(t, err, "blocked: SAFETY")
}

func TestComputerSessionDesktopActions(t *testing.T) {
	t.Parallel()

	model := `{"role":"model","parts":[
		{"functionCall":{"id":"a","name":"move","args":{"x":100,"y":200}}},
		{"functionCall":{"id":"b","name":"triple_click","args":{"x":0,"y":0}}},
		{"functionCall":{"id":"c","name":"middle_click","args":{"x":0,"y":0}}},
		{"functionCall":{"id":"d","name":"mouse_down","args":{"x":500,"y":500}}},
		{"functionCall":{"id":"e","name":"mouse_up","args":{}}},
		{"functionCall":{"id":"f","name":"wait","args":{}}},
		{"functionCall":{"id":"g","name":"type_text_at","args":{"x":0,"y":0,"text":"new"}}}
	]}`
	server := generateServer(`{"candidates":[{"content":` + model + `}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"thoughtsTokenCount":7,"totalTokenCount":20}}`)
	session := newComputerSession(t, server)

	turn, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.NoError(t, err)
	assert.Equal(t, 10, turn.Usage.CompletionTokens, "thinking tokens count as output")
	kinds := make([]string, 0, len(turn.Actions))
	for _, action := range turn.Actions {
		kinds = append(kinds, string(action.Kind))
	}
	assert.Equal(t, []string{"move", "click", "click", "move", "mouse_down", "mouse_up", "wait", "click", "type"}, kinds)
	assert.Equal(t, &computeruse.Point{X: 100, Y: 100}, turn.Actions[0].Point)
	assert.Equal(t, 3, turn.Actions[1].Count)
	assert.Equal(t, computeruse.ButtonMiddle, turn.Actions[2].Button)
	assert.Equal(t, &computeruse.Point{X: 500, Y: 250}, turn.Actions[3].Point, "mouse_down moves to its position first")
	assert.Equal(t, time.Second, turn.Actions[6].Duration, "wait defaults to one second")
	assert.Equal(t, 3, turn.Actions[7].Count, "type_text_at clears the field by default")
}

// A call that cannot be performed stops the batch.
func TestComputerSessionHaltsBatch(t *testing.T) {
	t.Parallel()

	server := generateServer(
		`{"candidates":[{"content":{"role":"model","parts":[
			{"functionCall":{"id":"a","name":"click","args":{"x":1}}},
			{"functionCall":{"id":"b","name":"type","args":{"text":"x"}}}
		]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`,
	)
	session := newComputerSession(t, server)

	turn, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.NoError(t, err)
	assert.Empty(t, turn.Actions)

	_, err = session.Next(context.Background(), computeruse.Observation{Screen: testScreen("second")})
	require.NoError(t, err)
	responses := server.Requests()[1]["contents"].([]any)[2].(map[string]any)["parts"].([]any)
	response := func(i int) any {
		return responses[i].(map[string]any)["functionResponse"].(map[string]any)["response"]
	}
	assert.Equal(t, map[string]any{"error": "click needs a position"}, response(0))
	assert.Equal(t, map[string]any{"error": computeruse.SkippedText}, response(1), "the call after the halt is reported as skipped")
}

func TestComputerSessionStops(t *testing.T) {
	t.Parallel()

	for body, want := range map[string]string{
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"click","args":{"x":1,"y":1,"safety_decision":{"decision":"blocked","explanation":"Payment"}}}}]}}]}`: "stopped the action (blocked): Payment",
		`{"candidates":[{"finishReason":"MALFORMED_FUNCTION_CALL"}]}`:         "no content (finish reason MALFORMED_FUNCTION_CALL)",
		`{"candidates":[{"content":{"role":"model"},"finishReason":"STOP"}]}`: "no content (finish reason STOP)",
	} {
		session := newComputerSession(t, generateServer(body))
		_, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
		require.ErrorContains(t, err, want)
	}
}

// Models before 3.5 have no desktop environment and use the generic session
// in auto mode.
func TestComputerSessionModelSupport(t *testing.T) {
	t.Parallel()

	for model, native := range map[string]bool{
		"gemini-3.8-flash":                        true,
		"gemini-3.5-flash-lite":                   true,
		"models/gemini-4-pro":                     true,
		"tuned-desktop-model":                     true,
		"gemini-3-flash-preview":                  false,
		"gemini-2.5-computer-use-preview-10-2025": false,
	} {
		provider, err := llm.NewProvider(llm.ProviderGemini, llm.Config{APIKey: "test-key"})
		require.NoError(t, err)
		_, err = computeruse.New(llm.ProviderGemini, provider, computeruse.ModeNative, computeruse.Options{Model: model})
		if native {
			assert.NoError(t, err, model)
		} else {
			assert.ErrorIs(t, err, computeruse.ErrModelNotSupported, model)
		}
	}
}
