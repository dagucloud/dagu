// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	_ "github.com/dagucloud/dagu/v2/internal/llm/allproviders"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

const (
	respondToolName        = "respond"
	respondToolDescription = "Return the answer as arguments that match the parameter schema exactly."
	toolChoiceRequired     = "required"
)

// schemaKeysToStrip are schema annotations some providers reject in tool
// parameters.
var schemaKeysToStrip = []string{"$schema", "$id"}

// providerFactory builds a provider for one resolved model configuration.
type providerFactory func(ctx context.Context, cfg *ir.LLMConfig) (llmpkg.Provider, error)

// modelBridge answers browser runtime model requests with Dagu providers,
// trying the configured models in order.
type modelBridge struct {
	cfg         *ir.LLMConfig
	models      []ir.ModelEntry
	masker      *masking.Masker
	newProvider providerFactory
	mu          sync.Mutex
	providers   map[int]llmpkg.Provider
	usage       tokenUsage
	lastModel   string
}

func newModelBridge(ctx context.Context, cfg *ir.LLMConfig, masker *masking.Masker, factory providerFactory) (*modelBridge, error) {
	models, err := runtime.ResolveModels(ctx, cfg.GetModels())
	if err != nil {
		return nil, err
	}
	if factory == nil {
		factory = runtime.NewLLMProvider
	}
	return &modelBridge{
		cfg:         cfg,
		models:      models,
		masker:      masker,
		newProvider: factory,
		providers:   make(map[int]llmpkg.Provider, len(models)),
	}, nil
}

// totals returns the tokens used so far.
func (b *modelBridge) totals() tokenUsage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.usage
}

// modelName returns the model that answered the latest request.
func (b *modelBridge) modelName() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastModel
}

func (b *modelBridge) generate(ctx context.Context, req generateRequest) (generateResponse, error) {
	parameters, err := toolParameters(req.Schema)
	if err != nil {
		return generateResponse{}, err
	}
	messages := make([]llmpkg.Message, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, llmpkg.Message{Role: llmpkg.RoleSystem, Content: b.masker.MaskString(req.System)})
	}
	for _, message := range req.Messages {
		messages = append(messages, llmpkg.Message{Role: llmpkg.ParseRole(message.Role), Content: b.masker.MaskString(message.Text)})
	}

	var errs []error
	for i, model := range b.models {
		effective := runtime.EffectiveLLMConfig(b.cfg, model)
		provider, err := b.provider(ctx, i, effective)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", model.Provider, model.Name, err))
			continue
		}
		chatReq := &llmpkg.ChatRequest{
			Model:       effective.Model,
			Messages:    messages,
			Temperature: effective.Temperature,
			MaxTokens:   effective.MaxTokens,
			TopP:        effective.TopP,
			Tools: []llmpkg.Tool{{
				Type: "function",
				Function: llmpkg.ToolFunction{
					Name:        respondToolName,
					Description: respondToolDescription,
					Parameters:  parameters,
				},
			}},
			ToolChoice: toolChoiceRequired,
		}
		if chatReq.Temperature == nil {
			chatReq.Temperature = req.Temperature
		}
		resp, err := llmpkg.ChatWithRetry(ctx, provider, chatReq, llmpkg.DefaultLogicalRetryConfig())
		if err != nil {
			if ctx.Err() != nil {
				return generateResponse{}, ctx.Err()
			}
			errs = append(errs, fmt.Errorf("%s/%s: %w", model.Provider, model.Name, err))
			continue
		}
		answer, err := structuredAnswer(resp)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", model.Provider, model.Name, err))
			continue
		}
		usage := tokenUsage{Input: resp.Usage.PromptTokens, Output: resp.Usage.CompletionTokens}
		b.record(usage, model.Name)
		return generateResponse{JSON: answer, Usage: usage}, nil
	}
	return generateResponse{}, fmt.Errorf("model request failed: %w", errors.Join(errs...))
}

func (b *modelBridge) provider(ctx context.Context, index int, cfg *ir.LLMConfig) (llmpkg.Provider, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if provider, ok := b.providers[index]; ok {
		return provider, nil
	}
	provider, err := b.newProvider(ctx, cfg)
	if err != nil {
		return nil, err
	}
	b.providers[index] = provider
	return provider, nil
}

func (b *modelBridge) record(usage tokenUsage, model string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.usage.Input += usage.Input
	b.usage.Output += usage.Output
	b.lastModel = model
}

// toolParameters converts the requested response schema into tool parameters.
func toolParameters(schema json.RawMessage) (map[string]any, error) {
	var parameters map[string]any
	if err := json.Unmarshal(schema, &parameters); err != nil {
		return nil, fmt.Errorf("decode response schema: %w", err)
	}
	for _, key := range schemaKeysToStrip {
		delete(parameters, key)
	}
	return parameters, nil
}

// structuredAnswer returns the JSON the model produced, preferring the
// respond tool call and falling back to JSON in the text content.
func structuredAnswer(resp *llmpkg.ChatResponse) (json.RawMessage, error) {
	for _, call := range resp.ToolCalls {
		if call.Function.Name == respondToolName && json.Valid([]byte(call.Function.Arguments)) {
			return json.RawMessage(call.Function.Arguments), nil
		}
	}
	text := strings.TrimSpace(resp.Content)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	if text != "" && json.Valid([]byte(text)) {
		return json.RawMessage(text), nil
	}
	return nil, errors.New("model did not return structured output")
}
