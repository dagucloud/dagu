// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import "context"

// WebSearchToolName is the canonical explicit tool name for web search.
const WebSearchToolName = "web_search"

// NormalizeChatRequest applies provider-independent request invariants before a
// request reaches a concrete provider.
func NormalizeChatRequest(req *ChatRequest) *ChatRequest {
	if req == nil {
		return nil
	}

	normalized := *req
	normalized.Tools = dedupeTools(req.Tools)
	normalized.WebSearch = cloneWebSearchRequest(req.WebSearch)

	if normalized.WebSearch != nil && normalized.WebSearch.Enabled && hasToolNamed(normalized.Tools, WebSearchToolName) {
		normalized.WebSearch.Enabled = false
	}

	return &normalized
}

// cloneWebSearchRequest returns an independent copy of provider-native web
// search settings so normalization never mutates caller-owned request state.
func cloneWebSearchRequest(req *WebSearchRequest) *WebSearchRequest {
	if req == nil {
		return nil
	}

	out := *req
	out.AllowedDomains = append([]string(nil), req.AllowedDomains...)
	out.BlockedDomains = append([]string(nil), req.BlockedDomains...)
	if req.UserLocation != nil {
		location := *req.UserLocation
		out.UserLocation = &location
	}
	return &out
}

// dedupeTools preserves the first definition for each tool name and drops later
// duplicates so providers never receive invalid same-name tool lists.
func dedupeTools(tools []Tool) []Tool {
	if len(tools) == 0 {
		return nil
	}

	out := make([]Tool, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		name := tool.Function.Name
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, tool)
	}
	return out
}

// hasToolNamed reports whether tools contains a definition for name.
func hasToolNamed(tools []Tool, name string) bool {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

// normalizedProvider enforces shared request normalization around every
// concrete provider implementation returned by the factory.
type normalizedProvider struct {
	Provider
}

// Unwrap returns the concrete provider behind a provider NewProvider
// returned, so a provider package can reach its own implementation.
func Unwrap(provider Provider) Provider {
	if normalized, ok := provider.(normalizedProvider); ok {
		return normalized.Provider
	}
	return provider
}

// Chat normalizes each request before delegating to the concrete provider,
// and reports the usage of an answered call to the context's usage sink.
func (p normalizedProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	req = NormalizeChatRequest(req)
	resp, err := p.Provider.Chat(ctx, req)
	if err == nil && resp != nil {
		RecordUsage(ctx, UsageRecord{Provider: p.Name(), Model: req.Model, Usage: resp.Usage})
	}
	return resp, err
}

// ChatStream normalizes each streaming request before delegating to the
// concrete provider. When the context carries a usage sink, the stream's
// final event reports the call's usage to it; otherwise the provider's
// stream is returned as it is.
func (p normalizedProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error) {
	req = NormalizeChatRequest(req)
	events, err := p.Provider.ChatStream(ctx, req)
	sink := usageSink(ctx)
	if err != nil || sink == nil {
		return events, err
	}
	out := make(chan StreamEvent)
	go func() {
		defer close(out)
		for event := range events {
			if event.Done && event.Error == nil {
				var usage Usage
				if event.Usage != nil {
					usage = *event.Usage
				}
				sink.RecordLLMUsage(UsageRecord{Provider: p.Name(), Model: req.Model, Usage: usage})
			}
			select {
			case out <- event:
			case <-ctx.Done():
				// The reader is gone; drain so the provider can finish.
				for range events {
				}
				return
			}
		}
	}()
	return out, nil
}
