// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computeruse_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedProvider answers each chat request with the next scripted
// response and records the requests.
type scriptedProvider struct {
	responses []*llm.ChatResponse
	requests  []*llm.ChatRequest
}

func (p *scriptedProvider) Chat(_ context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	clone := *req
	clone.Messages = append([]llm.Message(nil), req.Messages...)
	p.requests = append(p.requests, &clone)
	resp := p.responses[0]
	p.responses = p.responses[1:]
	return resp, nil
}

func (p *scriptedProvider) ChatStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	panic("not used")
}

func (p *scriptedProvider) Name() string { return "scripted" }

func toolCall(id, name, arguments string) llm.ToolCall {
	return llm.ToolCall{ID: id, Type: "function", Function: llm.ToolCallFunction{Name: name, Arguments: arguments}}
}

func screen(label string) computeruse.Screen {
	return computeruse.Screen{Image: llm.Image{MediaType: "image/png", Data: []byte(label)}, Width: 800, Height: 600}
}

func newGeneric(t *testing.T, provider llm.Provider) computeruse.Session {
	t.Helper()
	session, err := computeruse.New(llm.ProviderLocal, provider, computeruse.ModeAuto, computeruse.Options{
		Model:  "test-model",
		Task:   "Open the settings",
		System: "The computer runs macOS.",
	})
	require.NoError(t, err)
	return session
}

func TestGenericSessionRunsTask(t *testing.T) {
	t.Parallel()

	provider := &scriptedProvider{responses: []*llm.ChatResponse{
		{Content: "Clicking the gear.", ToolCalls: []llm.ToolCall{
			toolCall("c1", "click", `{"x":10,"y":20,"count":2,"modifiers":["shift"]}`),
			toolCall("c2", "key", `{"keys":"ctrl+s","repeat":2}`),
		}},
		{ToolCalls: []llm.ToolCall{toolCall("c3", "done", `{"success":true,"summary":"Settings open"}`)}},
	}}
	session := newGeneric(t, provider)

	turn, err := session.Next(context.Background(), computeruse.Observation{Screen: screen("first")})
	require.NoError(t, err)
	assert.Equal(t, "Clicking the gear.", turn.Text)
	assert.Equal(t, []computeruse.Action{
		{CallID: "c1", Kind: computeruse.KindClick, Point: &computeruse.Point{X: 10, Y: 20}, Count: 2, Modifiers: []string{"shift"}},
		{CallID: "c2", Kind: computeruse.KindKey, Keys: []string{"ctrl", "s"}, Repeat: 2},
	}, turn.Actions)
	assert.Nil(t, turn.Done)

	first := provider.requests[0]
	assert.Equal(t, "required", first.ToolChoice)
	require.Len(t, first.Messages, 2)
	assert.Equal(t, llm.RoleSystem, first.Messages[0].Role)
	assert.Contains(t, first.Messages[0].Content, "The computer runs macOS.")
	assert.Contains(t, first.Messages[1].Content, "Task: Open the settings")
	assert.Equal(t, []byte("first"), first.Messages[1].Images[0].Data)

	turn, err = session.Next(context.Background(), computeruse.Observation{
		Screen: screen("second"),
		Results: []computeruse.Result{
			{CallID: "c1"},
			{CallID: "c2", Error: "key not found"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, &computeruse.Done{Success: true, Summary: "Settings open"}, turn.Done)

	messages := provider.requests[1].Messages
	require.Len(t, messages, 6)
	assert.Equal(t, llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Name: "click", Content: "OK"}, messages[3])
	assert.Equal(t, llm.Message{Role: llm.RoleTool, ToolCallID: "c2", Name: "key", Content: "Error: key not found"}, messages[4])
	assert.NotContains(t, messages[5].Content, "Task:")
	assert.Equal(t, []byte("second"), messages[5].Images[0].Data)
}

// Every tool call must be answered. A call whose arguments cannot be parsed
// stops the turn, so the calls after it are answered as skipped.
func TestGenericSessionAnswersEveryCall(t *testing.T) {
	t.Parallel()

	provider := &scriptedProvider{responses: []*llm.ChatResponse{
		{ToolCalls: []llm.ToolCall{
			toolCall("bad", "click", `{"x":1}`),
			toolCall("ok", "type", `{"text":"hi"}`),
			toolCall("lost", "wait", `{"seconds":1}`),
		}},
		{Content: "Finished."},
	}}
	session := newGeneric(t, provider)

	turn, err := session.Next(context.Background(), computeruse.Observation{Screen: screen("a")})
	require.NoError(t, err)
	assert.Empty(t, turn.Actions, "the call without y stops the turn")

	_, err = session.Next(context.Background(), computeruse.Observation{Screen: screen("b")})
	require.NoError(t, err)

	messages := provider.requests[1].Messages
	assert.Equal(t, "Error: x and y are required", messages[3].Content)
	assert.Equal(t, computeruse.SkippedText, messages[4].Content)
	assert.Equal(t, computeruse.SkippedText, messages[5].Content)
}

// A model asked for its report alone is offered no tool but done.
func TestGenericSessionOffersOnlyDoneForReport(t *testing.T) {
	t.Parallel()

	provider := &scriptedProvider{responses: []*llm.ChatResponse{
		{ToolCalls: []llm.ToolCall{toolCall("c1", "click", `{"x":1,"y":2}`)}},
		{ToolCalls: []llm.ToolCall{toolCall("d1", "done", `{"success":true,"summary":"Settings open"}`)}},
	}}
	session := newGeneric(t, provider)
	_, err := session.Next(context.Background(), computeruse.Observation{Screen: screen("a")})
	require.NoError(t, err)
	turn, err := session.Next(context.Background(), computeruse.Observation{
		Screen:     screen("b"),
		Results:    []computeruse.Result{{CallID: "c1"}},
		Note:       "Report the task now.",
		ReportOnly: true,
	})
	require.NoError(t, err)
	assert.Equal(t, &computeruse.Done{Success: true, Summary: "Settings open"}, turn.Done)

	toolNames := func(req *llm.ChatRequest) []string {
		var names []string
		for _, tool := range req.Tools {
			names = append(names, tool.Function.Name)
		}
		return names
	}
	assert.Contains(t, toolNames(provider.requests[0]), "click", "an ordinary observation offers the actions")
	assert.Equal(t, []string{computeruse.DoneToolName}, toolNames(provider.requests[1]))
	assert.Equal(t, "required", provider.requests[1].ToolChoice)
}

// Only the latest screenshots are sent; older ones are replaced with a note.
func TestGenericSessionKeepsLatestScreenshots(t *testing.T) {
	t.Parallel()

	provider := &scriptedProvider{}
	for range 5 {
		provider.responses = append(provider.responses, &llm.ChatResponse{Content: "thinking"})
	}
	session := newGeneric(t, provider)
	for _, label := range []string{"1", "2", "3", "4", "5"} {
		_, err := session.Next(context.Background(), computeruse.Observation{Screen: screen(label), Note: "Call done when finished."})
		require.NoError(t, err)
	}

	var images []string
	for _, message := range provider.requests[4].Messages {
		for _, image := range message.Images {
			images = append(images, string(image.Data))
		}
	}
	assert.Equal(t, []string{"3", "4", "5"}, images)
}

// The latest screen's elements are listed with what each holds; the lists of
// earlier screens, whose ids no longer name anything, are taken out of the
// conversation.
func TestGenericSessionListsOnlyTheLatestElements(t *testing.T) {
	t.Parallel()

	provider := &scriptedProvider{}
	for range 2 {
		provider.responses = append(provider.responses, &llm.ChatResponse{Content: "thinking"})
	}
	session := newGeneric(t, provider)
	first := []computeruse.Element{{ID: "e1", Role: "button", Name: "新規受注…"}}
	latest := []computeruse.Element{{ID: "e1", Role: "combo_box", Name: "顧客", Value: "%customer%"}, {ID: "e2", Role: "text_field", Name: "数量"}}
	for _, elements := range [][]computeruse.Element{first, latest} {
		_, err := session.Next(context.Background(), computeruse.Observation{Screen: screen("s"), Elements: elements})
		require.NoError(t, err)
	}

	var texts []string
	for _, message := range provider.requests[1].Messages {
		if message.Role == llm.RoleUser {
			texts = append(texts, message.Content)
		}
	}
	require.Len(t, texts, 2)
	assert.NotContains(t, texts[0], "新規受注", "the earlier list is gone")
	assert.Contains(t, texts[0], "element list was removed")
	assert.Contains(t, texts[1], `- e1: combo_box "顧客" value "%customer%"`)
	assert.Contains(t, texts[1], `- e2: text_field "数量"`)
	assert.NotContains(t, texts[1], `"数量" value`, "an element with no value shows none")
}

// A task that quotes the element list's header keeps all of its text when
// the next screen removes the earlier list.
func TestGenericSessionKeepsATaskThatQuotesTheListHeader(t *testing.T) {
	t.Parallel()

	provider := &scriptedProvider{}
	for range 2 {
		provider.responses = append(provider.responses, &llm.ChatResponse{Content: "thinking"})
	}
	const task = "Read the form.\nElements you can target with click_element by id: then press 確定 last"
	session, err := computeruse.New(llm.ProviderLocal, provider, computeruse.ModeAuto, computeruse.Options{Model: "test-model", Task: task})
	require.NoError(t, err)
	for range 2 {
		_, err := session.Next(context.Background(), computeruse.Observation{Screen: screen("s"), Elements: []computeruse.Element{{ID: "e1", Role: "button", Name: "確定"}}})
		require.NoError(t, err)
	}
	var first string
	for _, message := range provider.requests[1].Messages {
		if message.Role == llm.RoleUser {
			first = message.Content
			break
		}
	}
	assert.Contains(t, first, task, "the task is whole")
	assert.Contains(t, first, "element list was removed")
}

// A model the native tool does not support uses the generic session in auto
// mode and fails in native mode.
func TestNewFallsBackForUnsupportedModel(t *testing.T) {
	t.Parallel()

	const providerType llm.ProviderType = "test-unsupported"
	computeruse.RegisterNative(providerType, func(llm.Provider, computeruse.Options) (computeruse.Session, error) {
		return nil, computeruse.ErrModelNotSupported
	})

	session, err := computeruse.New(providerType, &scriptedProvider{}, computeruse.ModeAuto, computeruse.Options{})
	require.NoError(t, err)
	assert.NotNil(t, session)

	_, err = computeruse.New(providerType, &scriptedProvider{}, computeruse.ModeNative, computeruse.Options{})
	require.ErrorIs(t, err, computeruse.ErrModelNotSupported)
}

func TestNewSelectsNativeSession(t *testing.T) {
	t.Parallel()

	const providerType llm.ProviderType = "test-native"
	var native computeruse.Session = &scriptedSession{}
	computeruse.RegisterNative(providerType, func(llm.Provider, computeruse.Options) (computeruse.Session, error) {
		return native, nil
	})
	assert.True(t, computeruse.HasNative(providerType))

	session, err := computeruse.New(providerType, &scriptedProvider{}, computeruse.ModeAuto, computeruse.Options{})
	require.NoError(t, err)
	assert.Same(t, native, session)

	session, err = computeruse.New(providerType, &scriptedProvider{}, computeruse.ModeGeneric, computeruse.Options{})
	require.NoError(t, err)
	assert.NotSame(t, native, session)

	_, err = computeruse.New(llm.ProviderLocal, &scriptedProvider{}, computeruse.ModeNative, computeruse.Options{})
	require.ErrorContains(t, err, "no native computer use")
}

type scriptedSession struct{}

func (*scriptedSession) ImageLimit() computeruse.ImageLimit { return computeruse.ImageLimit{} }

func (*scriptedSession) Next(context.Context, computeruse.Observation) (*computeruse.Turn, error) {
	return &computeruse.Turn{}, nil
}

func TestSplitKeys(t *testing.T) {
	t.Parallel()

	for combo, want := range map[string][]string{
		"Return":       {"Return"},
		"ctrl+shift+t": {"ctrl", "shift", "t"},
		" cmd + c ":    {"cmd", "c"},
		"ctrl++":       {"ctrl", "+"},
		"+":            {"+"},
		"":             nil,
	} {
		assert.Equal(t, want, computeruse.SplitKeys(combo), combo)
	}
}

func TestPixelsToNotches(t *testing.T) {
	t.Parallel()

	for pixels, want := range map[int]int{0: 0, 10: 1, -10: -1, 149: 1, 150: 2, -300: -3} {
		assert.Equal(t, want, computeruse.PixelsToNotches(pixels), pixels)
	}
}
