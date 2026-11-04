// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package jq

import (
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/google/jsonschema-go/jsonschema"
)

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Properties: map[string]*jsonschema.Schema{
		"raw":   {Type: "boolean", Description: "Output raw strings without JSON encoding (like jq -r)"},
		"input": {Type: "string", Description: "File path to read JSON input from. Mutually exclusive with script."},
		// filter and data are consumed by the jq.filter action normalizer and
		// folded into command/script before the executor config is validated;
		// they are listed here so the closed schema does not reject them when a
		// caller supplies them directly.
		"filter": {Description: "jq filter expression (jq.filter action)."},
		"data":   {Description: "jq input, an inline value or a file:// reference (jq.filter action)."},
		"args": {
			Type:                 "object",
			Description:          "Variables to bind in the jq filter. Each key is available as $<key>; values keep their YAML type (strings may use ${...} references). Supplying args, including {}, makes the filter literal jq source.",
			AdditionalProperties: &jsonschema.Schema{},
		},
	},
}

func init() {
	registry.RegisterExecutorConfigSchema("jq", configSchema)
}
