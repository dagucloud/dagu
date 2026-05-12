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

func hasToolNamed(tools []Tool, name string) bool {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

type normalizedProvider struct {
	Provider
}

func (p normalizedProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return p.Provider.Chat(ctx, NormalizeChatRequest(req))
}

func (p normalizedProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error) {
	return p.Provider.ChatStream(ctx, NormalizeChatRequest(req))
}
