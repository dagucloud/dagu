// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package http

import (
	"github.com/dagucloud/dagu/internal/core"
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
		"body":            {Type: "string", Description: "Request body content"},
		"silent":          {Type: "boolean", Description: "Suppress headers/status output on success"},
		"debug":           {Type: "boolean", Description: "Enable debug mode"},
		"json":            {Type: "boolean", Description: "Format output as JSON"},
		"skip_tls_verify": {Type: "boolean", Description: "Skip TLS certificate verification"},
	},
}

func init() {
	core.RegisterExecutorConfigSchema("http", configSchema)
}
