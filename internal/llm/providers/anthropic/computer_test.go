// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/llm/llmtest"
	_ "github.com/dagucloud/dagu/v2/internal/llm/providers/anthropic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// messagesServer returns a Messages API server that answers with the scripted
// response bodies.
func messagesServer(responses ...string) *llmtest.Server {
	return &llmtest.Server{Path: "/v1/messages", Headers: map[string]string{"x-api-key": "test-key"}, Responses: responses}
}

func newComputerSession(t *testing.T, server *llmtest.Server) computeruse.Session {
	t.Helper()
	provider, err := llm.NewProvider(llm.ProviderAnthropic, llm.Config{APIKey: "test-key", BaseURL: server.Start(t)})
	require.NoError(t, err)
	session, err := computeruse.New(llm.ProviderAnthropic, provider, computeruse.ModeAuto, computeruse.Options{
		Model:  "claude-opus-5",
		Task:   "Save the document",
		System: "The computer runs macOS.",
	})
	require.NoError(t, err)
	return session
}

func testScreen(label string) computeruse.Screen {
	return computeruse.Screen{Image: llm.Image{MediaType: "image/png", Data: []byte(label)}, Width: 1280, Height: 800}
}

func TestComputerSession(t *testing.T) {
	t.Parallel()

	assistant := `[
		{"type":"thinking","thinking":"","signature":"sig-1"},
		{"type":"text","text":"Saving."},
		{"type":"tool_use","id":"t1","name":"left_click","toolset_name":"computer","input":{"coordinate":[10,20],"text":"shift"}},
		{"type":"tool_use","id":"t2","name":"key","toolset_name":"computer","input":{"text":"cmd+s","repeat":2}},
		{"type":"tool_use","id":"t3","name":"scroll","toolset_name":"computer","input":{"scroll_direction":"up","scroll_amount":5}},
		{"type":"tool_use","id":"t4","name":"screenshot","toolset_name":"computer","input":{}}
	]`
	server := messagesServer(
		`{"content":`+assistant+`,"stop_reason":"tool_use","usage":{"input_tokens":100,"output_tokens":20}}`,
		`{"content":[{"type":"tool_use","id":"t5","name":"done","input":{"success":true,"summary":"Saved"}}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`,
	)
	session := newComputerSession(t, server)
	assert.Equal(t, 2000, session.ImageLimit().LongEdge)

	turn, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.NoError(t, err)
	assert.Equal(t, "Saving.", turn.Text)
	assert.Equal(t, llm.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120}, turn.Usage)
	assert.Equal(t, []computeruse.Action{
		{CallID: "t1", Kind: computeruse.KindClick, Point: &computeruse.Point{X: 10, Y: 20}, Button: computeruse.ButtonLeft, Count: 1, Modifiers: []string{"shift"}},
		{CallID: "t2", Kind: computeruse.KindKey, Keys: []string{"cmd", "s"}, Repeat: 2},
		{CallID: "t3", Kind: computeruse.KindScroll, ScrollY: -5},
		{CallID: "t4", Kind: computeruse.KindScreenshot},
	}, turn.Actions)

	first := server.Requests()[0]
	assert.Equal(t, "claude-opus-5", first["model"])
	assert.NotContains(t, first, "temperature")
	assert.NotContains(t, first, "thinking")
	assert.Equal(t, map[string]any{"type": "auto"}, first["tool_choice"])
	assert.Contains(t, first["system"], "The computer runs macOS.")
	tools := first["tools"].([]any)
	assert.Equal(t, map[string]any{"type": "computer_toolset_20260801"}, tools[0])
	assert.Equal(t, "done", tools[1].(map[string]any)["name"])
	firstContent := first["messages"].([]any)[0].(map[string]any)["content"].([]any)
	assert.Contains(t, firstContent[0].(map[string]any)["text"], "Task: Save the document")
	assert.Equal(t, "image", firstContent[1].(map[string]any)["type"])

	turn, err = session.Next(context.Background(), computeruse.Observation{
		Screen: testScreen("second"),
		Results: []computeruse.Result{
			{CallID: "t1"},
			{CallID: "t2", Error: "unknown key"},
			{CallID: "t3", Skipped: true},
			{CallID: "t4", Skipped: true},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, &computeruse.Done{Success: true, Summary: "Saved"}, turn.Done)

	messages := server.Requests()[1]["messages"].([]any)
	require.Len(t, messages, 3)
	var echoed, sent any
	require.NoError(t, json.Unmarshal([]byte(assistant), &sent))
	echoed = messages[1].(map[string]any)["content"]
	assert.Equal(t, sent, echoed, "the assistant turn is sent back unchanged")

	results := messages[2].(map[string]any)["content"].([]any)
	require.Len(t, results, 4)
	assert.Equal(t, map[string]any{
		"type": "tool_result", "tool_use_id": "t1", "toolset_name": "computer",
		"content": []any{map[string]any{"type": "text", "text": "OK"}},
	}, results[0])
	assert.Equal(t, map[string]any{
		"type": "tool_result", "tool_use_id": "t2", "toolset_name": "computer", "is_error": true,
		"content": []any{map[string]any{"type": "text", "text": "Error: unknown key"}},
	}, results[1])
	assert.Equal(t, "Not executed: an earlier computer action in this turn failed.", results[3].(map[string]any)["content"].([]any)[0].(map[string]any)["text"])
}

func TestComputerSessionScreenshotResult(t *testing.T) {
	t.Parallel()

	server := messagesServer(
		`{"content":[{"type":"tool_use","id":"t1","name":"zoom","toolset_name":"computer","input":{"region":[0,0,100,50]}}],"stop_reason":"tool_use"}`,
		`{"content":[{"type":"text","text":"Looks saved."}],"stop_reason":"end_turn"}`,
	)
	session := newComputerSession(t, server)

	turn, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.NoError(t, err)
	assert.Equal(t, &computeruse.Rect{Max: computeruse.Point{X: 100, Y: 50}}, turn.Actions[0].Region)

	zoomed := llm.Image{MediaType: "image/png", Data: []byte{1, 2}}
	turn, err = session.Next(context.Background(), computeruse.Observation{
		Screen:  testScreen("second"),
		Results: []computeruse.Result{{CallID: "t1", Image: &zoomed}},
	})
	require.NoError(t, err)
	assert.Empty(t, turn.Actions)
	assert.Nil(t, turn.Done)

	result := server.Requests()[1]["messages"].([]any)[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	assert.Equal(t, []any{map[string]any{
		"type":   "image",
		"source": map[string]any{"type": "base64", "media_type": "image/png", "data": "AQI="},
	}}, result["content"])
}

// A turn with no tool call is followed by the screen itself.
func TestComputerSessionNoteAfterText(t *testing.T) {
	t.Parallel()

	server := messagesServer(
		`{"content":[{"type":"text","text":"Done, I think."}],"stop_reason":"end_turn"}`,
		`{"content":[{"type":"tool_use","id":"t1","name":"wait","toolset_name":"computer","input":{"duration":1.5}}],"stop_reason":"tool_use"}`,
	)
	session := newComputerSession(t, server)

	_, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.NoError(t, err)
	turn, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("second"), Note: "Call done to finish."})
	require.NoError(t, err)
	assert.Equal(t, 1500*time.Millisecond, turn.Actions[0].Duration)

	content := server.Requests()[1]["messages"].([]any)[2].(map[string]any)["content"].([]any)
	require.Len(t, content, 2)
	assert.Contains(t, content[0].(map[string]any)["text"], "Call done to finish.")
	assert.NotContains(t, content[0].(map[string]any)["text"], "Task:")
	assert.Equal(t, "image", content[1].(map[string]any)["type"])
}

func TestComputerSessionRefusal(t *testing.T) {
	t.Parallel()

	server := messagesServer(
		`{"content":[],"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"not allowed"}}`,
	)
	session := newComputerSession(t, server)

	_, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.ErrorContains(t, err, "not allowed")
}

// A member call that cannot be performed stops the batch; the calls after it
// are not run and are answered as skipped.
func TestComputerSessionHaltsBatch(t *testing.T) {
	t.Parallel()

	server := messagesServer(
		`{"content":[
			{"type":"tool_use","id":"t1","name":"left_click","toolset_name":"computer","input":{"coordinate":[1,2]}},
			{"type":"tool_use","id":"t2","name":"left_click","toolset_name":"computer","input":{"coordinate":[1]}},
			{"type":"tool_use","id":"t3","name":"type","toolset_name":"computer","input":{"text":"x"}}
		],"stop_reason":"tool_use"}`,
		`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`,
	)
	session := newComputerSession(t, server)

	turn, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.NoError(t, err)
	require.Len(t, turn.Actions, 1, "only the call before the invalid one runs")

	_, err = session.Next(context.Background(), computeruse.Observation{
		Screen:  testScreen("second"),
		Results: []computeruse.Result{{CallID: "t1"}},
	})
	require.NoError(t, err)
	results := server.Requests()[1]["messages"].([]any)[2].(map[string]any)["content"].([]any)
	text := func(i int) any {
		return results[i].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]
	}
	assert.Equal(t, "OK", text(0))
	assert.Equal(t, "Error: coordinate must be [x, y]", text(1))
	assert.Equal(t, "Not executed: an earlier computer action in this turn failed.", text(2))
}

// A tool_use cut off by max_tokens is not run.
func TestComputerSessionTruncated(t *testing.T) {
	t.Parallel()

	server := messagesServer(`{"content":[{"type":"tool_use","id":"t1","name":"type","toolset_name":"computer","input":{"text":"hel"}}],"stop_reason":"max_tokens"}`)
	session := newComputerSession(t, server)

	_, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.ErrorContains(t, err, "cut off (max_tokens)")
}

// Models before the computer toolset use the generic session in auto mode.
func TestComputerSessionModelSupport(t *testing.T) {
	t.Parallel()

	for model, native := range map[string]bool{
		"claude-opus-5-5":            true,
		"claude-fable-5-1":           true,
		"claude-sonnet-5":            true,
		"claude-opus-4-8":            true,
		"anthropic.claude-opus-5":    true,
		"my-deployment":              true,
		"claude-opus-4-7":            false,
		"claude-sonnet-4-6":          false,
		"claude-sonnet-4-5-20250929": false,
		"claude-opus-4-5@20251101":   false,
		"claude-haiku-4-5":           false,
		"claude-3-7-sonnet-20250219": false,
	} {
		provider, err := llm.NewProvider(llm.ProviderAnthropic, llm.Config{APIKey: "test-key"})
		require.NoError(t, err)
		_, err = computeruse.New(llm.ProviderAnthropic, provider, computeruse.ModeNative, computeruse.Options{Model: model})
		if native {
			assert.NoError(t, err, model)
		} else {
			assert.ErrorIs(t, err, computeruse.ErrModelNotSupported, model)
		}
	}
}
