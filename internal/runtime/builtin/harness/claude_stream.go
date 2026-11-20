// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentrun"
)

// Spec 091 reads the stream of `claude --output-format stream-json`: one JSON
// object for each line. Dagu ignores a line that is not JSON.

// claudeEvent is the part of a stream object that Dagu reads.
type claudeEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	// MCPServers is the status of each MCP server in the event system/init.
	MCPServers []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`
	// Error is the error of an assistant message.
	Error   string `json:"error"`
	Message struct {
		Content json.RawMessage `json:"content"`
		Error   string          `json:"error"`
	} `json:"message"`
	IsError          bool            `json:"is_error"`
	Result           *string         `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	APIErrorStatus   *int            `json:"api_error_status"`
}

// claudeStream collects what Dagu needs from the lines that the CLI wrote. It
// implements io.Writer, so it can read the stream while the process runs.
type claudeStream struct {
	partial []byte
	// skipping is true while Write drops a line over maxStreamLine.
	skipping bool
	// mcpServers maps the name of each MCP server of the event system/init to
	// its status. It is nil when the stream has no such event.
	mcpServers map[string]string
	// toolCalls are the normalized names of the tool_use blocks.
	toolCalls []string
	// lastAssistantError is the error of the last assistant message.
	lastAssistantError string
	// result is the last message of the type result.
	result *claudeEvent
	// lines counts the objects that Dagu read.
	lines int
}

// maxStreamLine is the longest line of the stream that Dagu reads. A longer line
// is dropped up to its line end, so a CLI that writes no line end cannot fill
// the memory of the worker.
const maxStreamLine = 64 << 20

// Write reads the complete lines of p and keeps the rest for the next call.
func (s *claudeStream) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		if s.skipping {
			if end < 0 {
				return n, nil
			}
			s.skipping = false
			p = p[end+1:]
			continue
		}
		if end < 0 {
			if len(s.partial)+len(p) > maxStreamLine {
				s.partial, s.skipping = nil, true
				return n, nil
			}
			s.partial = append(s.partial, p...)
			return n, nil
		}
		if len(s.partial)+end <= maxStreamLine {
			s.readLine(append(s.partial, p[:end]...))
		}
		s.partial = s.partial[:0]
		p = p[end+1:]
	}
	return n, nil
}

// Flush reads a last line that has no line end.
func (s *claudeStream) Flush() {
	if len(bytes.TrimSpace(s.partial)) > 0 {
		s.readLine(s.partial)
	}
	s.partial, s.skipping = nil, false
}

func (s *claudeStream) readLine(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return
	}
	var event claudeEvent
	if err := json.Unmarshal(line, &event); err != nil {
		return
	}
	s.lines++
	switch event.Type {
	case "system":
		if event.Subtype == "init" {
			s.mcpServers = make(map[string]string, len(event.MCPServers))
			for _, server := range event.MCPServers {
				s.mcpServers[server.Name] = server.Status
			}
		}
	case "assistant":
		s.lastAssistantError = firstNonEmpty(event.Error, event.Message.Error)
		s.readToolUses(event.Message.Content)
	case "result":
		copied := event
		s.result = &copied
	}
}

// readToolUses lists the tool_use blocks of an assistant message in order. The
// tool StructuredOutput carries the typed result and is not a call of the agent.
func (s *claudeStream) readToolUses(content json.RawMessage) {
	if len(content) == 0 || content[0] != '[' {
		return
	}
	var blocks []struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return
	}
	for _, block := range blocks {
		if block.Type != "tool_use" || block.Name == agentrun.StructuredOutputTool {
			continue
		}
		s.toolCalls = append(s.toolCalls, agentrun.NormalizeToolName(block.Name))
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// claudeOutcome is the class of one attempt with claude.
type claudeOutcome struct {
	kind ir.AgentFailureKind
	// reason is a short key such as max_tool_iterations, no_result or schema_mismatch.
	reason string
	// exitCode is the exit code of the process.
	exitCode int
	// detail is a short text for the error, without the answer of the agent.
	detail string
}

// classifyClaude applies the table of Spec 091, "Detection with claude". Dagu uses the final result and the last assistant message. It
// does not use the events system/api_retry, because the CLI can recover from
// them. A text test is a fallback when the structured fields are absent.
func classifyClaude(exitCode int, stream *claudeStream) claudeOutcome {
	outcome := claudeOutcome{kind: ir.AgentKindError, exitCode: exitCode}
	result := stream.result

	if result == nil {
		outcome.reason = "no_result"
		outcome.detail = "the stream has no result message"
		if exitCode != 0 {
			outcome.reason = "exit"
			outcome.detail = "the process exited with the code " + strconv.Itoa(exitCode) + " and the stream has no result message"
		}
		return outcome
	}

	if result.IsError {
		switch {
		case stream.lastAssistantError == "authentication_failed" || statusIs(result.APIErrorStatus, 401):
			outcome.kind = ir.AgentKindAuthRequired
		case stream.lastAssistantError == "rate_limit" || statusIs(result.APIErrorStatus, 429):
			outcome.kind = ir.AgentKindRateLimited
		case stream.lastAssistantError == "" && result.APIErrorStatus == nil:
			// The structured fields are absent: read the text of the result.
			outcome.kind = kindFromText(result.Result)
		}
		if outcome.kind != ir.AgentKindError {
			outcome.reason = string(outcome.kind)
			return outcome
		}
	}

	switch {
	case result.Subtype == "success" && !result.IsError && exitCode == 0:
		outcome.kind = ir.AgentKindOK
	case result.Subtype == "error_max_turns":
		outcome.reason = "max_tool_iterations"
		outcome.detail = "the CLI reached the limit max_tool_iterations (--max-turns) before an answer"
	case result.Subtype == "error_max_structured_output_retries":
		outcome.reason = string(agentrun.ReasonSchemaMismatch)
		outcome.detail = "the CLI reports error_max_structured_output_retries"
	case result.Subtype == "error_max_budget_usd", result.Subtype == "error_during_execution":
		outcome.reason = result.Subtype
		outcome.detail = "the CLI ended with " + result.Subtype
	case result.Subtype == "success" && exitCode != 0:
		outcome.reason = "exit"
		outcome.detail = "the CLI reported success and exited with the code " + strconv.Itoa(exitCode)
	default:
		outcome.reason = firstNonEmpty(result.Subtype, "error")
		outcome.detail = "the CLI ended with an error result"
	}
	return outcome
}

func statusIs(status *int, want int) bool { return status != nil && *status == want }

// kindFromText is the fallback of Spec 091 for an error result with no
// structured field. The texts are to confirm; Dagu reads the two that Spec 091
// names.
func kindFromText(text *string) ir.AgentFailureKind {
	if text == nil {
		return ir.AgentKindError
	}
	lower := strings.ToLower(*text)
	switch {
	case strings.Contains(lower, "/login"):
		return ir.AgentKindAuthRequired
	case strings.Contains(lower, "hit your") && strings.Contains(lower, "limit"):
		return ir.AgentKindRateLimited
	default:
		return ir.AgentKindError
	}
}
