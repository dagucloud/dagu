// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package openrouter provides an LLM provider implementation for OpenRouter's API.
// OpenRouter is a unified API that routes requests to various LLM providers.
package openrouter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/claudemodel"
	"github.com/dagucloud/dagu/v2/internal/llm/providers/chatcontent"
	"github.com/dagucloud/dagu/v2/internal/llm/providers/chatusage"
)

const (
	providerName        = "openrouter"
	defaultChatEndpoint = "/chat/completions"
	streamPrefix        = "data: "
	streamDoneMarker    = "[DONE]"
)

func init() {
	llm.RegisterProvider(llm.ProviderOpenRouter, New)
}

// Provider implements the llm.Provider interface for OpenRouter.
var _ llm.Provider = (*Provider)(nil)

type Provider struct {
	config     llm.Config
	httpClient *llm.HTTPClient
	// Optional metadata for OpenRouter
	SiteURL  string
	SiteName string
}

// New creates a new OpenRouter provider.
func New(cfg llm.Config) (llm.Provider, error) {
	if cfg.APIKey == "" {
		return nil, llm.ErrNoAPIKey
	}

	return &Provider{
		config:     cfg,
		httpClient: llm.NewHTTPClient(cfg),
		SiteURL:    "https://github.com/dagucloud/dagu",
		SiteName:   "Dagu Workflow Engine",
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

	if err := resp.failure(); err != nil {
		return nil, err
	}

	choice := resp.Choices[0]
	result := &llm.ChatResponse{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		Usage:        resp.Usage.LLM(),
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
			Content: chatcontent.Content(m.Content, m.Images),
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
	if req.TopP != nil {
		chatReq.TopP = req.TopP
	}
	if len(req.Stop) > 0 {
		chatReq.Stop = req.Stop
	}
	// allow_fallbacks keeps OpenRouter's default. The account's data policy
	// still filters every fallback endpoint, and refusing fallbacks would
	// fail the request whenever the named endpoints are unavailable.
	if len(p.config.ProviderOrder) > 0 {
		chatReq.Provider = &providerPreferences{Order: p.config.ProviderOrder}
	}

	// Add reasoning configuration if enabled
	// OpenRouter normalizes reasoning across different providers
	// Note: max_tokens must be strictly higher than reasoning budget
	var reasoningBudget int
	if req.Thinking != nil && req.Thinking.Enabled {
		reasoning := &reasoningRequest{Effort: string(reasoningEffort(req.Thinking.Effort))}
		if req.Thinking.BudgetTokens != nil {
			reasoning.MaxTokens = req.Thinking.BudgetTokens
			reasoningBudget = *req.Thinking.BudgetTokens
		}
		chatReq.Reasoning = reasoning
	}

	// Set max_tokens after reasoning config
	if req.MaxTokens != nil {
		chatReq.MaxTokens = req.MaxTokens
	}

	// Ensure max_tokens > reasoning budget when reasoning is enabled
	// OpenRouter requires max_tokens to be strictly higher than reasoning budget
	if reasoningBudget > 0 {
		currentMax := 0
		if chatReq.MaxTokens != nil {
			currentMax = *chatReq.MaxTokens
		}
		if currentMax <= reasoningBudget {
			chatReq.MaxTokens = new(reasoningBudget + 4096)
		}
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
		if claudeRejectsForcedChoice(req) {
			chatReq.ToolChoice = "auto"
		}
	}

	// Append web search plugin if enabled.
	if req.WebSearch != nil && req.WebSearch.Enabled {
		p := plugin{ID: "web"}
		if req.WebSearch.MaxUses != nil {
			p.MaxResults = req.WebSearch.MaxUses
		}
		chatReq.Plugins = []plugin{p}
		chatReq.WebSearchOptions = &webSearchOptions{
			SearchContextSize: "medium",
		}
	}

	return json.Marshal(chatReq)
}

// reasoningEffort returns the reasoning effort to send: the requested level,
// or xhigh for max, which the API lacks. An unset effort is medium.
func reasoningEffort(effort llm.ThinkingEffort) llm.ThinkingEffort {
	switch effort {
	case llm.ThinkingEffortNone, llm.ThinkingEffortMinimal, llm.ThinkingEffortLow,
		llm.ThinkingEffortMedium, llm.ThinkingEffortHigh, llm.ThinkingEffortXHigh:
		return effort
	case llm.ThinkingEffortMax:
		return llm.ThinkingEffortXHigh
	default:
		return llm.ThinkingEffortMedium
	}
}

// claudeRejectsForcedChoice reports whether a request forces tool use on a
// Claude model that refuses it: every Claude model while reasoning, and
// models from Claude 5 on.
func claudeRejectsForcedChoice(req *llm.ChatRequest) bool {
	if req.ToolChoice == "auto" || req.ToolChoice == "none" {
		return false
	}
	if _, ok := claudemodel.Parse(req.Model); !ok {
		return false
	}
	thinking := req.Thinking != nil && req.Thinking.Enabled
	return thinking || !claudemodel.ForcedToolChoiceSupported(req.Model)
}

func (p *Provider) doRequest(ctx context.Context, body []byte) (io.ReadCloser, error) {
	return p.httpClient.Do(ctx, p.config.BaseURL+defaultChatEndpoint, body, p.authHeaders())
}

func (p *Provider) authHeaders() map[string]string {
	headers := map[string]string{
		"Authorization": "Bearer " + p.config.APIKey,
	}
	// OpenRouter-specific headers
	if p.SiteURL != "" {
		headers["HTTP-Referer"] = p.SiteURL
	}
	if p.SiteName != "" {
		headers["X-Title"] = p.SiteName
	}
	return headers
}

func (p *Provider) streamResponse(ctx context.Context, body io.ReadCloser, events chan<- llm.StreamEvent) {
	defer close(events)
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var usage *llm.Usage
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
			events <- llm.StreamEvent{Done: true, Usage: usage, FinishReason: finishReason}
			return
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		// A failure after the status was sent arrives as an error chunk
		// that ends the stream.
		if chunk.Error != nil {
			events <- llm.StreamEvent{Error: chunk.Error.apiError(), Done: true}
			return
		}

		// Capture usage if present
		if chunk.Usage != nil {
			chunkUsage := chunk.Usage.LLM()
			usage = &chunkUsage
		}

		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].FinishReason != "" {
				finishReason = chunk.Choices[0].FinishReason
			}
			if chunk.Choices[0].Delta.Content != "" {
				events <- llm.StreamEvent{Delta: chunk.Choices[0].Delta.Content}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		events <- llm.StreamEvent{Error: llm.WrapError(providerName, err), Done: true, FinishReason: finishReason}
		return
	}

	// If we get here without [DONE], still signal completion
	events <- llm.StreamEvent{Done: true, Usage: usage, FinishReason: finishReason}
}

// API request/response types (OpenAI-compatible)

type message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
}

type chatCompletionRequest struct {
	Model            string            `json:"model"`
	Messages         []message         `json:"messages"`
	Temperature      *float64          `json:"temperature,omitempty"`
	MaxTokens        *int              `json:"max_tokens,omitempty"`
	TopP             *float64          `json:"top_p,omitempty"`
	Stop             []string          `json:"stop,omitempty"`
	Stream           bool              `json:"stream,omitempty"`
	Reasoning        *reasoningRequest `json:"reasoning,omitempty"`
	Tools            []tool            `json:"tools,omitempty"`
	ToolChoice       any               `json:"tool_choice,omitempty"`
	Plugins          []plugin          `json:"plugins,omitempty"`
	WebSearchOptions *webSearchOptions `json:"web_search_options,omitempty"`
	// Provider routes the request among the upstream providers serving the
	// model.
	Provider *providerPreferences `json:"provider,omitempty"`
}

// providerPreferences is OpenRouter's provider routing object.
type providerPreferences struct {
	Order []string `json:"order"`
}

// plugin represents an OpenRouter plugin configuration.
type plugin struct {
	ID         string `json:"id"`
	MaxResults *int   `json:"max_results,omitempty"`
}

// webSearchOptions configures web search behavior for OpenRouter.
type webSearchOptions struct {
	SearchContextSize string `json:"search_context_size,omitempty"`
}

// reasoningRequest represents OpenRouter's unified reasoning configuration.
type reasoningRequest struct {
	Effort    string `json:"effort,omitempty"`
	MaxTokens *int   `json:"max_tokens,omitempty"`
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
		// Error reports a failure that cut the choice short.
		Error *responseError `json:"error,omitempty"`
	} `json:"choices"`
	Usage chatusage.Usage `json:"usage"`
	// Error reports a failure in place of any choice.
	Error *responseError `json:"error,omitempty"`
}

// failure returns the error a response reports in place of an answer: an
// error object at the top level or on the first choice. A response with no
// choice at all is a transient failure.
func (r *chatCompletionResponse) failure() error {
	reported := r.Error
	if reported == nil && len(r.Choices) > 0 {
		reported = r.Choices[0].Error
	}
	if reported != nil {
		return reported.apiError()
	}
	if len(r.Choices) == 0 {
		return &llm.APIError{
			Provider:   providerName,
			StatusCode: http.StatusOK,
			Message:    "no choices in response",
			Retryable:  true,
		}
	}
	return nil
}

// responseError is the error object OpenRouter sends with a 200 status when
// the upstream provider fails after accepting the request. Code is the HTTP
// status the failure maps to.
type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *responseError) apiError() *llm.APIError {
	return llm.NewAPIError(providerName, e.Code, e.Message)
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
	Usage *chatusage.Usage `json:"usage,omitempty"`
	// Error reports a failure that ends the stream.
	Error *responseError `json:"error,omitempty"`
}
