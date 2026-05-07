// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/persis/fileagentskill"
	"github.com/dagucloud/dagu/internal/persis/filesession"
	"github.com/spf13/cobra"
)

const (
	flagAgentPrompt = "prompt"
	flagAgentModel  = "model"
	flagAgentSoul   = "soul"
	flagAgentLimit  = "limit"

	agentPollInterval = 500 * time.Millisecond
)

var agentPromptFlags = []commandLineFlag{
	{
		name:      flagAgentPrompt,
		shorthand: "p",
		usage:     "Prompt to send to the agent",
	},
	{
		name:  flagAgentModel,
		usage: "LLM model ID to use for this message",
	},
	{
		name:  flagAgentSoul,
		usage: "Soul ID to use for this session",
	},
}

// Agent returns the "agent" command for managing Dagu agent sessions.
func Agent() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "agent [flags]",
		Short: "Chat with the Dagu agent",
		Long: `Chat with the Dagu agent using the current CLI context.

Examples:
  dagu agent
  dagu agent -p "create a DAG that backs up /var/log every night"
  dagu agent --model gpt-4.1 -p "review this workflow"
  dagu agent history
  dagu agent resume xxxxx
  dagu agent resume xxxxx -p "continue"`,
		Args: cobra.NoArgs,
	}, agentPromptFlags, runAgent)

	cmd.AddCommand(agentHistoryCommand())
	cmd.AddCommand(agentResumeCommand())
	return cmd
}

func agentHistoryCommand() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "history",
		Short: "List agent sessions",
		Args:  cobra.NoArgs,
	}, nil, runAgentHistory)
	cmd.Flags().Int(flagAgentLimit, 30, "Maximum number of sessions to display")
	return cmd
}

func agentResumeCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "resume <session-id> [flags]",
		Short: "Show or continue an agent session",
		Args:  cobra.ExactArgs(1),
	}, agentPromptFlags, runAgentResume)
}

func runAgent(ctx *Context, _ []string) error {
	prompt, err := readOptionalAgentPrompt(ctx)
	if err != nil {
		return err
	}
	if prompt == "" {
		if ctx.Command.Flags().Changed(flagAgentPrompt) {
			return fmt.Errorf("--%s cannot be empty", flagAgentPrompt)
		}
		return runAgentInteractive(ctx, "")
	}
	return runAgentOnce(ctx, prompt)
}

func runAgentOnce(ctx *Context, prompt string) error {
	req, err := buildAgentChatRequest(ctx, prompt)
	if err != nil {
		return err
	}

	out := ctx.Command.OutOrStdout()
	if ctx.IsRemote() {
		resp, err := ctx.Remote.createAgentSession(ctx, req)
		if err != nil {
			return err
		}
		return followAgentSessionNonInteractive(ctx, out, resp.SessionId, func(context.Context) (*agentSessionDetail, error) {
			return ctx.Remote.getAgentSessionDetail(ctx, resp.SessionId)
		})
	}

	agentAPI, err := ctx.newAgentAPI()
	if err != nil {
		return err
	}
	user := localAgentUser()
	sessionID, _, err := agentAPI.CreateSession(ctx, user, toAgentChatRequest(req))
	if err != nil {
		return err
	}
	return followAgentSessionNonInteractive(ctx, out, sessionID, func(context.Context) (*agentSessionDetail, error) {
		return getLocalAgentSessionDetail(ctx, agentAPI, sessionID, user.UserID)
	})
}

func runAgentHistory(ctx *Context, _ []string) error {
	limit, err := ctx.Command.Flags().GetInt(flagAgentLimit)
	if err != nil {
		return fmt.Errorf("failed to read limit flag: %w", err)
	}
	if limit <= 0 {
		return fmt.Errorf("--%s must be greater than 0", flagAgentLimit)
	}

	var sessions []agentSessionRow
	if ctx.IsRemote() {
		resp, err := ctx.Remote.listAgentSessions(ctx, 1, limit)
		if err != nil {
			return err
		}
		sessions = agentRowsFromAPI(resp.Sessions)
	} else {
		agentAPI, err := ctx.newAgentAPI()
		if err != nil {
			return err
		}
		result := agentAPI.ListSessionsPaginated(ctx, localAgentUser().UserID, 1, limit)
		sessions = agentRowsFromLocal(result.Items)
	}

	return renderAgentSessions(ctx.Command.OutOrStdout(), sessions)
}

func runAgentResume(ctx *Context, args []string) error {
	sessionID := args[0]
	prompt, err := readOptionalAgentPrompt(ctx)
	if err != nil {
		return err
	}
	if prompt == "" {
		if ctx.Command.Flags().Changed(flagAgentPrompt) {
			return fmt.Errorf("--%s cannot be empty", flagAgentPrompt)
		}
		return runAgentInteractive(ctx, sessionID)
	}
	req, err := buildAgentChatRequest(ctx, prompt)
	if err != nil {
		return err
	}

	out := ctx.Command.OutOrStdout()
	if ctx.IsRemote() {
		if err := ctx.Remote.sendAgentMessage(ctx, sessionID, req); err != nil {
			return err
		}
		return followAgentSessionNonInteractive(ctx, out, sessionID, func(context.Context) (*agentSessionDetail, error) {
			return ctx.Remote.getAgentSessionDetail(ctx, sessionID)
		})
	}

	agentAPI, err := ctx.newAgentAPI()
	if err != nil {
		return err
	}
	user := localAgentUser()
	if err := agentAPI.SendMessage(ctx, sessionID, user, toAgentChatRequest(req)); err != nil {
		return err
	}
	return followAgentSessionNonInteractive(ctx, out, sessionID, func(context.Context) (*agentSessionDetail, error) {
		return getLocalAgentSessionDetail(ctx, agentAPI, sessionID, user.UserID)
	})
}

func readOptionalAgentPrompt(ctx *Context) (string, error) {
	prompt, err := ctx.StringParam(flagAgentPrompt)
	if err != nil {
		return "", fmt.Errorf("failed to read prompt flag: %w", err)
	}
	return strings.TrimSpace(prompt), nil
}

func buildAgentChatRequest(ctx *Context, prompt string) (api.AgentChatRequest, error) {
	model, err := ctx.StringParam(flagAgentModel)
	if err != nil {
		return api.AgentChatRequest{}, fmt.Errorf("failed to read model flag: %w", err)
	}
	soulID, err := ctx.StringParam(flagAgentSoul)
	if err != nil {
		return api.AgentChatRequest{}, fmt.Errorf("failed to read soul flag: %w", err)
	}
	return api.AgentChatRequest{
		Message: prompt,
		Model:   stringPtrOrNil(model),
		SoulId:  stringPtrOrNil(soulID),
	}, nil
}

func (c *Context) newAgentAPI() (*agent.API, error) {
	stores := c.agentStores()
	if stores.ConfigStore == nil || stores.ModelStore == nil {
		return nil, fmt.Errorf("agent is not configured properly")
	}

	sessStore, err := filesession.New(c.Config.Paths.SessionsDir, filesession.WithMaxPerUser(c.Config.Server.Session.MaxPerUser))
	if err != nil {
		logger.Warn(c, "Failed to create agent session store, persistence disabled", tag.Error(err))
	}

	dagStore, err := c.dagStore(dagStoreConfig{})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize DAG store: %w", err)
	}

	referencesDir := fileagentskill.SeedReferences(filepath.Join(c.Config.Paths.DataDir, "agent", "references"))
	hooks := agent.NewHooks()
	hooks.OnBeforeToolExec(newCLIAgentPolicyHook(stores.ConfigStore))
	agentAPI := agent.NewAPI(agent.APIConfig{
		ConfigStore:           stores.ConfigStore,
		ModelStore:            stores.ModelStore,
		SoulStore:             stores.SoulStore,
		WorkingDir:            c.Config.Paths.DAGsDir,
		SessionStore:          sessStore,
		DAGStore:              dagStore,
		Hooks:                 hooks,
		MemoryStore:           stores.MemoryStore,
		OAuthManager:          stores.OAuthManager,
		RemoteContextResolver: stores.ContextResolver,
		Logger:                newCLIAgentLogger(),
		EventService:          c.EventService,
		Environment: agent.EnvironmentInfo{
			DAGsDir:        c.Config.Paths.DAGsDir,
			DocsDir:        c.Config.Paths.DocsDir,
			LogDir:         c.Config.Paths.LogDir,
			DataDir:        c.Config.Paths.DataDir,
			SessionsDir:    c.Config.Paths.SessionsDir,
			ConfigFile:     c.Config.Paths.ConfigFileUsed,
			WorkingDir:     c.Config.Paths.DAGsDir,
			BaseConfigFile: c.Config.Paths.BaseConfig,
			ReferencesDir:  referencesDir,
		},
	})
	agentAPI.StartCleanup(c)
	return agentAPI, nil
}

func newCLIAgentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newCLIAgentPolicyHook(configStore agent.ConfigStore) agent.BeforeToolExecHookFunc {
	return func(ctx context.Context, info agent.ToolExecInfo) error {
		if configStore == nil {
			return fmt.Errorf("agent policy unavailable")
		}
		cfg, err := configStore.Load(ctx)
		if err != nil || cfg == nil {
			return fmt.Errorf("policy unavailable")
		}
		if err := agent.ValidateToolPolicy(cfg.ToolPolicy); err != nil {
			return fmt.Errorf("invalid policy configuration")
		}
		policy := agent.ResolveToolPolicy(cfg.ToolPolicy)
		if !agent.IsToolEnabledResolved(policy, info.ToolName) {
			return fmt.Errorf("tool %q is disabled by policy", info.ToolName)
		}
		if !agent.IsBashToolName(info.ToolName) {
			return nil
		}
		decision, err := agent.EvaluateBashPolicyResolved(policy, info.Input)
		if err != nil {
			return fmt.Errorf("bash policy evaluation failed: %w", err)
		}
		if decision.Allowed {
			return nil
		}
		if decision.DenyBehavior == agent.BashDenyBehaviorAskUser && !info.SafeMode {
			return nil
		}
		if decision.DenyBehavior == agent.BashDenyBehaviorAskUser && info.RequestCommandApproval != nil {
			reason := strings.TrimSpace(strings.Join([]string{
				decision.Reason,
				decision.RuleName,
			}, " "))
			approved, approvalErr := info.RequestCommandApproval(ctx, decision.Command, reason)
			if approvalErr != nil {
				return fmt.Errorf("bash command denied by policy (approval failed: %w)", approvalErr)
			}
			if approved {
				return nil
			}
			return fmt.Errorf("bash command denied by policy")
		}
		return fmt.Errorf("bash command denied by policy")
	}
}

func localAgentUser() agent.UserIdentity {
	return agent.UserIdentity{
		UserID:   "admin",
		Username: "admin",
		Role:     auth.RoleAdmin,
	}
}

func toAgentChatRequest(req api.AgentChatRequest) agent.ChatRequest {
	out := agent.ChatRequest{
		Message: req.Message,
	}
	if req.Model != nil {
		out.Model = *req.Model
	}
	if req.SoulId != nil {
		out.SoulID = *req.SoulId
	}
	if req.SessionId != nil {
		out.SessionID = *req.SessionId
	}
	if req.SafeMode != nil {
		out.SafeMode = *req.SafeMode
	}
	if req.DagContexts != nil {
		for _, dc := range *req.DagContexts {
			item := agent.DAGContext{DAGFile: dc.DagFile}
			if dc.DagRunId != nil {
				item.DAGRunID = *dc.DagRunId
			}
			out.DAGContexts = append(out.DAGContexts, item)
		}
	}
	return out
}

type agentSessionRow struct {
	ID        string
	Title     string
	Model     string
	UpdatedAt time.Time
	Working   bool
	TotalCost float64
}

type agentSessionDetail struct {
	Messages         []agentMessageRow
	Working          bool
	HasPendingPrompt bool
}

type agentMessageRow struct {
	ID         string
	Type       string
	Content    string
	UserPrompt *agentPromptRow
}

type agentPromptRow struct {
	ID            string
	Question      string
	Options       []agentPromptOptionRow
	AllowFreeText bool
	MultiSelect   bool
	PromptType    string
}

type agentPromptOptionRow struct {
	ID          string
	Label       string
	Description string
}

func getLocalAgentSessionDetail(ctx context.Context, agentAPI *agent.API, sessionID, userID string) (*agentSessionDetail, error) {
	detail, err := agentAPI.GetSessionDetail(ctx, sessionID, userID)
	if err != nil {
		return nil, err
	}
	out := &agentSessionDetail{
		Messages: agentMessagesFromLocal(detail.Messages),
	}
	if detail.SessionState != nil {
		out.Working = detail.SessionState.Working
		out.HasPendingPrompt = detail.SessionState.HasPendingPrompt
	}
	return out, nil
}

func agentMessagesFromLocal(messages []agent.Message) []agentMessageRow {
	out := make([]agentMessageRow, 0, len(messages))
	for _, msg := range messages {
		row := agentMessageRow{
			ID:      msg.ID,
			Type:    string(msg.Type),
			Content: msg.Content,
		}
		if msg.UserPrompt != nil {
			row.UserPrompt = &agentPromptRow{
				ID:            msg.UserPrompt.PromptID,
				Question:      msg.UserPrompt.Question,
				Options:       localPromptOptions(msg.UserPrompt.Options),
				AllowFreeText: msg.UserPrompt.AllowFreeText,
				MultiSelect:   msg.UserPrompt.MultiSelect,
				PromptType:    string(msg.UserPrompt.PromptType),
			}
		}
		out = append(out, row)
	}
	return out
}

func localPromptOptions(options []agent.UserPromptOption) []agentPromptOptionRow {
	out := make([]agentPromptOptionRow, 0, len(options))
	for _, opt := range options {
		out = append(out, agentPromptOptionRow{
			ID:          opt.ID,
			Label:       opt.Label,
			Description: opt.Description,
		})
	}
	return out
}

func agentMessagesFromAPI(messages []api.AgentMessage) []agentMessageRow {
	out := make([]agentMessageRow, 0, len(messages))
	for _, msg := range messages {
		row := agentMessageRow{
			ID:   msg.Id,
			Type: string(msg.Type),
		}
		if msg.Content != nil {
			row.Content = *msg.Content
		}
		if msg.UserPrompt != nil {
			promptType := ""
			if msg.UserPrompt.PromptType != nil {
				promptType = string(*msg.UserPrompt.PromptType)
			}
			row.UserPrompt = &agentPromptRow{
				ID:            msg.UserPrompt.PromptId,
				Question:      msg.UserPrompt.Question,
				Options:       apiPromptOptions(msg.UserPrompt.Options),
				AllowFreeText: msg.UserPrompt.AllowFreeText,
				MultiSelect:   msg.UserPrompt.MultiSelect,
				PromptType:    promptType,
			}
		}
		out = append(out, row)
	}
	return out
}

func apiPromptOptions(options *[]api.AgentUserPromptOption) []agentPromptOptionRow {
	if options == nil {
		return nil
	}
	out := make([]agentPromptOptionRow, 0, len(*options))
	for _, opt := range *options {
		row := agentPromptOptionRow{
			ID:    opt.Id,
			Label: opt.Label,
		}
		if opt.Description != nil {
			row.Description = *opt.Description
		}
		out = append(out, row)
	}
	return out
}

func agentRowsFromLocal(sessions []agent.SessionWithState) []agentSessionRow {
	out := make([]agentSessionRow, 0, len(sessions))
	for _, item := range sessions {
		out = append(out, agentSessionRow{
			ID:        item.Session.ID,
			Title:     item.Session.Title,
			Model:     item.Model,
			UpdatedAt: item.Session.UpdatedAt,
			Working:   item.Working,
			TotalCost: item.TotalCost,
		})
	}
	return out
}

func agentRowsFromAPI(sessions []api.AgentSessionWithState) []agentSessionRow {
	out := make([]agentSessionRow, 0, len(sessions))
	for _, item := range sessions {
		row := agentSessionRow{
			ID:        item.Session.Id,
			UpdatedAt: item.Session.UpdatedAt,
			Working:   item.Working,
			TotalCost: item.TotalCost,
		}
		if item.Session.Title != nil {
			row.Title = *item.Session.Title
		}
		if item.Model != nil {
			row.Model = *item.Model
		}
		out = append(out, row)
	}
	return out
}

func renderAgentSessions(out io.Writer, sessions []agentSessionRow) error {
	if len(sessions) == 0 {
		_, err := fmt.Fprintln(out, "No agent sessions found.")
		return err
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "SESSION ID\tUPDATED\tSTATUS\tMODEL\tTITLE"); err != nil {
		return err
	}
	for _, sess := range sessions {
		status := "idle"
		if sess.Working {
			status = "working"
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			sess.ID,
			sess.UpdatedAt.Format(time.RFC3339),
			status,
			sess.Model,
			sess.Title,
		); err != nil {
			return err
		}
	}
	return w.Flush()
}

func renderAgentTranscript(out io.Writer, messages []agentMessageRow) error {
	if len(messages) == 0 {
		_, err := fmt.Fprintln(out, "No messages found.")
		return err
	}
	for _, msg := range messages {
		if err := renderAgentMessage(out, msg); err != nil {
			return err
		}
	}
	return nil
}

func followAgentSession(ctx context.Context, out io.Writer, fetch func(context.Context) (*agentSessionDetail, error)) error {
	_, err := followAgentSessionWithSeen(ctx, out, map[string]struct{}{}, fetch)
	return err
}

func followAgentSessionNonInteractive(ctx context.Context, out io.Writer, sessionID string, fetch func(context.Context) (*agentSessionDetail, error)) error {
	detail, err := followAgentSessionWithSeen(ctx, out, map[string]struct{}{}, fetch)
	if err != nil {
		return err
	}
	return renderAgentPendingPromptHint(out, sessionID, detail)
}

func renderAgentPendingPromptHint(out io.Writer, sessionID string, detail *agentSessionDetail) error {
	if detail == nil || !detail.HasPendingPrompt {
		return nil
	}
	_, err := fmt.Fprintf(out, "\nPending input required; run `dagu agent resume %s` to respond.\n", sessionID)
	return err
}

func followAgentSessionWithSeen(ctx context.Context, out io.Writer, seen map[string]struct{}, fetch func(context.Context) (*agentSessionDetail, error)) (*agentSessionDetail, error) {
	if seen == nil {
		seen = map[string]struct{}{}
	}
	ticker := time.NewTicker(agentPollInterval)
	defer ticker.Stop()

	for {
		detail, err := fetch(ctx)
		if err != nil {
			return nil, err
		}
		for _, msg := range detail.Messages {
			if _, ok := seen[msg.ID]; ok {
				continue
			}
			seen[msg.ID] = struct{}{}
			if err := renderAgentMessage(out, msg); err != nil {
				return nil, err
			}
		}
		if !detail.Working || detail.HasPendingPrompt {
			return detail, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func renderAgentMessage(out io.Writer, msg agentMessageRow) error {
	switch msg.Type {
	case string(agent.MessageTypeUser):
		return nil
	case string(agent.MessageTypeAssistant):
		if strings.TrimSpace(msg.Content) == "" {
			return nil
		}
		_, err := fmt.Fprintf(out, "\n%s\n", strings.TrimSpace(msg.Content))
		return err
	case string(agent.MessageTypeError):
		if strings.TrimSpace(msg.Content) == "" {
			return nil
		}
		_, err := fmt.Fprintf(out, "\nError:\n%s\n", strings.TrimSpace(msg.Content))
		return err
	case string(agent.MessageTypeUserPrompt):
		return renderAgentPrompt(out, msg.UserPrompt)
	default:
		return nil
	}
}

func renderAgentPrompt(out io.Writer, prompt *agentPromptRow) error {
	if prompt == nil {
		return nil
	}
	if _, err := fmt.Fprintf(out, "\nPrompt:\n%s\n", prompt.Question); err != nil {
		return err
	}
	for _, option := range prompt.Options {
		label := option.Label
		if option.Description != "" {
			label = fmt.Sprintf("%s - %s", option.Label, option.Description)
		}
		if _, err := fmt.Fprintf(out, "- %s\n", label); err != nil {
			return err
		}
	}
	return nil
}

func runAgentInteractive(ctx *Context, sessionID string) error {
	out := ctx.Command.OutOrStdout()
	in := ctx.Command.InOrStdin()
	seen := map[string]struct{}{}

	var (
		localAPI *agent.API
		user     = localAgentUser()
	)
	getLocalAPI := func() (*agent.API, error) {
		if localAPI != nil {
			return localAPI, nil
		}
		agentAPI, err := ctx.newAgentAPI()
		if err != nil {
			return nil, err
		}
		localAPI = agentAPI
		return localAPI, nil
	}
	fetch := func(context.Context) (*agentSessionDetail, error) {
		if sessionID == "" {
			return &agentSessionDetail{}, nil
		}
		if ctx.IsRemote() {
			return ctx.Remote.getAgentSessionDetail(ctx, sessionID)
		}
		agentAPI, err := getLocalAPI()
		if err != nil {
			return nil, err
		}
		return getLocalAgentSessionDetail(ctx, agentAPI, sessionID, user.UserID)
	}

	fmt.Fprintln(out, "Dagu agent interactive. Type /exit or press Ctrl-D to quit.")

	var pending *agentPromptRow
	if sessionID != "" {
		detail, err := followAgentSessionWithSeen(ctx, out, seen, fetch)
		if err != nil {
			return err
		}
		pending = latestAgentPrompt(detail)
	}

	reader := bufio.NewReader(in)
	for {
		if pending != nil {
			fmt.Fprint(out, "\nresponse> ")
		} else {
			fmt.Fprint(out, "\n> ")
		}
		line, readErr := reader.ReadString('\n')
		if readErr != nil && len(line) == 0 {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
		input := strings.TrimSpace(line)
		if input == "" {
			if readErr == io.EOF {
				return nil
			}
			continue
		}
		if isAgentExitInput(input) {
			return nil
		}

		if pending != nil {
			if err := respondAgentPrompt(ctx, sessionID, pending, input, getLocalAPI, user); err != nil {
				fmt.Fprintf(out, "Error: %v\n", err)
				continue
			}
		} else if sessionID == "" {
			req, err := buildAgentChatRequest(ctx, input)
			if err != nil {
				return err
			}
			if ctx.IsRemote() {
				resp, err := ctx.Remote.createAgentSession(ctx, req)
				if err != nil {
					return err
				}
				sessionID = resp.SessionId
			} else {
				agentAPI, err := getLocalAPI()
				if err != nil {
					return err
				}
				createdID, _, err := agentAPI.CreateSession(ctx, user, toAgentChatRequest(req))
				if err != nil {
					return err
				}
				sessionID = createdID
			}
		} else {
			req, err := buildAgentChatRequest(ctx, input)
			if err != nil {
				return err
			}
			if ctx.IsRemote() {
				if err := ctx.Remote.sendAgentMessage(ctx, sessionID, req); err != nil {
					return err
				}
			} else {
				agentAPI, err := getLocalAPI()
				if err != nil {
					return err
				}
				if err := agentAPI.SendMessage(ctx, sessionID, user, toAgentChatRequest(req)); err != nil {
					return err
				}
			}
		}

		detail, err := followAgentSessionWithSeen(ctx, out, seen, fetch)
		if err != nil {
			return err
		}
		pending = latestAgentPrompt(detail)
		if readErr == io.EOF {
			return nil
		}
	}
}

func isAgentExitInput(input string) bool {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "/exit", "exit", "/quit", "quit":
		return true
	default:
		return false
	}
}

func latestAgentPrompt(detail *agentSessionDetail) *agentPromptRow {
	if detail == nil || !detail.HasPendingPrompt {
		return nil
	}
	for i := len(detail.Messages) - 1; i >= 0; i-- {
		msg := detail.Messages[i]
		if msg.Type == string(agent.MessageTypeUserPrompt) && msg.UserPrompt != nil {
			return msg.UserPrompt
		}
	}
	return nil
}

func respondAgentPrompt(
	ctx *Context,
	sessionID string,
	prompt *agentPromptRow,
	input string,
	getLocalAPI func() (*agent.API, error),
	user agent.UserIdentity,
) error {
	resp, err := buildAgentPromptResponse(prompt, input)
	if err != nil {
		return err
	}
	if ctx.IsRemote() {
		return ctx.Remote.respondAgentPrompt(ctx, sessionID, toAPIAgentPromptResponse(resp))
	}
	agentAPI, err := getLocalAPI()
	if err != nil {
		return err
	}
	return agentAPI.SubmitUserResponse(ctx, sessionID, user.UserID, resp)
}

func buildAgentPromptResponse(prompt *agentPromptRow, input string) (agent.UserPromptResponse, error) {
	if prompt == nil || prompt.ID == "" {
		return agent.UserPromptResponse{}, fmt.Errorf("agent prompt is missing an ID")
	}
	resp := agent.UserPromptResponse{PromptID: prompt.ID}
	if strings.EqualFold(input, "/skip") || strings.EqualFold(input, "skip") {
		resp.Cancelled = true
		return resp, nil
	}
	selected, unmatched := matchAgentPromptOptions(prompt, input)
	if len(selected) > 0 && len(unmatched) == 0 {
		resp.SelectedOptionIDs = selected
		return resp, nil
	}
	if len(prompt.Options) > 0 && !prompt.AllowFreeText {
		return agent.UserPromptResponse{}, fmt.Errorf("choose one of: %s", strings.Join(agentPromptOptionLabels(prompt.Options), ", "))
	}
	resp.FreeTextResponse = input
	return resp, nil
}

func matchAgentPromptOptions(prompt *agentPromptRow, input string) ([]string, []string) {
	if len(prompt.Options) == 0 {
		return nil, []string{input}
	}
	parts := []string{input}
	if prompt.MultiSelect {
		parts = strings.Split(input, ",")
	}
	selected := make([]string, 0, len(parts))
	var unmatched []string
	for _, part := range parts {
		token := strings.ToLower(strings.TrimSpace(part))
		if token == "" {
			continue
		}
		matched := false
		for _, opt := range prompt.Options {
			if token == strings.ToLower(opt.ID) || token == strings.ToLower(opt.Label) {
				selected = append(selected, opt.ID)
				matched = true
				break
			}
		}
		if !matched {
			unmatched = append(unmatched, part)
		}
	}
	return selected, unmatched
}

func agentPromptOptionLabels(options []agentPromptOptionRow) []string {
	labels := make([]string, 0, len(options))
	for _, opt := range options {
		labels = append(labels, opt.Label)
	}
	return labels
}

func toAPIAgentPromptResponse(resp agent.UserPromptResponse) api.AgentUserPromptResponse {
	out := api.AgentUserPromptResponse{
		PromptId: resp.PromptID,
	}
	if len(resp.SelectedOptionIDs) > 0 {
		out.SelectedOptionIds = &resp.SelectedOptionIDs
	}
	if resp.FreeTextResponse != "" {
		out.FreeTextResponse = &resp.FreeTextResponse
	}
	if resp.Cancelled {
		out.Cancelled = &resp.Cancelled
	}
	return out
}

func (c *remoteClient) createAgentSession(ctx context.Context, body api.AgentChatRequest) (*api.CreateAgentSessionResponse, error) {
	var out api.CreateAgentSessionResponse
	if err := c.do(ctx, http.MethodPost, "/agent/sessions", body, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) sendAgentMessage(ctx context.Context, sessionID string, body api.AgentChatRequest) error {
	return c.do(ctx, http.MethodPost, "/agent/sessions/"+url.PathEscape(sessionID)+"/chat", body, nil, nil)
}

func (c *remoteClient) respondAgentPrompt(ctx context.Context, sessionID string, body api.AgentUserPromptResponse) error {
	return c.do(ctx, http.MethodPost, "/agent/sessions/"+url.PathEscape(sessionID)+"/respond", body, nil, nil)
}

func (c *remoteClient) listAgentSessions(ctx context.Context, page, perPage int) (*api.ListAgentSessionsResponse, error) {
	var out api.ListAgentSessionsResponse
	params := map[string]string{
		"page":    fmt.Sprintf("%d", page),
		"perPage": fmt.Sprintf("%d", perPage),
	}
	if err := c.do(ctx, http.MethodGet, "/agent/sessions", nil, &out, params); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) getAgentSessionDetail(ctx context.Context, sessionID string) (*agentSessionDetail, error) {
	var out api.AgentSessionDetailResponse
	if err := c.do(ctx, http.MethodGet, "/agent/sessions/"+url.PathEscape(sessionID), nil, &out, nil); err != nil {
		return nil, err
	}
	detail := &agentSessionDetail{
		Messages: agentMessagesFromAPI(out.Messages),
		Working:  out.SessionState.Working,
	}
	if out.SessionState.HasPendingPrompt != nil {
		detail.HasPendingPrompt = *out.SessionState.HasPendingPrompt
	}
	return detail, nil
}
