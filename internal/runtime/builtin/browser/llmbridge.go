// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	_ "github.com/dagucloud/dagu/v2/internal/llm/allproviders"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/dagucloud/dagu/v2/internal/runtime/modelusage"
)

// errNoStepModel answers a model request of a step that has no model.
var errNoStepModel = errors.New("the step has no model: set llm at the DAG level or with.llm on the step")

// providerFactory builds a provider for one resolved model configuration.
type providerFactory func(ctx context.Context, cfg *ir.LLMConfig) (llmpkg.Provider, error)

// modelBridge answers browser runtime model requests with Dagu providers,
// trying the configured models in order.
type modelBridge struct {
	cfg       *ir.LLMConfig
	models    []ir.ModelEntry
	providers []llmpkg.Provider
	masker    *masking.Masker
	// log receives the answers a model gave that could not be used.
	log io.Writer
	// runCtx bounds every request. The browser runtime calls generate with
	// its own context, which carries no step cancellation.
	runCtx    context.Context
	mu        sync.Mutex
	usage     tokenUsage
	lastModel string
	// failed is set once a request got no answer from any model.
	failed bool
}

// newModelBridge resolves every configured model against the step's
// runtime environment. Provider settings such as base_url and API keys are
// resolved here because the browser runtime's callbacks carry no step
// environment. Without a configuration, every request fails.
func newModelBridge(ctx context.Context, cfg *ir.LLMConfig, masker *masking.Masker, log io.Writer, factory providerFactory) (*modelBridge, error) {
	if cfg == nil {
		return &modelBridge{masker: masker, log: log, runCtx: ctx}, nil
	}
	models, err := runtime.ResolveModels(ctx, cfg.GetModels())
	if err != nil {
		return nil, err
	}
	if factory == nil {
		factory = runtime.NewLLMProvider
	}
	providers := make([]llmpkg.Provider, len(models))
	for i, model := range models {
		if providers[i], err = factory(ctx, runtime.EffectiveLLMConfig(cfg, model)); err != nil {
			return nil, fmt.Errorf("%s/%s: %w", model.Provider, model.Name, err)
		}
	}
	return &modelBridge{
		cfg:       cfg,
		models:    models,
		providers: providers,
		masker:    masker,
		log:       log,
		runCtx:    ctx,
	}, nil
}

// totals returns the tokens used so far.
func (b *modelBridge) totals() tokenUsage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.usage
}

// failedRequest reports whether a request in this attempt got no answer from
// any model.
func (b *modelBridge) failedRequest() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failed
}

// resume counts the tokens an earlier part of the attempt used, before the
// step waited for a person.
func (b *modelBridge) resume(usage tokenUsage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.usage.Input += usage.Input
	b.usage.Output += usage.Output
}

// modelName returns the model that answered the latest request.
func (b *modelBridge) modelName() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastModel
}

func (b *modelBridge) generate(ctx context.Context, req generateRequest) (generateResponse, error) {
	if len(b.models) == 0 {
		b.mu.Lock()
		b.failed = true
		b.mu.Unlock()
		return generateResponse{}, errNoStepModel
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(b.runCtx, cancel)
	defer stop()
	// The step's model usage is counted on the step's meter, which only the
	// step's context carries.
	ctx = modelusage.WithMeter(ctx, modelusage.FromContext(b.runCtx))

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
		chatReq := &llmpkg.ChatRequest{
			Model:       effective.Model,
			Messages:    messages,
			Temperature: effective.Temperature,
			MaxTokens:   effective.MaxTokens,
			TopP:        effective.TopP,
			Tools: []llmpkg.Tool{{
				Type: "function",
				Function: llmpkg.ToolFunction{
					Name:        agentstep.RespondToolName,
					Description: agentstep.RespondToolDescription,
					Parameters:  parameters,
				},
			}},
			ToolChoice: agentstep.ToolChoiceRequired,
		}
		if chatReq.Temperature == nil {
			chatReq.Temperature = req.Temperature
		}
		answer, usage, err := b.ask(ctx, b.providers[i], model, chatReq)
		b.record(usage)
		if err != nil {
			if ctx.Err() != nil {
				return generateResponse{}, ctx.Err()
			}
			errs = append(errs, fmt.Errorf("%s/%s: %w", model.Provider, model.Name, err))
			continue
		}
		b.mu.Lock()
		b.lastModel = model.Name
		b.mu.Unlock()
		return generateResponse{JSON: answer, Usage: usage}, nil
	}
	b.mu.Lock()
	b.failed = true
	b.mu.Unlock()
	return generateResponse{}, fmt.Errorf("model request failed: %w", errors.Join(errs...))
}

// ask asks one model for a structured answer and returns the tokens its
// calls used. An answer that cannot be used is logged and corrected once,
// as plain messages, before the model counts as failed.
func (b *modelBridge) ask(ctx context.Context, provider llmpkg.Provider, model ir.ModelEntry, req *llmpkg.ChatRequest) (json.RawMessage, tokenUsage, error) {
	var usage tokenUsage
	corrected := false
	for {
		resp, err := llmpkg.ChatWithRetry(ctx, provider, req, llmpkg.DefaultLogicalRetryConfig())
		if err != nil {
			return nil, usage, err
		}
		usage.Input += resp.Usage.PromptTokens
		usage.Output += resp.Usage.CompletionTokens
		answer, err := agentstep.StructuredAnswer(resp)
		if err == nil {
			return answer, usage, nil
		}
		rejected := b.masker.MaskString(agentstep.RejectedAnswer(resp))
		_, _ = fmt.Fprintf(b.log, "warning: %s/%s answer rejected: %v\n%s\n", model.Provider, model.Name, err, rejected)
		if corrected {
			return nil, usage, err
		}
		corrected = true
		agentstep.AppendCorrection(req, rejected, err)
	}
}

func (b *modelBridge) record(usage tokenUsage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.usage.Input += usage.Input
	b.usage.Output += usage.Output
}

// toolParameters converts the requested response schema into tool parameters.
func toolParameters(schema json.RawMessage) (map[string]any, error) {
	var parameters map[string]any
	if err := json.Unmarshal(schema, &parameters); err != nil {
		return nil, fmt.Errorf("decode response schema: %w", err)
	}
	return agentstep.ToolParameters(parameters), nil
}
