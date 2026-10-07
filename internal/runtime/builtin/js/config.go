// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package js

import (
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

type jsConfig struct {
	Input     any    `mapstructure:"input"`
	InputFile string `mapstructure:"input_file"`
	Format    string `mapstructure:"format"`
	Timeout   any    `mapstructure:"timeout"`
}

var configSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"input": {
			Description: "Value bound to the script's input parameter. Any YAML value; strings may use ${...} references. Mutually exclusive with input_file.",
		},
		"input_file": {
			Type:        "string",
			Description: "File whose contents are bound to the script's input parameter. Mutually exclusive with input.",
		},
		"format": {
			Type:        "string",
			Enum:        []any{formatAuto, formatText, formatJSON},
			Description: "How string input is interpreted. auto (default) parses a JSON object or array and binds any other string as-is, text always binds the string as-is, json always parses and fails on invalid JSON. Applies to input_file contents and to a string input.",
		},
		"timeout": {
			Types:       []string{"integer", "string"},
			Description: "Maximum script run time in seconds, or a duration such as 2m. Defaults to the step timeout, or 60s when the step has none.",
		},
	},
}

func decodeConfig(raw map[string]any, cfg *jsConfig) (hasInput bool, err error) {
	if raw == nil {
		return false, nil
	}
	_, hasInput = raw["input"]
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           cfg,
		WeaklyTypedInput: true,
		ErrorUnused:      true,
		TagName:          "mapstructure",
	})
	if err != nil {
		return false, err
	}
	return hasInput, decoder.Decode(raw)
}

func init() {
	registry.RegisterExecutorConfigSchema("js", configSchema)
}
