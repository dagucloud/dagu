// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Spec 090, "Limits". The block agent_tools sets the limits of the tool catalog
// for each step of the process. The block has a closed schema. An unknown key
// stops Dagu at start, and the error names the key, the expected type and the
// value. The loader changes no value to make it fit. The block has no key that an
// environment variable can set: the file is the only source.

// Defaults of the block agent_tools.
const (
	DefaultAgentToolsMaxTools           = 64
	DefaultAgentToolsToolResultMaxBytes = 524288
)

// AgentToolsConfig is the block agent_tools after the checks.
type AgentToolsConfig struct {
	// MaxTools limits the effective catalog of a step. It is at least 1 in a
	// loaded configuration.
	MaxTools int
	// ToolResultMaxBytes limits one tool result sent to the model. Zero means no
	// limit.
	ToolResultMaxBytes int
	// Sources tells where each value came from: "file" or "default". The key is
	// the dotted name, such as max_tools.
	Sources map[string]string
}

// Limits returns the limits that apply: the loaded values, or the defaults for a
// Config that the loader did not build.
func (c AgentToolsConfig) Limits() (maxTools, toolResultMaxBytes int) {
	if c.MaxTools == 0 {
		return DefaultAgentToolsMaxTools, DefaultAgentToolsToolResultMaxBytes
	}
	return c.MaxTools, c.ToolResultMaxBytes
}

// agentToolsKeys lists the keys of the block, with the expected type for the
// error messages.
var agentToolsKeys = map[string]string{
	"max_tools":             "an integer of at least 1",
	"tool_result_max_bytes": "an integer of at least 0",
}

// AgentToolsKeyDocs lists each key of the block with its type and its default,
// for the reference and for the printed configuration. The code is the single
// source of this table.
var AgentToolsKeyDocs = []AgentToolsKeyDoc{
	{"max_tools", "integer", strconv.Itoa(DefaultAgentToolsMaxTools), "The most tools in the effective catalog of a step. A larger catalog fails the step."},
	{"tool_result_max_bytes", "integer", strconv.Itoa(DefaultAgentToolsToolResultMaxBytes), "The size limit of one tool result that goes to the model. Zero means no limit."},
}

// AgentToolsKeyDoc documents one key of the block agent_tools.
type AgentToolsKeyDoc struct {
	Key         string
	Type        string
	Default     string
	Description string
}

func agentToolsError(key, expected string, got any) error {
	return fmt.Errorf("config: key %q: expected %s, got %s", "agent_tools."+key, expected, showConfigValue(got))
}

// ParseAgentToolsConfig checks the raw block and returns the typed
// configuration. raw is the value of the key agent_tools, or nil when the file
// has none.
func ParseAgentToolsConfig(raw any) (AgentToolsConfig, error) {
	cfg := AgentToolsConfig{
		MaxTools:           DefaultAgentToolsMaxTools,
		ToolResultMaxBytes: DefaultAgentToolsToolResultMaxBytes,
		Sources:            map[string]string{},
	}
	for _, doc := range AgentToolsKeyDocs {
		cfg.Sources[doc.Key] = "default"
	}
	if raw == nil {
		return cfg, nil
	}
	block, ok := raw.(map[string]any)
	if !ok {
		return cfg, fmt.Errorf("config: key %q: expected an object, got %s", "agent_tools", showConfigValue(raw))
	}
	for _, key := range sortedMapKeys(block) {
		if _, valid := agentToolsKeys[key]; !valid {
			return cfg, fmt.Errorf("config: unknown key %q in the block agent_tools; valid keys are %s",
				"agent_tools."+key, strings.Join(sortedMapKeys(agentToolsKeys), ", "))
		}
	}
	for _, limit := range []struct {
		key     string
		minimum int64
		to      *int
	}{
		{"max_tools", 1, &cfg.MaxTools},
		{"tool_result_max_bytes", 0, &cfg.ToolResultMaxBytes},
	} {
		value, present := block[limit.key]
		if !present {
			continue
		}
		number, valid := configInt(value)
		if !valid || number < limit.minimum || number > int64(math.MaxInt) {
			return cfg, agentToolsError(limit.key, agentToolsKeys[limit.key], value)
		}
		*limit.to = int(number)
		cfg.Sources[limit.key] = "file"
	}
	return cfg, nil
}

// loadAgentToolsConfig reads the block agent_tools. The block has no default in
// the configuration reader and no environment variable: the file is the only
// source.
func (l *ConfigLoader) loadAgentToolsConfig(cfg *Config) error {
	parsed, err := ParseAgentToolsConfig(l.v.Get("agent_tools"))
	if err != nil {
		return err
	}
	cfg.AgentTools = parsed
	return nil
}
