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
	"slices"
	"strconv"
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

	data, err := io.ReadAll(respBody)
	if err != nil {
		return nil, llm.WrapError(providerName, fmt.Errorf("failed to read response: %w", err))
	}
	var resp generateContentResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, llm.WrapError(providerName, fmt.Errorf("failed to decode response: %w", err))
	}
	// The raw parts are kept so the turn can be sent back unchanged.
	var raw struct {
		Candidates []struct {
			Content struct {
				Parts json.RawMessage `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
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
		usage = resp.UsageMetadata.llm()
	}

	result := &llm.ChatResponse{
		Content:      content.String(),
		FinishReason: finishReason,
		Usage:        usage,
		ToolCalls:    toolCalls,
	}
	if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
		result.ProviderState = &llm.ProviderState{Provider: llm.ProviderGemini, Data: raw.Candidates[0].Content.Parts}
	}
	return result, nil
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
	// callNames maps tool call IDs to function names; a function response
	// must name the function it answers.
	callNames := make(map[string]string)

	for i, m := range reqMessages {
		switch m.Role {
		case llm.RoleSystem:
			if sysInstr == nil {
				sysInstr = &systemInstruction{}
			}
			sysInstr.Parts = append(sysInstr.Parts, part{Text: m.Content})

		case llm.RoleUser:
			parts := make([]part, 0, len(m.Images)+1)
			for _, image := range m.Images {
				parts = append(parts, part{InlineData: &inlineData{MimeType: image.MediaType, Data: image.Data}})
			}
			if m.Content != "" || len(parts) == 0 {
				parts = append(parts, part{Text: m.Content})
			}
			contents = append(contents, content{
				Role:  "user",
				Parts: parts,
			})

		case llm.RoleAssistant:
			for _, tc := range m.ToolCalls {
				callNames[tc.ID] = tc.Function.Name
			}
			if state := m.ProviderState; state != nil && state.Provider == llm.ProviderGemini {
				// The turn goes back as received, keeping its thought
				// signatures.
				contents = append(contents, content{Role: "model", rawParts: state.Data})
				continue
			}
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
			name := m.Name
			if name == "" {
				name = callNames[m.ToolCallID]
			}
			responsePart := part{FunctionResponse: &functionResponsePart{Name: name, Response: response}}
			// Responses to the calls of one turn share a single content.
			if i > 0 && reqMessages[i-1].Role == llm.RoleTool {
				last := &contents[len(contents)-1]
				last.Parts = append(last.Parts, responsePart)
				continue
			}
			contents = append(contents, content{Role: "user", Parts: []part{responsePart}})
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
			Name:                 t.Function.Name,
			Description:          t.Function.Description,
			ParametersJSONSchema: t.Function.Parameters,
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
		model := strings.ToLower(req.Model)
		// Use explicit budget if provided
		if req.Thinking.BudgetTokens != nil {
			genConfig.ThinkingConfig = &thinkingConfig{ThinkingBudget: req.Thinking.BudgetTokens}
			thinkingBudget = *req.Thinking.BudgetTokens
		} else if thinkingLevelSupported(model) {
			// Level-based models use their nearest supported effort level.
			level := p.mapEffortToThinkingLevel(model, req.Thinking.Effort)
			genConfig.ThinkingConfig = &thinkingConfig{
				ThinkingLevel: strings.ToUpper(string(level)),
			}
			thinkingBudget = max(
				p.estimateThinkingBudget(req.Thinking.Effort),
				p.estimateThinkingBudget(level),
			)
		} else {
			thinkingBudget = p.estimateThinkingBudget(req.Thinking.Effort)
			if req.Thinking.Effort == llm.ThinkingEffortNone &&
				(strings.Contains(model, "gemini-2.5-flash") || strings.Contains(model, "gemini-robotics-er-1.6-")) {
				thinkingBudget = 0
			}
			genConfig.ThinkingConfig = &thinkingConfig{ThinkingBudget: new(thinkingBudget)}
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

// thinkingLevelSupported reports whether a model takes thinkingLevel, which
// Gemini documents from version 3 and Robotics-ER 2. Unrecognized IDs, such as
// -latest aliases, are assumed to support it.
func thinkingLevelSupported(model string) bool {
	if strings.Contains(model, "gemini-robotics-er-1.") {
		return false
	}
	match := geminiVersionPattern.FindStringSubmatch(model)
	if match == nil {
		return true
	}
	major, _ := strconv.Atoi(match[1])
	return major >= 3
}

// thinkingLevels orders Gemini thinking levels from least to most reasoning.
var thinkingLevels = []llm.ThinkingEffort{
	llm.ThinkingEffortMinimal, llm.ThinkingEffortLow, llm.ThinkingEffortMedium, llm.ThinkingEffortHigh,
}

// limitedThinkingLevels lists models that accept only some thinking levels,
// each in ascending order. Other level-based models accept every level.
var limitedThinkingLevels = []struct {
	model  string
	levels []llm.ThinkingEffort
}{
	{"gemini-3-pro", []llm.ThinkingEffort{llm.ThinkingEffortLow, llm.ThinkingEffortHigh}},
	{"gemini-3.1-pro", []llm.ThinkingEffort{llm.ThinkingEffortLow, llm.ThinkingEffortMedium, llm.ThinkingEffortHigh}},
	// The alias follows the newest Pro release, which does not accept minimal.
	{"gemini-pro-latest", []llm.ThinkingEffort{llm.ThinkingEffortLow, llm.ThinkingEffortMedium, llm.ThinkingEffortHigh}},
	{"gemini-3.7-flash", []llm.ThinkingEffort{llm.ThinkingEffortLow, llm.ThinkingEffortMedium, llm.ThinkingEffortHigh}},
	{"gemini-3.8-flash", []llm.ThinkingEffort{llm.ThinkingEffortLow, llm.ThinkingEffortMedium, llm.ThinkingEffortHigh}},
	{"gemini-3.1-flash-image", []llm.ThinkingEffort{llm.ThinkingEffortMinimal, llm.ThinkingEffortHigh}},
	{"gemini-3.1-flash-lite-image", []llm.ThinkingEffort{llm.ThinkingEffortMinimal, llm.ThinkingEffortHigh}},
	{"gemini-nano-banana-2.1", []llm.ThinkingEffort{llm.ThinkingEffortMinimal, llm.ThinkingEffortMedium, llm.ThinkingEffortHigh}},
}

// mapEffortToThinkingLevel maps effort to the nearest level supported by the model.
func (p *Provider) mapEffortToThinkingLevel(model string, effort llm.ThinkingEffort) llm.ThinkingEffort {
	level := llm.ThinkingEffortMedium
	switch effort {
	case llm.ThinkingEffortNone, llm.ThinkingEffortMinimal:
		level = llm.ThinkingEffortMinimal
	case llm.ThinkingEffortLow:
		level = llm.ThinkingEffortLow
	case llm.ThinkingEffortMedium:
		level = llm.ThinkingEffortMedium
	case llm.ThinkingEffortHigh, llm.ThinkingEffortXHigh, llm.ThinkingEffortMax:
		level = llm.ThinkingEffortHigh
	}
	for _, limited := range limitedThinkingLevels {
		if strings.Contains(model, limited.model) {
			return nearestThinkingLevel(level, limited.levels)
		}
	}
	return level
}

// nearestThinkingLevel returns the supported level closest to want, preferring
// the higher one on a tie. Supported levels must be in ascending order.
func nearestThinkingLevel(want llm.ThinkingEffort, supported []llm.ThinkingEffort) llm.ThinkingEffort {
	wantRank := slices.Index(thinkingLevels, want)
	nearest, nearestDistance := supported[0], len(thinkingLevels)
	for _, level := range supported {
		distance := slices.Index(thinkingLevels, level) - wantRank
		if distance < 0 {
			distance = -distance
		}
		if distance <= nearestDistance {
			nearest, nearestDistance = level, distance
		}
	}
	return nearest
}

// estimateThinkingBudget returns the token budget for an effort level. It is
// the budget sent to budget-based models and the thinking allowance reserved
// in maxOutputTokens for level-based ones.
func (p *Provider) estimateThinkingBudget(effort llm.ThinkingEffort) int {
	switch effort {
	case llm.ThinkingEffortNone, llm.ThinkingEffortMinimal, llm.ThinkingEffortLow:
		return 1024
	case llm.ThinkingEffortMedium:
		return 4096
	case llm.ThinkingEffortHigh:
		return 8192
	case llm.ThinkingEffortXHigh, llm.ThinkingEffortMax:
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
			chunkUsage := chunk.UsageMetadata.llm()
			usage = &chunkUsage
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
	InlineData       *inlineData           `json:"inlineData,omitempty"`
	FunctionCall     *functionCallPart     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponsePart `json:"functionResponse,omitempty"`
}

// inlineData carries encoded media; Data marshals as base64.
type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     []byte `json:"data"`
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

// functionDeclaration sends parameters as JSON Schema; the OpenAPI-based
// parameters field rejects keywords such as additionalProperties and $defs.
type functionDeclaration struct {
	Name                 string         `json:"name"`
	Description          string         `json:"description,omitempty"`
	ParametersJSONSchema map[string]any `json:"parametersJsonSchema,omitempty"`
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
	// rawParts, when set, are sent in place of Parts.
	rawParts json.RawMessage
}

// MarshalJSON sends a replayed turn's parts exactly as received.
func (c content) MarshalJSON() ([]byte, error) {
	if c.rawParts != nil {
		return json.Marshal(struct {
			Role  string          `json:"role"`
			Parts json.RawMessage `json:"parts"`
		}{c.Role, c.rawParts})
	}
	type plain content
	return json.Marshal(plain(c))
}

type systemInstruction struct {
	Parts []part `json:"parts"`
}

type generationConfig struct {
	Temperature     *float64        `json:"temperature,omitempty"`
	MaxOutputTokens *int            `json:"maxOutputTokens,omitempty"`
	TopP            *float64        `json:"topP,omitempty"`
	StopSequences   []string        `json:"stopSequences,omitempty"`
	ThinkingConfig  *thinkingConfig `json:"thinkingConfig,omitempty"`
}

type thinkingConfig struct {
	// ThinkingLevel is MINIMAL, LOW, MEDIUM or HIGH, for models from Gemini 3
	// and Robotics-ER 2.
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
	// ThinkingBudget is a token budget: 0 disables thinking where the model
	// allows it, and -1 lets the model decide.
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
	UsageMetadata *usageMetadata `json:"usageMetadata"`
}
