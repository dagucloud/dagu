// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"fmt"
	"sort"
)

// CustomStepTypeEditorHint is editor-only metadata for a custom step type.
// It is derived from the same validated spec pipeline as runtime expansion.
type CustomStepTypeEditorHint struct {
	Name         string
	TargetType   string
	Description  string
	InputSchema  map[string]any
	OutputSchema map[string]any
}

// InheritedCustomStepTypeEditorHints returns editor hints for custom step types
// declared in base config. The returned schemas are fully resolved JSON Schema
// objects safe to embed into editor-generated DAG schemas.
func InheritedCustomStepTypeEditorHints(baseConfig []byte) ([]CustomStepTypeEditorHint, error) {
	if len(baseConfig) == 0 {
		return nil, nil
	}

	raw, err := unmarshalData(baseConfig)
	if err != nil {
		return nil, fmt.Errorf("unmarshal base config: %w", err)
	}

	baseDef, err := decode(raw)
	if err != nil {
		return nil, fmt.Errorf("decode base config: %w", err)
	}

	registry, err := buildCustomStepTypeRegistry(stepTypesOf(baseDef), nil)
	if err != nil {
		return nil, fmt.Errorf("build custom step type registry: %w", err)
	}
	if registry == nil || len(registry.entries) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(registry.entries))
	for name := range registry.entries {
		names = append(names, name)
	}
	sort.Strings(names)

	hints := make([]CustomStepTypeEditorHint, 0, len(names))
	for _, name := range names {
		entry := registry.entries[name]
		hint, ok, err := editorHintForCustomStepType(entry)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		hints = append(hints, hint)
	}

	return hints, nil
}

func editorHintForCustomStepType(entry *customStepType) (CustomStepTypeEditorHint, bool, error) {
	if entry == nil {
		return CustomStepTypeEditorHint{}, false, nil
	}

	schemaMap := map[string]any{}
	if entry.InputSchema != nil && entry.InputSchema.Schema() != nil {
		schemaData, err := json.Marshal(entry.InputSchema.Schema())
		if err != nil {
			return CustomStepTypeEditorHint{}, false, fmt.Errorf("marshal input schema for %q: %w", entry.Name, err)
		}
		if err := json.Unmarshal(schemaData, &schemaMap); err != nil {
			return CustomStepTypeEditorHint{}, false, fmt.Errorf("unmarshal input schema for %q: %w", entry.Name, err)
		}
	}

	return CustomStepTypeEditorHint{
		Name:         entry.Name,
		TargetType:   entry.Type,
		Description:  entry.Description,
		InputSchema:  schemaMap,
		OutputSchema: cloneMap(entry.OutputSchema),
	}, true, nil
}
