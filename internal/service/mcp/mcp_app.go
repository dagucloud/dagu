// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	_ "embed"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpAppsExtensionURI  = "io.modelcontextprotocol/ui"
	mcpAppMIMEType       = "text/html;profile=mcp-app"
	runInspectorURI      = "ui://dagu/run-inspector/v1"
	runInspectorMetaKey  = "ui/resourceUri"
	runInspectorResource = "run_inspector"
)

//go:embed app/run-inspector.html
var runInspectorHTML string

func runInspectorToolMeta() mcpsdk.Meta {
	return mcpsdk.Meta{
		"ui": map[string]any{
			"resourceUri": runInspectorURI,
			"visibility":  []string{"model", "app"},
		},
		runInspectorMetaKey: runInspectorURI,
	}
}

func runInspectorResourceMeta() mcpsdk.Meta {
	return mcpsdk.Meta{
		"ui": map[string]any{
			"prefersBorder": true,
		},
	}
}

func mcpAppsCapability() map[string]any {
	return map[string]any{
		"mimeTypes": []string{mcpAppMIMEType},
	}
}
