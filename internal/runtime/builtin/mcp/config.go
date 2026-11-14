// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"errors"
	"fmt"
	"net/url"
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

func decodeConfig(step ir.Step) (config, error) {
	var cfg config
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		ErrorUnused: true,
		Result:      &cfg,
	})
	if err != nil {
		return cfg, err
	}
	if err := dec.Decode(step.ExecutorConfig.Config); err != nil {
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
	_, err := decodeConfig(step)
	return err
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Required:             []string{"url", "tool"},
	Properties: map[string]*jsonschema.Schema{
		"url":         {Type: "string", Description: "Streamable HTTP MCP endpoint (http or https). Must not contain credentials."},
		"tool":        {Type: "string", Description: "Name of the MCP tool to call."},
		"arguments":   {Type: "object", Description: "JSON arguments passed to the tool.", AdditionalProperties: &jsonschema.Schema{}},
		"headers":     {Type: "object", Description: "HTTP headers sent with every request, for example Authorization: Bearer ${SECRET}.", AdditionalProperties: &jsonschema.Schema{Type: "string"}},
		"include_raw": {Type: "boolean", Description: "Also emit the unmodified MCP tool result under raw."},
	},
}

func init() {
	registry.RegisterExecutorConfigSchema(executorType, configSchema)
}
