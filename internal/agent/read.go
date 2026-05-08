// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dagucloud/dagu/internal/llm"
)

func init() {
	RegisterTool(ToolRegistration{
		Name:           "read",
		Label:          "Read",
		Description:    "Read files",
		DefaultEnabled: true,
		Factory:        func(_ ToolConfig) *AgentTool { return NewReadTool() },
	})
}

const (
	maxReadSize      = 1024 * 1024
	defaultLineLimit = 2000
)

// ReadToolInput defines the input parameters for the read tool.
type ReadToolInput struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// NewReadTool creates a new read tool for file reading.
func NewReadTool() *AgentTool {
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "read",
				Description: "Read the contents of a file. Returns the file content with line numbers.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "The path to the file to read (absolute or relative to working directory)",
						},
						"offset": map[string]any{
							"type":        "integer",
							"description": "Optional line offset to start reading from (1-based)",
						},
						"limit": map[string]any{
							"type":        "integer",
							"description": "Optional maximum number of lines to read (default: 2000)",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		Run: readRun,
		Audit: &AuditInfo{
			Action:          "file_read",
			DetailExtractor: ExtractFields("path"),
		},
	}
}

func readRun(ctx ToolContext, input json.RawMessage) ToolOut {
	var args ReadToolInput
	if err := decodeToolInput(input, &args); err != nil {
		return toolError("Failed to parse input: %v", err)
	}

	if args.Path == "" {
		return toolError("Path is required")
	}

	path := resolvePath(args.Path, ctx.WorkingDir)

	if out, ok := validateReadableFile(path, args.Path); !ok {
		return out
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return toolError("Failed to read file: %v", err)
	}

	return formatFileContent(string(content), args.Offset, args.Limit)
}

func validateReadableFile(path, displayPath string) (ToolOut, bool) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return toolError("File not found: %s", displayPath), false
		}
		return toolError("Failed to access file: %v", err), false
	}

	if info.IsDir() {
		return toolError("%s is a directory, not a file. Use bash with 'ls' to list directory contents.", displayPath), false
	}

	if info.Size() > maxReadSize {
		return toolError("File too large (%d bytes). Maximum size is %d bytes. Use offset and limit to read portions.", info.Size(), maxReadSize), false
	}

	return ToolOut{}, true
}

func formatFileContent(content string, requestedOffset, requestedLimit int) ToolOut {
	lines := strings.Split(content, "\n")
	total := len(lines)

	// Convert 1-based offset to 0-based index
	start := max(0, requestedOffset-1)
	limit := defaultLineLimit
	if requestedLimit > 0 {
		limit = requestedLimit
	}

	if start >= total {
		return toolError("Offset %d is beyond file length (%d lines)", requestedOffset, total)
	}

	end := min(start+limit, total)

	var result strings.Builder
	for i, line := range lines[start:end] {
		fmt.Fprintf(&result, "%6d\t%s\n", start+i+1, line)
	}

	if remaining := total - end; remaining > 0 {
		fmt.Fprintf(&result, "\n... [%d more lines, use offset=%d to continue]", remaining, end+1)
	}

	return ToolOut{Content: result.String()}
}
