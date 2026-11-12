// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingSink struct {
	mu      sync.Mutex
	records []UsageRecord
}

func (s *recordingSink) RecordLLMUsage(record UsageRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
}

func (s *recordingSink) all() []UsageRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]UsageRecord(nil), s.records...)
}

type stubProvider struct {
	resp   *ChatResponse
	err    error
	stream chan StreamEvent
}

func (p *stubProvider) Chat(context.Context, *ChatRequest) (*ChatResponse, error) {
	return p.resp, p.err
}

func (p *stubProvider) ChatStream(context.Context, *ChatRequest) (<-chan StreamEvent, error) {
	return p.stream, p.err
}

func (p *stubProvider) Name() string { return "stub" }

// An answered call reports its usage to the context's sink under the model
// the request named; a failed call reports nothing.
func TestChatRecordsUsage(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	ctx := WithUsageSink(context.Background(), sink)
	usage := Usage{PromptTokens: 12, CompletionTokens: 3, TotalTokens: 15}
	answered := normalizedProvider{Provider: &stubProvider{resp: &ChatResponse{Content: "ok", Usage: usage}}}
	_, err := answered.Chat(ctx, &ChatRequest{Model: "m1", Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	require.NoError(t, err)

	failed := normalizedProvider{Provider: &stubProvider{err: errors.New("down")}}
	_, err = failed.Chat(ctx, &ChatRequest{Model: "m2", Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	require.Error(t, err)

	assert.Equal(t, []UsageRecord{{Provider: "stub", Model: "m1", Usage: usage}}, sink.all())
}

// A stream reports its usage once, from its final event, and passes every
// event through; without a sink the provider's stream is returned as it is.
func TestChatStreamRecordsUsage(t *testing.T) {
	t.Parallel()

	usage := Usage{PromptTokens: 8, CompletionTokens: 2, TotalTokens: 10}
	newStream := func() chan StreamEvent {
		stream := make(chan StreamEvent, 2)
		stream <- StreamEvent{Delta: "hi"}
		stream <- StreamEvent{Done: true, Usage: &usage}
		close(stream)
		return stream
	}
	req := &ChatRequest{Model: "m1", Messages: []Message{{Role: RoleUser, Content: "hi"}}}

	sink := &recordingSink{}
	provider := normalizedProvider{Provider: &stubProvider{stream: newStream()}}
	events, err := provider.ChatStream(WithUsageSink(context.Background(), sink), req)
	require.NoError(t, err)
	var got []StreamEvent
	for event := range events {
		got = append(got, event)
	}
	assert.Len(t, got, 2)
	assert.Equal(t, []UsageRecord{{Provider: "stub", Model: "m1", Usage: usage}}, sink.all())

	stream := newStream()
	plain := normalizedProvider{Provider: &stubProvider{stream: stream}}
	events, err = plain.ChatStream(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, (<-chan StreamEvent)(stream), events, "no sink, no forwarding")
}

// A nil sink hides the one an outer context carries.
func TestWithUsageSinkNilHides(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	ctx := WithUsageSink(WithUsageSink(context.Background(), sink), nil)
	RecordUsage(ctx, UsageRecord{Model: "m"})
	assert.Empty(t, sink.all())
}
