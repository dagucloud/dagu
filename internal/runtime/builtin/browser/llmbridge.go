// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	_ "github.com/dagucloud/dagu/v2/internal/llm/allproviders"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/dagucloud/dagu/v2/internal/runtime/modelusage"
	"github.com/google/jsonschema-go/jsonschema"
)

// errNoStepModel answers a model request of a step that has no model.
var errNoStepModel = errors.New("the step has no model: set llm at the DAG level or with.llm on the step")

// emptyResultAdvice follows the reason in a correction for an answer that broke
// its schema. The browser runtime's extract prompt allows null for nothing
// found, which a typed field cannot hold.
const emptyResultAdvice = " (when the page has nothing for a list, answer with an empty list, not with items of nulls)"

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
	schema := newAnswerSchema(req.Schema, parameters)
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
		answer, usage, err := b.ask(ctx, b.providers[i], model, chatReq, schema)
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

// ask asks one model for a structured answer that fits schema and returns the
// tokens its calls used. An answer that cannot be used is logged and corrected
// once, as plain messages, before the model counts as failed.
func (b *modelBridge) ask(ctx context.Context, provider llmpkg.Provider, model ir.ModelEntry, req *llmpkg.ChatRequest, schema *answerSchema) (json.RawMessage, tokenUsage, error) {
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
		advice := ""
		if err == nil {
			if err = schema.check(answer); err == nil {
				return answer, usage, nil
			}
			advice = emptyResultAdvice
		}
		// A validator's reason can quote the answer, as the answer itself does.
		reason := errors.New(b.masker.MaskString(err.Error()))
		rejected := b.masker.MaskString(agentstep.RejectedAnswer(resp))
		_, _ = fmt.Fprintf(b.log, "warning: %s/%s answer rejected: %v\n%s\n", model.Provider, model.Name, reason, rejected)
		if corrected {
			return nil, usage, reason
		}
		corrected = true
		agentstep.AppendCorrection(req, rejected, fmt.Errorf("%w%s", reason, advice))
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

// answerSchema tells whether a model's answer fits the schema of its request.
type answerSchema struct {
	fields   map[string]any
	resolved *jsonschema.Resolved
}

// newAnswerSchema prepares a request's schema for checking answers. The browser
// runtime validates every answer as well, so a schema that cannot be applied
// here as the runtime applies it accepts every answer: one with a pattern Go
// cannot compile, or one with defaults, which the runtime fills in first.
func newAnswerSchema(raw json.RawMessage, fields map[string]any) *answerSchema {
	schema := &answerSchema{fields: fields}
	if bytes.Contains(raw, []byte(`"default"`)) {
		return schema
	}
	var parsed jsonschema.Schema
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return schema
	}
	if resolved, err := parsed.Resolve(nil); err == nil {
		schema.resolved = resolved
	}
	return schema
}

// check returns a sentence saying why answer does not fit the schema, or nil
// when it fits.
func (s *answerSchema) check(answer json.RawMessage) error {
	if s.resolved == nil {
		return nil
	}
	var value any
	if err := json.Unmarshal(answer, &value); err != nil {
		return err
	}
	err := s.resolved.Validate(value)
	if err == nil {
		return nil
	}
	detail := firstMismatch("", s.fields, value)
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("the answer does not match the schema: %s", detail)
}

// firstMismatch names the first place in value, found at path, that has the
// wrong JSON type or lacks a required field. It returns "" when the mismatch
// is of another kind.
func firstMismatch(path string, schema map[string]any, value any) string {
	want, _ := schema["type"].(string)
	if got := jsonKind(value); want != "" && got != want && (want != "integer" || got != "number") {
		return fmt.Sprintf("%s is %s, expected %s", describePath(path), got, want)
	}
	switch value := value.(type) {
	case map[string]any:
		required, _ := schema["required"].([]any)
		for _, name := range required {
			if name, ok := name.(string); ok {
				if _, present := value[name]; !present {
					return joinPath(path, name) + " is missing"
				}
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for _, name := range slices.Sorted(maps.Keys(properties)) {
			field, present := value[name]
			child, isSchema := properties[name].(map[string]any)
			if !present || !isSchema {
				continue
			}
			if found := firstMismatch(joinPath(path, name), child, field); found != "" {
				return found
			}
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for i, item := range value {
			if found := firstMismatch(fmt.Sprintf("%s[%d]", path, i), items, item); found != "" {
				return found
			}
		}
	}
	return ""
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func describePath(path string) string {
	if path == "" {
		return "the answer"
	}
	return path
}

// jsonKind returns the JSON type name of a decoded value.
func jsonKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	default:
		return "object"
	}
}
