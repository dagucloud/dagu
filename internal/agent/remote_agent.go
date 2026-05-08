// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/llm"
	"github.com/google/uuid"
)

func init() {
	RegisterTool(ToolRegistration{
		Name:           "remote_agent",
		Label:          "Remote Agent",
		Description:    "Send tasks to AI agents through remote CLI contexts",
		DefaultEnabled: true,
		Factory: func(cfg ToolConfig) *AgentTool {
			if cfg.RemoteContextResolver == nil {
				return nil
			}
			return NewRemoteAgentTool(cfg.RemoteContextResolver)
		},
	})
}

const (
	// remoteAgentDefaultTimeout is the default timeout for remote agent operations.
	remoteAgentDefaultTimeout = 5 * time.Minute

	// remoteAgentConnectTimeout is the timeout for establishing connections.
	remoteAgentConnectTimeout = 10 * time.Second

	// remoteAgentResponseTimeout is the timeout for individual HTTP responses.
	remoteAgentResponseTimeout = 30 * time.Second

	// remoteAgentMaxConsecutiveFailures is the max consecutive poll failures before giving up.
	remoteAgentMaxConsecutiveFailures = 3

	// remoteAgentMaxResultLen is the max length of the result content.
	remoteAgentMaxResultLen = 10000

	// remoteAgentHeadLen is the number of characters to keep at the head when truncating.
	remoteAgentHeadLen = 500

	// remoteAgentPromptSummaryLen is the max length of a prompt summary in rejection reports.
	remoteAgentPromptSummaryLen = 200
)

// remotePollBackoff defines the exponential backoff policy for polling remote sessions.
var remotePollBackoff = &backoff.ExponentialBackoffPolicy{
	InitialInterval: 500 * time.Millisecond,
	BackoffFactor:   1.5,
	MaxInterval:     5 * time.Second,
	MaxRetries:      0, // Unlimited — exit is controlled by context timeout.
}

type remoteAgentInput struct {
	Context string `json:"context"`
	Message string `json:"message"`
}

// rejectedPrompt tracks a prompt that was auto-rejected during remote execution.
type rejectedPrompt struct {
	PromptType string
	Summary    string
}

// NewRemoteAgentTool creates a tool for sending tasks to AI agents through remote CLI contexts.
func NewRemoteAgentTool(resolver RemoteContextResolver) *AgentTool {
	contexts, err := resolver.ListRemoteContexts(context.Background())
	var contextNames []any
	var contextDesc string
	if err == nil && len(contexts) > 0 {
		names := make([]string, 0, len(contexts))
		for _, n := range contexts {
			contextNames = append(contextNames, n.Name)
			names = append(names, n.Name)
		}
		contextDesc = fmt.Sprintf(
			"Send a task to an AI agent through a remote CLI context. The remote agent runs in safe mode "+
				"(destructive commands are auto-rejected). Available contexts: %s",
			strings.Join(names, ", "),
		)
	} else {
		contextDesc = "Send a task to an AI agent through a remote CLI context. The remote agent runs in safe mode " +
			"(destructive commands are auto-rejected)."
	}

	contextProp := map[string]any{
		"type":        "string",
		"description": "Name of the remote CLI context to target",
	}
	if len(contextNames) > 0 {
		contextProp["enum"] = contextNames
	}

	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "remote_agent",
				Description: contextDesc,
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"context": contextProp,
						"message": map[string]any{
							"type":        "string",
							"description": "The task or message to send to the remote agent",
						},
					},
					"required": []any{"context", "message"},
				},
			},
		},
		Run: makeRemoteAgentRun(resolver),
		Audit: &AuditInfo{
			Action: "remote_agent_exec",
			DetailExtractor: func(input json.RawMessage) map[string]any {
				var raw map[string]any
				_ = json.Unmarshal(input, &raw)
				details := make(map[string]any)
				if v, ok := raw["context"]; ok {
					details["context"] = v
				}
				if v, ok := raw["message"]; ok {
					if s, ok := v.(string); ok {
						details["message_length"] = len(s)
					}
				}
				return details
			},
		},
	}
}

func makeRemoteAgentRun(resolver RemoteContextResolver) ToolFunc {
	return func(ctx ToolContext, input json.RawMessage) ToolOut {
		if ctx.Role.IsSet() && !ctx.Role.CanExecute() {
			return toolError("Permission denied: remote_agent requires execute permission")
		}

		var args remoteAgentInput
		if err := decodeToolInput(input, &args); err != nil {
			return toolError("Failed to parse input: %v", err)
		}

		if args.Context == "" {
			return toolError("Parameter 'context' is required")
		}
		if args.Message == "" {
			return toolError("Parameter 'message' is required")
		}

		// Resolve the target context.
		node, err := resolver.GetByName(ctx.Context, args.Context)
		if err != nil {
			available, listErr := resolver.ListRemoteContexts(ctx.Context)
			if errors.Is(err, ErrRemoteContextNotFound) && listErr == nil && len(available) > 0 {
				names := make([]string, 0, len(available))
				for _, n := range available {
					names = append(names, n.Name)
				}
				return toolError("Unknown context %q. Available contexts: %s", args.Context, strings.Join(names, ", "))
			}
			return toolError("Failed to resolve context %q: %v", args.Context, err)
		}

		// Compute overall timeout.
		timeout := node.Timeout
		if timeout == 0 {
			timeout = remoteAgentDefaultTimeout
		}
		deadlineCtx, cancel := context.WithTimeout(ctx.Context, timeout)
		defer cancel()

		// Create HTTP client for this node.
		client := newRemoteHTTPClient(node.SkipTLSVerify)

		// Generate idempotent session ID.
		sessionID := uuid.New().String()

		// Create remote session.
		if err := remoteCreateSession(deadlineCtx, client, &node, sessionID, args.Message); err != nil {
			if deadlineCtx.Err() != nil {
				return toolError("Remote agent on %q timed out during session creation (session: %s)", args.Context, sessionID)
			}
			return toolError("Failed to create remote session on %q (session: %s): %v", args.Context, sessionID, err)
		}

		// Poll for completion.
		result, rejected, err := remotePollSession(deadlineCtx, client, &node, sessionID)
		if err != nil {
			// On context cancellation or timeout, attempt best-effort cancel.
			if deadlineCtx.Err() != nil {
				remoteCancelSession(context.Background(), client, &node, sessionID)
				if ctx.Context.Err() != nil {
					return toolError("Remote agent on %q cancelled (session: %s)", args.Context, sessionID)
				}
				return toolError("Remote agent on %q timed out (session: %s)", args.Context, sessionID)
			}
			return toolError("Remote agent on %q failed (session: %s): %v", args.Context, sessionID, err)
		}

		if result == "" {
			return toolError("Remote agent completed but produced no output (session: %s)", sessionID)
		}

		// Truncate if too long.
		result = truncateResult(result)

		// Append prompt rejection summary if any.
		if len(rejected) > 0 {
			result = appendRejectionSummary(result, rejected)
		}

		return ToolOut{
			Content: result,
			AuditDetails: map[string]any{
				"remote_session_id": sessionID,
			},
		}
	}
}

// newRemoteHTTPClient creates an HTTP client for remote node communication.
func newRemoteHTTPClient(skipTLSVerify bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if skipTLSVerify {
		transport.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, //nolint:gosec // User-configured per-node skip.
		}
	}
	transport.ResponseHeaderTimeout = remoteAgentResponseTimeout
	return &http.Client{
		Transport: transport,
		Timeout:   remoteAgentConnectTimeout + remoteAgentResponseTimeout,
	}
}

// remoteURL constructs a full API URL for the given context and path suffix.
func remoteURL(node *RemoteContextInfo, path string) string {
	return strings.TrimRight(node.APIBaseURL, "/") + path
}

// remoteDoRequest executes an authenticated HTTP request and checks the response status.
// On success (2xx), returns the response (caller must close Body). On failure, returns
// an error containing the status code and up to 1KB of the response body.
func remoteDoRequest(
	ctx context.Context, client *http.Client, node *RemoteContextInfo,
	method, path string, body io.Reader,
) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, remoteURL(node, path), body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	node.ApplyAuth(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	return resp, nil
}

// remoteSessionRequest is the POST body for creating a remote agent session.
type remoteSessionRequest struct {
	SessionID string `json:"sessionId"`
	Message   string `json:"message"`
	SafeMode  bool   `json:"safeMode"`
}

// remoteCreateSession creates a new agent session on the remote server.
func remoteCreateSession(ctx context.Context, client *http.Client, node *RemoteContextInfo, sessionID, message string) error {
	body, err := json.Marshal(remoteSessionRequest{
		SessionID: sessionID,
		Message:   message,
		SafeMode:  true,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	resp, err := remoteDoRequest(ctx, client, node, http.MethodPost, "/agent/sessions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// remoteSessionState represents the session state returned by the remote API.
// JSON tags use camelCase to match the Dagu REST API (generated from OpenAPI spec).
type remoteSessionState struct {
	Working          bool   `json:"working"`
	HasPendingPrompt bool   `json:"hasPendingPrompt"`
	SessionID        string `json:"sessionId"`
}

// remoteSessionDetail represents the session detail response.
type remoteSessionDetail struct {
	Session      json.RawMessage    `json:"session"`
	SessionState remoteSessionState `json:"sessionState"`
	Messages     []remoteMessage    `json:"messages"`
}

// remoteMessage represents a message from the remote session.
type remoteMessage struct {
	Type       string           `json:"type"`
	Content    string           `json:"content,omitempty"`
	UserPrompt *remotePromptRef `json:"userPrompt,omitempty"`
}

// remotePromptRef is a reference to a pending prompt on the remote session.
type remotePromptRef struct {
	PromptID   string `json:"promptId"`
	Question   string `json:"question"`
	PromptType string `json:"promptType,omitempty"`
}

// remotePollSession polls the remote session until completion or timeout.
// Returns the last assistant message content, any auto-rejected prompts, and an error.
func remotePollSession(
	ctx context.Context, client *http.Client, node *RemoteContextInfo, sessionID string,
) (string, []rejectedPrompt, error) {
	var (
		consecutiveFailures int
		rejected            []rejectedPrompt
		retryCount          int
	)

	for {
		// Compute backoff interval using the shared policy.
		waitDur, _ := remotePollBackoff.ComputeNextInterval(retryCount, 0, nil)

		select {
		case <-ctx.Done():
			return "", rejected, ctx.Err()
		case <-time.After(waitDur):
		}

		detail, err := remoteGetSession(ctx, client, node, sessionID)
		if err != nil {
			consecutiveFailures++
			if consecutiveFailures >= remoteAgentMaxConsecutiveFailures {
				return "", rejected, fmt.Errorf("failed to poll remote session after %d retries: %v",
					consecutiveFailures, err)
			}
			retryCount++
			continue
		}
		consecutiveFailures = 0

		// Handle pending prompt — auto-reject it.
		if detail.SessionState.HasPendingPrompt {
			promptType, summary := extractPromptInfo(detail)
			rejectErr := remoteRespondToPrompt(ctx, client, node, sessionID, detail)
			if rejectErr != nil {
				// Log warning but continue polling.
				_ = rejectErr
			}
			rejected = append(rejected, rejectedPrompt{
				PromptType: promptType,
				Summary:    summary,
			})
			retryCount = 0 // Reset backoff after prompt handling.
			continue
		}

		// Check if done.
		if !detail.SessionState.Working {
			content := extractLastAssistantContent(detail)
			return content, rejected, nil
		}

		retryCount++
	}
}

// remoteGetSession fetches the session detail from the remote node.
func remoteGetSession(
	ctx context.Context, client *http.Client, node *RemoteContextInfo, sessionID string,
) (*remoteSessionDetail, error) {
	path := fmt.Sprintf("/agent/sessions/%s", sessionID)
	resp, err := remoteDoRequest(ctx, client, node, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var detail remoteSessionDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &detail, nil
}

// remoteRespondToPrompt auto-rejects a pending prompt on the remote session.
func remoteRespondToPrompt(
	ctx context.Context, client *http.Client, node *RemoteContextInfo, sessionID string,
	detail *remoteSessionDetail,
) error {
	promptID := extractPromptID(detail)
	if promptID == "" {
		return fmt.Errorf("no promptId found in session detail")
	}

	body, err := json.Marshal(map[string]any{
		"promptId":  promptID,
		"cancelled": true,
	})
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	path := fmt.Sprintf("/agent/sessions/%s/respond", sessionID)
	resp, err := remoteDoRequest(ctx, client, node, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// remoteCancelSession sends a best-effort cancel to the remote session.
func remoteCancelSession(ctx context.Context, client *http.Client, node *RemoteContextInfo, sessionID string) {
	path := fmt.Sprintf("/agent/sessions/%s/cancel", sessionID)
	resp, err := remoteDoRequest(ctx, client, node, http.MethodPost, path, nil)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// extractPromptID finds the prompt_id from the last user_prompt message.
func extractPromptID(detail *remoteSessionDetail) string {
	for i := len(detail.Messages) - 1; i >= 0; i-- {
		msg := detail.Messages[i]
		if msg.Type == "user_prompt" && msg.UserPrompt != nil {
			return msg.UserPrompt.PromptID
		}
	}
	return ""
}

// extractPromptInfo extracts the prompt type and a summary from the last pending prompt.
func extractPromptInfo(detail *remoteSessionDetail) (promptType, summary string) {
	for i := len(detail.Messages) - 1; i >= 0; i-- {
		msg := detail.Messages[i]
		if msg.Type == "user_prompt" && msg.UserPrompt != nil {
			pt := msg.UserPrompt.PromptType
			if pt == "" {
				pt = "general"
			}
			s := msg.UserPrompt.Question
			if len(s) > remoteAgentPromptSummaryLen {
				s = s[:remoteAgentPromptSummaryLen]
			}
			return pt, s
		}
	}
	return "unknown", ""
}

// extractLastAssistantContent finds the last assistant message content.
func extractLastAssistantContent(detail *remoteSessionDetail) string {
	for i := len(detail.Messages) - 1; i >= 0; i-- {
		msg := detail.Messages[i]
		if msg.Type == "assistant" && msg.Content != "" {
			return msg.Content
		}
	}
	return ""
}

// truncateResult truncates content to fit within the max result length.
// Keeps head and tail with a truncation marker in the middle.
func truncateResult(s string) string {
	if len(s) <= remoteAgentMaxResultLen {
		return s
	}
	removed := len(s) - remoteAgentMaxResultLen
	marker := fmt.Sprintf("\n\n... [truncated %d chars] ...\n\n", removed)
	tailLen := max(remoteAgentMaxResultLen-remoteAgentHeadLen-len(marker), 0)
	return s[:remoteAgentHeadLen] + marker + s[len(s)-tailLen:]
}

// appendRejectionSummary appends auto-rejected prompt information to the result.
func appendRejectionSummary(result string, rejected []rejectedPrompt) string {
	var sb strings.Builder
	sb.WriteString(result)
	fmt.Fprintf(&sb, "\n\nNote: %d prompt(s) were auto-rejected during remote execution:", len(rejected))
	for _, r := range rejected {
		summary := r.Summary
		if len(summary) > remoteAgentPromptSummaryLen {
			summary = summary[:remoteAgentPromptSummaryLen] + "..."
		}
		fmt.Fprintf(&sb, "\n- [%s]: %s", r.PromptType, summary)
	}
	return sb.String()
}
