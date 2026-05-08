// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/internal/llm"
)

const (
	sessionSearchToolName           = "session_search"
	defaultSessionSearchLimit       = 5
	maxSessionSearchLimit           = 20
	sessionSearchScanMultiplier     = 20
	maxSessionSearchSnippets        = 3
	sessionSearchSnippetRunesBefore = 80
	sessionSearchSnippetRunesAfter  = 140
)

func init() {
	RegisterTool(ToolRegistration{
		Name:           sessionSearchToolName,
		Label:          "Session Search",
		Description:    "Search past agent session transcripts",
		DefaultEnabled: true,
		Factory:        func(_ ToolConfig) *AgentTool { return NewSessionSearchTool() },
	})
}

// SessionSearchInput defines the input parameters for the session_search tool.
type SessionSearchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty" lenient:"true"`
}

type sessionSearchResult struct {
	session       *Session
	metadataMatch bool
	snippets      []sessionSearchSnippet
}

type sessionSearchSnippet struct {
	messageType MessageType
	sequenceID  int64
	createdAt   time.Time
	content     string
}

// NewSessionSearchTool creates a tool for searching persisted agent sessions.
func NewSessionSearchTool() *AgentTool {
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        sessionSearchToolName,
				Description: "Search past persisted agent session transcripts for the current user. Use this when the user references earlier conversations or prior task context.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Case-insensitive text to search for in prior session titles, DAG names, and transcript messages",
						},
						"limit": map[string]any{
							"type":        "integer",
							"description": "Maximum number of sessions to return (default 5, max 20)",
						},
					},
					"required": []string{"query"},
				},
			},
		},
		Run: sessionSearchRun,
		Audit: &AuditInfo{
			Action:          "session_search",
			DetailExtractor: ExtractFields("query", "limit"),
		},
	}
}

func sessionSearchRun(ctx ToolContext, input json.RawMessage) ToolOut {
	var args SessionSearchInput
	if err := decodeToolInput(input, &args); err != nil {
		return toolError("Failed to parse input: %v", err)
	}

	query := strings.TrimSpace(args.Query)
	if query == "" {
		return toolError("Query is required")
	}
	if ctx.SessionStore == nil {
		return toolError("Session store is not available")
	}
	userID := strings.TrimSpace(ctx.User.UserID)
	if userID == "" {
		return toolError("User context is required")
	}

	callCtx := ctx.Context
	if callCtx == nil {
		callCtx = context.Background()
	}

	sessions, err := ctx.SessionStore.ListSessions(callCtx, userID)
	if err != nil {
		return toolError("Failed to list sessions: %v", err)
	}

	limit := normalizeSessionSearchLimit(args.Limit)
	candidates := prepareSessionSearchCandidates(sessions, limit)
	results := make([]sessionSearchResult, 0, limit)
	queryLower := strings.ToLower(query)
	for _, sess := range candidates {
		if sess == nil || sess.ID == "" || sess.ID == ctx.SessionID {
			continue
		}

		result, err := searchSessionMessages(callCtx, ctx.SessionStore, sess, query, queryLower)
		if err != nil {
			return toolError("Failed to search session %s: %v", sess.ID, err)
		}
		if !result.metadataMatch && len(result.snippets) == 0 {
			continue
		}

		results = append(results, result)
		if len(results) >= limit {
			break
		}
	}

	if len(results) == 0 {
		return ToolOut{Content: fmt.Sprintf("No matching past sessions found for %q.", query)}
	}

	return ToolOut{Content: formatSessionSearchResults(query, results)}
}

func normalizeSessionSearchLimit(limit int) int {
	if limit <= 0 {
		return defaultSessionSearchLimit
	}
	return min(limit, maxSessionSearchLimit)
}

func prepareSessionSearchCandidates(sessions []*Session, limit int) []*Session {
	candidates := append([]*Session(nil), sessions...)
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left == nil {
			return false
		}
		if right == nil {
			return true
		}
		return left.UpdatedAt.After(right.UpdatedAt)
	})

	maxCandidates := limit * sessionSearchScanMultiplier
	if maxCandidates > 0 && len(candidates) > maxCandidates {
		return candidates[:maxCandidates]
	}
	return candidates
}

func searchSessionMessages(ctx context.Context, store SessionStore, sess *Session, query, queryLower string) (sessionSearchResult, error) {
	result := sessionSearchResult{
		session:       sess,
		metadataMatch: sessionMetadataMatches(sess, queryLower),
	}

	messages, err := store.GetMessages(ctx, sess.ID)
	if err != nil {
		return result, err
	}

	for _, msg := range messages {
		text := sessionSearchMessageText(msg)
		if text == "" {
			continue
		}
		matchRuneIndex := sessionSearchIndex(text, queryLower)
		if matchRuneIndex < 0 {
			continue
		}
		result.snippets = append(result.snippets, sessionSearchSnippet{
			messageType: msg.Type,
			sequenceID:  msg.SequenceID,
			createdAt:   msg.CreatedAt,
			content:     sessionSearchSnippetText(text, query, matchRuneIndex),
		})
		if len(result.snippets) >= maxSessionSearchSnippets {
			break
		}
	}

	return result, nil
}

func sessionMetadataMatches(sess *Session, queryLower string) bool {
	fields := []string{sess.ID, sess.Title, sess.DAGName, sess.ParentSessionID, sess.DelegateTask}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), queryLower) {
			return true
		}
	}
	return false
}

func sessionSearchMessageText(msg Message) string {
	parts := make([]string, 0, 2+len(msg.ToolResults))
	if msg.Content != "" {
		parts = append(parts, msg.Content)
	}
	if msg.UserPrompt != nil {
		parts = append(parts, msg.UserPrompt.Question)
	}
	for _, result := range msg.ToolResults {
		if result.Content != "" {
			parts = append(parts, result.Content)
		}
	}
	return strings.Join(parts, "\n")
}

func sessionSearchIndex(text, queryLower string) int {
	if queryLower == "" {
		return -1
	}
	lowerText := strings.ToLower(text)
	before, _, ok := strings.Cut(lowerText, queryLower)
	if !ok {
		return -1
	}
	return utf8.RuneCountInString(before)
}

func sessionSearchSnippetText(text, query string, matchRuneIndex int) string {
	queryRunes := len([]rune(query))
	runes := []rune(text)

	start := max(0, matchRuneIndex-sessionSearchSnippetRunesBefore)
	end := min(len(runes), matchRuneIndex+queryRunes+sessionSearchSnippetRunesAfter)

	snippet := strings.Join(strings.Fields(string(runes[start:end])), " ")
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(runes) {
		snippet += "..."
	}
	return snippet
}

func formatSessionSearchResults(query string, results []sessionSearchResult) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Found %d matching past session(s) for %q:\n", len(results), query)
	for i, result := range results {
		sess := result.session
		fmt.Fprintf(&out, "\n%d. session_id: %s", i+1, sess.ID)
		if sess.Title != "" {
			fmt.Fprintf(&out, " | title: %s", sess.Title)
		}
		if sess.DAGName != "" {
			fmt.Fprintf(&out, " | dag: %s", sess.DAGName)
		}
		if sess.ParentSessionID != "" {
			fmt.Fprintf(&out, " | parent: %s", sess.ParentSessionID)
		}
		if !sess.UpdatedAt.IsZero() {
			fmt.Fprintf(&out, " | updated: %s", sess.UpdatedAt.Format(time.RFC3339))
		}
		out.WriteByte('\n')
		if result.metadataMatch && len(result.snippets) == 0 {
			out.WriteString("   - matched session metadata\n")
		}
		for _, snippet := range result.snippets {
			fmt.Fprintf(&out, "   - %s", sessionSearchMessageType(snippet.messageType))
			if snippet.sequenceID > 0 {
				fmt.Fprintf(&out, " seq=%d", snippet.sequenceID)
			}
			if !snippet.createdAt.IsZero() {
				fmt.Fprintf(&out, " at=%s", snippet.createdAt.Format(time.RFC3339))
			}
			fmt.Fprintf(&out, ": %s\n", snippet.content)
		}
	}
	return out.String()
}

func sessionSearchMessageType(messageType MessageType) string {
	if messageType == "" {
		return "message"
	}
	return string(messageType)
}
