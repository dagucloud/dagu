// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/cmn/jsonutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/google/jsonschema-go/jsonschema"
)

const (
	fieldOutputSchema = "output_schema"
	fieldWebSearch    = "llm.web_search"
	fieldTools        = "llm.tools"
)

// validateStep checks the chat settings that the generic step checks leave
// out.
func validateStep(step ir.Step) error {
	return validateOutputSchema(step)
}

// validateOutputSchema checks that the model can answer a step's
// output_schema through the respond tool, whose parameters are that schema.
func validateOutputSchema(step ir.Step) error {
	if !step.HasOutputSchema() {
		return nil
	}
	schema := step.OutputSchema
	if schema["type"] != "object" {
		return ir.NewValidationError(fieldOutputSchema, nil,
			errors.New("a chat step's output_schema must declare type: object"))
	}
	properties, _ := schema["properties"].(map[string]any)
	if len(properties) == 0 {
		return ir.NewValidationError(fieldOutputSchema, nil,
			errors.New("a chat step's output_schema must list at least one property"))
	}
	for _, name := range requiredNames(schema["required"]) {
		if _, ok := properties[name]; !ok {
			// Unlisted names are dropped from the answer, so such a
			// schema could never be satisfied.
			return ir.NewValidationError(fieldOutputSchema, name,
				errors.New("a required property must be listed in properties"))
		}
	}
	if cfg := step.LLM; cfg != nil {
		if cfg.WebSearch != nil && cfg.WebSearch.Enabled {
			return ir.NewValidationError(fieldWebSearch, nil,
				errors.New("web search cannot be combined with output_schema"))
		}
		if slices.Contains(cfg.Tools, agentstep.RespondToolName) {
			return ir.NewValidationError(fieldTools, agentstep.RespondToolName, reservedToolError())
		}
	}
	return nil
}

// reservedToolError reports a tool that takes the name of the respond tool.
func reservedToolError() error {
	return fmt.Errorf("the tool name %q is reserved for the output_schema answer", agentstep.RespondToolName)
}

// requiredNames returns the property names a schema's required keyword lists.
func requiredNames(value any) []string {
	switch names := value.(type) {
	case []string:
		return names
	case []any:
		result := make([]string, 0, len(names))
		for _, name := range names {
			if s, ok := name.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

const (
	// respondInstruction asks for the answer through the respond tool. It is
	// added to each request and never saved to the session.
	respondInstruction = "When you have the answer, call the respond tool with arguments that match its parameter schema. That call ends the conversation."
	correctionFormat   = "Your answer could not be used: %v. Call the respond tool again with arguments that match its parameter schema."
	emptyAnswer        = "(no answer)"
)

// errNoAnswer is the step error when no model answers the output_schema. It
// carries no answer data; the rejected answers go to stderr.
var errNoAnswer = errors.New("the model gave no answer that matches output_schema")

// answerSchema is a step's output_schema prepared as the respond tool.
type answerSchema struct {
	tool       llmpkg.Tool
	properties map[string]any
	resolved   *jsonschema.Resolved
}

// newAnswerSchema prepares an output_schema for answers. It fails when the
// schema cannot be resolved for validation.
func newAnswerSchema(schema map[string]any) (*answerSchema, error) {
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal output_schema: %w", err)
	}
	var parsed jsonschema.Schema
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse output_schema: %w", err)
	}
	resolved, err := parsed.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve output_schema: %w", err)
	}
	properties, _ := schema["properties"].(map[string]any)
	return &answerSchema{
		tool: llmpkg.Tool{
			Type: "function",
			Function: llmpkg.ToolFunction{
				Name:        agentstep.RespondToolName,
				Description: agentstep.RespondToolDescription,
				Parameters:  agentstep.ToolParameters(schema),
			},
		},
		properties: properties,
		resolved:   resolved,
	}, nil
}

// accept returns the listed properties of a model's answer as one JSON
// object, or the reason the answer cannot be used. Unlisted properties are
// dropped before validation, so the result is what the step publishes.
func (a *answerSchema) accept(resp *llmpkg.ChatResponse) ([]byte, error) {
	raw, err := agentstep.StructuredAnswer(resp)
	if err != nil {
		return nil, errors.New("the reply holds no JSON answer")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, errors.New("the answer must be a JSON object")
	}
	for name := range fields {
		if _, ok := a.properties[name]; !ok {
			delete(fields, name)
		}
	}
	answer, err := jsonutil.MarshalUnescaped(fields)
	if err != nil {
		return nil, err
	}
	// The validator needs a plain decode; RawMessage values above keep
	// integers beyond float64 exact in the published answer.
	var value any
	if err := json.Unmarshal(answer, &value); err != nil {
		return nil, err
	}
	if err := a.resolved.Validate(value); err != nil {
		return nil, err
	}
	return answer, nil
}

// runStructuredForModel asks one model for an answer to the step's
// output_schema and prints it. Tool DAGs are offered next to the respond
// tool; a turn that calls respond ends the session without running the
// turn's other calls. An unusable answer gets one correction as plain
// messages, which keeps the history append-only and free of unanswered
// tool calls. A model that gives no usable answer, after its correction or
// within the tool iteration limit, returns an error so Run tries the next
// model.
func (e *Executor) runStructuredForModel(ctx context.Context, provider llmpkg.Provider, allMessages []ir.LLMMessage, cfg *ir.LLMConfig) error {
	tools := []llmpkg.Tool{e.answer.tool}
	if e.toolRegistry.HasTools() {
		e.toolExecutor = NewToolExecutor(e.toolRegistry, runtime.GetEnv(ctx).WorkingDir)
		tools = append(e.toolRegistry.ToLLMTools(), tools...)
	}
	e.savedToolDefinitions = toolDefinitions(tools)

	conv := newConversation(allMessages)
	corrected := false
	maxIterations := cfg.GetMaxToolIterations()
	for range maxIterations {
		req := &llmpkg.ChatRequest{
			Model:       cfg.Model,
			Messages:    withInstruction(conv.request(ctx)),
			Temperature: cfg.Temperature,
			MaxTokens:   cfg.MaxTokens,
			TopP:        cfg.TopP,
			Thinking:    toThinkingRequest(cfg.Thinking),
			Tools:       tools,
			ToolChoice:  agentstep.ToolChoiceRequired,
		}
		resp, err := llmpkg.ChatWithRetry(ctx, provider, req, llmpkg.DefaultLogicalRetryConfig())
		if err != nil {
			return fmt.Errorf("chat request failed: %w", err)
		}
		if e.toolExecutor != nil && len(resp.ToolCalls) > 0 && !callsRespond(resp.ToolCalls) {
			e.processToolCalls(ctx, conv, resp)
			continue
		}

		metadata := e.createResponseMetadata(cfg, &resp.Usage)
		answer, reason := e.answer.accept(resp)
		if reason == nil {
			if _, err := fmt.Fprintln(e.stdout, string(answer)); err != nil {
				logger.Error(ctx, "failed to write response", tag.Error(err))
			}
			conv.add(ir.LLMMessage{Role: ir.LLMRoleAssistant, Content: string(answer), Metadata: metadata}, nil)
			e.savedMessages = conv.messages
			return nil
		}

		rejected := rejectedAnswer(resp)
		logger.Warn(ctx, "Model answer does not match output_schema",
			slog.String("provider", cfg.Provider),
			slog.String("model", cfg.Model),
			slog.Bool("corrected", corrected),
		)
		if _, err := fmt.Fprintf(e.stderr, "%s/%s answer rejected: %v\n%s\n", cfg.Provider, cfg.Model, reason, rejected); err != nil {
			logger.Error(ctx, "failed to write rejected answer", tag.Error(err))
		}
		if corrected {
			e.savedMessages = conv.messages
			return fmt.Errorf("%s/%s: %w", cfg.Provider, cfg.Model, errNoAnswer)
		}
		corrected = true
		conv.add(ir.LLMMessage{Role: ir.LLMRoleAssistant, Content: rejected, Metadata: metadata}, nil)
		conv.add(ir.LLMMessage{Role: ir.LLMRoleUser, Content: fmt.Sprintf(correctionFormat, reason)}, nil)
	}

	e.savedMessages = conv.messages
	return fmt.Errorf("%s/%s: max tool iterations (%d) reached without a %s answer",
		cfg.Provider, cfg.Model, maxIterations, agentstep.RespondToolName)
}

// withInstruction adds the respond instruction to the first system message,
// or puts it first when there is none.
func withInstruction(msgs []llmpkg.Message) []llmpkg.Message {
	for i, msg := range msgs {
		if msg.Role == llmpkg.RoleSystem {
			msgs[i].Content = msg.Content + "\n\n" + respondInstruction
			return msgs
		}
	}
	return append([]llmpkg.Message{{Role: llmpkg.RoleSystem, Content: respondInstruction}}, msgs...)
}

// callsRespond reports whether a turn's tool calls include the respond tool.
func callsRespond(calls []llmpkg.ToolCall) bool {
	return slices.ContainsFunc(calls, func(call llmpkg.ToolCall) bool {
		return call.Function.Name == agentstep.RespondToolName
	})
}

// rejectedAnswer returns what the model answered: the respond call's
// arguments, otherwise its text.
func rejectedAnswer(resp *llmpkg.ChatResponse) string {
	for _, call := range resp.ToolCalls {
		if call.Function.Name == agentstep.RespondToolName {
			return call.Function.Arguments
		}
	}
	if resp.Content != "" {
		return resp.Content
	}
	return emptyAnswer
}
