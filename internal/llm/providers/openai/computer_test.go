// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package openai_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/llm/llmtest"
	_ "github.com/dagucloud/dagu/v2/internal/llm/providers/openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// responsesServer returns a Responses API server that answers with the scripted
// response bodies.
func responsesServer(responses ...string) *llmtest.Server {
	return &llmtest.Server{Path: "/responses", Headers: map[string]string{"Authorization": "Bearer test-key"}, Responses: responses}
}

func newComputerSession(t *testing.T, providerType llm.ProviderType, server *llmtest.Server) (computeruse.Session, error) {
	t.Helper()
	provider, err := llm.NewProvider(providerType, llm.Config{APIKey: "test-key", BaseURL: server.Start(t)})
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

	server := responsesServer(
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
	)
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

	first := server.Requests()[0]
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

	second := server.Requests()[1]
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

	server := responsesServer(
		`{"id":"resp_1","status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"I can't help with that."}]}]}`,
	)
	session, err := newComputerSession(t, llm.ProviderOpenAI, server)
	require.NoError(t, err)

	_, err = session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.ErrorContains(t, err, "I can't help with that.")
}

// OpenCode shares the OpenAI provider but has no Responses API computer tool.
func TestComputerSessionNotNativeForOpenCode(t *testing.T) {
	t.Parallel()

	_, err := newComputerSession(t, llm.ProviderOpenCode, responsesServer())
	require.ErrorContains(t, err, "no native computer use")
}

// A safety check without a message still requires confirmation, and only
// the call that raised it acknowledges it.
func TestComputerSessionSafetyChecks(t *testing.T) {
	t.Parallel()

	server := responsesServer(
		`{"id":"resp_1","status":"completed","output":[
			{"type":"computer_call","call_id":"call_1","actions":[{"type":"move","x":1,"y":2,"keys":["SHIFT"]}],"pending_safety_checks":[{"id":"sc_1","code":"malicious_instructions","message":null}]},
			{"type":"computer_call","call_id":"call_2","actions":[{"type":"screenshot"}]}
		]}`,
		`{"id":"resp_2","status":"completed","output":[{"type":"function_call","call_id":"f1","name":"done","arguments":"{\"success\":true,\"summary\":\"ok\"}"}]}`,
	)
	session, err := newComputerSession(t, llm.ProviderOpenAI, server)
	require.NoError(t, err)

	turn, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.NoError(t, err)
	assert.Equal(t, "malicious_instructions", turn.Confirmation)
	assert.Equal(t, []string{"SHIFT"}, turn.Actions[0].Modifiers, "keys held while moving")

	_, err = session.Next(context.Background(), computeruse.Observation{Screen: testScreen("second"), Acknowledged: true})
	require.NoError(t, err)
	input := server.Requests()[1]["input"].([]any)
	assert.Equal(t, []any{map[string]any{"id": "sc_1", "code": "malicious_instructions"}}, input[0].(map[string]any)["acknowledged_safety_checks"])
	assert.NotContains(t, input[1], "acknowledged_safety_checks")
}

func TestComputerSessionIncomplete(t *testing.T) {
	t.Parallel()

	for body, want := range map[string]string{
		`{"id":"r","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`: "incomplete (max_output_tokens)",
		`{"id":"r","status":"failed","error":null,"output":[]}`:                                            "the response failed",
	} {
		session, err := newComputerSession(t, llm.ProviderOpenAI, responsesServer(body))
		require.NoError(t, err)
		_, err = session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
		require.ErrorContains(t, err, want)
	}
}

// A computer call after one that cannot be performed is answered and
// reported as not performed.
func TestComputerSessionHaltsBatch(t *testing.T) {
	t.Parallel()

	server := responsesServer(
		`{"id":"resp_1","status":"completed","output":[
			{"type":"computer_call","call_id":"call_1","actions":[{"type":"click","button":"back","x":1,"y":2}]},
			{"type":"computer_call","call_id":"call_2","actions":[{"type":"type","text":"x"}]}
		]}`,
		`{"id":"resp_2","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`,
	)
	session, err := newComputerSession(t, llm.ProviderOpenAI, server)
	require.NoError(t, err)

	turn, err := session.Next(context.Background(), computeruse.Observation{Screen: testScreen("first")})
	require.NoError(t, err)
	assert.Empty(t, turn.Actions)

	_, err = session.Next(context.Background(), computeruse.Observation{Screen: testScreen("second")})
	require.NoError(t, err)
	input := server.Requests()[1]["input"].([]any)
	require.Len(t, input, 3, "both calls are answered, then a note")
	note := input[2].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]
	assert.Contains(t, note, `The computer call could not be performed: unsupported mouse button "back"`)
	assert.Contains(t, note, "The computer call was not performed: an earlier action failed.")
}

// Models without the computer tool use the generic session in auto mode.
func TestComputerSessionModelSupport(t *testing.T) {
	t.Parallel()

	for model, native := range map[string]bool{
		"gpt-5.4":                   true,
		"gpt-5.4-mini":              true,
		"gpt-5.6-sol":               true,
		"gpt-6-astra":               true,
		"ft:gpt-5.6-sol:acme::abc1": true,
		"my-azure-deployment":       true,
		"gpt-5.4-nano":              false,
		"gpt-5":                     false,
		"gpt-4.1":                   false,
		"o3":                        false,
		"o4-mini":                   false,
		"computer-use-preview":      false,
	} {
		provider, err := llm.NewProvider(llm.ProviderOpenAI, llm.Config{APIKey: "test-key"})
		require.NoError(t, err)
		_, err = computeruse.New(llm.ProviderOpenAI, provider, computeruse.ModeNative, computeruse.Options{Model: model})
		if native {
			assert.NoError(t, err, model)
		} else {
			assert.ErrorIs(t, err, computeruse.ErrModelNotSupported, model)
		}
	}
}
