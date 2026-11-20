// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"errors"
	"fmt"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// This file has the checks of the block endpoint of agent.run and chat.completion
// (Spec 094), and of the key endpoint of an entry of a model list.
// The checks read no store: they cannot tell if the endpoint exists. The start of
// the step checks that.

// endpointBlockKeys lists the keys of the block endpoint.
var endpointBlockKeys = []string{"name", "model", "effort"}

// endpointBlock is the block endpoint of a step, after the checks.
type endpointBlock struct {
	name  string
	model string
	// effort is the value of the key effort. It is empty when the block has none.
	effort string
}

// readEndpointBlock checks the block endpoint of the action at field. One load
// reports both a missing name and a missing model. effortRef is true when the
// effort can be a value reference, which the step checks when it starts.
func readEndpointBlock(field string, raw any, action string, effortRef bool) (endpointBlock, error) {
	var block endpointBlock
	fields, ok := raw.(map[string]any)
	if !ok {
		return block, ir.NewValidationError(field, raw, fmt.Errorf("endpoint must be a block with name and model"))
	}
	if err := checkClosedKeys(field, fields, "the block endpoint", endpointBlockKeys); err != nil {
		return block, err
	}
	src := agentSource{label: field, raw: fields}

	var problems []error
	if !src.has("name") {
		problems = append(problems, ir.NewValidationError(src.field("name"), nil,
			fmt.Errorf("%s needs the name of an endpoint", action)))
	} else if err := checkEndpointName(src.field("name"), "name", fields["name"]); err != nil {
		problems = append(problems, err)
	} else {
		block.name = fields["name"].(string)
	}
	if !src.has("model") {
		problems = append(problems, ir.NewValidationError(src.field("model"), nil,
			fmt.Errorf("%s needs a model; choose one that the endpoint lists", action)))
	} else if model, _, err := src.str("model"); err != nil {
		problems = append(problems, err)
	} else {
		block.model = model
	}
	if text, ok := fields["effort"].(string); ok && effortRef && value.HasAnyReference(text) {
		block.effort = text
	} else if effort, _, err := src.enum("effort", ir.AgentEffortLevels...); err != nil {
		problems = append(problems, err)
	} else {
		block.effort = effort
	}
	if len(problems) > 0 {
		return block, errors.Join(problems...)
	}
	return block, nil
}

// checkEndpointName checks that raw, the value of the key at field, is the
// literal name of an AI endpoint.
func checkEndpointName(field, key string, raw any) error {
	name, ok := raw.(string)
	if !ok || name == "" {
		return ir.NewValidationError(field, raw, fmt.Errorf("%s must be a non-empty string", key))
	}
	if value.HasAnyReference(name) {
		return ir.NewValidationError(field, name,
			fmt.Errorf("%s must be a literal name, not a reference: Dagu checks the use rule of the endpoint with this name", key))
	}
	if err := aiendpoint.ValidateName(name); err != nil {
		return ir.NewValidationError(field, name, fmt.Errorf("%s must match %s", key, aiendpoint.NamePattern))
	}
	return nil
}

// endpointContradiction is a key of the old method that an endpoint replaces,
// with the reason of the contradiction.
type endpointContradiction struct{ key, reason string }

// entryContradictions are the keys of an entry of a model list that the key
// endpoint of the entry replaces.
var entryContradictions = []endpointContradiction{
	{"api_key_name", "an endpoint holds its own key"},
	{"base_url", "an endpoint holds its own base URL"},
	{"provider", "an endpoint gives the provider type"},
}

// blockContradictions are the keys of the with that the block endpoint replaces.
var blockContradictions = append(slices.Clone(entryContradictions),
	endpointContradiction{"model", "the block endpoint gives the model"},
	endpointContradiction{"thinking", "the block endpoint gives the effort"},
)

// checkEndpointContradiction reports the first key of the list that set reports.
// The error names the two keys and the path of the key endpoint, so that a form
// shows it under the field of the endpoint.
func checkEndpointContradiction(field string, set func(key string) bool, keys []endpointContradiction) error {
	for _, c := range keys {
		if set(c.key) {
			return ir.NewValidationError(field, nil,
				fmt.Errorf("key %q and key %q contradict each other; %s", "endpoint", c.key, c.reason))
		}
	}
	return nil
}

// isSetIn reports whether the map has the key with a value that is not null or
// empty.
func isSetIn(m map[string]any) func(key string) bool {
	return func(key string) bool {
		raw, ok := m[key]
		return ok && raw != nil && raw != ""
	}
}

// chatEndpoint is the name of the endpoint of a chat.completion step in its llm
// block. Only normalizeChatEndpoint makes it, from the block endpoint of the
// with. A YAML value cannot fill it, so the key endpoint of another llm block is
// an error.
type chatEndpoint struct {
	name string
}

// errLLMEndpoint is the error of the key endpoint in an llm block that does not
// come from the with of a chat.completion step.
var errLLMEndpoint = errors.New("the key endpoint is valid only with the action chat.completion")

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (*chatEndpoint) UnmarshalYAML([]byte) error {
	return errLLMEndpoint
}

// String returns the endpoint name, or an empty string for nil.
func (e *chatEndpoint) String() string {
	if e == nil {
		return ""
	}
	return e.name
}

// normalizeChatEndpoint checks the block endpoint of the with of a chat.completion
// step, before the loader turns the with into the llm block. It folds the block
// into the keys of the llm block: the endpoint name, the model, and the thinking
// that the effort sets. It then checks the key endpoint of each entry of a model
// list.
func normalizeChatEndpoint(with map[string]any) error {
	set := isSetIn(with)
	if raw, present := with["endpoint"]; present {
		if _, isBlock := raw.(map[string]any); isBlock {
			if err := checkEndpointContradiction("with.endpoint", set, blockContradictions); err != nil {
				return err
			}
		}
		block, err := readEndpointBlock("with.endpoint", raw, "chat.completion", false)
		if err != nil {
			return err
		}
		with["endpoint"] = &chatEndpoint{name: block.name}
		with["model"] = block.model
		if block.effort != "" {
			with["thinking"] = map[string]any{"enabled": true, "effort": block.effort}
		}
	}
	entries, _ := with["model"].([]any)
	for i, item := range entries {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if raw, present := entry["endpoint"]; present {
			if err := checkEntryEndpoint(fmt.Sprintf("with.model[%d].endpoint", i), raw, isSetIn(entry)); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkEntryEndpoint checks the key endpoint of one entry of a model list: the
// name, and the contradiction with the keys api_key_name, base_url and provider
// of the entry.
func checkEntryEndpoint(field string, raw any, set func(key string) bool) error {
	if err := checkEndpointName(field, "endpoint", raw); err != nil {
		return err
	}
	return checkEndpointContradiction(field, set, entryContradictions)
}

// usesEndpoint reports whether the llm block names an endpoint, in its own key or
// in an entry of its model list.
func (c *llmConfig) usesEndpoint() bool {
	if c.Endpoint != nil {
		return true
	}
	for _, entry := range c.Model.Entries() {
		if entry.Endpoint != "" {
			return true
		}
	}
	return false
}

// rejectLLMEndpoint refuses an endpoint in an llm block that does not belong to a
// chat.completion step: the llm block of the DAG, which an agent DAG uses for its
// decisions, or the llm block of another action.
func rejectLLMEndpoint(field string, cfg *llmConfig) error {
	if cfg.usesEndpoint() {
		return ir.NewValidationError(field, nil, errLLMEndpoint)
	}
	if cfg.usesNamedMCP() {
		return ir.NewValidationError(field+".tools.mcp", nil,
			fmt.Errorf("an mcp entry with no url names a MCP server entry of the AI page; only the actions chat.completion and agent.run can use it, and the llm block of a DAG or of another action cannot"))
	}
	return nil
}

// usesNamedMCP reports whether the tool map of the llm block has an mcp entry with
// no url (Spec 095, "The key tools.mcp"). The tool map is checked later: this reads
// the raw value only.
func (c *llmConfig) usesNamedMCP() bool {
	tools, ok := c.Tools.(map[string]any)
	if !ok {
		return false
	}
	entries, _ := tools[toolCategoryMCP].([]any)
	for _, item := range entries {
		if entry, ok := item.(map[string]any); ok {
			if _, hasURL := entry["url"]; !hasURL {
				return true
			}
		}
	}
	return false
}

// validateLLMEndpoints checks the key endpoint of each entry of the model list
// of the llm block of a chat step, as normalizeChatEndpoint does for the with.
// It covers the llm key of a step, which takes the model list of the with.
func validateLLMEndpoints(cfg *llmConfig) error {
	for i, entry := range cfg.Model.Entries() {
		if entry.Endpoint == "" {
			continue
		}
		set := func(key string) bool {
			switch key {
			case "api_key_name":
				return entry.APIKeyName != ""
			case "base_url":
				return entry.BaseURL != ""
			default:
				return entry.Provider != ""
			}
		}
		if err := checkEntryEndpoint(fmt.Sprintf("llm.model[%d].endpoint", i), entry.Endpoint, set); err != nil {
			return err
		}
	}
	return nil
}
