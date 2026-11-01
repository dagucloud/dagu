// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package clickhouse

import (
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/google/jsonschema-go/jsonschema"
)

// configSchema defines the JSON schema for the ClickHouse executor
// configuration. Numeric and boolean options also accept a string so a
// variable reference passes until it is resolved; ParseConfig checks the
// resolved values.
var configSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"url":      {Type: "string", Description: "ClickHouse HTTP endpoint (http:// or https://), without credentials"},
		"user":     {Type: "string", Description: "ClickHouse user"},
		"password": {Type: "string", Description: "Password of the user"},
		"database": {Type: "string", Description: "Default database"},
		"file":     {Type: "string", Description: "Path of a .sql file to run instead of an inline query"},
		"params": {
			Type:                 "object",
			AdditionalProperties: &jsonschema.Schema{},
			Description:          "Server-side query parameters, bound to {name:Type} placeholders",
		},
		"settings": {
			Type:                 "object",
			AdditionalProperties: &jsonschema.Schema{},
			Description:          "ClickHouse settings applied to every statement",
		},
		"output_format":   {Type: "string", Description: "Output format: jsonl (default), json, or csv"},
		"headers":         {Types: []string{"boolean", "string"}, Description: "Include a header row in CSV output"},
		"timeout":         {Types: []string{"integer", "string"}, Description: "Step timeout in seconds (0 = none)"},
		"tls_skip_verify": {Types: []string{"boolean", "string"}, Description: "Skip TLS certificate verification"},
		"tls_ca":          {Type: "string", Description: "Path to a PEM bundle of additional root CAs"},
	},
}

func init() {
	registry.RegisterExecutorConfigSchema(executorType, configSchema)
}
