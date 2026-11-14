// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"reflect"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

const executorType = "mcp"

type config struct {
	URL        string            `mapstructure:"url"`
	Tool       string            `mapstructure:"tool"`
	Arguments  map[string]any    `mapstructure:"arguments"`
	Headers    map[string]string `mapstructure:"headers"`
	IncludeRaw bool              `mapstructure:"include_raw"`
}

// argumentsHook lets a whole-value reference such as ${params.args} resolve to
// a JSON object string at runtime.
func argumentsHook(from, to reflect.Type, data any) (any, error) {
	str, ok := data.(string)
	if !ok || from.Kind() != reflect.String || to.Kind() != reflect.Map {
		return data, nil
	}
	dec := json.NewDecoder(strings.NewReader(str))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil || obj == nil || dec.More() {
		return nil, errors.New("arguments must be a JSON object")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("arguments must be a JSON object")
	}
	return obj, nil
}

// decodeConfig decodes with.*; at load time an unresolved reference in
// arguments is accepted and checked once resolved at runtime.
func decodeConfig(step ir.Step, loadTime bool) (config, error) {
	var cfg config
	raw := step.ExecutorConfig.Config
	if args, ok := raw["arguments"].(string); ok && loadTime && strings.Contains(args, "$") {
		raw = maps.Clone(raw)
		delete(raw, "arguments")
	}
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		ErrorUnused: true,
		DecodeHook:  argumentsHook,
		Result:      &cfg,
	})
	if err != nil {
		return cfg, err
	}
	if err := dec.Decode(raw); err != nil {
		return cfg, fmt.Errorf("mcp.call: invalid with: %w", err)
	}
	return cfg, cfg.validate()
}

func (c config) validate() error {
	if strings.TrimSpace(c.Tool) == "" {
		return errors.New("mcp.call: with.tool is required")
	}
	if c.URL == "" {
		return errors.New("mcp.call: with.url is required")
	}
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("mcp.call: with.url must be an absolute http(s) URL")
	}
	if u.User != nil {
		return errors.New("mcp.call: with.url must not contain credentials; use with.headers")
	}
	return nil
}

func validateStep(step ir.Step) error {
	if step.ExecutorConfig.Type != executorType {
		return nil
	}
	_, err := decodeConfig(step, true)
	return err
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Required:             []string{"url", "tool"},
	Properties: map[string]*jsonschema.Schema{
		"url":  {Type: "string", Description: "Streamable HTTP MCP endpoint (http or https). Must not contain credentials."},
		"tool": {Type: "string", Description: "Name of the MCP tool to call."},
		"arguments": {AnyOf: []*jsonschema.Schema{
			{Type: "object", AdditionalProperties: &jsonschema.Schema{}},
			{Type: "string", Description: "Whole-value reference resolving to a JSON object."},
		}, Description: "JSON arguments passed to the tool."},
		"headers":     {Type: "object", Description: "HTTP headers sent with every request, for example Authorization: Bearer ${SECRET}.", AdditionalProperties: &jsonschema.Schema{Type: "string"}},
		"include_raw": {Type: "boolean", Description: "Also emit the unmodified MCP tool result under raw."},
	},
}

func init() {
	registry.RegisterExecutorConfigSchema(executorType, configSchema)
}
