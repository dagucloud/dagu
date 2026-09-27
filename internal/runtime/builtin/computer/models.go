// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	_ "github.com/dagucloud/dagu/v2/internal/llm/allproviders"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const (
	// visionLimitLongEdge and visionLimitPixels fit screenshots sent for
	// extract and expect within common vision model limits.
	visionLimitLongEdge = 1568
	visionLimitPixels   = 1_150_000
)

// statementSchema is the answer schema used to judge when and expect
// statements.
var statementSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []any{"answer", "reason"},
	"properties": map[string]any{
		"answer": map[string]any{"type": "boolean", "description": "Whether the statement is true for the current screen"},
		"reason": map[string]any{"type": "string", "description": "One sentence explaining the answer"},
	},
}

// model is one configured model and its provider.
type model struct {
	cfg          *ir.LLMConfig
	providerType llmpkg.ProviderType
	provider     llmpkg.Provider
}

func (m model) label() string {
	return string(m.providerType) + "/" + m.cfg.Model
}

// newModels builds a provider for every configured model, in fallback
// order.
func newModels(ctx context.Context, cfg *ir.LLMConfig, factory providerFactory) ([]model, error) {
	entries, err := runtime.ResolveModels(ctx, cfg.GetModels())
	if err != nil {
		return nil, err
	}
	models := make([]model, 0, len(entries))
	for _, entry := range entries {
		effective := runtime.EffectiveLLMConfig(cfg, entry)
		providerType, err := llmpkg.ParseProviderType(entry.Provider)
		if err != nil {
			return nil, err
		}
		provider, err := factory(ctx, effective)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", entry.Provider, entry.Name, err)
		}
		models = append(models, model{cfg: effective, providerType: providerType, provider: provider})
	}
	return models, nil
}

// tokenUsage counts model tokens.
type tokenUsage struct {
	Input  int
	Output int
}

func (u tokenUsage) total() int {
	return u.Input + u.Output
}

func (u tokenUsage) sub(other tokenUsage) tokenUsage {
	return tokenUsage{Input: u.Input - other.Input, Output: u.Output - other.Output}
}

func (u *tokenUsage) add(usage llmpkg.Usage) {
	u.Input += usage.PromptTokens
	u.Output += usage.CompletionTokens
}

// modelFailure marks an error of a model request, as opposed to an answer
// the step could not use.
type modelFailure struct{ err error }

func (f modelFailure) Error() string { return f.err.Error() }
func (f modelFailure) Unwrap() error { return f.err }

// query sends a screenshot and an instruction to the models in order and
// returns the first answer that matches schema.
func (r *run) query(ctx context.Context, instruction string, schema map[string]any, screenshot llmpkg.Image) (json.RawMessage, error) {
	parameters := agentstep.ToolParameters(schema)
	messages := []llmpkg.Message{
		{Role: llmpkg.RoleSystem, Content: "You read screenshots of a computer screen. Answer by calling the " + agentstep.RespondToolName + " tool."},
		{Role: llmpkg.RoleUser, Content: r.masker.MaskString(instruction), Images: []llmpkg.Image{screenshot}},
	}
	var errs []error
	for _, m := range r.models {
		resp, err := llmpkg.ChatWithRetry(ctx, m.provider, &llmpkg.ChatRequest{
			Model:       m.cfg.Model,
			Messages:    messages,
			Temperature: m.cfg.Temperature,
			MaxTokens:   m.cfg.MaxTokens,
			TopP:        m.cfg.TopP,
			Tools: []llmpkg.Tool{{
				Type: "function",
				Function: llmpkg.ToolFunction{
					Name:        agentstep.RespondToolName,
					Description: agentstep.RespondToolDescription,
					Parameters:  parameters,
				},
			}},
		}, llmpkg.DefaultLogicalRetryConfig())
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			errs = append(errs, fmt.Errorf("%s: %w", m.label(), err))
			continue
		}
		r.usage.add(resp.Usage)
		answer, err := agentstep.StructuredAnswer(resp)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.label(), err))
			continue
		}
		return answer, nil
	}
	return nil, modelFailure{fmt.Errorf("model request failed: %w", errors.Join(errs...))}
}

// judge asks the models whether a statement holds for the screen.
func (r *run) judge(ctx context.Context, statement string, screenshot llmpkg.Image) (bool, string, error) {
	instruction := fmt.Sprintf("Decide whether this statement is true for the screen: %q. Set answer to true or false and give a one-sentence reason.", statement)
	data, err := r.query(ctx, instruction, statementSchema, screenshot)
	if err != nil {
		return false, "", err
	}
	var verdict struct {
		Answer bool   `json:"answer"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(data, &verdict); err != nil {
		return false, "", fmt.Errorf("decode statement verdict: %w", err)
	}
	return verdict.Answer, verdict.Reason, nil
}
