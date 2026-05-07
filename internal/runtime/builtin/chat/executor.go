// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package chat provides an executor for chat (LLM-based session) steps.
package chat

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/masking"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	llmpkg "github.com/dagucloud/dagu/internal/llm"

	// Import all providers to register them
	_ "github.com/dagucloud/dagu/internal/llm/allproviders"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

var _ executor.Executor = (*Executor)(nil)
var _ executor.ChatMessageHandler = (*Executor)(nil)
var _ executor.PushBackAware = (*Executor)(nil)
var _ executor.SubRunProvider = (*Executor)(nil)
var _ executor.ToolDefinitionProvider = (*Executor)(nil)

// Executor implements the executor.Executor interface for chat steps.
type Executor struct {
	stdout            io.Writer
	stderr            io.Writer
	step              core.Step
	providerType      llmpkg.ProviderType
	apiKeyEnvVar      string
	messages          []exec.LLMMessage
	contextMessages   []exec.LLMMessage
	savedMessages     []exec.LLMMessage
	pushBackInputs    map[string]string
	pushBackIteration int

	// Tool calling support
	toolRegistry *ToolRegistry
	toolExecutor *ToolExecutor

	// Collected sub-runs from tool executions for UI drill-down
	collectedSubRuns []exec.SubDAGRun

	// Tool definitions that were available to the LLM (for UI visibility)
	savedToolDefinitions []exec.ToolDefinition
}

// newChatExecutor creates a new chat executor from a step configuration.
func newChatExecutor(ctx context.Context, step core.Step) (executor.Executor, error) {
	if step.LLM == nil {
		return nil, fmt.Errorf("llm configuration is required for chat step")
	}

	cfg := step.LLM

	// Get primary model's provider (first model in array or legacy Provider field)
	models := cfg.GetModels()
	primaryProvider := models[0].Provider
	if primaryProvider == "" {
		primaryProvider = cfg.Provider
	}

	// Parse provider type (required field, validated in spec)
	providerType, err := llmpkg.ParseProviderType(primaryProvider)
	if err != nil {
		return nil, fmt.Errorf("invalid provider: %w", err)
	}

	// Determine which environment variable to use for API key
	apiKeyEnvVar := models[0].APIKeyName
	if apiKeyEnvVar == "" {
		apiKeyEnvVar = cfg.APIKeyName
	}
	if apiKeyEnvVar == "" {
		apiKeyEnvVar = llmpkg.DefaultAPIKeyEnvVar(providerType)
	}

	// Convert messages from core.LLMMessage to execution.LLMMessage
	// Messages are now at step level, not inside LLM config
	messages := make([]exec.LLMMessage, 0, len(step.Messages)+1)

	// Add system message from config if specified
	if cfg.System != "" {
		messages = append(messages, exec.LLMMessage{
			Role:    core.LLMRoleSystem,
			Content: cfg.System,
		})
	}

	// Add step-level messages
	for _, msg := range step.Messages {
		messages = append(messages, exec.LLMMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	e := &Executor{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		step:         step,
		providerType: providerType,
		apiKeyEnvVar: apiKeyEnvVar,
		messages:     messages,
	}

	// Initialize tool registry if tools are configured
	if cfg.HasTools() {
		registry, err := NewToolRegistry(ctx, cfg.Tools)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize tool registry: %w", err)
		}
		e.toolRegistry = registry
	}

	return e, nil
}

// SetStdout sets the stdout writer.
func (e *Executor) SetStdout(out io.Writer) {
	e.stdout = out
}

// SetStderr sets the stderr writer.
func (e *Executor) SetStderr(out io.Writer) {
	e.stderr = out
}

// Kill terminates any running tool DAG executions.
func (e *Executor) Kill(sig os.Signal) error {
	if e.toolExecutor != nil {
		return e.toolExecutor.Kill(sig)
	}
	return nil
}

// SetContext sets the session context from prior steps.
func (e *Executor) SetContext(messages []exec.LLMMessage) {
	e.contextMessages = messages
}

// GetMessages returns the complete session messages after execution.
// This includes inherited messages, step messages, and the assistant response.
func (e *Executor) GetMessages() []exec.LLMMessage {
	return e.savedMessages
}

func (e *Executor) SetPushBackContext(inputs map[string]string, iteration int) {
	e.pushBackInputs = inputs
	e.pushBackIteration = iteration
}

// GetSubRuns returns the collected sub-DAG runs from tool executions.
// This implements the SubRunProvider interface for UI drill-down functionality.
func (e *Executor) GetSubRuns() []exec.SubDAGRun {
	return e.collectedSubRuns
}

// GetToolDefinitions returns the tool definitions that were available to the LLM.
// This implements the ToolDefinitionProvider interface for UI visibility.
func (e *Executor) GetToolDefinitions() []exec.ToolDefinition {
	return e.savedToolDefinitions
}

// buildMessageList orders messages so step's system message takes precedence over context.
func buildMessageList(stepMsgs, contextMsgs []exec.LLMMessage) []exec.LLMMessage {
	var result []exec.LLMMessage
	var stepSystemMsg *exec.LLMMessage
	var stepOtherMsgs []exec.LLMMessage

	for i := range stepMsgs {
		if stepMsgs[i].Role == exec.RoleSystem {
			stepSystemMsg = &stepMsgs[i]
		} else {
			stepOtherMsgs = append(stepOtherMsgs, stepMsgs[i])
		}
	}

	if stepSystemMsg != nil {
		result = append(result, *stepSystemMsg)
	}
	result = append(result, contextMsgs...)
	result = append(result, stepOtherMsgs...)

	return exec.DeduplicateSystemMessages(result)
}

func (e *Executor) executionMessages(ctx context.Context) ([]exec.LLMMessage, error) {
	evaluatedMessages, err := evalMessages(ctx, e.messages)
	if err != nil {
		return nil, err
	}
	if e.pushBackIteration == 0 {
		return buildMessageList(evaluatedMessages, e.contextMessages), nil
	}

	pushBackMessages := systemMessages(evaluatedMessages)
	pushBackMessages = append(pushBackMessages, exec.LLMMessage{
		Role:    exec.RoleUser,
		Content: formatPushBackFeedback(e.pushBackInputs, e.pushBackIteration, e.step.Approval),
	})
	return buildMessageList(pushBackMessages, e.contextMessages), nil
}

func systemMessages(messages []exec.LLMMessage) []exec.LLMMessage {
	var result []exec.LLMMessage
	for _, msg := range messages {
		if msg.Role == exec.RoleSystem {
			result = append(result, msg)
		}
	}
	return result
}

func formatPushBackFeedback(inputs map[string]string, iteration int, approval *core.ApprovalConfig) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "The reviewer has requested changes to your previous work for push-back iteration %d.\n", iteration)

	var allowed map[string]struct{}
	if approval != nil && len(approval.Input) > 0 {
		allowed = make(map[string]struct{}, len(approval.Input))
		for _, key := range approval.Input {
			allowed[key] = struct{}{}
		}
	}

	keys := make([]string, 0, len(inputs))
	for key := range inputs {
		if allowed != nil {
			if _, ok := allowed[key]; !ok {
				continue
			}
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		sb.WriteString("\nReviewer feedback:\n")
		for _, key := range keys {
			fmt.Fprintf(&sb, "- %s: %s\n", key, inputs[key])
		}
	}

	sb.WriteString("\nPlease revise your response based on this feedback.")
	return sb.String()
}

// toLLMMessages converts execution.LLMMessage to llmpkg.Message for provider calls.
func toLLMMessages(msgs []exec.LLMMessage) []llmpkg.Message {
	result := make([]llmpkg.Message, len(msgs))
	for i, msg := range msgs {
		result[i] = llmpkg.Message{
			Role:       llmpkg.Role(msg.Role),
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
		}
		// Convert tool calls if present
		if len(msg.ToolCalls) > 0 {
			result[i].ToolCalls = make([]llmpkg.ToolCall, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				result[i].ToolCalls[j] = llmpkg.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: llmpkg.ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}
	}
	return result
}

// toThinkingRequest converts core.ThinkingConfig to llmpkg.ThinkingRequest.
func toThinkingRequest(cfg *core.ThinkingConfig) *llmpkg.ThinkingRequest {
	if cfg == nil {
		return nil
	}
	return &llmpkg.ThinkingRequest{
		Enabled:         cfg.Enabled,
		Effort:          llmpkg.ThinkingEffort(cfg.Effort),
		BudgetTokens:    cfg.BudgetTokens,
		IncludeInOutput: cfg.IncludeInOutput,
	}
}

// toWebSearchRequest converts core.WebSearchConfig to llmpkg.WebSearchRequest.
func toWebSearchRequest(cfg *core.WebSearchConfig) *llmpkg.WebSearchRequest {
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	result := &llmpkg.WebSearchRequest{
		Enabled:        true,
		MaxUses:        cfg.MaxUses,
		AllowedDomains: cfg.AllowedDomains,
		BlockedDomains: cfg.BlockedDomains,
	}
	if cfg.UserLocation != nil {
		result.UserLocation = &llmpkg.UserLocation{
			City:     cfg.UserLocation.City,
			Region:   cfg.UserLocation.Region,
			Country:  cfg.UserLocation.Country,
			Timezone: cfg.UserLocation.Timezone,
		}
	}
	return result
}

// normalizeEnvVarExpr converts an environment variable reference to ${VAR} format.
// Handles: VAR → ${VAR}, $VAR → ${VAR}, ${VAR} → ${VAR}, "" → ""
func normalizeEnvVarExpr(expr string) string {
	if expr == "" {
		return ""
	}
	if strings.HasPrefix(expr, "${") {
		// Already in ${VAR} format, use as-is
		return expr
	}
	if after, ok := strings.CutPrefix(expr, "$"); ok {
		// Convert $VAR to ${VAR}
		return "${" + after + "}"
	}
	// Plain variable name, wrap in ${...}
	return "${" + expr + "}"
}

// evalMessages evaluates variable substitution in message content.
func evalMessages(ctx context.Context, msgs []exec.LLMMessage) ([]exec.LLMMessage, error) {
	result := make([]exec.LLMMessage, len(msgs))
	for i, msg := range msgs {
		content, err := runtime.EvalStepString(ctx, msg.Content)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate message content: %w", err)
		}
		result[i] = exec.LLMMessage{
			Role:       msg.Role,
			Content:    content,
			ToolCallID: msg.ToolCallID,
			ToolCalls:  msg.ToolCalls,
			Metadata:   msg.Metadata,
		}
	}
	return result, nil
}

// maskSecretsForProvider masks secret values in messages before sending to LLM provider.
// This prevents secrets from being leaked to external LLM APIs.
func maskSecretsForProvider(ctx context.Context, msgs []exec.LLMMessage) []exec.LLMMessage {
	// Use EnvScope.AllSecrets() for unified source tracking
	rCtx := runtime.GetDAGContext(ctx)
	if rCtx.EnvScope == nil {
		return msgs
	}
	secrets := rCtx.EnvScope.AllSecrets()
	if len(secrets) == 0 {
		return msgs
	}

	envPairs := make([]string, 0, len(secrets))
	for k, v := range secrets {
		envPairs = append(envPairs, k+"="+v)
	}

	masker := masking.NewMasker(masking.SourcedEnvVars{Secrets: envPairs})

	result := make([]exec.LLMMessage, len(msgs))
	for i, msg := range msgs {
		result[i] = exec.LLMMessage{
			Role:       msg.Role,
			Content:    masker.MaskString(msg.Content),
			ToolCallID: msg.ToolCallID,
			ToolCalls:  msg.ToolCalls, // Preserve tool calls (no secrets in IDs/names)
			Metadata:   msg.Metadata,
		}
	}
	return result
}

// Run executes the chat request.
func (e *Executor) Run(ctx context.Context) error {
	allMessages, err := e.executionMessages(ctx)
	if err != nil {
		return err
	}

	models := e.step.LLM.GetModels()

	// If only one model, use simple path (no fallback)
	if len(models) == 1 {
		return e.runWithModel(ctx, models[0], allMessages)
	}

	// Multiple models: implement fallback
	var lastErr error
	for i, model := range models {
		// Reset per-attempt state before each attempt
		e.collectedSubRuns = nil
		e.savedMessages = nil

		logger.Info(ctx, "Attempting LLM request",
			slog.String("provider", model.Provider),
			slog.String("model", model.Name),
			slog.Int("attemptIndex", i))

		err := e.runWithModel(ctx, model, allMessages)
		if err == nil {
			return nil // Success
		}

		lastErr = err
		logger.Warn(ctx, "LLM request failed",
			slog.String("provider", model.Provider),
			slog.String("model", model.Name),
			tag.Error(err))

		// More models to try?
		if i < len(models)-1 {
			logger.Info(ctx, "Falling back to next model",
				slog.String("next", models[i+1].Name))
		}
	}

	return fmt.Errorf("all %d models exhausted: %w", len(models), lastErr)
}

// runWithModel executes a chat request with a specific model.
func (e *Executor) runWithModel(ctx context.Context, model core.ModelEntry, allMessages []exec.LLMMessage) error {
	// Build effective config for this model
	effectiveCfg := e.buildEffectiveConfig(model)

	// Disable streaming if fallback is configured to avoid output corruption
	if e.step.LLM.HasFallback() && effectiveCfg.StreamEnabled() {
		effectiveCfg.Stream = new(false)
	}

	// Create provider for this model
	provider, err := e.createProviderForModel(ctx, model, effectiveCfg)
	if err != nil {
		return err
	}

	// Dispatch to tool-enabled execution if tools are configured
	if e.toolRegistry != nil && e.toolRegistry.HasTools() {
		return e.runWithToolsForModel(ctx, provider, allMessages, effectiveCfg)
	}

	// Standard execution without tools
	return e.runSimpleForModel(ctx, provider, allMessages, effectiveCfg)
}

// buildEffectiveConfig merges model-specific overrides with shared config.
func (e *Executor) buildEffectiveConfig(model core.ModelEntry) *core.LLMConfig {
	cfg := e.step.LLM

	return &core.LLMConfig{
		Provider:          model.Provider,
		Model:             model.Name,
		System:            cfg.System,
		Stream:            cfg.Stream,
		Thinking:          cfg.Thinking,
		Tools:             cfg.Tools,
		MaxToolIterations: cfg.MaxToolIterations,
		WebSearch:         cfg.WebSearch,
		Temperature:       coalescePtr(model.Temperature, cfg.Temperature),
		MaxTokens:         coalescePtr(model.MaxTokens, cfg.MaxTokens),
		TopP:              coalescePtr(model.TopP, cfg.TopP),
		BaseURL:           coalesceStr(model.BaseURL, cfg.BaseURL),
		APIKeyName:        coalesceStr(model.APIKeyName, cfg.APIKeyName),
	}
}

// coalescePtr returns the first non-nil pointer.
func coalescePtr[T any](override, fallback *T) *T {
	if override != nil {
		return override
	}
	return fallback
}

// coalesceStr returns the first non-empty string.
func coalesceStr(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}

// createProviderForModel creates an LLM provider for a specific model.
func (e *Executor) createProviderForModel(ctx context.Context, model core.ModelEntry, cfg *core.LLMConfig) (llmpkg.Provider, error) {
	// Parse provider type for this model
	providerType, err := llmpkg.ParseProviderType(cfg.Provider)
	if err != nil {
		return nil, fmt.Errorf("invalid provider: %w", err)
	}

	// Determine API key env var
	apiKeyEnvVar := cfg.APIKeyName
	if apiKeyEnvVar == "" {
		apiKeyEnvVar = llmpkg.DefaultAPIKeyEnvVar(providerType)
	}

	// Evaluate API key from environment variable
	var apiKey string
	if apiKeyEnvVar != "" {
		apiKeyExpr := normalizeEnvVarExpr(apiKeyEnvVar)
		apiKey, err = runtime.EvalStepString(ctx, apiKeyExpr)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate API key: %w", err)
		}
	}

	// Evaluate base URL if specified
	baseURL := cfg.BaseURL
	if baseURL != "" {
		baseURL, err = runtime.EvalStepString(ctx, baseURL)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate baseURL: %w", err)
		}
	}

	// Use default base URL if not specified
	if baseURL == "" {
		baseURL = llmpkg.DefaultBaseURL(providerType)
	}

	// Build provider config
	providerCfg := llmpkg.Config{
		APIKey:          apiKey,
		BaseURL:         baseURL,
		Timeout:         5 * time.Minute,
		MaxRetries:      3,
		InitialInterval: 1 * time.Second,
		MaxInterval:     30 * time.Second,
		Multiplier:      2.0,
	}

	// Create provider
	provider, err := llmpkg.NewProvider(providerType, providerCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM provider: %w", err)
	}

	return provider, nil
}

// runSimpleForModel executes a chat request without tool calling, using the given config.
func (e *Executor) runSimpleForModel(ctx context.Context, provider llmpkg.Provider, allMessages []exec.LLMMessage, cfg *core.LLMConfig) error {
	maskedForProvider := maskSecretsForProvider(ctx, allMessages)

	req := &llmpkg.ChatRequest{
		Model:       cfg.Model,
		Messages:    toLLMMessages(maskedForProvider),
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
		TopP:        cfg.TopP,
		Thinking:    toThinkingRequest(cfg.Thinking),
		WebSearch:   toWebSearchRequest(cfg.WebSearch),
	}

	var responseContent string
	var usage *llmpkg.Usage

	// Execute request (streaming or non-streaming)
	if cfg.StreamEnabled() {
		var err error
		responseContent, usage, err = e.runStreamForModel(ctx, provider, req)
		if err != nil {
			return err
		}
	} else {
		resp, err := llmpkg.ChatWithRetry(ctx, provider, req, llmpkg.DefaultLogicalRetryConfig())
		if err != nil {
			return fmt.Errorf("chat request failed: %w", err)
		}
		responseContent = resp.Content
		usage = &resp.Usage
		if _, err := fmt.Fprintln(e.stdout, responseContent); err != nil {
			logger.Error(ctx, "failed to write response", tag.Error(err))
		}
	}

	// Build metadata for the assistant response
	metadata := &exec.LLMMessageMetadata{
		Provider: cfg.Provider,
		Model:    cfg.Model,
	}
	if usage != nil {
		metadata.PromptTokens = usage.PromptTokens
		metadata.CompletionTokens = usage.CompletionTokens
		metadata.TotalTokens = usage.TotalTokens
	}

	// Save full session (inherited + step messages + response)
	e.savedMessages = append(allMessages, exec.LLMMessage{
		Role:     exec.RoleAssistant,
		Content:  responseContent,
		Metadata: metadata,
	})

	return nil
}

// runWithToolsForModel executes a chat request with tool calling support, using the given config.
// It implements a multi-turn loop where:
// 1. Send messages + tools to LLM
// 2. If LLM requests tool calls, execute them
// 3. Add tool results to session
// 4. Repeat until LLM provides final response (no more tool calls) or max iterations
func (e *Executor) runWithToolsForModel(ctx context.Context, provider llmpkg.Provider, allMessages []exec.LLMMessage, cfg *core.LLMConfig) error {
	maxIterations := cfg.GetMaxToolIterations()
	workDir := runtime.GetEnv(ctx).WorkingDir

	// Initialize tool executor for running DAGs
	e.toolExecutor = NewToolExecutor(e.toolRegistry, workDir)

	// Get tools in LLM format
	tools := e.toolRegistry.ToLLMTools()

	// Store tool definitions for UI visibility
	e.savedToolDefinitions = make([]exec.ToolDefinition, len(tools))
	for i, t := range tools {
		e.savedToolDefinitions[i] = exec.ToolDefinition{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		}
	}

	logger.Info(ctx, "Starting tool-enabled chat execution",
		slog.Int("tool_count", len(tools)),
		slog.Int("max_iterations", maxIterations),
	)

	// Working copy of messages for the tool loop
	sessionMessages := make([]exec.LLMMessage, len(allMessages))
	copy(sessionMessages, allMessages)

	for iteration := range maxIterations {
		var done bool
		var err error
		sessionMessages, done, err = e.executeToolStep(ctx, provider, cfg, tools, sessionMessages, iteration)
		if err != nil {
			return err
		}
		if done {
			e.savedMessages = sessionMessages
			return nil
		}
	}

	// Max iterations reached
	return e.handleMaxIterationsReached(ctx, maxIterations, sessionMessages)
}

// executeToolStep performs a single iteration of the tool execution loop.
// Returns updated messages, whether the session is done, and any error.
func (e *Executor) executeToolStep(
	ctx context.Context,
	provider llmpkg.Provider,
	cfg *core.LLMConfig,
	tools []llmpkg.Tool,
	msgs []exec.LLMMessage,
	iteration int,
) ([]exec.LLMMessage, bool, error) {
	logger.Debug(ctx, "Tool loop iteration",
		slog.Int("iteration", iteration+1),
		slog.Int("message_count", len(msgs)),
	)

	// Mask secrets before sending to provider
	maskedForProvider := maskSecretsForProvider(ctx, msgs)

	req := &llmpkg.ChatRequest{
		Model:       cfg.Model,
		Messages:    toLLMMessages(maskedForProvider),
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
		TopP:        cfg.TopP,
		Thinking:    toThinkingRequest(cfg.Thinking),
		Tools:       tools,
		ToolChoice:  "auto",
		WebSearch:   toWebSearchRequest(cfg.WebSearch),
	}

	// Execute request
	resp, err := llmpkg.ChatWithRetry(ctx, provider, req, llmpkg.DefaultLogicalRetryConfig())
	if err != nil {
		return nil, false, fmt.Errorf("chat request failed: %w", err)
	}

	// Check for final response (no tool calls)
	if len(resp.ToolCalls) == 0 {
		e.handleFinalResponse(ctx, msgs, resp, cfg, iteration)
		// Return updated messages including the final response
		finalMsgs := append(msgs, exec.LLMMessage{
			Role:     exec.RoleAssistant,
			Content:  resp.Content,
			Metadata: e.createResponseMetadata(cfg, &resp.Usage),
		})
		return finalMsgs, true, nil
	}

	// Process tool calls
	return e.processToolCalls(ctx, msgs, resp, iteration)
}

func (e *Executor) runStreamForModel(ctx context.Context, provider llmpkg.Provider, req *llmpkg.ChatRequest) (string, *llmpkg.Usage, error) {
	retryCfg := llmpkg.DefaultLogicalRetryConfig()

	for attempt := 1; attempt <= retryCfg.MaxAttempts; attempt++ {
		events, err := provider.ChatStream(ctx, req)
		if err != nil {
			if attempt < retryCfg.MaxAttempts && llmpkg.ShouldRetryRequest(ctx, err) {
				if waitErr := waitForStreamRetry(ctx, retryCfg, attempt); waitErr != nil {
					return "", nil, waitErr
				}
				continue
			}
			return "", nil, fmt.Errorf("chat stream request failed: %w", err)
		}

		var responseContent string
		var usage *llmpkg.Usage
		emittedDelta := false

		for event := range events {
			if event.Error != nil {
				if !emittedDelta && attempt < retryCfg.MaxAttempts && llmpkg.ShouldRetryRequest(ctx, event.Error) {
					if waitErr := waitForStreamRetry(ctx, retryCfg, attempt); waitErr != nil {
						return "", nil, waitErr
					}
					responseContent = ""
					usage = nil
					goto retryStream
				}
				return "", nil, fmt.Errorf("chat stream error: %w", event.Error)
			}
			if event.Delta != "" {
				emittedDelta = true
				responseContent += event.Delta
				if _, err := e.stdout.Write([]byte(event.Delta)); err != nil {
					logger.Error(ctx, "failed to write streaming response", tag.Error(err))
				}
			}
			if event.Usage != nil {
				usage = event.Usage
			}
		}

		if _, err := e.stdout.Write([]byte("\n")); err != nil {
			logger.Error(ctx, "failed to write newline", tag.Error(err))
		}
		return responseContent, usage, nil

	retryStream:
	}

	return "", nil, fmt.Errorf("chat stream request failed after retries")
}

func waitForStreamRetry(ctx context.Context, cfg llmpkg.LogicalRetryConfig, failedAttempt int) error {
	delay := cfg.InitialInterval
	for i := 1; i < failedAttempt; i++ {
		delay = time.Duration(float64(delay) * cfg.Multiplier)
		if delay > cfg.MaxInterval {
			delay = cfg.MaxInterval
			break
		}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// handleFinalResponse processes and logs the final response from the LLM.
func (e *Executor) handleFinalResponse(
	ctx context.Context,
	msgs []exec.LLMMessage,
	resp *llmpkg.ChatResponse,
	cfg *core.LLMConfig,
	iteration int,
) {
	logger.Info(ctx, "LLM provided final response (no tool calls)",
		slog.Int("iterations_used", iteration+1),
	)

	if resp.Content != "" {
		if _, err := fmt.Fprintln(e.stdout, resp.Content); err != nil {
			logger.Error(ctx, "failed to write response", tag.Error(err))
		}
	}
}

// processToolCalls handles the execution of tool calls requested by the LLM.
func (e *Executor) processToolCalls(
	ctx context.Context,
	msgs []exec.LLMMessage,
	resp *llmpkg.ChatResponse,
	iteration int,
) ([]exec.LLMMessage, bool, error) {
	logger.Info(ctx, "LLM requested tool calls",
		slog.Int("tool_call_count", len(resp.ToolCalls)),
	)

	// Add assistant message with tool calls
	execToolCalls := make([]exec.ToolCall, len(resp.ToolCalls))
	for i, tc := range resp.ToolCalls {
		execToolCalls[i] = exec.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: exec.ToolCallFunction{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		}
	}

	assistantMsg := exec.LLMMessage{
		Role:      exec.RoleAssistant,
		Content:   resp.Content,
		ToolCalls: execToolCalls,
	}
	newMsgs := append(msgs, assistantMsg)

	// Execute tools
	toolCallResults := e.toolExecutor.ExecuteToolCalls(ctx, resp.ToolCalls)

	// Append results
	for _, tcr := range toolCallResults {
		result := tcr.Result
		toolMsg := exec.LLMMessage{
			Role:       exec.RoleTool,
			Content:    result.Content,
			ToolCallID: result.ToolCallID,
		}
		if result.Error != "" {
			toolMsg.Content = fmt.Sprintf("Error: %s", result.Error)
		}
		newMsgs = append(newMsgs, toolMsg)

		if tcr.SubRun.DAGRunID != "" {
			e.collectedSubRuns = append(e.collectedSubRuns, tcr.SubRun)
		}

		contentPreview := result.Content
		if len(contentPreview) > 200 {
			contentPreview = contentPreview[:200] + "..."
		}
		logger.Info(ctx, "Tool execution result",
			tag.Tool(result.Name),
			tag.ToolCallID(result.ToolCallID),
			slog.String("content_preview", contentPreview),
		)
	}

	return newMsgs, false, nil
}

// handleMaxIterationsReached handles the case where the tool loop hits the limit.
func (e *Executor) handleMaxIterationsReached(
	ctx context.Context,
	maxIterations int,
	msgs []exec.LLMMessage,
) error {
	logger.Warn(ctx, "Max tool iterations reached",
		slog.Int("max_iterations", maxIterations),
	)

	lastContent := fmt.Sprintf("[Max tool iterations (%d) reached. The LLM may not have provided a complete response.]", maxIterations)

	// Try to find the last assistant message
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == exec.RoleAssistant {
			lastContent = msgs[i].Content
			break
		}
	}

	if _, err := fmt.Fprintln(e.stdout, lastContent); err != nil {
		logger.Error(ctx, "failed to write response", tag.Error(err))
	}

	e.savedMessages = msgs
	return nil
}

// createResponseMetadata builds metadata for the assistant response.
func (e *Executor) createResponseMetadata(cfg *core.LLMConfig, usage *llmpkg.Usage) *exec.LLMMessageMetadata {
	metadata := &exec.LLMMessageMetadata{
		Provider: cfg.Provider,
		Model:    cfg.Model,
	}
	if usage != nil {
		metadata.PromptTokens = usage.PromptTokens
		metadata.CompletionTokens = usage.CompletionTokens
		metadata.TotalTokens = usage.TotalTokens
	}
	return metadata
}

func init() {
	executor.RegisterExecutor(core.ExecutorTypeChat, newChatExecutor, nil, core.ExecutorCapabilities{
		LLM: true,
		// All others false - chat doesn't support command, script, shell, container, subdag
	})
}
