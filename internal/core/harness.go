// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
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

const (
	// HarnessProviderBuiltin selects Dagu's in-process agent harness.
	HarnessProviderBuiltin = "builtin"
)

var builtinHarnessCLIProviders = map[string]struct{}{
	"claude":   {},
	"codex":    {},
	"copilot":  {},
	"opencode": {},
	"pi":       {},
}

// IsBuiltinHarnessProvider reports whether name is a built-in harness provider.
func IsBuiltinHarnessProvider(name string) bool {
	return IsBuiltinAgentHarnessProvider(name) || IsBuiltinCLIHarnessProvider(name)
}

// IsBuiltinAgentHarnessProvider reports whether name selects the in-process
// agent harness instead of a CLI harness provider.
func IsBuiltinAgentHarnessProvider(name string) bool {
	return name == HarnessProviderBuiltin
}

// IsBuiltinCLIHarnessProvider reports whether name selects a built-in CLI
// harness provider.
func IsBuiltinCLIHarnessProvider(name string) bool {
	_, ok := builtinHarnessCLIProviders[name]
	return ok
}

var builtinAgentHarnessConfigKeys = map[string]struct{}{
	"fallback":       {},
	"max_iterations": {},
	"memory":         {},
	"model":          {},
	"provider":       {},
	"safe_mode":      {},
	"skills":         {},
	"soul":           {},
	"tools":          {},
	"web_search":     {},
}

var builtinAgentHarnessConfigAliases = map[string]string{
	"max-iterations": "max_iterations",
	"safe-mode":      "safe_mode",
	"web-search":     "web_search",
}

// ValidateBuiltinAgentHarnessConfig rejects pass-through CLI flags for the
// in-process agent harness provider.
func ValidateBuiltinAgentHarnessConfig(cfg map[string]any) error {
	for key := range cfg {
		if _, ok := builtinAgentHarnessConfigKeys[canonicalBuiltinAgentHarnessConfigKey(key)]; !ok {
			return fmt.Errorf("unsupported builtin provider field %q", key)
		}
	}
	return nil
}

// NormalizeBuiltinAgentHarnessConfig clones cfg and canonicalizes known
// builtin agent field aliases to the agent config field names.
func NormalizeBuiltinAgentHarnessConfig(cfg map[string]any) map[string]any {
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
		canonical := canonicalBuiltinAgentHarnessConfigKey(key)
		if prevKey, exists := sourceKeys[canonical]; exists && prevKey == canonical {
			continue
		}
		normalized[canonical] = cloneHarnessValue(cfg[key])
		sourceKeys[canonical] = key
	}

	return normalized
}

func canonicalBuiltinAgentHarnessConfigKey(key string) string {
	if canonical, ok := builtinAgentHarnessConfigAliases[key]; ok {
		return canonical
	}
	return key
}

// BuiltinHarnessProviderNames returns the built-in harness provider names.
func BuiltinHarnessProviderNames() []string {
	names := append([]string{HarnessProviderBuiltin}, BuiltinCLIHarnessProviderNames()...)
	sort.Strings(names)
	return names
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
		normalized[canonical] = cloneHarnessValue(cfg[key])
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
	return &HarnessDefinition{
		Binary:         def.Binary,
		PrefixArgs:     append([]string(nil), def.PrefixArgs...),
		PromptMode:     def.PromptMode,
		PromptFlag:     def.PromptFlag,
		PromptPosition: def.PromptPosition,
		FlagStyle:      def.FlagStyle,
		OptionFlags:    maps.Clone(def.OptionFlags),
	}
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
