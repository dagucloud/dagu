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

	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

const executorType = "mcp"

type config struct {
	URL  string `mapstructure:"url"`
	Tool string `mapstructure:"tool"`
	// Connection names the connection whose token the call uses, in place
	// of url and an Authorization header.
	Connection string `mapstructure:"connection"`
	// Server names the connection's service to call.
	Server     string            `mapstructure:"server"`
	Arguments  map[string]any    `mapstructure:"arguments"`
	Headers    map[string]string `mapstructure:"headers"`
	IncludeRaw bool              `mapstructure:"include_raw"`
	SaveBinary bool              `mapstructure:"save_binary"`
}

// argumentsHook lets a whole-value reference such as ${params.args} resolve to
// a JSON object string at runtime.
func argumentsHook(from, to reflect.Type, data any) (any, error) {
	str, ok := data.(string)
	if !ok || from.Kind() != reflect.String || to.Kind() != reflect.Map {
		return data, nil
	}
	obj, ok := decodeJSON(str).(map[string]any)
	if !ok {
		return nil, errors.New("arguments must be a JSON object")
	}
	return obj, nil
}

// decodeJSON returns the single JSON value s holds, with numbers kept as
// written, or nil when s is not exactly one JSON value.
func decodeJSON(s string) any {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil
	}
	return v
}

// decodeConfig decodes with.*; at load time an unresolved reference in
// arguments or url is accepted and checked once resolved at runtime.
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
	return cfg, cfg.validate(loadTime)
}

func (c config) validate(loadTime bool) error {
	if strings.TrimSpace(c.Tool) == "" {
		return errors.New("mcp.call: with.tool is required")
	}
	if c.Server != "" && c.Connection == "" {
		return errors.New("mcp.call: with.server requires with.connection")
	}
	// The DAG builder validates the connection's name and server, which it
	// collects for the run's admission checks.
	if c.Connection != "" && !loadTime {
		return errors.New("mcp.call: with.connection is not supported yet")
	}
	if c.URL == "" {
		if c.Connection != "" {
			return nil
		}
		return errors.New("mcp.call: with.url is required")
	}
	if loadTime && value.HasAnyReference(c.URL) {
		return nil
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
	Required:             []string{"tool"},
	Properties: map[string]*jsonschema.Schema{
		"url":        {Type: "string", Description: "Streamable HTTP MCP endpoint (http or https). Must not contain credentials. Required without connection."},
		"tool":       {Type: "string", Description: "Name of the MCP tool to call."},
		"connection": {Type: "string", Description: "Name of the connection whose token the call uses, in place of url and an Authorization header."},
		"server":     {Type: "string", Description: "The connection's service to call, such as gmail. Requires connection."},
		"arguments": {AnyOf: []*jsonschema.Schema{
			{Type: "object", AdditionalProperties: &jsonschema.Schema{}},
			{Type: "string", Description: "Whole-value reference resolving to a JSON object."},
		}, Description: "JSON arguments passed to the tool."},
		"headers":     {Type: "object", Description: "HTTP headers sent with every request, for example Authorization: Bearer ${SECRET}.", AdditionalProperties: &jsonschema.Schema{Type: "string"}},
		"include_raw": {Type: "boolean", Description: "Also emit the unmodified MCP tool result under raw."},
		"save_binary": {Type: "boolean", Description: "Save image, audio, and blob resource data as run artifacts and output each file's path instead of base64. Requires artifacts."},
	},
}

func init() {
	registry.RegisterExecutorConfigSchema(executorType, configSchema)
}
