// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"fmt"
	"sort"
)

// LegacyDefinitionEditorHint is editor-only metadata for a deprecated step_types entry.
// It is derived from the same validated spec pipeline as runtime expansion.
type LegacyDefinitionEditorHint struct {
	Name         string
	TargetType   string
	Description  string
	InputSchema  map[string]any
	OutputSchema map[string]any
}

// CustomActionEditorHint is editor-only metadata for a custom action.
// It is derived from the same validated spec pipeline as runtime expansion.
type CustomActionEditorHint struct {
	Name         string
	Description  string
	InputSchema  map[string]any
	OutputSchema map[string]any
}

// InheritedLegacyDefinitionEditorHints returns editor hints for deprecated step_types
// declared in base config. The returned schemas are fully resolved JSON Schema
// objects safe to embed into editor-generated DAG schemas.
func InheritedLegacyDefinitionEditorHints(baseConfig []byte) ([]LegacyDefinitionEditorHint, error) {
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
		return nil, fmt.Errorf("build legacy step_types registry: %w", err)
	}
	if registry == nil || len(registry.entries) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(registry.entries))
	for name := range registry.entries {
		names = append(names, name)
	}
	sort.Strings(names)

	hints := make([]LegacyDefinitionEditorHint, 0, len(names))
	for _, name := range names {
		entry := registry.entries[name]
		hint, ok, err := editorHintForLegacyDefinition(entry)
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

// InheritedCustomActionEditorHints returns editor hints for custom actions
// declared in base config. The returned schemas are fully resolved JSON Schema
// objects safe to embed into editor-generated DAG schemas.
func InheritedCustomActionEditorHints(baseConfig []byte) ([]CustomActionEditorHint, error) {
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

	registry, err := buildCustomStepActionRegistry(stepTypesOf(baseDef), nil, actionsOf(baseDef), nil)
	if err != nil {
		return nil, fmt.Errorf("build custom action registry: %w", err)
	}
	if registry == nil || len(registry.entries) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(registry.entries))
	for name := range registry.entries {
		names = append(names, name)
	}
	sort.Strings(names)

	hints := make([]CustomActionEditorHint, 0, len(names))
	for _, name := range names {
		entry := registry.entries[name]
		hint, ok, err := editorHintForCustomAction(entry)
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

func editorHintForLegacyDefinition(entry *customStepType) (LegacyDefinitionEditorHint, bool, error) {
	if entry == nil {
		return LegacyDefinitionEditorHint{}, false, nil
	}

	schemaMap := map[string]any{}
	if entry.InputSchema != nil && entry.InputSchema.Schema() != nil {
		schemaData, err := json.Marshal(entry.InputSchema.Schema())
		if err != nil {
			return LegacyDefinitionEditorHint{}, false, fmt.Errorf("marshal input schema for %q: %w", entry.Name, err)
		}
		if err := json.Unmarshal(schemaData, &schemaMap); err != nil {
			return LegacyDefinitionEditorHint{}, false, fmt.Errorf("unmarshal input schema for %q: %w", entry.Name, err)
		}
	}

	return LegacyDefinitionEditorHint{
		Name:         entry.Name,
		TargetType:   entry.Type,
		Description:  entry.Description,
		InputSchema:  schemaMap,
		OutputSchema: cloneMap(entry.OutputSchema),
	}, true, nil
}

func editorHintForCustomAction(entry *customStepType) (CustomActionEditorHint, bool, error) {
	if entry == nil || entry.Kind != customStepKindAction {
		return CustomActionEditorHint{}, false, nil
	}

	schemaMap := map[string]any{}
	if entry.InputSchema != nil && entry.InputSchema.Schema() != nil {
		schemaData, err := json.Marshal(entry.InputSchema.Schema())
		if err != nil {
			return CustomActionEditorHint{}, false, fmt.Errorf("marshal input schema for %q: %w", entry.Name, err)
		}
		if err := json.Unmarshal(schemaData, &schemaMap); err != nil {
			return CustomActionEditorHint{}, false, fmt.Errorf("unmarshal input schema for %q: %w", entry.Name, err)
		}
	}

	return CustomActionEditorHint{
		Name:         entry.Name,
		Description:  entry.Description,
		InputSchema:  schemaMap,
		OutputSchema: cloneMap(entry.OutputSchema),
	}, true, nil
}
