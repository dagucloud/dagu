// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
)

// DecisionKind classifies what the controller chose to do this turn.
type DecisionKind int

const (
	// DecideRunStep runs one declared step.
	DecideRunStep DecisionKind = iota
	// DecideSetTaskStatus records where a task stands.
	DecideSetTaskStatus
	// DecideAskUser puts a question to a person and waits for the answer.
	DecideAskUser
	// DecideStop is returned when the model answers without calling a tool.
	DecideStop
	// DecideInvalid is returned when the model calls a tool that does not exist
	// or passes arguments that cannot be decoded. The caller reports the problem
	// back to the model and continues.
	DecideInvalid
)

// Decision is the single action the controller chose for this turn.
type Decision struct {
	Kind       DecisionKind
	ToolCallID string
	ToolName   string
	// Step is the declared step to run, set when Kind is DecideRunStep.
	Step string
	// Args are the child DAG parameters, set when Kind is DecideRunStep.
	Args map[string]any
	// Task, TaskStatus and Reason are set when Kind is DecideSetTaskStatus.
	Task       string
	TaskStatus TaskStatus
	Reason     string
	// Question is set when Kind is DecideAskUser.
	Question string
	// Content is the model's prose, set when Kind is DecideStop.
	Content string
	// Problem describes why the decision was rejected, set when Kind is DecideInvalid.
	Problem string
}

// Planner asks the model which action to take next.
// MaskFunc hides secret values in the copy of a conversation that leaves for an
// external model.
type MaskFunc func([]exec.LLMMessage) []exec.LLMMessage

type Planner struct {
	provider llmpkg.Provider
	cfg      *core.LLMConfig
	catalog  *Catalog
	system   string
	mask     MaskFunc
}

// NewPlanner builds a planner over a provider and an action catalog. The system
// prompt is prepended to the controller's own framing; callers pass it already
// resolved, so a workflow can parameterise its instructions.
func NewPlanner(
	provider llmpkg.Provider,
	cfg *core.LLMConfig,
	catalog *Catalog,
	system string,
	mask MaskFunc,
) *Planner {
	return &Planner{
		provider: provider,
		cfg:      cfg,
		catalog:  catalog,
		system:   strings.TrimSpace(system),
		mask:     mask,
	}
}

// Next runs one turn of the decision loop. The conversation in st is extended
// with the model's reply; the caller is responsible for appending the tool
// result once the decision has been carried out.
func (p *Planner) Next(ctx context.Context, st *State) (*Decision, error) {
	msgs := make([]exec.LLMMessage, 0, len(st.Messages())+1)
	msgs = append(msgs, exec.LLMMessage{Role: exec.RoleSystem, Content: p.systemPrompt(st)})
	msgs = append(msgs, st.Messages()...)

	// The prompt and transcript hold values resolved from the run scope, which
	// can include secrets. Only the outbound copy is masked; the transcript the
	// run keeps stays readable.
	outbound := msgs
	if p.mask != nil {
		outbound = p.mask(msgs)
	}

	req := &llmpkg.ChatRequest{
		Model:       p.cfg.Model,
		Messages:    toProviderMessages(outbound),
		Temperature: p.cfg.Temperature,
		MaxTokens:   p.cfg.MaxTokens,
		TopP:        p.cfg.TopP,
		Tools:       p.catalog.Tools(),
		ToolChoice:  "auto",
	}

	resp, err := llmpkg.ChatWithRetry(ctx, p.provider, req, llmpkg.DefaultLogicalRetryConfig())
	if err != nil {
		return nil, fmt.Errorf("controller decision failed: %w", err)
	}

	st.Turns++

	if len(resp.ToolCalls) == 0 {
		st.Append(exec.LLMMessage{
			Role:     exec.RoleAssistant,
			Content:  resp.Content,
			Metadata: p.metadata(&resp.Usage),
		})
		return &Decision{Kind: DecideStop, Content: resp.Content}, nil
	}

	// One action per turn: the conversation records only the call that runs, so
	// history never references a tool result that was never produced.
	call := resp.ToolCalls[0]
	st.Append(exec.LLMMessage{
		Role:    exec.RoleAssistant,
		Content: resp.Content,
		ToolCalls: []exec.ToolCall{{
			ID:   call.ID,
			Type: call.Type,
			Function: exec.ToolCallFunction{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		}},
		Metadata: p.metadata(&resp.Usage),
	})

	decision := &Decision{ToolCallID: call.ID, ToolName: call.Function.Name}

	args, err := decodeArgs(call.Function.Arguments)
	if err != nil {
		decision.Kind = DecideInvalid
		decision.Problem = fmt.Sprintf("could not decode arguments: %v", err)
		return decision, nil
	}

	if call.Function.Name == AskUserTool {
		question, _ := args["question"].(string)
		if strings.TrimSpace(question) == "" {
			decision.Kind = DecideInvalid
			decision.Problem = AskUserTool + " requires a question"
			return decision, nil
		}
		decision.Kind = DecideAskUser
		decision.Question = question
		return decision, nil
	}

	if call.Function.Name == SetTaskStatusTool {
		task, _ := args["task"].(string)
		status, _ := args["status"].(string)
		reason, _ := args["reason"].(string)
		if task == "" {
			decision.Kind = DecideInvalid
			decision.Problem = SetTaskStatusTool + " requires a task name"
			return decision, nil
		}
		if !ValidTaskStatus(status) {
			decision.Kind = DecideInvalid
			decision.Problem = fmt.Sprintf(
				"%q is not a task status; use completed, skipped, failed, or open", status)
			return decision, nil
		}
		decision.Kind = DecideSetTaskStatus
		decision.Task = task
		decision.TaskStatus = TaskStatus(status)
		decision.Reason = reason
		return decision, nil
	}

	step, ok := p.catalog.StepFor(call.Function.Name)
	if !ok {
		decision.Kind = DecideInvalid
		decision.Problem = fmt.Sprintf("no such action %q; available actions are %s",
			call.Function.Name, strings.Join(p.catalog.ToolNames(), ", "))
		return decision, nil
	}

	decision.Kind = DecideRunStep
	decision.Step = step
	decision.Args = args
	return decision, nil
}

// systemPrompt states the controller's job, the actions available to it, and the
// current status of every task.
func (p *Planner) systemPrompt(st *State) string {
	var sb strings.Builder

	if p.system != "" {
		sb.WriteString(p.system)
		sb.WriteString("\n\n")
	}

	sb.WriteString("You are the controller of this workflow. Each turn you choose exactly one action ")
	sb.WriteString("by calling exactly one tool. You observe the result, then choose again.\n\n")

	sb.WriteString("Tasks — the run ends once none is open, and fails if any is failed:\n")
	for _, task := range st.Tasks {
		status := task.Status
		if status == "" {
			status = TaskOpen
		}
		fmt.Fprintf(&sb, "- [%s] %s: %s", status, task.Name, task.Description)
		if status != TaskOpen && task.Reason != "" {
			fmt.Fprintf(&sb, " (%s)", task.Reason)
		}
		sb.WriteString("\n")
	}

	if len(st.Answers) > 0 {
		// Repeated on every turn rather than left to the conversation: a model
		// that re-reads its instructions can otherwise ask the same thing again
		// in different words, and each question stops the run on a person.
		sb.WriteString("\nAnswers you already have. Use these; do not ask for them again:\n")
		for _, question := range slices.Sorted(maps.Keys(st.Answers)) {
			fmt.Fprintf(&sb, "- %s\n  %s\n", question, st.Answers[question])
		}
	}

	sb.WriteString("\nRules:\n")
	sb.WriteString("- Call exactly one tool per turn.\n")
	fmt.Fprintf(&sb, "- Call %s to settle a task: completed once its criteria are met, "+
		"skipped when it turns out nothing needs doing, failed when it cannot be achieved.\n",
		SetTaskStatusTool)
	fmt.Fprintf(&sb, "- Do not leave a task open because it is unnecessary or impossible. "+
		"Settle it as skipped or failed and say why. Use %s with status open only to undo a "+
		"decision that later work invalidated.\n", SetTaskStatusTool)
	fmt.Fprintf(&sb, "- Call %s when a decision is genuinely someone else's to make. "+
		"The run pauses until they answer, so prefer acting and observing where you can.\n",
		AskUserTool)
	sb.WriteString("- When an action fails, read the error and decide whether to retry it, ")
	sb.WriteString("run a different action, or give up.\n")
	sb.WriteString("- Actions may be repeated when earlier work needs redoing, within a per-action limit.\n")
	sb.WriteString("- When no task is open, stop calling tools and reply with a short summary.\n")

	return sb.String()
}

func (p *Planner) metadata(usage *llmpkg.Usage) *exec.LLMMessageMetadata {
	meta := &exec.LLMMessageMetadata{Provider: p.cfg.Provider, Model: p.cfg.Model}
	if usage != nil {
		meta.PromptTokens = usage.PromptTokens
		meta.CompletionTokens = usage.CompletionTokens
		meta.TotalTokens = usage.TotalTokens
	}
	return meta
}

func decodeArgs(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

func toProviderMessages(msgs []exec.LLMMessage) []llmpkg.Message {
	result := make([]llmpkg.Message, len(msgs))
	for i, msg := range msgs {
		result[i] = llmpkg.Message{
			Role:       llmpkg.Role(msg.Role),
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
		}
		if len(msg.ToolCalls) == 0 {
			continue
		}
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
	return result
}
