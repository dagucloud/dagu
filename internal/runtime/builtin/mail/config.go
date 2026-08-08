// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mail

import (
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/google/jsonschema-go/jsonschema"
)

var configSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"from":    {Type: "string", Description: "Sender email address"},
		"to":      {Description: "Recipient email address(es) - string or array of strings"},
		"subject": {Type: "string", Description: "Email subject line"},
		"message": {Type: "string", Description: "Email body content"},
		"attachments": {
			Type:        "array",
			Items:       &jsonschema.Schema{Type: "string"},
			Description: "File paths to attach",
		},
	},
}

func init() {
	registry.RegisterExecutorConfigSchema("mail", configSchema)
}
