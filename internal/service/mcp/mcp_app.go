// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	_ "embed"
	"encoding/json"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpAppsExtensionURI  = "io.modelcontextprotocol/ui"
	mcpAppMIMEType       = "text/html;profile=mcp-app"
	runInspectorURI      = "ui://dagu/run-inspector/v3"
	runInspectorMetaKey  = "ui/resourceUri"
	runInspectorResource = "run_inspector"
)

//go:embed app/run-inspector.html
var runInspectorHTML string

func runInspectorHTMLWithWebBaseURL(webBaseURL string) string {
	encodedURL, err := json.Marshal(strings.TrimRight(webBaseURL, "/"))
	if err != nil {
		encodedURL = []byte(`""`)
	}
	return strings.Replace(runInspectorHTML, "__DAGU_WEB_BASE_URL__", string(encodedURL), 1)
}

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
