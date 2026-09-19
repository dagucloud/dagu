// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package decision

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/google/jsonschema-go/jsonschema"
)

const (
	executorType      = "decision"
	choiceType        = "choice"
	scoreType         = "score"
	noulType          = "noul"
	openRouter        = "openrouter"
	typeSafe          = "typesafe"
	openRouterBaseURL = "https://openrouter.ai/api/alpha"
	openRouterPath    = "decisions"
	openRouterKey     = "OPENROUTER_API_KEY"
	typeSafeBaseURL   = "https://api.typesafe.ai/v1"
	typeSafePath      = "systemone"
	typeSafeKey       = "TYPESAFE_API_KEY"
)

type config struct {
	Provider   string              `json:"provider"`
	Model      string              `json:"model"`
	BaseURL    string              `json:"base_url,omitempty"`
	APIKeyName string              `json:"api_key_name,omitempty"`
	State      any                 `json:"state"`
	Questions  map[string]question `json:"questions"`
}

type question struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type providerDefinition struct {
	baseURL string
	path    string
	keyName string
}

var providers = map[string]providerDefinition{
	openRouter: {baseURL: openRouterBaseURL, path: openRouterPath, keyName: openRouterKey},
	typeSafe:   {baseURL: typeSafeBaseURL, path: typeSafePath, keyName: typeSafeKey},
}

func parseConfig(raw map[string]any) (config, error) {
	var cfg config
	if err := registry.ValidateExecutorConfig(executorType, raw); err != nil {
		return cfg, err
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return cfg, fmt.Errorf("decision configuration: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("decision configuration: %w", err)
	}
	return cfg, nil
}

func validateStep(step ir.Step) error {
	cfg, err := parseConfig(step.ExecutorConfig.Config)
	if err != nil {
		return err
	}
	return cfg.validate(true)
}

func (c config) validate(deferReferences bool) error {
	if !deferReferences || !value.HasValueReference(c.Provider) {
		if _, ok := providers[c.Provider]; !ok {
			return fmt.Errorf("decision: with.provider must be openrouter or typesafe")
		}
	}
	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("decision: with.model is required")
	}
	if c.APIKeyName != "" && !value.ValidEnvName(c.APIKeyName) {
		return fmt.Errorf("decision: with.api_key_name must name an environment variable")
	}
	if c.BaseURL != "" && (!deferReferences || !value.HasValueReference(c.BaseURL)) {
		u, err := url.Parse(c.BaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || strings.ContainsAny(c.BaseURL, "?#") {
			return fmt.Errorf("decision: with.base_url must be an absolute HTTP API root without credentials, query, or fragment")
		}
		if u.Scheme == "http" && !strings.EqualFold(u.Hostname(), "localhost") && !net.ParseIP(u.Hostname()).IsLoopback() {
			return fmt.Errorf("decision: with.base_url must use HTTPS for non-loopback hosts")
		}
	}
	return nil
}

func (c config) connection() (endpoint, keyName string, err error) {
	provider := providers[c.Provider]
	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = provider.baseURL
	}
	keyName = c.APIKeyName
	if keyName == "" {
		keyName = provider.keyName
	}
	endpoint, err = url.JoinPath(baseURL, provider.path)
	return endpoint, keyName, err
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Required:             []string{"provider", "model", "state", "questions"},
	Properties: map[string]*jsonschema.Schema{
		"provider":     {Type: "string", MinLength: new(1)},
		"model":        {Type: "string", MinLength: new(1)},
		"base_url":     {Type: "string"},
		"api_key_name": {Type: "string"},
		"state":        {Types: []string{"string", "object", "array"}},
		"questions": {
			Type:          "object",
			MinProperties: new(1),
			AdditionalProperties: &jsonschema.Schema{
				Type:                 "object",
				AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
				Required:             []string{"type", "instructions"},
				Properties: map[string]*jsonschema.Schema{
					"type":         {Type: "string", Enum: []any{choiceType, scoreType, noulType}},
					"instructions": {Types: []string{"string", "object", "array"}},
					"criteria":     {},
				},
				OneOf: []*jsonschema.Schema{
					{
						Required: []string{"criteria"},
						Properties: map[string]*jsonschema.Schema{
							"type": {Enum: []any{choiceType}},
							"criteria": {
								Type: "object", MinProperties: new(2), MaxProperties: new(255),
								AdditionalProperties: &jsonschema.Schema{Types: []string{"string", "null"}},
							},
						},
					},
					{
						Required: []string{"criteria"},
						Properties: map[string]*jsonschema.Schema{
							"type":     {Enum: []any{scoreType}},
							"criteria": {Type: "array", MinItems: new(2), MaxItems: new(10), Items: &jsonschema.Schema{Type: "string"}},
						},
					},
					{
						Properties: map[string]*jsonschema.Schema{
							"type": {Enum: []any{noulType}},
							"criteria": {
								Type: "object", Required: []string{"true", "false"},
								AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
								Properties:           map[string]*jsonschema.Schema{"true": {Type: "string"}, "false": {Type: "string"}},
							},
						},
					},
				},
			},
		},
	},
}
