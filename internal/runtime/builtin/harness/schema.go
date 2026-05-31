// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"github.com/dagucloud/dagu/internal/core"
	"github.com/google/jsonschema-go/jsonschema"
)

var configSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"provider": {Type: "string", Description: "Harness provider name. Use builtin for Dagu's in-process agent, a built-in CLI provider, or a custom top-level harnesses entry."},
		"fallback": {
			Type: "array",
			Items: &jsonschema.Schema{
				Type: "object",
			},
			Description: "Ordered alternative provider configs tried after the primary config fails",
		},
	},
	// provider is required (validated in Go).
	// CLI providers pass other keys through as CLI flags; builtin validates its
	// agent fields in Go.
}

func init() {
	core.RegisterExecutorConfigSchema("harness", configSchema)
}
