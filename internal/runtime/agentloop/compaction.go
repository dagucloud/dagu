// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentloop

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

const observationSummaryDetailBytes = 240

type decisionReference struct {
	turn int
	tool string
}

// LatestPromptTokens returns the most recent prompt size reported by the
// provider. Zero means no decision reported usage.
func (s *State) LatestPromptTokens() int {
	for _, msg := range slices.Backward(s.messages) {
		if msg.Role == ir.LLMRoleAssistant && msg.Metadata != nil && msg.Metadata.PromptTokens > 0 {
			return msg.Metadata.PromptTokens
		}
	}
	return 0
}

// EnableObservationAging keeps old observations compacted for subsequent
// decisions and across suspension or retry.
func (s *State) EnableObservationAging() {
	s.ObservationAging = true
}

// CompactObservations replaces all but the newest keepRecent tool results with
// deterministic one-line summaries. Positive maxBytes values also bound each
// summary. Assistant tool calls and result IDs remain unchanged so the
// conversation continues to satisfy provider tool protocols. Zero keepRecent
// disables compaction.
func (s *State) CompactObservations(keepRecent, maxBytes int) int {
	if keepRecent < 1 {
		return 0
	}
	return s.compactObservations(keepRecent, maxBytes, false)
}

// CompactAllObservations replaces each tool result whose deterministic summary
// is smaller. It returns the number replaced so callers can avoid retrying an
// unchanged request.
func (s *State) CompactAllObservations(maxBytes int) int {
	return s.compactObservations(0, maxBytes, true)
}

func (s *State) compactObservations(keepRecent, maxBytes int, onlyIfSmaller bool) int {
	var toolIndices []int
	for i, msg := range s.messages {
		if msg.Role == ir.LLMRoleTool {
			toolIndices = append(toolIndices, i)
		}
	}
	compactCount := len(toolIndices) - keepRecent
	if compactCount <= 0 {
		return 0
	}

	references := decisionReferences(s.messages)
	eventsByCall := make(map[string]Event, len(s.Events))
	legacyEventsByTurn := make(map[int]Event, len(s.Events))
	for _, event := range s.Events {
		if event.ToolCallID != "" {
			eventsByCall[event.ToolCallID] = event
		}
		legacyEventsByTurn[event.Turn] = event
	}

	changed := 0
	for _, index := range toolIndices[:compactCount] {
		msg := &s.messages[index]
		ref := references[msg.ToolCallID]
		event, ok := eventsByCall[msg.ToolCallID]
		if !ok {
			event = legacyEventsByTurn[ref.turn]
		}
		summary := observationSummary(ref, event, msg.Content)
		if maxBytes > 0 {
			summary = stringutil.TruncUTF8Bytes(summary, maxBytes)
		}
		if onlyIfSmaller && len(summary) >= len(msg.Content) {
			continue
		}
		if msg.Content != summary {
			msg.Content = summary
			changed++
		}
	}
	return changed
}

func decisionReferences(messages []ir.LLMMessage) map[string]decisionReference {
	result := make(map[string]decisionReference)
	turn := 0
	for _, msg := range messages {
		if msg.Role != ir.LLMRoleAssistant {
			continue
		}
		turn++
		for _, call := range msg.ToolCalls {
			result[call.ID] = decisionReference{turn: turn, tool: call.Function.Name}
		}
	}
	return result
}

func observationSummary(ref decisionReference, event Event, content string) string {
	turn := ref.turn
	if event.Turn > 0 {
		turn = event.Turn
	}
	prefix := fmt.Sprintf("turn %d: ", turn)

	switch event.Kind {
	case EventAction:
		name := event.Name
		if name == "" {
			name = ref.tool
		}
		if event.Attempt > 1 {
			name += fmt.Sprintf(" (attempt %d)", event.Attempt)
		}
		return prefix + name + " → " + summaryOutcome(event.Status, event.Reason)
	case EventTaskStatus:
		return prefix + "task " + event.Name + " → " + summaryOutcome(event.Status, event.Reason)
	case EventAskUser:
		return prefix + eventName(event, ref.tool) + " → " + summaryOutcome(event.Status, "")
	case EventRejected:
		return prefix + eventName(event, ref.tool) + " → rejected" + summaryReason(event.Reason)
	}
	if isObservationSummary(content) {
		return content
	}

	name := ref.tool
	if name == "" {
		name = "tool"
	}
	outcome := strings.TrimSpace(strings.SplitN(content, "\n", 2)[0])
	outcome = strings.TrimPrefix(outcome, "status: ")
	if after, ok := strings.CutPrefix(outcome, "Error: "); ok {
		return prefix + name + " → rejected" + summaryReason(after)
	}
	if outcome == "" {
		outcome = "completed"
	}
	return prefix + name + " → " + compactSummaryText(outcome)
}

func isObservationSummary(content string) bool {
	if strings.Contains(content, "\n") {
		return false
	}
	rest, ok := strings.CutPrefix(content, "turn ")
	if !ok {
		return false
	}
	turn, rest, ok := strings.Cut(rest, ": ")
	if !ok || !strings.Contains(rest, " → ") {
		return false
	}
	value, err := strconv.Atoi(turn)
	return err == nil && value >= 0
}

func eventName(event Event, fallback string) string {
	if event.Name != "" {
		return event.Name
	}
	if fallback != "" {
		return fallback
	}
	return "tool"
}

func summaryOutcome(status, reason string) string {
	if status == "" {
		status = "completed"
	}
	return status + summaryReason(reason)
}

func summaryReason(reason string) string {
	reason = compactSummaryText(reason)
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}

func compactSummaryText(value string) string {
	return stringutil.TruncUTF8Bytes(strings.Join(strings.Fields(value), " "), observationSummaryDetailBytes)
}
