// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"maps"
	"sort"
	"strings"
)

type HarnessPromptMode string

const (
	HarnessPromptModeArg   HarnessPromptMode = "arg"
	HarnessPromptModeFlag  HarnessPromptMode = "flag"
	HarnessPromptModeStdin HarnessPromptMode = "stdin"
)

type HarnessPromptPosition string

const (
	HarnessPromptPositionBeforeFlags HarnessPromptPosition = "before_flags"
	HarnessPromptPositionAfterFlags  HarnessPromptPosition = "after_flags"
)

type HarnessFlagStyle string

const (
	HarnessFlagStyleGNULong    HarnessFlagStyle = "gnu_long"
	HarnessFlagStyleSingleDash HarnessFlagStyle = "single_dash"
)

// HarnessDefinition describes how to invoke a named harness CLI.
type HarnessDefinition struct {
	Binary         string                `json:"binary,omitempty"`
	PrefixArgs     []string              `json:"prefixArgs,omitempty"`
	PromptMode     HarnessPromptMode     `json:"promptMode,omitempty"`
	PromptFlag     string                `json:"promptFlag,omitempty"`
	PromptPosition HarnessPromptPosition `json:"promptPosition,omitempty"`
	FlagStyle      HarnessFlagStyle      `json:"flagStyle,omitempty"`
	OptionFlags    map[string]string     `json:"optionFlags,omitempty"`
}

// HarnessDefinitions contains named reusable harness definitions.
// Nil values are used internally during base-config merge to delete inherited entries.
type HarnessDefinitions map[string]*HarnessDefinition

var builtinHarnessCLIProviders = map[string]struct{}{
	"aider":    {},
	"amp":      {},
	"claude":   {},
	"cline":    {},
	"codex":    {},
	"copilot":  {},
	"cursor":   {},
	"deepseek": {},
	"droid":    {},
	"gemini":   {},
	"goose":    {},
	"kiro":     {},
	"opencode": {},
	"pi":       {},
	"qwen":     {},
}

// IsBuiltinCLIHarnessProvider reports whether name selects a built-in CLI
// harness provider.
func IsBuiltinCLIHarnessProvider(name string) bool {
	_, ok := builtinHarnessCLIProviders[name]
	return ok
}

// IsEffectiveBuiltinCLIHarnessProvider reports whether name resolves to a
// built-in provider after applying custom harness definitions.
func IsEffectiveBuiltinCLIHarnessProvider(name string, defs HarnessDefinitions) bool {
	if def, ok := defs[name]; ok && def != nil {
		return false
	}
	return IsBuiltinCLIHarnessProvider(name)
}

// BuiltinCLIHarnessProviderNames returns the built-in CLI harness provider names.
func BuiltinCLIHarnessProviderNames() []string {
	names := make([]string, 0, len(builtinHarnessCLIProviders))
	for name := range builtinHarnessCLIProviders {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// NormalizeBuiltinHarnessFlagKeys clones cfg and canonicalizes builtin harness
// flag aliases to kebab-case so equivalent keys merge predictably.
func NormalizeBuiltinHarnessFlagKeys(cfg map[string]any) map[string]any {
	if cfg == nil {
		return nil
	}

	normalized := make(map[string]any, len(cfg))
	sourceKeys := make(map[string]string, len(cfg))
	keys := make([]string, 0, len(cfg))
	for key := range cfg {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		canonical := canonicalBuiltinHarnessFlagKey(key)
		prevKey, exists := sourceKeys[canonical]
		if exists && !preferBuiltinHarnessKeyVariant(key, prevKey) {
			continue
		}
		normalized[canonical] = cloneConfigValue(cfg[key])
		sourceKeys[canonical] = key
	}

	return normalized
}

func canonicalBuiltinHarnessFlagKey(key string) string {
	if isBuiltinHarnessReservedKey(key) {
		return key
	}
	return strings.ReplaceAll(key, "_", "-")
}

func isBuiltinHarnessReservedKey(key string) bool {
	switch key {
	case "provider", "fallback":
		return true
	default:
		return false
	}
}

func preferBuiltinHarnessKeyVariant(candidate, current string) bool {
	candidateCanonical := !strings.Contains(candidate, "_")
	currentCanonical := !strings.Contains(current, "_")
	if candidateCanonical != currentCanonical {
		return candidateCanonical
	}
	return false
}

func cloneHarnessDefinition(def *HarnessDefinition) *HarnessDefinition {
	if def == nil {
		return nil
	}
	cloned := *def
	cloned.PrefixArgs = append([]string(nil), def.PrefixArgs...)
	cloned.OptionFlags = maps.Clone(def.OptionFlags)
	return &cloned
}

func cloneHarnessDefinitions(defs HarnessDefinitions) HarnessDefinitions {
	if defs == nil {
		return nil
	}
	cloned := make(HarnessDefinitions, len(defs))
	for name, def := range defs {
		cloned[name] = cloneHarnessDefinition(def)
	}
	return cloned
}
