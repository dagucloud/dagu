// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package openaicodex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/dagucloud/dagu/internal/llm"
)

const (
	providerName     = "openai-codex"
	defaultEndpoint  = "/codex/responses"
	streamPrefix     = "data:"
	streamDoneMarker = "[DONE]"
)

func init() {
	llm.RegisterProvider(llm.ProviderOpenAICodex, New)
}

var _ llm.Provider = (*Provider)(nil)

type Provider struct {
	config     llm.Config
	httpClient *http.Client
}

func New(cfg llm.Config) (llm.Provider, error) {
	if cfg.OAuthCredentialProvider == nil && strings.TrimSpace(cfg.APIKey) == "" {
		return nil, llm.ErrNoAPIKey
	}
	if cfg.OAuthCredentialProvider == nil && strings.TrimSpace(cfg.AccountID) == "" {
		return nil, fmt.Errorf("openai-codex account ID is required when using a direct access token")
	}
	return &Provider{
		config:     cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
	}, nil
}

func (p *Provider) Name() string {
	return providerName
}

func (p *Provider) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	events, err := p.ChatStream(ctx, req)
	if err != nil {
		return nil, err
	}

	var (
		content      strings.Builder
		usage        llm.Usage
		toolCalls    []llm.ToolCall
		finishReason string
	)
	for event := range events {
		if event.Delta != "" {
			content.WriteString(event.Delta)
		}
		if event.Done {
			if event.Error != nil {
				return nil, event.Error
			}
			if event.Usage != nil {
				usage = *event.Usage
			}
			toolCalls = event.ToolCalls
			finishReason = event.FinishReason
		}
	}

	return &llm.ChatResponse{
		Content:      content.String(),
		FinishReason: finishReason,
		Usage:        usage,
		ToolCalls:    toolCalls,
	}, nil
}

func (p *Provider) ChatStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	cred, err := p.resolveCredential(ctx)
	if err != nil {
		return nil, llm.WrapError(providerName, err)
	}

	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, err
	}

	respBody, err := p.doRequest(ctx, body, cred)
	if err != nil {
		return nil, err
	}

	events := make(chan llm.StreamEvent)
	go p.streamResponse(ctx, respBody, events)
	return events, nil
}

func (p *Provider) resolveCredential(ctx context.Context) (llm.OAuthCredential, error) {
	if p.config.OAuthCredentialProvider != nil {
		return p.config.OAuthCredentialProvider.ResolveCredential(ctx)
	}
	if strings.TrimSpace(p.config.APIKey) == "" || strings.TrimSpace(p.config.AccountID) == "" {
		return llm.OAuthCredential{}, fmt.Errorf("openai-codex requires an OAuth access token and account ID")
	}
	return llm.OAuthCredential{
		AccessToken: p.config.APIKey,
		AccountID:   p.config.AccountID,
	}, nil
}

func (p *Provider) buildRequestBody(req *llm.ChatRequest) ([]byte, error) {
	instructions, input := convertMessages(req.Messages)

	body := map[string]any{
		"model":  req.Model,
		"store":  false,
		"stream": true,
		"input":  input,
		"text":   map[string]any{"verbosity": "medium"},
	}
	if instructions != "" {
		body["instructions"] = instructions
	}
	if len(req.Tools) > 0 && req.ToolChoice != "none" {
		body["tools"] = convertTools(req.Tools)
		body["parallel_tool_calls"] = true
		switch req.ToolChoice {
		case "", "auto", "required":
			body["tool_choice"] = "auto"
		default:
			body["tool_choice"] = map[string]any{
				"type": "function",
				"name": req.ToolChoice,
			}
		}
	}
	if req.Temperature != nil {
		body["temperature"] = req.Temperature
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		effort := req.Thinking.Effort
		if effort == "" {
			effort = llm.ThinkingEffortMedium
		}
		body["reasoning"] = map[string]any{
			"effort":  string(effort),
			"summary": "auto",
		}
	}

	return json.Marshal(body)
}

func (p *Provider) doRequest(ctx context.Context, body []byte, cred llm.OAuthCredential) (io.ReadCloser, error) {
	baseURL := strings.TrimRight(p.config.BaseURL, "/")
	if baseURL == "" {
		baseURL = llm.DefaultBaseURL(llm.ProviderOpenAICodex)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+defaultEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, llm.WrapError(providerName, fmt.Errorf("failed to create request: %w", err))
	}
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("chatgpt-account-id", cred.AccountID)
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", "dagu")
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("content-type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, llm.WrapError(providerName, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.Body, nil
	}

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()
	message := parseErrorMessage(bodyBytes)
	if message == "" {
		message = strings.TrimSpace(string(bodyBytes))
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return nil, llm.NewAPIError(providerName, resp.StatusCode, message)
}

func (p *Provider) streamResponse(ctx context.Context, body io.ReadCloser, events chan<- llm.StreamEvent) {
	defer close(events)
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var (
		currentMessage  bool
		currentToolCall *partialToolCall
		usage           *llm.Usage
		toolCalls       []llm.ToolCall
		finishReason    = "stop"
	)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			events <- llm.StreamEvent{Error: ctx.Err(), Done: true}
			return
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, streamPrefix) {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, streamPrefix))
		if data == "" {
			continue
		}
		if data == streamDoneMarker {
			events <- llm.StreamEvent{Done: true, Usage: usage, ToolCalls: toolCalls, FinishReason: finishReason}
			return
		}

		var event responseEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			slog.Debug("failed to unmarshal Codex response", slog.String("provider", providerName), slog.Any("error", err), slog.String("data", data))
			continue
		}

		switch event.Type {
		case "error":
			events <- llm.StreamEvent{
				Error: llm.NewAPIError(providerName, 500, strings.TrimSpace(cmpOr(event.Message, event.Code))),
				Done:  true,
			}
			return
		case "response.output_item.added":
			currentMessage = false
			currentToolCall = nil
			switch event.Item.Type {
			case "message":
				currentMessage = true
			case "function_call":
				currentToolCall = &partialToolCall{
					id:      strings.TrimSpace(event.Item.CallID + "|" + event.Item.ID),
					name:    event.Item.Name,
					rawArgs: event.Item.Arguments,
				}
			}
		case "response.output_text.delta", "response.refusal.delta":
			if currentMessage && event.Delta != "" {
				events <- llm.StreamEvent{Delta: event.Delta}
			}
		case "response.function_call_arguments.delta":
			if currentToolCall != nil {
				currentToolCall.rawArgs += event.Delta
			}
		case "response.function_call_arguments.done":
			if currentToolCall != nil && event.Arguments != "" {
				currentToolCall.rawArgs = event.Arguments
			}
		case "response.output_item.done":
			if event.Item.Type == "function_call" {
				tc, err := finalizeToolCall(currentToolCall, event.Item)
				if err != nil {
					events <- llm.StreamEvent{Error: llm.WrapError(providerName, err), Done: true}
					return
				}
				toolCalls = append(toolCalls, tc)
				currentToolCall = nil
			}
		case "response.completed", "response.done":
			usage = event.toUsage()
			finishReason = event.finishReason(len(toolCalls) > 0)
			events <- llm.StreamEvent{Done: true, Usage: usage, ToolCalls: toolCalls, FinishReason: finishReason}
			return
		case "response.failed":
			events <- llm.StreamEvent{Error: llm.NewAPIError(providerName, 500, cmpOr(event.Response.Error.Message, "Codex response failed")), Done: true}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		events <- llm.StreamEvent{Error: llm.WrapError(providerName, err), Done: true}
		return
	}

	events <- llm.StreamEvent{Done: true, Usage: usage, ToolCalls: toolCalls, FinishReason: finishReason}
}

type partialToolCall struct {
	id      string
	name    string
	rawArgs string
}

func finalizeToolCall(partial *partialToolCall, item responseItem) (llm.ToolCall, error) {
	if partial == nil {
		partial = &partialToolCall{
			id:      strings.TrimSpace(item.CallID + "|" + item.ID),
			name:    item.Name,
			rawArgs: item.Arguments,
		}
	}
	if strings.TrimSpace(partial.id) == "" {
		return llm.ToolCall{}, fmt.Errorf("missing tool call ID")
	}
	if strings.TrimSpace(partial.name) == "" {
		return llm.ToolCall{}, fmt.Errorf("missing tool name")
	}
	if strings.TrimSpace(partial.rawArgs) == "" {
		partial.rawArgs = "{}"
	}

	var payload any
	if err := json.Unmarshal([]byte(partial.rawArgs), &payload); err != nil {
		return llm.ToolCall{}, fmt.Errorf("decode tool call arguments: %w", err)
	}
	normalizedArgs, err := json.Marshal(payload)
	if err != nil {
		return llm.ToolCall{}, fmt.Errorf("normalize tool call arguments: %w", err)
	}

	return llm.ToolCall{
		ID:   partial.id,
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      partial.name,
			Arguments: string(normalizedArgs),
		},
	}, nil
}

func convertMessages(messages []llm.Message) (instructions string, input []any) {
	input = make([]any, 0, len(messages))
	var systemParts []string

	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleSystem:
			if strings.TrimSpace(msg.Content) != "" {
				systemParts = append(systemParts, msg.Content)
			}
		case llm.RoleUser:
			input = append(input, map[string]any{
				"role": "user",
				"content": []map[string]any{{
					"type": "input_text",
					"text": msg.Content,
				}},
			})
		case llm.RoleAssistant:
			if strings.TrimSpace(msg.Content) != "" {
				input = append(input, map[string]any{
					"type":   "message",
					"role":   "assistant",
					"status": "completed",
					"content": []map[string]any{{
						"type":        "output_text",
						"text":        msg.Content,
						"annotations": []any{},
					}},
				})
			}
			for _, tc := range msg.ToolCalls {
				callID, itemID := splitToolCallID(tc.ID)
				toolCall := map[string]any{
					"type":      "function_call",
					"call_id":   callID,
					"name":      tc.Function.Name,
					"arguments": defaultJSON(tc.Function.Arguments),
				}
				if itemID != "" {
					toolCall["id"] = itemID
				}
				input = append(input, toolCall)
			}
		case llm.RoleTool:
			callID, _ := splitToolCallID(msg.ToolCallID)
			if strings.TrimSpace(callID) == "" {
				callID = strings.TrimSpace(msg.ToolCallID)
			}
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  cmpOr(msg.Content, "(no output)"),
			})
		}
	}

	return strings.Join(systemParts, "\n\n"), input
}

func convertTools(tools []llm.Tool) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		result = append(result, map[string]any{
			"type":        "function",
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
			"parameters":  tool.Function.Parameters,
		})
	}
	return result
}

func splitToolCallID(value string) (callID, itemID string) {
	parts := strings.SplitN(value, "|", 2)
	callID = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		itemID = strings.TrimSpace(parts[1])
	}
	return callID, itemID
}

func defaultJSON(value string) string {
	if strings.TrimSpace(value) == "" {
		return "{}"
	}
	return value
}

func parseErrorMessage(body []byte) string {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(cmpOr(payload.Error.Message, payload.Message, payload.Error.Code, payload.Error.Type))
}

type responseEvent struct {
	Type      string       `json:"type"`
	Delta     string       `json:"delta"`
	Arguments string       `json:"arguments"`
	Message   string       `json:"message"`
	Code      string       `json:"code"`
	Item      responseItem `json:"item"`
	Response  struct {
		Status string `json:"status"`
		Usage  struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			TotalTokens        int `json:"total_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}

type responseItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (e responseEvent) toUsage() *llm.Usage {
	if e.Response.Usage.TotalTokens == 0 && e.Response.Usage.InputTokens == 0 && e.Response.Usage.OutputTokens == 0 {
		return nil
	}
	promptTokens := e.Response.Usage.InputTokens - e.Response.Usage.InputTokensDetails.CachedTokens
	if promptTokens < 0 {
		promptTokens = e.Response.Usage.InputTokens
	}
	return &llm.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: e.Response.Usage.OutputTokens,
		TotalTokens:      e.Response.Usage.TotalTokens,
	}
}

func (e responseEvent) finishReason(hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_calls"
	}
	switch e.Response.Status {
	case "", "completed", "queued", "in_progress":
		return "stop"
	case "incomplete":
		return "length"
	case "cancelled", "failed":
		return "error"
	default:
		return "stop"
	}
}

func cmpOr(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
