// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package state

import (
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

type config struct {
	Scope           string `mapstructure:"scope"`
	Namespace       string `mapstructure:"namespace"`
	Key             string `mapstructure:"key"`
	Prefix          string `mapstructure:"prefix"`
	Value           any    `mapstructure:"value"`
	Default         any    `mapstructure:"default"`
	ExpectedVersion *int64 `mapstructure:"expected_version"`
	CreateOnly      bool   `mapstructure:"create_only"`
	Required        bool   `mapstructure:"required"`
	Update          *bool  `mapstructure:"update"`
	Limit           int    `mapstructure:"limit"`
	IncludeValues   bool   `mapstructure:"include_values"`

	hasValue   bool
	hasDefault bool
}

var stateListLimitMinimum = float64(0)

func decodeConfig(raw map[string]any, cfg *config) error {
	if raw == nil {
		raw = map[string]any{}
	}
	_, cfg.hasValue = raw["value"]
	_, cfg.hasDefault = raw["default"]

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
	switch operation {
	case opGet, opSet, opDelete, opDiff:
		if strings.TrimSpace(cfg.Key) == "" {
			return fmt.Errorf("%w: key is required", errConfig)
		}
	case opList:
	default:
		return fmt.Errorf("%w: unsupported operation %q", errConfig, operation)
	}

	if (operation == opSet || operation == opDiff) && !cfg.hasValue {
		return fmt.Errorf("%w: value is required for %s", errConfig, operation)
	}
	if cfg.Limit < 0 {
		return fmt.Errorf("%w: limit must be greater than or equal to zero", errConfig)
	}
	return nil
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Properties: map[string]*jsonschema.Schema{
		"scope":            {Type: "string", Enum: []any{"dag", "root_dag", "global", "custom"}, Description: "State scope. Defaults to dag."},
		"namespace":        {Type: "string", Description: "State namespace. Required for custom scope; otherwise defaults from scope."},
		"key":              {Type: "string", Description: "State key for get, set, delete, and diff."},
		"prefix":           {Type: "string", Description: "Key prefix for state.list."},
		"value":            {Description: "JSON-serializable value for state.set and state.diff."},
		"default":          {Description: "Default JSON-serializable value returned by state.get when the key is missing."},
		"expected_version": {Type: "integer", Description: "Optimistic concurrency version required for state.set or state.diff."},
		"create_only":      {Type: "boolean", Description: "Fail state.set when the key already exists."},
		"required":         {Type: "boolean", Description: "Fail state.get when the key is missing."},
		"update":           {Type: "boolean", Description: "Whether state.diff writes the new value when changed. Defaults to true."},
		"limit":            {Type: "integer", Minimum: &stateListLimitMinimum, Description: "Maximum entries returned by state.list. Zero means no limit."},
		"include_values":   {Type: "boolean", Description: "Include entry values in state.list output."},
	},
}

func init() {
	core.RegisterExecutorConfigSchema(executorType, configSchema)
}
