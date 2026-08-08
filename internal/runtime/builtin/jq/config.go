// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package jq

import (
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/google/jsonschema-go/jsonschema"
)

var configSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"raw":   {Type: "boolean", Description: "Output raw strings without JSON encoding (like jq -r)"},
		"input": {Type: "string", Description: "File path to read JSON input from. Mutually exclusive with script."},
	},
}

func init() {
	registry.RegisterExecutorConfigSchema("jq", configSchema)
}
