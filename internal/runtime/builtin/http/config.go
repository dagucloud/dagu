// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package http

import (
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/google/jsonschema-go/jsonschema"
)

var configSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"method":  {Type: "string", Description: "HTTP method for action: http.request"},
		"url":     {Type: "string", Description: "Request URL for action: http.request"},
		"timeout": {Type: "integer", Description: "Request timeout in seconds"},
		"headers": {
			Type:                 "object",
			AdditionalProperties: &jsonschema.Schema{Type: "string"},
			Description:          "HTTP headers to send",
		},
		"query": {
			Type:                 "object",
			AdditionalProperties: &jsonschema.Schema{Type: "string"},
			Description:          "Query parameters",
		},
		"form": {
			Type:                 "object",
			AdditionalProperties: &jsonschema.Schema{Type: "string"},
			Description:          "Multipart form fields keyed by field name",
		},
		"files": {
			Type:                 "object",
			AdditionalProperties: &jsonschema.Schema{Type: "string"},
			Description:          "Multipart file paths keyed by field name",
		},
		"body":            {Type: "string", Description: "Request body content"},
		"silent":          {Type: "boolean", Description: "Suppress headers/status output on success"},
		"debug":           {Type: "boolean", Description: "Enable debug mode"},
		"format":          {Type: "string", Description: "Response output format. Use json for structured stdout."},
		"output":          {Type: "string", Description: "File path to write the response body to. When set, the response body is written to this file instead of stdout."},
		"json":            {Type: "boolean", Description: "Format output as JSON"},
		"skip_tls_verify": {Type: "boolean", Description: "Skip TLS certificate verification"},
	},
}

func init() {
	registry.RegisterExecutorConfigSchema("http", configSchema)
}
