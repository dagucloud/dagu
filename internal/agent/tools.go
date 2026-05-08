// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
)

// CreateTools returns all registered agent tools, constructed with the given config.
// Tools whose factory returns nil (e.g., when a required dependency is missing) are skipped.
func CreateTools(cfg ToolConfig) []*AgentTool {
	regs := RegisteredTools()
	tools := make([]*AgentTool, 0, len(regs))
	for _, reg := range regs {
		if tool := reg.Factory(cfg); tool != nil {
			tools = append(tools, tool)
		}
	}
	return tools
}

// GetToolByName finds a tool by name from the given slice, or nil if not found.
func GetToolByName(tools []*AgentTool, name string) *AgentTool {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return tool
		}
	}
	return nil
}

// toolError creates a ToolOut marked as an error with a formatted message.
func toolError(format string, args ...any) ToolOut {
	return ToolOut{
		Content: fmt.Sprintf(format, args...),
		IsError: true,
	}
}

// resolvePath joins path with workingDir if path is relative and workingDir is set.
func resolvePath(path, workingDir string) string {
	if !filepath.IsAbs(path) && !isRootedPath(path) && workingDir != "" {
		return filepath.Join(workingDir, path)
	}
	if filepath.IsAbs(path) || isRootedPath(path) {
		return filepath.Clean(path)
	}
	return path
}

func isRootedPath(path string) bool {
	if len(path) == 0 {
		return false
	}
	return path[0] == '/' || path[0] == '\\'
}

// decodeToolInput decodes tool-call arguments leniently. Models sometimes send
// optional fields that are irrelevant to the selected action, or include stale
// fields from another operation. Unknown fields and malformed optional fields
// should not block the tool; action-specific validation below each tool decides
// which fields are actually required.
func decodeToolInput(input json.RawMessage, dest any) error {
	if strings.TrimSpace(string(input)) == "" {
		input = json.RawMessage(`{}`)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return err
	}

	value := reflect.ValueOf(dest)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return fmt.Errorf("destination must be a non-nil pointer")
	}

	value = value.Elem()
	if value.Kind() != reflect.Struct {
		return json.Unmarshal(input, dest)
	}

	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		fieldType := typ.Field(i)
		if fieldType.PkgPath != "" {
			continue
		}
		name := jsonFieldName(fieldType)
		if name == "" {
			continue
		}
		raw, ok := fields[name]
		if !ok {
			continue
		}
		field := value.Field(i)
		if !field.CanSet() {
			continue
		}
		_ = json.Unmarshal(raw, field.Addr().Interface())
	}

	return nil
}

func jsonFieldName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return ""
	}
	if tag == "" {
		return field.Name
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return field.Name
	}
	return name
}
