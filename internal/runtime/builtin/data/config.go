// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package data

import (
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

type config struct {
	From      string   `mapstructure:"from"`
	To        string   `mapstructure:"to"`
	Input     string   `mapstructure:"input"`
	Data      any      `mapstructure:"data"`
	Select    string   `mapstructure:"select"`
	Raw       bool     `mapstructure:"raw"`
	HasHeader *bool    `mapstructure:"has_header"`
	Headers   *bool    `mapstructure:"headers"`
	Delimiter string   `mapstructure:"delimiter"`
	Columns   []string `mapstructure:"columns"`

	hasData bool
}

func decodeConfig(raw map[string]any, cfg *config) error {
	if raw == nil {
		raw = map[string]any{}
	}
	_, cfg.hasData = raw["data"]

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           cfg,
		WeaklyTypedInput: true,
		ErrorUnused:      true,
		TagName:          "mapstructure",
	})
	if err != nil {
		return err
	}
	return decoder.Decode(raw)
}

func validateConfig(operation string, cfg config) error {
	if strings.TrimSpace(cfg.From) == "" {
		return fmt.Errorf("%w: from is required", errConfig)
	}
	if !isSupportedFormat(cfg.From) {
		return fmt.Errorf("%w: unsupported from format %q", errConfig, cfg.From)
	}
	if cfg.hasData && strings.TrimSpace(cfg.Input) != "" {
		return fmt.Errorf("%w: data and input are mutually exclusive", errConfig)
	}
	if !cfg.hasData && strings.TrimSpace(cfg.Input) == "" {
		return fmt.Errorf("%w: either data or input is required", errConfig)
	}
	if cfg.Delimiter != "" && len([]rune(cfg.Delimiter)) != 1 {
		return fmt.Errorf("%w: delimiter must be a single character", errConfig)
	}

	switch operation {
	case opConvert:
		if strings.TrimSpace(cfg.To) == "" {
			return fmt.Errorf("%w: to is required", errConfig)
		}
		if !isSupportedFormat(cfg.To) {
			return fmt.Errorf("%w: unsupported to format %q", errConfig, cfg.To)
		}
	case opPick:
		if strings.TrimSpace(cfg.Select) == "" {
			return fmt.Errorf("%w: select is required", errConfig)
		}
		if cfg.Raw && strings.TrimSpace(cfg.To) != "" {
			return fmt.Errorf("%w: raw and to are mutually exclusive", errConfig)
		}
		if strings.TrimSpace(cfg.To) != "" && !isSupportedFormat(cfg.To) {
			return fmt.Errorf("%w: unsupported to format %q", errConfig, cfg.To)
		}
	default:
		return fmt.Errorf("%w: unsupported operation %q", errConfig, operation)
	}
	return nil
}

func isSupportedFormat(format string) bool {
	switch strings.ToLower(format) {
	case formatJSON, formatYAML, formatCSV, formatTSV, formatText:
		return true
	default:
		return false
	}
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	OneOf: []*jsonschema.Schema{
		{Required: []string{"data"}},
		{Required: []string{"input"}},
	},
	Properties: map[string]*jsonschema.Schema{
		"from":       {Type: "string", Enum: []any{formatJSON, formatYAML, formatCSV, formatTSV, formatText}, Description: "Input format."},
		"to":         {Type: "string", Enum: []any{formatJSON, formatYAML, formatCSV, formatTSV, formatText}, Description: "Output format."},
		"input":      {Type: "string", Description: "File path to read input from. Mutually exclusive with data."},
		"data":       {Description: "Inline data to convert. Mutually exclusive with input."},
		"select":     {Type: "string", Description: "jq-style path to select for action: data.pick."},
		"raw":        {Type: "boolean", Description: "Write selected scalar values without JSON/YAML encoding for action: data.pick."},
		"has_header": {Type: "boolean", Description: "Whether CSV or TSV input has a header row. Defaults to true."},
		"headers":    {Type: "boolean", Description: "Include a header row for CSV or TSV output. Defaults to true."},
		"delimiter":  {Type: "string", Description: "Single-character delimiter override for CSV or TSV input and output."},
		"columns":    {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Column names for headerless CSV/TSV input or CSV/TSV output ordering."},
	},
	Required: []string{"from"},
}

func init() {
	core.RegisterExecutorConfigSchema(executorType, configSchema)
}
