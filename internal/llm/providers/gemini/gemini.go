// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package gemini provides an LLM provider implementation for Google's Gemini API.
package gemini

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
	providerName = "gemini"
	// Endpoint format: /models/{model}:generateContent
	generateContentPath = "/models/%s:generateContent"
	streamContentPath   = "/models/%s:streamGenerateContent"
	streamPrefix        = "data: "
)

func init() {
	llm.RegisterProvider(llm.ProviderGemini, New)
}

// Provider implements the llm.Provider interface for Google Gemini.
var _ llm.Provider = (*Provider)(nil)

type Provider struct {
	config     llm.Config
	httpClient *llm.HTTPClient
}

// New creates a new Gemini provider.
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
	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf(generateContentPath, req.Model)
	respBody, err := p.doRequest(ctx, endpoint, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = respBody.Close() }()

	var resp generateContentResponse
	if err := json.NewDecoder(respBody).Decode(&resp); err != nil {
		return nil, llm.WrapError(providerName, fmt.Errorf("failed to decode response: %w", err))
	}

	// Extract content and function calls from response
	var content strings.Builder
	var finishReason string
	var toolCalls []llm.ToolCall
	toolCallIdx := 0

	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		finishReason = candidate.FinishReason
		for _, p := range candidate.Content.Parts {
			if p.Text != "" {
				content.WriteString(p.Text)
			}
			if p.FunctionCall != nil {
				// Convert args map to JSON string for Arguments
				argsJSON, _ := json.Marshal(p.FunctionCall.Args)
				// Gemini doesn't provide tool call IDs, so we generate one
				toolCalls = append(toolCalls, llm.ToolCall{
					ID:   fmt.Sprintf("call_%d", toolCallIdx),
					Type: "function",
					Function: llm.ToolCallFunction{
						Name:      p.FunctionCall.Name,
						Arguments: string(argsJSON),
					},
				})
				toolCallIdx++
			}
		}
	}

	// Calculate usage
	var usage llm.Usage
	if resp.UsageMetadata != nil {
		usage = llm.Usage{
			PromptTokens:     resp.UsageMetadata.PromptTokenCount,
			CompletionTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      resp.UsageMetadata.TotalTokenCount,
		}
	}

	return &llm.ChatResponse{
		Content:      content.String(),
		FinishReason: finishReason,
		Usage:        usage,
		ToolCalls:    toolCalls,
	}, nil
}

// ChatStream sends messages and streams the response.
func (p *Provider) ChatStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf(streamContentPath, req.Model)
	respBody, err := p.doRequest(ctx, endpoint+"?alt=sse", body)
	if err != nil {
		return nil, err
	}

	events := make(chan llm.StreamEvent)
	go p.streamResponse(ctx, respBody, events)

	return events, nil
}

func (p *Provider) buildRequestBody(req *llm.ChatRequest) ([]byte, error) {
	// Separate system instruction from other messages
	sysInstr, contents := p.processMessages(req.Messages)

	geminiReq := generateContentRequest{
		Contents: contents,
	}

	if sysInstr != nil {
		geminiReq.SystemInstruction = sysInstr
	}

	// Add tools if provided
	if len(req.Tools) > 0 {
		geminiReq.Tools = p.convertTools(req.Tools)
	}

	// Append Google Search grounding tool if web search is enabled.
	if req.WebSearch != nil && req.WebSearch.Enabled {
		geminiReq.Tools = append(geminiReq.Tools, geminiTool{
			GoogleSearch: &struct{}{},
		})
	}

	// Add tool choice config if specified
	if req.ToolChoice != "" {
		switch req.ToolChoice {
		case "auto":
			geminiReq.ToolConfig = &toolConfig{
				FunctionCallingConfig: &functionCallingConfig{Mode: "AUTO"},
			}
		case "required":
			geminiReq.ToolConfig = &toolConfig{
				FunctionCallingConfig: &functionCallingConfig{Mode: "ANY"},
			}
		case "none":
			geminiReq.ToolConfig = &toolConfig{
				FunctionCallingConfig: &functionCallingConfig{Mode: "NONE"},
			}
		}
	}

	// Build generation config
	if genConfig := p.buildGenerationConfig(req); genConfig != nil {
		geminiReq.GenerationConfig = genConfig
	}

	return json.Marshal(geminiReq)
}

func (p *Provider) processMessages(reqMessages []llm.Message) (*systemInstruction, []content) {
	var sysInstr *systemInstruction
	contents := make([]content, 0, len(reqMessages))

	for _, m := range reqMessages {
		switch m.Role {
		case llm.RoleSystem:
			if sysInstr == nil {
				sysInstr = &systemInstruction{}
			}
			sysInstr.Parts = append(sysInstr.Parts, part{Text: m.Content})

		case llm.RoleUser:
			contents = append(contents, content{
				Role:  "user",
				Parts: []part{{Text: m.Content}},
			})

		case llm.RoleAssistant:
			// Check if this assistant message has tool calls
			if len(m.ToolCalls) > 0 {
				parts := make([]part, 0, len(m.ToolCalls)+1)
				if m.Content != "" {
					parts = append(parts, part{Text: m.Content})
				}
				for _, tc := range m.ToolCalls {
					// Parse arguments from JSON string
					var args map[string]any
					if tc.Function.Arguments != "" {
						if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
							args = map[string]any{}
						}
					}
					parts = append(parts, part{
						FunctionCall: &functionCallPart{
							Name: tc.Function.Name,
							Args: args,
						},
					})
				}
				contents = append(contents, content{
					Role:  "model",
					Parts: parts,
				})
			} else {
				contents = append(contents, content{
					Role:  "model", // Gemini uses "model" instead of "assistant"
					Parts: []part{{Text: m.Content}},
				})
			}

		case llm.RoleTool:
			// Gemini uses functionResponse part for tool results
			// The response should be a map or the raw content
			var response any = map[string]string{"result": m.Content}
			// Try to parse as JSON
			var jsonResponse map[string]any
			if err := json.Unmarshal([]byte(m.Content), &jsonResponse); err == nil {
				response = jsonResponse
			}
			contents = append(contents, content{
				Role: "user",
				Parts: []part{{
					FunctionResponse: &functionResponsePart{
						Name:     m.Name,
						Response: response,
					},
				}},
			})
		}
	}
	return sysInstr, contents
}

func (p *Provider) convertTools(tools []llm.Tool) []geminiTool {
	if len(tools) == 0 {
		return nil
	}
	funcDecls := make([]functionDeclaration, len(tools))
	for i, t := range tools {
		funcDecls[i] = functionDeclaration{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		}
	}
	return []geminiTool{{FunctionDeclarations: funcDecls}}
}

func (p *Provider) buildGenerationConfig(req *llm.ChatRequest) *generationConfig {
	genConfig := &generationConfig{}
	hasConfig := false

	if req.Temperature != nil {
		genConfig.Temperature = req.Temperature
		hasConfig = true
	}
	if req.TopP != nil {
		genConfig.TopP = req.TopP
		hasConfig = true
	}
	if len(req.Stop) > 0 {
		genConfig.StopSequences = req.Stop
		hasConfig = true
	}

	// Add thinking configuration if enabled
	// Note: maxOutputTokens must accommodate both thinking tokens AND response tokens
	// Known issue: MAX_TOKENS finish reason if thoughts_token_count + output_token_count > maxOutputTokens
	var thinkingBudget int
	if req.Thinking != nil && req.Thinking.Enabled {
		// Use explicit budget if provided
		if req.Thinking.BudgetTokens != nil {
			genConfig.ThinkingBudget = req.Thinking.BudgetTokens
			thinkingBudget = *req.Thinking.BudgetTokens
		} else {
			// Map effort level to thinkingLevel
			genConfig.ThinkingLevel = p.mapEffortToThinkingLevel(req.Thinking.Effort)
			// Estimate budget based on effort level for maxOutputTokens calculation
			thinkingBudget = p.estimateThinkingBudget(req.Thinking.Effort)
		}
		hasConfig = true
	}

	// Set maxOutputTokens after thinking config
	if req.MaxTokens != nil {
		genConfig.MaxOutputTokens = req.MaxTokens
		hasConfig = true
	}

	// Ensure maxOutputTokens > thinkingBudget when thinking is enabled
	// Gemini requires maxOutputTokens to accommodate both thinking AND response
	if thinkingBudget > 0 {
		currentMax := 0
		if genConfig.MaxOutputTokens != nil {
			currentMax = *genConfig.MaxOutputTokens
		}
		if currentMax <= thinkingBudget {
			genConfig.MaxOutputTokens = new(thinkingBudget + 4096)
			hasConfig = true
		}
	}

	if !hasConfig {
		return nil
	}
	return genConfig
}

// mapEffortToThinkingLevel maps unified effort levels to Gemini thinkingLevel.
func (p *Provider) mapEffortToThinkingLevel(effort llm.ThinkingEffort) string {
	switch effort {
	case llm.ThinkingEffortLow:
		return "low"
	case llm.ThinkingEffortMedium:
		return "medium"
	case llm.ThinkingEffortHigh, llm.ThinkingEffortXHigh:
		return "high"
	default:
		return "medium"
	}
}

// estimateThinkingBudget estimates token budget for effort levels when using thinkingLevel.
// These are conservative estimates to ensure maxOutputTokens is sufficient.
func (p *Provider) estimateThinkingBudget(effort llm.ThinkingEffort) int {
	switch effort {
	case llm.ThinkingEffortLow:
		return 1024
	case llm.ThinkingEffortMedium:
		return 4096
	case llm.ThinkingEffortHigh:
		return 8192
	case llm.ThinkingEffortXHigh:
		return 16384
	default:
		return 4096
	}
}

func (p *Provider) doRequest(ctx context.Context, endpoint string, body []byte) (io.ReadCloser, error) {
	return p.httpClient.Do(ctx, p.config.BaseURL+endpoint, body, p.authHeaders())
}

func (p *Provider) authHeaders() map[string]string {
	return map[string]string{
		"x-goog-api-key": p.config.APIKey,
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

		// Gemini streaming uses SSE format with "data: " prefix
		if !strings.HasPrefix(line, streamPrefix) {
			continue
		}

		data := strings.TrimPrefix(line, streamPrefix)

		var chunk generateContentResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		// Capture usage if present
		if chunk.UsageMetadata != nil {
			usage = &llm.Usage{
				PromptTokens:     chunk.UsageMetadata.PromptTokenCount,
				CompletionTokens: chunk.UsageMetadata.CandidatesTokenCount,
				TotalTokens:      chunk.UsageMetadata.TotalTokenCount,
			}
		}

		// Extract text content
		if len(chunk.Candidates) > 0 {
			candidate := chunk.Candidates[0]
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					events <- llm.StreamEvent{Delta: part.Text}
				}
			}

			// Check for completion
			if candidate.FinishReason != "" && candidate.FinishReason != "UNSPECIFIED" {
				events <- llm.StreamEvent{Done: true, Usage: usage}
				return
			}
		}
	}

	if err := scanner.Err(); err != nil {
		events <- llm.StreamEvent{Error: llm.WrapError(providerName, err), Done: true}
		return
	}

	// Signal completion
	events <- llm.StreamEvent{Done: true, Usage: usage}
}

// API request/response types

type part struct {
	Text             string                `json:"text,omitempty"`
	FunctionCall     *functionCallPart     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponsePart `json:"functionResponse,omitempty"`
}

// Function calling types
type functionCallPart struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type functionResponsePart struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
}

type functionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type toolConfig struct {
	FunctionCallingConfig *functionCallingConfig `json:"functionCallingConfig,omitempty"`
}

type functionCallingConfig struct {
	Mode string `json:"mode,omitempty"` // AUTO, ANY, NONE
}

type content struct {
	Role  string `json:"role"`
	Parts []part `json:"parts"`
}

type systemInstruction struct {
	Parts []part `json:"parts"`
}

type generationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
	// ThinkingLevel for Gemini 3 models: low, medium, high
	// Maps from effort: low→low, medium→medium, high/xhigh→high
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
	// ThinkingBudget for Gemini 2.5 models: 128-32768, 0=disable, -1=dynamic
	ThinkingBudget *int `json:"thinkingBudget,omitempty"`
}

type generateContentRequest struct {
	Contents          []content          `json:"contents"`
	SystemInstruction *systemInstruction `json:"systemInstruction,omitempty"`
	GenerationConfig  *generationConfig  `json:"generationConfig,omitempty"`
	Tools             []geminiTool       `json:"tools,omitempty"`
	ToolConfig        *toolConfig        `json:"toolConfig,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []functionDeclaration `json:"functionDeclarations,omitempty"`
	GoogleSearch         *struct{}             `json:"google_search,omitempty"`
}

type responsePart struct {
	Text         string            `json:"text,omitempty"`
	FunctionCall *functionCallPart `json:"functionCall,omitempty"`
}

type generateContentResponse struct {
	Candidates []struct {
		Content struct {
			Parts []responsePart `json:"parts"`
			Role  string         `json:"role"`
		} `json:"content"`
		FinishReason  string `json:"finishReason"`
		Index         int    `json:"index"`
		SafetyRatings []struct {
			Category    string `json:"category"`
			Probability string `json:"probability"`
		} `json:"safetyRatings"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}
