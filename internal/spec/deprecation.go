// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

type deprecatedStepField struct {
	name        string
	replacement string
}

var deprecatedStepExecutionFields = []deprecatedStepField{
	{name: "call", replacement: "use action: dag.run with with.dag"},
	{name: "command", replacement: "use run"},
	{name: "config", replacement: "use with"},
	{name: "exec", replacement: "use action: exec"},
	{name: "llm", replacement: "use action: chat.completion with with"},
	{name: "messages", replacement: "use action: chat.completion"},
	{name: "params", replacement: "use action: dag.run with with.params"},
	{name: "routes", replacement: "use action: router.route with with.routes"},
	{name: "script", replacement: "use run"},
	{name: "shell", replacement: "use run with with.shell"},
	{name: "shell_args", replacement: "use run with with.shell_args"},
	{name: "shell_packages", replacement: "use run with with.shell_packages"},
	{name: "type", replacement: "use action"},
	{name: "value", replacement: "use action: router.route with with.value"},
}

// DeprecatedSyntaxWarnings returns validate-only deprecation warnings for legacy
// DAG syntax. Runtime loading intentionally does not call this function.
func DeprecatedSyntaxWarnings(data []byte) []string {
	var warnings []string
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	docIndex := 0
	for {
		var doc map[string]any
		if err := decoder.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return warnings
		}
		if len(doc) == 0 {
			docIndex++
			continue
		}
		prefix := ""
		if docIndex > 0 {
			prefix = fmt.Sprintf("document[%d].", docIndex)
		}
		warnings = append(warnings, deprecatedSyntaxWarningsForDocument(prefix, doc)...)
		docIndex++
	}
	return warnings
}

func deprecatedSyntaxWarningsForDocument(prefix string, doc map[string]any) []string {
	var warnings []string
	if _, ok := doc["step_types"]; ok {
		warnings = append(warnings, fmt.Sprintf("Deprecated DAG syntax: %sstep_types is deprecated; use actions", prefix))
	}
	if t, ok := doc["type"].(string); ok && strings.TrimSpace(t) == legacyAgentDAGType {
		warnings = append(warnings, fmt.Sprintf("Deprecated DAG syntax: %stype: controller is deprecated; use type: agent", prefix))
	}
	warnings = append(warnings, deprecatedToolsListWarning(prefix+"llm", doc["llm"])...)
	warnings = append(warnings, deprecatedSyntaxWarningsForSteps(prefix+"steps", doc["steps"])...)
	if handlerRaw, ok := doc["handler_on"].(map[string]any); ok {
		names := sortedKeys(handlerRaw)
		for _, name := range names {
			warnings = append(warnings, deprecatedSyntaxWarningsForStep(fmt.Sprintf("%shandler_on.%s", prefix, name), handlerRaw[name])...)
		}
	}
	return warnings
}

func deprecatedSyntaxWarningsForSteps(path string, raw any) []string {
	switch steps := raw.(type) {
	case []any:
		warnings := make([]string, 0)
		for i, stepRaw := range steps {
			warnings = append(warnings, deprecatedSyntaxWarningsForStep(fmt.Sprintf("%s[%d]", path, i), stepRaw)...)
		}
		return warnings
	case map[string]any:
		warnings := make([]string, 0)
		names := sortedKeys(steps)
		for _, name := range names {
			warnings = append(warnings, deprecatedSyntaxWarningsForStep(fmt.Sprintf("%s.%s", path, name), steps[name])...)
		}
		return warnings
	default:
		return nil
	}
}

func deprecatedSyntaxWarningsForStep(path string, raw any) []string {
	switch step := raw.(type) {
	case string:
		return []string{fmt.Sprintf("Deprecated DAG syntax: %s string shorthand is deprecated; use run", path)}
	case map[string]any:
		return deprecatedSyntaxWarningsForStepMap(path, step)
	default:
		return nil
	}
}

func deprecatedSyntaxWarningsForStepMap(path string, step map[string]any) []string {
	hasRun := false
	if _, ok := step["run"]; ok {
		hasRun = true
	}
	hasAction := false
	if _, ok := step["action"]; ok {
		hasAction = true
	}

	var warnings []string
	for _, field := range deprecatedStepExecutionFields {
		if _, ok := step[field.name]; ok {
			warnings = append(warnings, fmt.Sprintf("Deprecated DAG syntax: %s.%s is deprecated; %s", path, field.name, field.replacement))
		}
	}
	if _, ok := step["with"]; ok && !hasRun && !hasAction {
		warnings = append(warnings, fmt.Sprintf("Deprecated DAG syntax: %s.with is deprecated with legacy execution syntax; use action with with", path))
	}
	if step["action"] != "agent.run" {
		warnings = append(warnings, deprecatedToolsListWarning(path+".with", step["with"])...)
	}
	warnings = append(warnings, deprecatedToolsListWarning(path+".llm", step["llm"])...)
	if step["action"] == "chat.completion" {
		warnings = append(warnings, deprecatedChatProviderWarning(path+".with", step["with"])...)
	}
	return warnings
}

// chatProviderKeys are the keys of the old method of chat.completion, in the
// order of the warning. The block endpoint replaces them.
var chatProviderKeys = []string{"provider", "base_url", "api_key_name"}

// deprecatedChatProviderWarning reports the keys of the old method of
// chat.completion in a with block and in the rows of its model list. It gives
// one warning for the step, which names each key that it finds.
func deprecatedChatProviderWarning(path string, raw any) []string {
	with, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	var found []string
	for _, key := range chatProviderKeys {
		if _, set := with[key]; set {
			found = append(found, path+"."+key)
		}
	}
	rows, _ := with["model"].([]any)
	for i, rowRaw := range rows {
		row, ok := rowRaw.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range chatProviderKeys {
			if _, set := row[key]; set {
				found = append(found, fmt.Sprintf("%s.model[%d].%s", path, i, key))
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("Deprecated DAG syntax: %s: use with.endpoint, an endpoint of the AI page", strings.Join(found, ", "))}
}

// deprecatedToolsListWarning reports the list form of tools, a list of DAG
// names, in a with block or in an llm block. The key dags replaces it.
func deprecatedToolsListWarning(path string, raw any) []string {
	block, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	if _, isList := block["tools"].([]any); !isList {
		return nil
	}
	return []string{fmt.Sprintf("Deprecated DAG syntax: %s.tools: The list form of `tools` is deprecated. Use the key dags.", path)}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
