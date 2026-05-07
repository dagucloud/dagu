// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/goccy/go-yaml"
)

type deprecatedStepField struct {
	name        string
	replacement string
}

var deprecatedStepExecutionFields = []deprecatedStepField{
	{name: "agent", replacement: "use action: agent.run with with"},
	{name: "call", replacement: "use action: dag.run with with.dag"},
	{name: "command", replacement: "use run"},
	{name: "config", replacement: "use with"},
	{name: "exec", replacement: "use action: exec"},
	{name: "llm", replacement: "use action: chat.completion with with"},
	{name: "messages", replacement: "use action: chat.completion or action: agent.run"},
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
			return nil
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
	return warnings
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
