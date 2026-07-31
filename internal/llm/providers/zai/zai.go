// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package zai provides an LLM provider implementation for Z.AI's API.
// Z.AI offers GLM-series models via an OpenAI-compatible API with
// custom thinking support.
package zai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/llm"
)

const (
	providerName        = "zai"
	defaultChatEndpoint = "/chat/completions"
	streamPrefix        = "data: "
	streamDoneMarker    = "[DONE]"
)

func init() {
	llm.RegisterProvider(llm.ProviderZAI, New)
}

// Provider implements the llm.Provider interface for Z.AI.
var _ llm.Provider = (*Provider)(nil)

type Provider struct {
	config     llm.Config
	httpClient *llm.HTTPClient
	headers    map[string]string
}

// New creates a new Z.AI provider.
func New(cfg llm.Config) (llm.Provider, error) {
	if cfg.APIKey == "" {
		return nil, llm.ErrNoAPIKey
	}

	return &Provider{
		config:     cfg,
		httpClient: llm.NewHTTPClient(cfg),
		headers: map[string]string{
			"Authorization": "Bearer " + cfg.APIKey,
		},
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

	var resp chatCompletionResponse
	if err := json.NewDecoder(respBody).Decode(&resp); err != nil {
		return nil, llm.WrapError(providerName, fmt.Errorf("failed to decode response: %w", err))
	}

	if len(resp.Choices) == 0 {
		return nil, llm.WrapError(providerName, fmt.Errorf("no choices in response"))
	}

	choice := resp.Choices[0]
	// Prepend reasoning content to the response when present so thinking
	// output is not silently lost (ChatResponse has no separate field for it).
	content := choice.Message.Content
	if choice.Message.ReasoningContent != "" {
		content = choice.Message.ReasoningContent + content
	}
	result := &llm.ChatResponse{
		Content:      content,
		FinishReason: choice.FinishReason,
		Usage: llm.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}

	// Extract tool calls if present
	if len(choice.Message.ToolCalls) > 0 {
		result.ToolCalls = make([]llm.ToolCall, len(choice.Message.ToolCalls))
		for i, tc := range choice.Message.ToolCalls {
			result.ToolCalls[i] = llm.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: llm.ToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}
		}
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
	messages := make([]message, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = message{
			Role:    string(m.Role),
			Content: m.Content,
		}
		if m.Name != "" {
			messages[i].Name = m.Name
		}
		if m.ToolCallID != "" {
			messages[i].ToolCallID = m.ToolCallID
		}
		// Convert tool calls if present (for assistant messages with tool calls)
		if len(m.ToolCalls) > 0 {
			messages[i].ToolCalls = make([]toolCall, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				messages[i].ToolCalls[j] = toolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: toolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}
	}

	chatReq := chatCompletionRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   stream,
	}

	if req.Temperature != nil {
		chatReq.Temperature = req.Temperature
	}
	if req.MaxTokens != nil {
		chatReq.MaxTokens = req.MaxTokens
	}
	if req.TopP != nil {
		chatReq.TopP = req.TopP
	}
	if len(req.Stop) > 0 {
		chatReq.Stop = req.Stop
	}

	// Add tools if provided
	if len(req.Tools) > 0 {
		chatReq.Tools = make([]tool, len(req.Tools))
		for i, t := range req.Tools {
			chatReq.Tools[i] = tool{
				Type: t.Type,
				Function: toolFunction{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					Parameters:  t.Function.Parameters,
				},
			}
		}
	}

	// Add tool choice if specified
	if req.ToolChoice != "" {
		chatReq.ToolChoice = req.ToolChoice
	}

	// Add thinking configuration if enabled.
	// Z.AI uses a binary toggle {"thinking": {"type": "enabled"}} instead of
	// OpenAI's reasoning effort levels. Effort and BudgetTokens are not supported
	// by the Z.AI API and are intentionally ignored here.
	if req.Thinking != nil && req.Thinking.Enabled {
		chatReq.Thinking = &thinkingRequest{Type: "enabled"}
	}

	return json.Marshal(chatReq)
}

func (p *Provider) doRequest(ctx context.Context, body []byte) (io.ReadCloser, error) {
	return p.httpClient.Do(ctx, p.config.BaseURL+defaultChatEndpoint, body, p.headers)
}

func (p *Provider) streamResponse(ctx context.Context, body io.ReadCloser, events chan<- llm.StreamEvent) {
	defer close(events)
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var usage *llm.Usage
	var toolCalls []llm.ToolCall
	var finishReason string

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

		if !strings.HasPrefix(line, streamPrefix) {
			continue
		}

		data := strings.TrimPrefix(line, streamPrefix)
		if data == streamDoneMarker {
			events <- llm.StreamEvent{Done: true, Usage: usage, ToolCalls: toolCalls, FinishReason: finishReason}
			return
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		// Capture usage if present
		if chunk.Usage != nil {
			usage = &llm.Usage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			}
		}

		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			delta := choice.Delta

			// Capture finish reason
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}

			// Accumulate streamed tool calls
			for _, tc := range delta.ToolCalls {
				toolCalls = append(toolCalls, llm.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: llm.ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}

			// Z.AI sends thinking output in reasoning_content and main output in content.
			// Emit whichever is non-empty; avoid allocating a concatenation on every chunk.
			switch {
			case delta.ReasoningContent != "" && delta.Content != "":
				events <- llm.StreamEvent{Delta: delta.ReasoningContent + delta.Content}
			case delta.ReasoningContent != "":
				events <- llm.StreamEvent{Delta: delta.ReasoningContent}
			case delta.Content != "":
				events <- llm.StreamEvent{Delta: delta.Content}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		events <- llm.StreamEvent{Error: llm.WrapError(providerName, err), Done: true}
		return
	}

	// If we get here without [DONE], still signal completion
	events <- llm.StreamEvent{Done: true, Usage: usage, ToolCalls: toolCalls, FinishReason: finishReason}
}

// API request/response types (OpenAI-compatible with Z.AI thinking extension)

type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
}

// Tool calling types
type tool struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type toolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function toolCallFunction `json:"function"`
}

type toolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// thinkingRequest represents Z.AI's thinking configuration.
// Unlike OpenAI's reasoning field, Z.AI uses {"type": "enabled"/"disabled"}.
type thinkingRequest struct {
	Type string `json:"type"`
}

type chatCompletionRequest struct {
	Model       string           `json:"model"`
	Messages    []message        `json:"messages"`
	Temperature *float64         `json:"temperature,omitempty"`
	MaxTokens   *int             `json:"max_tokens,omitempty"`
	TopP        *float64         `json:"top_p,omitempty"`
	Stop        []string         `json:"stop,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
	Thinking    *thinkingRequest `json:"thinking,omitempty"`
	Tools       []tool           `json:"tools,omitempty"`
	ToolChoice  any              `json:"tool_choice,omitempty"`
}

type chatCompletionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int             `json:"index"`
		Message      responseMessage `json:"message"`
		FinishReason string          `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type responseMessage struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

type streamChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string     `json:"role,omitempty"`
			Content          string     `json:"content,omitempty"`
			ReasoningContent string     `json:"reasoning_content,omitempty"`
			ToolCalls        []toolCall `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}
