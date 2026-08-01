// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	_ "embed"
	"encoding/json"
	"net/url"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpAppsExtensionURI  = "io.modelcontextprotocol/ui"
	mcpAppMIMEType       = "text/html;profile=mcp-app"
	runInspectorURI      = "ui://dagu/run-inspector/v8"
	runInspectorMetaKey  = "ui/resourceUri"
	runInspectorResource = "run_inspector"
)

//go:embed app/run-inspector.html
var runInspectorHTML string

func runInspectorHTMLWithWebBaseURL(webBaseURL string) string {
	encodedURL, _ := json.Marshal(strings.TrimRight(webBaseURL, "/"))
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

func runInspectorResourceMeta(webBaseURL string) mcpsdk.Meta {
	meta := mcpsdk.Meta{
		"ui": map[string]any{
			"prefersBorder": true,
		},
	}
	if origin := webOrigin(webBaseURL); origin != "" {
		meta["openai/widgetCSP"] = map[string]any{
			"redirect_domains": []string{origin},
		}
	}
	return meta
}

func webOrigin(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	return (&url.URL{Scheme: scheme, Host: parsed.Host}).String()
}

func mcpAppsCapability() map[string]any {
	return map[string]any{
		"mimeTypes": []string{mcpAppMIMEType},
	}
}
