// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package agentstep provides an executor for agent-type workflow steps.
// It wraps the agent.Loop to run the AI agent as a single-shot
// step within a DAG workflow.
package agentstep

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/llm"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"

	// Import all LLM providers to register them.
	_ "github.com/dagucloud/dagu/internal/llm/allproviders"
)

var _ executor.Executor = (*Executor)(nil)
var _ executor.ChatMessageHandler = (*Executor)(nil)
var _ executor.PushBackAware = (*Executor)(nil)

func init() {
	executor.RegisterExecutor(
		core.ExecutorTypeAgent,
		newAgentExecutor,
		nil,
		core.ExecutorCapabilities{Agent: true},
	)
}

// Executor runs the agent loop as a workflow step.
type Executor struct {
	step              core.Step
	stdout            io.Writer
	stderr            io.Writer
	mu                sync.Mutex
	cancelLoop        context.CancelFunc
	contextMessages   []exec.LLMMessage
	savedMessages     []exec.LLMMessage
	pushBackInputs    map[string]string
	pushBackIteration int
}

func newAgentExecutor(_ context.Context, step core.Step) (executor.Executor, error) {
	return &Executor{step: step}, nil
}

func (e *Executor) SetStdout(w io.Writer) { e.stdout = w }
func (e *Executor) SetStderr(w io.Writer) { e.stderr = w }

// SetContext sets the session context from prior steps.
func (e *Executor) SetContext(msgs []exec.LLMMessage) { e.contextMessages = msgs }

// GetMessages returns the collected messages after execution.
func (e *Executor) GetMessages() []exec.LLMMessage { return e.savedMessages }

// SetPushBackContext provides push-back feedback for iterative re-execution.
func (e *Executor) SetPushBackContext(inputs map[string]string, iteration int) {
	e.pushBackInputs = inputs
	e.pushBackIteration = iteration
}

func (e *Executor) Kill(_ os.Signal) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancelLoop != nil {
		e.cancelLoop()
	}
	return nil
}

func (e *Executor) Run(ctx context.Context) error {
	dagCtx := exec.GetContext(ctx)
	stderr := e.stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	stdout := e.stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	// Resolve agent configuration from context.
	configStore := agent.GetConfigStore(ctx)
	if configStore == nil {
		return fmt.Errorf("agent config store not available; ensure the agent feature is configured")
	}
	modelStore := agent.GetModelStore(ctx)
	if modelStore == nil {
		return fmt.Errorf("agent model store not available; ensure models are configured in Agent Settings")
	}

	agentCfg, err := configStore.Load(ctx)
	if err != nil {
		return fmt.Errorf("failed to load agent config: %w", err)
	}

	// Resolve global tool policy for tool filtering and bash enforcement.
	globalPolicy := agent.ResolveToolPolicy(agentCfg.ToolPolicy)

	// Resolve model ID: step override → global default.
	modelID := agentCfg.DefaultModelID
	stepCfg := e.step.Agent
	if stepCfg != nil && stepCfg.Model != "" {
		modelID = stepCfg.Model
	}
	if modelID == "" {
		return fmt.Errorf("no model configured; set a default model in Agent Settings or specify agent.model in the step")
	}

	modelCfg, err := modelStore.GetByID(ctx, modelID)
	if err != nil {
		return fmt.Errorf("failed to get model %q: %w", modelID, err)
	}

	// Create LLM provider.
	provider, err := agent.CreateLLMProvider(modelCfg.ToLLMConfig(), agent.ProviderDeps{
		OAuthManager: agent.GetOAuthManager(ctx),
	})
	if err != nil {
		return fmt.Errorf("failed to create LLM provider: %w", err)
	}

	// Build tools filtered by global policy (exclude navigate and ask_user; add output tool).
	tools := buildTools(ctx, dagCtx, stepCfg, agentCfg, globalPolicy, stdout)

	// Load memory content if enabled.
	var memoryContent agent.MemoryContent
	if stepCfg != nil && stepCfg.Memory != nil && stepCfg.Memory.Enabled {
		if memStore := agent.GetMemoryStore(ctx); memStore != nil {
			dagName := ""
			if dagCtx.DAG != nil {
				dagName = dagCtx.DAG.Name
			}
			memoryContent = loadMemoryContent(ctx, memStore, dagName)
		}
	}

	// Load soul if step specifies one.
	var soul *agent.Soul
	if stepCfg != nil && stepCfg.Soul != "" {
		if soulStore := agent.GetSoulStore(ctx); soulStore != nil {
			var soulErr error
			soul, soulErr = soulStore.GetByID(ctx, stepCfg.Soul)
			if soulErr != nil {
				logf(stderr, "Warning: soul %q not available: %v", stepCfg.Soul, soulErr)
			} else if soul == nil {
				logf(stderr, "Warning: soul %q not found in store", stepCfg.Soul)
			}
		}
	}

	// Generate system prompt.
	systemPrompt := buildSystemPrompt(dagCtx, stepCfg, memoryContent, soul)

	// Enhance system prompt for push-back re-execution.
	if e.pushBackIteration > 0 {
		systemPrompt += fmt.Sprintf(
			"\n\n## Revision Context\n\nThis is a revision (iteration %d). "+
				"Your previous conversation is included in the history. "+
				"The reviewer's feedback follows as the next user message. "+
				"Revise your work accordingly.\n",
			e.pushBackIteration,
		)
	}

	// Resolve safe mode and max iterations.
	safeMode := true
	maxIterations := 50
	if stepCfg != nil {
		safeMode = stepCfg.SafeMode
		if stepCfg.MaxIterations > 0 {
			maxIterations = stepCfg.MaxIterations
		}
	}

	// Evaluate variable substitution in messages.
	var userMessages []llm.Message
	for _, msg := range e.step.Messages {
		content, evalErr := runtime.EvalStepString(ctx, msg.Content)
		if evalErr != nil {
			return fmt.Errorf("failed to evaluate message content: %w", evalErr)
		}
		userMessages = append(userMessages, llm.Message{
			Role:    llm.Role(msg.Role),
			Content: content,
		})
	}

	// Register bash policy enforcement hook.
	// Merge step-level bash policy over global policy (step overrides global).
	effectivePolicy := mergeStepBashPolicy(globalPolicy, stepCfg)
	hooks := agent.NewHooks()
	hooks.OnBeforeToolExec(buildPolicyHook(effectivePolicy))

	logf(stderr, "Starting (model: %s, tools: %d, safe_mode: %v, max_iterations: %d)",
		modelCfg.Name, len(tools), safeMode, maxIterations)

	// Create a cancellable context for stopping the loop after completion.
	loopCtx, cancelLoop := context.WithCancel(ctx)
	defer cancelLoop()

	e.mu.Lock()
	e.cancelLoop = cancelLoop
	e.mu.Unlock()

	// Initialize savedMessages from context so GetMessages() returns the full chain.
	if len(e.contextMessages) > 0 {
		e.savedMessages = append([]exec.LLMMessage(nil), e.contextMessages...)
	}

	iteration := 0

	// Resolve web search config: step-level overrides global agent config.
	webSearch := resolveWebSearch(stepCfg, agentCfg)

	loop := agent.NewLoop(agent.LoopConfig{
		Provider:         provider,
		Model:            modelCfg.Model,
		Tools:            tools,
		History:          contextToLLMHistory(e.contextMessages),
		SystemPrompt:     systemPrompt,
		SafeMode:         safeMode,
		Hooks:            hooks,
		Logger:           slog.Default(),
		WebSearch:        webSearch,
		ReturnTurnErrors: true,
		RecordMessage: func(_ context.Context, msg agent.Message) {
			logMessage(stderr, msg)
			converted := convertMessage(msg, modelCfg)
			e.savedMessages = append(e.savedMessages, converted...)
		},
		OnWorking: func(working bool) {
			if !working {
				iteration++
				if iteration >= maxIterations {
					logf(stderr, "Max iterations reached (%d)", maxIterations)
					cancelLoop()
				}
			}
		},
		OnTurnComplete: func() {
			// In a workflow step, cancel the loop after the first successful
			// turn since no new messages will be queued externally.
			cancelLoop()
		},
	})

	// Queue messages: either push-back feedback or original YAML messages.
	if e.pushBackIteration > 0 {
		// Push-back re-execution: previous conversation is already in history
		// via contextMessages. Queue only the feedback message.
		feedback := formatPushBackFeedback(e.pushBackInputs, e.step.Approval)
		loop.QueueUserMessage(llm.Message{
			Role:    llm.RoleUser,
			Content: feedback,
		})
	} else {
		for _, msg := range userMessages {
			loop.QueueUserMessage(msg)
		}
	}

	// Run the loop. It returns context.Canceled when we cancel it (expected).
	err = loop.Go(loopCtx)
	if err != nil && err != context.Canceled {
		logf(stderr, "Failed: %v", err)
		return err
	}

	logf(stderr, "Completed (%d iterations)", iteration)
	return nil
}

// buildTools creates the tool list for the agent step.
// Tools are filtered first by global policy, then by step-level agent config.
func buildTools(ctx context.Context, dagCtx exec.Context, stepCfg *core.AgentStepConfig, agentCfg *agent.Config, globalPolicy agent.ToolPolicyConfig, stdout io.Writer) []*agent.AgentTool {
	dagsDir := ""
	if dagCtx.DAG != nil {
		dagsDir = dagCtx.DAG.Location
	}

	// All available tools (excluding navigate and ask_user).
	allTools := map[string]*agent.AgentTool{
		"bash":   agent.NewBashTool(),
		"read":   agent.NewReadTool(),
		"patch":  agent.NewPatchTool(dagsDir),
		"think":  agent.NewThinkTool(),
		"output": agent.NewOutputTool(stdout),
	}

	remoteResolver := agent.GetRemoteContextResolver(ctx)
	if remoteResolver != nil {
		if t := agent.NewRemoteAgentTool(remoteResolver); t != nil {
			allTools["remote_agent"] = t
		}
		if t := agent.NewListContextsTool(remoteResolver); t != nil {
			allTools["list_contexts"] = t
		}
	}
	if dagStore := agent.GetDAGStore(ctx); dagStore != nil {
		allTools["dag_def_manage"] = agent.NewDAGDefManageTool(dagStore)
	}
	if dagRunStore := agent.GetDAGRunStore(ctx); dagRunStore != nil {
		allTools["dag_run_manage"] = agent.NewDAGRunManageTool(dagRunStore)
	}
	if agentCfg != nil && agentCfg.WebTools != nil {
		if t := agent.NewWebSearchTool(*agentCfg.WebTools); t != nil {
			allTools["web_search"] = t
		}
		if t := agent.NewWebExtractTool(*agentCfg.WebTools); t != nil {
			allTools["web_extract"] = t
		}
	}

	// Remove tools disabled by global policy (output is step-only, always kept).
	for name := range allTools {
		if name != "output" && !agent.IsToolEnabledResolved(globalPolicy, name) {
			delete(allTools, name)
		}
	}

	// If step specifies enabled tools, filter to only those.
	if stepCfg != nil && stepCfg.Tools != nil && len(stepCfg.Tools.Enabled) > 0 {
		var tools []*agent.AgentTool
		for _, name := range stepCfg.Tools.Enabled {
			if tool, ok := allTools[name]; ok {
				tools = append(tools, tool)
			}
		}
		// Always include the output tool even if not explicitly listed.
		if agent.GetToolByName(tools, "output") == nil {
			tools = append(tools, allTools["output"])
		}
		return tools
	}

	// Default: all globally-enabled tools.
	var tools []*agent.AgentTool
	for _, tool := range allTools {
		tools = append(tools, tool)
	}
	return tools
}

// buildSystemPrompt generates the system prompt for the agent step.
func buildSystemPrompt(dagCtx exec.Context, stepCfg *core.AgentStepConfig, memory agent.MemoryContent, soul *agent.Soul) string {
	env := agent.EnvironmentInfo{}
	if dagCtx.DAG != nil {
		env.DAGsDir = dagCtx.DAG.Location
		env.DocsDir = filepath.Join(dagCtx.DAG.Location, "docs")
	}

	var currentDAG *agent.CurrentDAG
	if dagCtx.DAG != nil {
		currentDAG = &agent.CurrentDAG{
			Name: dagCtx.DAG.Name,
		}
	}

	prompt := agent.GenerateSystemPrompt(agent.SystemPromptParams{
		Env:        env,
		CurrentDAG: currentDAG,
		Memory:     memory,
		Soul:       soul,
	})

	// Append instruction about the output tool.
	prompt += "\n\n## Output\n\nWhen you have completed your task, use the `output` tool to write your final result. " +
		"The content you provide to the output tool will be captured as the step's output variable for downstream steps.\n"

	// Append step-level prompt if specified.
	if stepCfg != nil && stepCfg.Prompt != "" {
		prompt += "\n## Additional Instructions\n\n" + stepCfg.Prompt + "\n"
	}

	return prompt
}

// loadMemoryContent loads memory content from the memory store for system prompt injection.
func loadMemoryContent(ctx context.Context, store agent.MemoryStore, dagName string) agent.MemoryContent {
	global, _ := store.LoadGlobalMemory(ctx)
	var dagMem string
	if dagName != "" {
		dagMem, _ = store.LoadDAGMemory(ctx, dagName)
	}
	readOnly := false
	if roStore, ok := store.(agent.SnapshotReadOnlyMemoryStore); ok {
		readOnly = roStore.MemoryReadOnly()
	}
	return agent.MemoryContent{
		GlobalMemory: global,
		DAGMemory:    dagMem,
		DAGName:      dagName,
		MemoryDir:    store.MemoryDir(),
		ReadOnly:     readOnly,
	}
}

// mergeStepBashPolicy merges step-level bash policy over the resolved global policy.
// Step-level settings override global when specified; unset fields fall back to global.
func mergeStepBashPolicy(global agent.ToolPolicyConfig, stepCfg *core.AgentStepConfig) agent.ToolPolicyConfig {
	if stepCfg == nil || stepCfg.Tools == nil || stepCfg.Tools.BashPolicy == nil {
		return global
	}
	bp := stepCfg.Tools.BashPolicy
	merged := global
	if bp.DefaultBehavior != "" {
		merged.Bash.DefaultBehavior = agent.BashDefaultBehavior(bp.DefaultBehavior)
	}
	if bp.DenyBehavior != "" {
		merged.Bash.DenyBehavior = agent.BashDenyBehavior(bp.DenyBehavior)
	}
	if len(bp.Rules) > 0 {
		rules := make([]agent.BashRule, len(bp.Rules))
		for i, r := range bp.Rules {
			rules[i] = agent.BashRule{
				Name:    r.Name,
				Pattern: r.Pattern,
				Action:  agent.BashRuleAction(r.Action),
			}
		}
		merged.Bash.Rules = rules
	}
	return merged
}

// resolveWebSearch resolves the web search config for the agent step.
// Step-level agent config overrides global agent config.
func resolveWebSearch(stepCfg *core.AgentStepConfig, agentCfg *agent.Config) *llm.WebSearchRequest {
	// Step-level override takes precedence.
	if stepCfg != nil && stepCfg.WebSearch != nil {
		ws := stepCfg.WebSearch
		if !ws.Enabled {
			return nil
		}
		result := &llm.WebSearchRequest{
			Enabled:        true,
			MaxUses:        ws.MaxUses,
			AllowedDomains: ws.AllowedDomains,
			BlockedDomains: ws.BlockedDomains,
		}
		if ws.UserLocation != nil {
			result.UserLocation = &llm.UserLocation{
				City:     ws.UserLocation.City,
				Region:   ws.UserLocation.Region,
				Country:  ws.UserLocation.Country,
				Timezone: ws.UserLocation.Timezone,
			}
		}
		return result
	}

	// Fall back to global agent config.
	if agentCfg != nil && agentCfg.WebSearch != nil && agentCfg.WebSearch.Enabled {
		return &llm.WebSearchRequest{
			Enabled: true,
			MaxUses: agentCfg.WebSearch.MaxUses,
		}
	}

	return nil
}

// buildPolicyHook returns a before-tool hook that enforces bash command policy.
func buildPolicyHook(policy agent.ToolPolicyConfig) agent.BeforeToolExecHookFunc {
	return func(_ context.Context, info agent.ToolExecInfo) error {
		if !agent.IsBashToolName(info.ToolName) {
			return nil
		}
		decision, err := agent.EvaluateBashPolicyResolved(policy, info.Input)
		if err != nil {
			return fmt.Errorf("bash policy evaluation failed: %w", err)
		}
		if !decision.Allowed {
			return fmt.Errorf("bash command denied by policy: %s", decision.Reason)
		}
		return nil
	}
}

// logf writes a formatted log line to stderr with [agent] prefix.
func logf(w io.Writer, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(w, "[agent] %s\n", msg)
}

const (
	toolCallArgsTruncateLen     = 200
	assistantContentTruncateLen = 500
)

// logMessage writes a structured log entry for an agent message.
func logMessage(w io.Writer, msg agent.Message) {
	switch msg.Type {
	case agent.MessageTypeAssistant:
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				args := tc.Function.Arguments
				if len(args) > toolCallArgsTruncateLen {
					args = args[:toolCallArgsTruncateLen] + "..."
				}
				logf(w, "Tool call: %s %s", tc.Function.Name, args)
			}
		}
		if msg.Content != "" {
			content := msg.Content
			if len(content) > assistantContentTruncateLen {
				content = content[:assistantContentTruncateLen] + "..."
			}
			logf(w, "Assistant: %s", strings.ReplaceAll(content, "\n", " "))
		}

	case agent.MessageTypeUser:
		if len(msg.ToolResults) > 0 {
			for _, tr := range msg.ToolResults {
				status := "success"
				if tr.IsError {
					status = "error"
				}
				logf(w, "Tool result: [%s, %d chars]", status, len(tr.Content))
			}
		}

	case agent.MessageTypeError:
		logf(w, "Error: %s", msg.Content)
	}
}
