// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package local provides an LLM provider implementation for local OpenAI-compatible servers.
// This includes Ollama, vLLM, llama.cpp server, LocalAI, and other compatible implementations.
package local

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
	providerName        = "local"
	defaultChatEndpoint = "/chat/completions"
	streamPrefix        = "data: "
	streamDoneMarker    = "[DONE]"
)

func init() {
	llm.RegisterProvider(llm.ProviderLocal, New)
}

// Provider implements the llm.Provider interface for local OpenAI-compatible servers.
var _ llm.Provider = (*Provider)(nil)

type Provider struct {
	config     llm.Config
	httpClient *llm.HTTPClient
}

// New creates a new local provider.
// Unlike cloud providers, local providers don't require an API key.
func New(cfg llm.Config) (llm.Provider, error) {
	// Local providers don't require API key
	// BaseURL should be set; use default if not provided
	if cfg.BaseURL == "" {
		cfg.BaseURL = llm.DefaultBaseURL(llm.ProviderLocal)
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

	var resp chatCompletionResponse
	if err := json.NewDecoder(respBody).Decode(&resp); err != nil {
		return nil, llm.WrapError(providerName, fmt.Errorf("failed to decode response: %w", err))
	}

	if len(resp.Choices) == 0 {
		return nil, llm.WrapError(providerName, fmt.Errorf("no choices in response"))
	}

	choice := resp.Choices[0]
	result := &llm.ChatResponse{
		Content:      choice.Message.Content,
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

	// Note: Reasoning/thinking support for local models is highly model-dependent.
	// Most local models ignore unrecognized fields, so we omit reasoning config
	// rather than sending potentially incompatible parameters.
	// Users needing reasoning with specific models (DeepSeek-R1, etc.) should
	// configure model-specific parameters through the model's native interface.

	return json.Marshal(chatReq)
}

func (p *Provider) doRequest(ctx context.Context, body []byte) (io.ReadCloser, error) {
	return p.httpClient.Do(ctx, p.config.BaseURL+defaultChatEndpoint, body, p.authHeaders())
}

func (p *Provider) authHeaders() map[string]string {
	// Only set Authorization header if API key is provided
	if p.config.APIKey != "" {
		return map[string]string{
			"Authorization": "Bearer " + p.config.APIKey,
		}
	}
	return nil
}

func (p *Provider) streamResponse(ctx context.Context, body io.ReadCloser, events chan<- llm.StreamEvent) {
	defer close(events)
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
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

		if !strings.HasPrefix(line, streamPrefix) {
			continue
		}

		data := strings.TrimPrefix(line, streamPrefix)
		if data == streamDoneMarker {
			events <- llm.StreamEvent{Done: true, Usage: usage}
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

		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			events <- llm.StreamEvent{Delta: chunk.Choices[0].Delta.Content}
		}
	}

	if err := scanner.Err(); err != nil {
		events <- llm.StreamEvent{Error: llm.WrapError(providerName, err), Done: true}
		return
	}

	// If we get here without [DONE], still signal completion
	events <- llm.StreamEvent{Done: true, Usage: usage}
}

// API request/response types (OpenAI-compatible)

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

type chatCompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
	TopP        *float64  `json:"top_p,omitempty"`
	Stop        []string  `json:"stop,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
	Tools       []tool    `json:"tools,omitempty"`
	ToolChoice  any       `json:"tool_choice,omitempty"`
}

type responseMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
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

type streamChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string `json:"role,omitempty"`
			Content string `json:"content,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}
