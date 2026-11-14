// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"errors"
	"maps"
	"net/url"
	"path"
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

// HarnessProtocol selects CLI invocation or an ACP connection.
type HarnessProtocol string

const (
	HarnessProtocolCLI HarnessProtocol = "cli"
	HarnessProtocolACP HarnessProtocol = "acp"
)

// HarnessTransport selects the wire transport for an ACP connection.
type HarnessTransport string

const (
	HarnessTransportStdio     HarnessTransport = "stdio"
	HarnessTransportWebSocket HarnessTransport = "websocket"
	HarnessTransportHTTP      HarnessTransport = "http"
)

// HarnessInheritedOptionsKey is the executor metadata key listing the DAG-level
// harness options merged into a step whose provider is chosen by a variable.
// Options the resolved provider cannot accept are dropped at run time.
const HarnessInheritedOptionsKey = "harness_inherited_options"

// HarnessDefinition describes how to invoke a CLI or connect to an ACP agent.
type HarnessDefinition struct {
	Protocol          HarnessProtocol       `json:"protocol,omitempty"`
	Transport         HarnessTransport      `json:"transport,omitempty"`
	URL               string                `json:"url,omitempty"`
	CAFile            string                `json:"caFile,omitempty"`
	BearerSubprotocol bool                  `json:"bearerSubprotocol,omitempty"`
	Headers           map[string]string     `json:"headers,omitempty"`
	Cwd               string                `json:"cwd,omitempty"`
	Binary            string                `json:"binary,omitempty"`
	PrefixArgs        []string              `json:"prefixArgs,omitempty"`
	PromptMode        HarnessPromptMode     `json:"promptMode,omitempty"`
	PromptFlag        string                `json:"promptFlag,omitempty"`
	PromptPosition    HarnessPromptPosition `json:"promptPosition,omitempty"`
	FlagStyle         HarnessFlagStyle      `json:"flagStyle,omitempty"`
	OptionFlags       map[string]string     `json:"optionFlags,omitempty"`
}

// IsRemote reports whether the definition connects to an ACP agent over the network.
func (d *HarnessDefinition) IsRemote() bool {
	return d.Transport == HarnessTransportWebSocket || d.Transport == HarnessTransportHTTP
}

// ValidateConnection checks the mutually exclusive local and remote launch settings.
// URL and cwd values with variable references are checked again after runtime
// variable resolution.
func (d *HarnessDefinition) ValidateConnection() error {
	if d.Transport != "" && d.Transport != HarnessTransportStdio && d.Transport != HarnessTransportWebSocket && d.Transport != HarnessTransportHTTP {
		return errors.New("transport must be stdio, websocket, or http")
	}
	if d.Transport != "" && d.Protocol != HarnessProtocolACP {
		return errors.New("transport requires protocol: acp")
	}
	if d.BearerSubprotocol && d.Transport != HarnessTransportWebSocket {
		return errors.New("bearer_subprotocol requires transport: websocket")
	}
	if !d.IsRemote() {
		if d.URL != "" || len(d.Headers) != 0 || d.Cwd != "" || d.CAFile != "" {
			return errors.New("url, headers, cwd, and ca_file require a remote transport")
		}
		if d.Binary == "" {
			return errors.New("binary is required")
		}
		return nil
	}
	if d.Binary != "" || len(d.PrefixArgs) != 0 || len(d.OptionFlags) != 0 {
		return errors.New("remote transport does not support binary, prefix_args, or option_flags")
	}
	if d.URL == "" || d.Cwd == "" {
		return errors.New("remote transport requires url and cwd")
	}
	if !strings.Contains(d.URL, "$") {
		u, err := url.Parse(d.URL)
		validScheme := err == nil && ((d.Transport == HarnessTransportWebSocket && (u.Scheme == "ws" || u.Scheme == "wss")) || (d.Transport == HarnessTransportHTTP && (u.Scheme == "http" || u.Scheme == "https")))
		if !validScheme || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return errors.New("url must match the transport (ws/wss or http/https), without user info or a fragment")
		}
	}
	// The path belongs to the agent host, which may use a different OS.
	driveAbsolute := len(d.Cwd) >= 3 &&
		((d.Cwd[0] >= 'A' && d.Cwd[0] <= 'Z') || (d.Cwd[0] >= 'a' && d.Cwd[0] <= 'z')) &&
		d.Cwd[1] == ':' && (d.Cwd[2] == '/' || d.Cwd[2] == '\\')
	absolute := path.IsAbs(d.Cwd) || driveAbsolute || strings.HasPrefix(d.Cwd, `\\`)
	if !strings.Contains(d.Cwd, "$") && !absolute {
		return errors.New("cwd must be an absolute path on the remote agent host")
	}
	return nil
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
	"kilo":     {},
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
	case "provider", "fallback", "acp_permission":
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

// CloneHarnessDefinition copies a definition without sharing mutable slices or maps.
func CloneHarnessDefinition(def *HarnessDefinition) *HarnessDefinition {
	if def == nil {
		return nil
	}
	cloned := *def
	cloned.PrefixArgs = append([]string(nil), def.PrefixArgs...)
	cloned.OptionFlags = maps.Clone(def.OptionFlags)
	cloned.Headers = maps.Clone(def.Headers)
	return &cloned
}

func cloneHarnessDefinitions(defs HarnessDefinitions) HarnessDefinitions {
	if defs == nil {
		return nil
	}
	cloned := make(HarnessDefinitions, len(defs))
	for name, def := range defs {
		cloned[name] = CloneHarnessDefinition(def)
	}
	return cloned
}
