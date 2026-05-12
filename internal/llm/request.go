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

// Chat normalizes each request before delegating to the concrete provider.
func (p normalizedProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return p.Provider.Chat(ctx, NormalizeChatRequest(req))
}

// ChatStream normalizes each streaming request before delegating to the
// concrete provider.
func (p normalizedProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error) {
	return p.Provider.ChatStream(ctx, NormalizeChatRequest(req))
}
