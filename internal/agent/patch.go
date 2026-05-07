// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/llm"
)

func init() {
	RegisterTool(ToolRegistration{
		Name:           "patch",
		Label:          "Patch",
		Description:    "Create/edit/delete files",
		DefaultEnabled: true,
		Factory:        func(cfg ToolConfig) *AgentTool { return NewPatchTool(cfg.DAGsDir) },
	})
}

const (
	dirPermission  = 0o750
	filePermission = 0o600
)

// PatchOperation defines the type of patch operation.
type PatchOperation string

const (
	PatchOpCreate       PatchOperation = "create"
	PatchOpReplace      PatchOperation = "replace"
	PatchOpAppend       PatchOperation = "append"
	PatchOpInsertBefore PatchOperation = "insert_before"
	PatchOpInsertAfter  PatchOperation = "insert_after"
	PatchOpDelete       PatchOperation = "delete"
)

// PatchToolInput is the input schema for the patch tool.
type PatchToolInput struct {
	Path      string         `json:"path"`
	Operation PatchOperation `json:"operation"`
	Content   string         `json:"content,omitempty"`    // For create, append, and insert operations
	OldString string         `json:"old_string,omitempty"` // For replace operation
	NewString string         `json:"new_string,omitempty"` // For replace operation
	Anchor    string         `json:"anchor,omitempty"`     // For insert operations
}

// NewPatchTool creates a new patch tool for file editing.
// The dagsDir parameter is used to auto-validate DAG files after write operations.
func NewPatchTool(dagsDir string) *AgentTool {
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "patch",
				Description: "Create, edit, or delete files. Use 'create' to write a full file, 'replace' only to replace an exact unique old_string, 'append' to add content at EOF, 'insert_before'/'insert_after' to add content around an exact unique anchor, or 'delete' to remove a file. Do not use replace to express an append.",
				Parameters:  patchToolParameters(),
			},
		},
		Run: func(ctx ToolContext, input json.RawMessage) ToolOut {
			return patchRun(ctx, input, dagsDir)
		},
		Audit: &AuditInfo{
			Action:          "file_patch",
			DetailExtractor: ExtractFields("path", "operation"),
		},
	}
}

func patchToolParameters() map[string]any {
	path := patchPathProperty()
	operation := map[string]any{
		"type":        "string",
		"enum":        []any{"create", "replace", "append", "insert_before", "insert_after", "delete"},
		"description": "The operation to perform. Each operation has its own required fields; do not include fields from another operation.",
	}
	content := map[string]any{
		"type":        "string",
		"description": "For create: full file content. For append and insert operations: content to add.",
	}
	oldString := map[string]any{
		"type":        "string",
		"description": "For replace only: exact unique text to find. Must be copied exactly from a prior read result.",
	}
	newString := map[string]any{
		"type":        "string",
		"description": "For replace only: replacement text. Use append or insert_after to add content instead.",
	}
	anchor := map[string]any{
		"type":        "string",
		"description": "For insert_before and insert_after only: exact unique text that must remain in the file.",
	}

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path":       path,
			"operation":  operation,
			"content":    content,
			"old_string": oldString,
			"new_string": newString,
			"anchor":     anchor,
		},
		"required": []any{"path", "operation"},
		"oneOf": []any{
			patchOperationSchema(PatchOpCreate, []any{"path", "operation", "content"}, map[string]any{
				"content": content,
			}),
			patchOperationSchema(PatchOpReplace, []any{"path", "operation", "old_string", "new_string"}, map[string]any{
				"old_string": oldString,
				"new_string": newString,
			}),
			patchOperationSchema(PatchOpAppend, []any{"path", "operation", "content"}, map[string]any{
				"content": content,
			}),
			patchOperationSchema(PatchOpInsertBefore, []any{"path", "operation", "anchor", "content"}, map[string]any{
				"anchor":  anchor,
				"content": content,
			}),
			patchOperationSchema(PatchOpInsertAfter, []any{"path", "operation", "anchor", "content"}, map[string]any{
				"anchor":  anchor,
				"content": content,
			}),
			patchOperationSchema(PatchOpDelete, []any{"path", "operation"}, nil),
		},
	}
}

func patchPathProperty() map[string]any {
	return map[string]any{
		"type":        "string",
		"description": "The path to the file (absolute or relative to working directory).",
	}
}

func patchOperationSchema(operation PatchOperation, required []any, extraProperties map[string]any) map[string]any {
	properties := map[string]any{
		"path": patchPathProperty(),
		"operation": map[string]any{
			"type":        "string",
			"enum":        []any{string(operation)},
			"description": fmt.Sprintf("Must be %q for this argument shape.", operation),
		},
	}
	maps.Copy(properties, extraProperties)
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

func patchRun(ctx ToolContext, input json.RawMessage, dagsDir string) ToolOut {
	if ctx.Role.IsSet() && !ctx.Role.CanWrite() {
		return toolError("Permission denied: patch requires write permission")
	}

	var args PatchToolInput
	if err := json.Unmarshal(input, &args); err != nil {
		return toolError("Failed to parse input: %v", err)
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(input, &rawFields); err != nil {
		return toolError("Failed to parse input: %v", err)
	}

	if args.Path == "" {
		return toolError("Path is required")
	}

	path := resolvePath(args.Path, ctx.WorkingDir)

	switch args.Operation {
	case PatchOpCreate:
		if err := validateNoFields(args.Operation, rawFields, "old_string", "new_string", "anchor"); err != nil {
			return toolError("%s", err.Error())
		}
		if err := validateRequiredFields(args.Operation, rawFields, "content"); err != nil {
			return toolError("%s", err.Error())
		}
		return patchCreate(ctx.Context, path, args.Content, dagsDir)
	case PatchOpReplace:
		if err := validateNoFields(args.Operation, rawFields, "content", "anchor"); err != nil {
			return toolError("%s", err.Error())
		}
		if err := validateRequiredFields(args.Operation, rawFields, "old_string", "new_string"); err != nil {
			return toolError("%s", err.Error())
		}
		return patchReplace(ctx.Context, path, args.OldString, args.NewString, dagsDir)
	case PatchOpAppend:
		if err := validateNoFields(args.Operation, rawFields, "old_string", "new_string", "anchor"); err != nil {
			return toolError("%s", err.Error())
		}
		if err := validateRequiredFields(args.Operation, rawFields, "content"); err != nil {
			return toolError("%s", err.Error())
		}
		return patchAppend(ctx.Context, path, args.Content, dagsDir)
	case PatchOpInsertBefore:
		if err := validateNoFields(args.Operation, rawFields, "old_string", "new_string"); err != nil {
			return toolError("%s", err.Error())
		}
		if err := validateRequiredFields(args.Operation, rawFields, "anchor", "content"); err != nil {
			return toolError("%s", err.Error())
		}
		return patchInsert(ctx.Context, path, args.Anchor, args.Content, false, dagsDir)
	case PatchOpInsertAfter:
		if err := validateNoFields(args.Operation, rawFields, "old_string", "new_string"); err != nil {
			return toolError("%s", err.Error())
		}
		if err := validateRequiredFields(args.Operation, rawFields, "anchor", "content"); err != nil {
			return toolError("%s", err.Error())
		}
		return patchInsert(ctx.Context, path, args.Anchor, args.Content, true, dagsDir)
	case PatchOpDelete:
		if err := validateNoFields(args.Operation, rawFields, "content", "old_string", "new_string", "anchor"); err != nil {
			return toolError("%s", err.Error())
		}
		return patchDelete(path)
	default:
		return toolError("Unknown operation: %s. Use 'create', 'replace', 'append', 'insert_before', 'insert_after', or 'delete'.", args.Operation)
	}
}

func patchCreate(ctx context.Context, path, content, dagsDir string) ToolOut {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return toolError("Path is a directory: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return toolError("Failed to stat path: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPermission); err != nil {
		return toolError("Failed to create directory: %v", err)
	}

	if err := fileutil.WriteFileAtomic(path, []byte(content), filePermission); err != nil {
		return toolError("Failed to write file: %v", err)
	}

	msg := fmt.Sprintf("Created %s (%d lines)", path, countLines(content))
	if errs := validateIfDAGFile(ctx, path, dagsDir); len(errs) > 0 {
		msg += "\n\nDAG Validation Errors:\n- " + strings.Join(errs, "\n- ")
	}
	return ToolOut{Content: msg}
}

func patchReplace(ctx context.Context, path, oldString, newString, dagsDir string) ToolOut {
	if oldString == "" {
		return toolError("old_string is required for replace operation")
	}

	contentStr, mode, out := readExistingRegularFile(path)
	if out.IsError {
		return out
	}

	count := strings.Count(contentStr, oldString)

	if count == 0 {
		return toolError("old_string not found in file. Make sure to include exact text including whitespace and indentation.")
	}
	if count > 1 {
		return toolError("old_string found %d times in file. It must be unique. Include more context to make it unique.", count)
	}

	newContent := strings.Replace(contentStr, oldString, newString, 1)
	if err := fileutil.WriteFileAtomic(path, []byte(newContent), mode); err != nil {
		return toolError("Failed to write file: %v", err)
	}

	msg := fmt.Sprintf("Replaced %d lines with %d lines in %s", countLines(oldString), countLines(newString), path)
	if errs := validateIfDAGFile(ctx, path, dagsDir); len(errs) > 0 {
		msg += "\n\nDAG Validation Errors:\n- " + strings.Join(errs, "\n- ")
	}
	return ToolOut{Content: msg}
}

func patchAppend(ctx context.Context, path, content, dagsDir string) ToolOut {
	if content == "" {
		return toolError("content is required for append operation")
	}

	contentStr, mode, out := readExistingRegularFile(path)
	if out.IsError {
		return out
	}

	newContent := contentStr
	if newContent != "" && !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}
	newContent += content

	if err := fileutil.WriteFileAtomic(path, []byte(newContent), mode); err != nil {
		return toolError("Failed to write file: %v", err)
	}

	msg := fmt.Sprintf("Appended %d lines to %s", countLines(content), path)
	if errs := validateIfDAGFile(ctx, path, dagsDir); len(errs) > 0 {
		msg += "\n\nDAG Validation Errors:\n- " + strings.Join(errs, "\n- ")
	}
	return ToolOut{Content: msg}
}

func patchInsert(ctx context.Context, path, anchor, content string, after bool, dagsDir string) ToolOut {
	if anchor == "" {
		return toolError("anchor is required for insert operation")
	}
	if content == "" {
		return toolError("content is required for insert operation")
	}

	contentStr, mode, out := readExistingRegularFile(path)
	if out.IsError {
		return out
	}

	count := strings.Count(contentStr, anchor)
	if count == 0 {
		return toolError("anchor not found in file. Make sure to include exact text including whitespace and indentation.")
	}
	if count > 1 {
		return toolError("anchor found %d times in file. It must be unique. Include more context to make it unique.", count)
	}

	replacement := content + anchor
	opName := "Inserted before"
	if after {
		replacement = anchor + content
		opName = "Inserted after"
	}
	newContent := strings.Replace(contentStr, anchor, replacement, 1)

	if err := fileutil.WriteFileAtomic(path, []byte(newContent), mode); err != nil {
		return toolError("Failed to write file: %v", err)
	}

	msg := fmt.Sprintf("%s anchor: %d lines in %s", opName, countLines(content), path)
	if errs := validateIfDAGFile(ctx, path, dagsDir); len(errs) > 0 {
		msg += "\n\nDAG Validation Errors:\n- " + strings.Join(errs, "\n- ")
	}
	return ToolOut{Content: msg}
}

func patchDelete(path string) ToolOut {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return toolError("Path is a directory: %s", path)
	}
	err := os.Remove(path)
	if err != nil {
		if os.IsNotExist(err) {
			return toolError("File not found: %s", path)
		}
		return toolError("Failed to delete file: %v", err)
	}
	return ToolOut{Content: fmt.Sprintf("Deleted %s", path)}
}

func validateNoFields(operation PatchOperation, rawFields map[string]json.RawMessage, fields ...string) error {
	for _, field := range fields {
		if _, ok := rawFields[field]; ok {
			return fmt.Errorf("%s is not allowed for %s operation", field, operation)
		}
	}
	return nil
}

func validateRequiredFields(operation PatchOperation, rawFields map[string]json.RawMessage, fields ...string) error {
	for _, field := range fields {
		raw, ok := rawFields[field]
		if !ok || strings.TrimSpace(string(raw)) == "null" {
			return fmt.Errorf("%s is required for %s operation", field, operation)
		}
	}
	return nil
}

func readExistingRegularFile(path string) (string, os.FileMode, ToolOut) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, toolError("File not found: %s. Use 'create' operation to create new files.", path)
		}
		return "", 0, toolError("Failed to stat file: %v", err)
	}
	if info.IsDir() {
		return "", 0, toolError("Path is a directory: %s", path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", 0, toolError("Failed to read file: %v", err)
	}
	return string(content), info.Mode().Perm(), ToolOut{}
}

func countLines(s string) int {
	return strings.Count(s, "\n") + 1
}

// isDAGFile checks if the path is a YAML file within the DAGs directory.
// Uses filepath.Rel to prevent path containment bypass attacks (e.g., /dags-malicious/).
func isDAGFile(path, dagsDir string) bool {
	if dagsDir == "" || !strings.HasSuffix(path, ".yaml") {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dagsDir), filepath.Clean(path))
	return err == nil && !strings.HasPrefix(rel, "..")
}

// validateIfDAGFile validates the file if it's a DAG file, returning any validation errors.
func validateIfDAGFile(ctx context.Context, path, dagsDir string) []string {
	if !isDAGFile(path, dagsDir) {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("failed to read for validation: %v", err)}
	}

	_, err = spec.LoadYAML(ctx, data, spec.WithoutEval())
	if err != nil {
		var errList core.ErrorList
		if errors.As(err, &errList) {
			return errList.ToStringList()
		}
		return []string{err.Error()}
	}
	return nil
}
