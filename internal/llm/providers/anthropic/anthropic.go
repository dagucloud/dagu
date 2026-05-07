// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package anthropic provides an LLM provider implementation for Anthropic's Claude API.
package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/dagucloud/dagu/internal/llm"
)

const (
	providerName        = "anthropic"
	defaultMessagesPath = "/v1/messages"
	anthropicAPIVersion = "2023-06-01"
	streamPrefix        = "data: "

	// Thinking budget token limits for different effort levels.
	// Note: Anthropic recommends budgets <= 32K to avoid timeout issues.
	thinkingBudgetLow    = 1024
	thinkingBudgetMedium = 4096
	thinkingBudgetHigh   = 16384
	thinkingBudgetXHigh  = 32768
)

func init() {
	llm.RegisterProvider(llm.ProviderAnthropic, New)
}

// Provider implements the llm.Provider interface for Anthropic Claude.
var _ llm.Provider = (*Provider)(nil)

type Provider struct {
	config     llm.Config
	httpClient *llm.HTTPClient
}

// New creates a new Anthropic provider.
func New(cfg llm.Config) (llm.Provider, error) {
	if cfg.APIKey == "" {
		return nil, llm.ErrNoAPIKey
	}

	return &Provider{
		config:     cfg,
		httpClient: llm.NewHTTPClient(cfg),
	}, nil
}

// Name returns the provider name.
func (p *Provider) Name() string {
	return providerName
}

// Chat sends messages and returns the complete response.
func (p *Provider) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	body, err := p.buildRequestBody(req, false)
	if err != nil {
		return nil, err
	}

	respBody, err := p.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = respBody.Close() }()

	var resp messagesResponse
	if err := json.NewDecoder(respBody).Decode(&resp); err != nil {
		return nil, llm.WrapError(providerName, fmt.Errorf("failed to decode response: %w", err))
	}

	// Extract content and tool calls from response
	var content strings.Builder
	var toolCalls []llm.ToolCall

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "tool_use":
			// Convert input map to JSON string for Arguments
			argsJSON, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, llm.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			})
		case "server_tool_use", "web_search_tool_result":
			// Provider-internal blocks from web search; skip silently.
		}
	}

	result := &llm.ChatResponse{
		Content:      content.String(),
		FinishReason: resp.StopReason,
		Usage: llm.Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
		ToolCalls: toolCalls,
	}

	return result, nil
}

// ChatStream sends messages and streams the response.
func (p *Provider) ChatStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	body, err := p.buildRequestBody(req, true)
	if err != nil {
		return nil, err
	}

	respBody, err := p.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}

	events := make(chan llm.StreamEvent)
	go p.streamResponse(ctx, respBody, events)

	return events, nil
}

func (p *Provider) buildRequestBody(req *llm.ChatRequest, stream bool) ([]byte, error) {
	// Anthropic separates system message from other messages
	systemContent, messages := p.processMessages(req.Messages)
	// Anthropic requires at least one user message
	if len(messages) == 0 {
		return nil, llm.WrapError(providerName, fmt.Errorf("at least one user message is required"))
	}

	chatReq := messagesRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   stream,
	}

	// Set system message if present
	if systemContent != "" {
		chatReq.System = systemContent
	}

	// Add tools if provided
	if len(req.Tools) > 0 {
		chatReq.Tools = p.convertTools(req.Tools)
	}

	// Append provider-native web search tool if enabled.
	if req.WebSearch != nil && req.WebSearch.Enabled {
		wsEntry := map[string]any{
			"type": "web_search_20260209",
			"name": "web_search",
		}
		// Haiku 4.5 only supports allowed_callers=["direct"] for web_search.
		// Other models accept any value, so "direct" is safe as a universal default.
		if strings.Contains(req.Model, "haiku") {
			wsEntry["allowed_callers"] = []string{"direct"}
		}
		if req.WebSearch.MaxUses != nil {
			wsEntry["max_uses"] = *req.WebSearch.MaxUses
		}
		if len(req.WebSearch.AllowedDomains) > 0 {
			wsEntry["allowed_domains"] = req.WebSearch.AllowedDomains
		}
		if len(req.WebSearch.BlockedDomains) > 0 {
			wsEntry["blocked_domains"] = req.WebSearch.BlockedDomains
		}
		if req.WebSearch.UserLocation != nil {
			wsEntry["user_location"] = map[string]any{
				"type":     "approximate",
				"city":     req.WebSearch.UserLocation.City,
				"region":   req.WebSearch.UserLocation.Region,
				"country":  req.WebSearch.UserLocation.Country,
				"timezone": req.WebSearch.UserLocation.Timezone,
			}
		}
		chatReq.Tools = append(chatReq.Tools, wsEntry)
	}

	// Add tool choice if specified
	if req.ToolChoice != "" {
		switch req.ToolChoice {
		case "auto":
			chatReq.ToolChoice = map[string]string{"type": "auto"}
		case "required":
			chatReq.ToolChoice = map[string]string{"type": "any"}
		case "none":
			// Don't include tools
			chatReq.Tools = nil
		default:
			// Specific tool name
			chatReq.ToolChoice = map[string]string{"type": "tool", "name": req.ToolChoice}
		}
	}

	// Add thinking configuration if enabled
	// Must be done before setting max_tokens since max_tokens must be > budget_tokens
	var thinkingBudget int
	if req.Thinking != nil && req.Thinking.Enabled {
		thinkingBudget = p.getThinkingBudget(req.Thinking)
		chatReq.Thinking = &thinkingRequest{
			Type:        "enabled",
			BudgetToken: thinkingBudget,
		}
	}

	// Set max_tokens (required by Anthropic)
	// When thinking is enabled, max_tokens must be > budget_tokens
	if req.MaxTokens != nil {
		chatReq.MaxTokens = *req.MaxTokens
	} else {
		// Default to 4096 if not specified
		chatReq.MaxTokens = 4096
	}

	// Ensure max_tokens > budget_tokens when thinking is enabled
	if thinkingBudget > 0 && chatReq.MaxTokens <= thinkingBudget {
		// Set max_tokens to budget + reasonable buffer for response
		// Anthropic recommends having room for the actual response after thinking
		chatReq.MaxTokens = thinkingBudget + 4096
	}

	if req.Temperature != nil {
		chatReq.Temperature = req.Temperature
	}
	if req.TopP != nil {
		chatReq.TopP = req.TopP
	}
	if len(req.Stop) > 0 {
		chatReq.StopSequences = req.Stop
	}

	return json.Marshal(chatReq)
}

func (p *Provider) processMessages(reqMessages []llm.Message) (string, []message) {
	var systemContent string
	messages := make([]message, 0, len(reqMessages))

	for _, m := range reqMessages {
		switch m.Role {
		case llm.RoleSystem:
			// Concatenate system messages
			if systemContent != "" {
				systemContent += "\n\n"
			}
			systemContent += m.Content
		case llm.RoleUser:
			messages = append(messages, message{
				Role:    string(m.Role),
				Content: m.Content,
			})
		case llm.RoleAssistant:
			// Check if this assistant message has tool calls
			if len(m.ToolCalls) > 0 {
				// Convert to content blocks with tool_use
				contentBlocks := make([]any, 0, len(m.ToolCalls)+1)
				if m.Content != "" {
					contentBlocks = append(contentBlocks, map[string]any{
						"type": "text",
						"text": m.Content,
					})
				}
				for _, tc := range m.ToolCalls {
					// Parse arguments from JSON string
					var input map[string]any
					if tc.Function.Arguments != "" {
						if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
							input = map[string]any{}
						}
					}
					contentBlocks = append(contentBlocks, map[string]any{
						"type":  "tool_use",
						"id":    tc.ID,
						"name":  tc.Function.Name,
						"input": input,
					})
				}
				messages = append(messages, message{
					Role:    "assistant",
					Content: contentBlocks,
				})
			} else {
				messages = append(messages, message{
					Role:    string(m.Role),
					Content: m.Content,
				})
			}
		case llm.RoleTool:
			// Tool results in Anthropic are sent as user messages with tool_result content blocks
			contentBlocks := []any{
				map[string]any{
					"type":        "tool_result",
					"tool_use_id": m.ToolCallID,
					"content":     m.Content,
				},
			}
			messages = append(messages, message{
				Role:    "user",
				Content: contentBlocks,
			})
		}
	}
	return systemContent, messages
}

func (p *Provider) convertTools(tools []llm.Tool) []any {
	if len(tools) == 0 {
		return nil
	}

	result := make([]any, len(tools))
	for i, t := range tools {
		result[i] = tool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: anthropicInputSchema(t.Function.Parameters),
		}
	}
	return result
}

func anthropicInputSchema(parameters map[string]any) map[string]any {
	if len(parameters) == 0 {
		return map[string]any{"type": "object"}
	}
	return parameters
}

// getThinkingBudget determines the token budget for thinking mode.
// Uses explicit BudgetTokens if provided, otherwise maps effort level to tokens.
func (p *Provider) getThinkingBudget(thinking *llm.ThinkingRequest) int {
	// Use explicit budget if provided
	if thinking.BudgetTokens != nil && *thinking.BudgetTokens > 0 {
		return *thinking.BudgetTokens
	}

	// Map effort level to budget tokens
	switch thinking.Effort {
	case llm.ThinkingEffortLow:
		return thinkingBudgetLow
	case llm.ThinkingEffortMedium:
		return thinkingBudgetMedium
	case llm.ThinkingEffortHigh:
		return thinkingBudgetHigh
	case llm.ThinkingEffortXHigh:
		return thinkingBudgetXHigh
	default:
		return thinkingBudgetMedium
	}
}

func (p *Provider) doRequest(ctx context.Context, body []byte) (io.ReadCloser, error) {
	return p.httpClient.Do(ctx, p.config.BaseURL+defaultMessagesPath, body, p.authHeaders())
}

func (p *Provider) authHeaders() map[string]string {
	return map[string]string{
		"x-api-key":         p.config.APIKey,
		"anthropic-version": anthropicAPIVersion,
	}
}

func (p *Provider) streamResponse(ctx context.Context, body io.ReadCloser, events chan<- llm.StreamEvent) {
	defer close(events)
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	var usage *llm.Usage

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			events <- llm.StreamEvent{Error: ctx.Err(), Done: true}
			return
		default:
		}

		line := scanner.Text()
		if line == "" {
			continue
		}

		// Anthropic uses event/data format
		if !strings.HasPrefix(line, streamPrefix) {
			continue
		}

		data := strings.TrimPrefix(line, streamPrefix)

		var event streamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "content_block_delta":
			if event.Delta != nil && event.Delta.Type == "text_delta" {
				events <- llm.StreamEvent{Delta: event.Delta.Text}
			}

		case "message_delta":
			// Message completed, may contain usage
			if event.Usage != nil {
				usage = &llm.Usage{
					PromptTokens:     event.Usage.InputTokens,
					CompletionTokens: event.Usage.OutputTokens,
					TotalTokens:      event.Usage.InputTokens + event.Usage.OutputTokens,
				}
			}

		case "message_start":
			// May contain initial usage (input tokens)
			if event.Message != nil && event.Message.Usage != nil {
				usage = &llm.Usage{
					PromptTokens: event.Message.Usage.InputTokens,
				}
			}

		case "message_stop":
			events <- llm.StreamEvent{Done: true, Usage: usage}
			return

		case "error":
			errMsg := "unknown streaming error"
			if event.Error != nil {
				errMsg = event.Error.Message
			}
			events <- llm.StreamEvent{
				Error: llm.WrapError(providerName, errors.New(errMsg)),
				Done:  true,
			}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		events <- llm.StreamEvent{Error: llm.WrapError(providerName, err), Done: true}
		return
	}

	// If we get here without message_stop, still signal completion
	events <- llm.StreamEvent{Done: true, Usage: usage}
}

// API request/response types

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []contentBlock
}

// Tool calling types
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type messagesRequest struct {
	Model         string           `json:"model"`
	Messages      []message        `json:"messages"`
	System        string           `json:"system,omitempty"`
	MaxTokens     int              `json:"max_tokens"`
	Temperature   *float64         `json:"temperature,omitempty"`
	TopP          *float64         `json:"top_p,omitempty"`
	StopSequences []string         `json:"stop_sequences,omitempty"`
	Stream        bool             `json:"stream,omitempty"`
	Thinking      *thinkingRequest `json:"thinking,omitempty"`
	Tools         []any            `json:"tools,omitempty"`
	ToolChoice    any              `json:"tool_choice,omitempty"` // "auto", "any", or {"type": "tool", "name": "..."}
}

// thinkingRequest represents Anthropic's extended thinking configuration.
type thinkingRequest struct {
	Type        string `json:"type"`
	BudgetToken int    `json:"budget_tokens"`
}

type contentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text,omitempty"`
	ID    string         `json:"id,omitempty"`    // For tool_use blocks
	Name  string         `json:"name,omitempty"`  // For tool_use blocks
	Input map[string]any `json:"input,omitempty"` // For tool_use blocks
}

type messagesResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []contentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason"`
	StopSequence string         `json:"stop_sequence,omitempty"`
	Usage        struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index,omitempty"`
	Message *struct {
		ID    string `json:"id"`
		Type  string `json:"type"`
		Role  string `json:"role"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage,omitempty"`
	} `json:"message,omitempty"`
	Delta *struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"delta,omitempty"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}
