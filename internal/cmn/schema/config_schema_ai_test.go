// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

// The schema of the block ai (Spec 093) and the table of keys of the code agree:
// the same keys, the same defaults and the same limits.
func TestConfigSchemaAIMatchesTheKeyTable(t *testing.T) {
	t.Parallel()

	var doc struct {
		Definitions map[string]struct {
			Properties map[string]struct {
				Type    string `json:"type"`
				Minimum *int   `json:"minimum"`
				Maximum *int   `json:"maximum"`
				Default any    `json:"default"`
				Ref     string `json:"$ref"`
			} `json:"properties"`
		} `json:"definitions"`
	}
	require.NoError(t, json.Unmarshal(ConfigSchemaJSON, &doc))

	blockOf := func(name string) map[string]struct {
		Type    string `json:"type"`
		Minimum *int   `json:"minimum"`
		Maximum *int   `json:"maximum"`
		Default any    `json:"default"`
		Ref     string `json:"$ref"`
	} {
		return doc.Definitions[name].Properties
	}
	blocks := map[string]string{"": "AIDef", "upstream.": "AIUpstreamDef", "relay.": "AIRelayDef"}

	seen := 0
	for _, key := range config.AIKeyDocs() {
		prefix := ""
		if before, _, nested := strings.Cut(key.Key, "."); nested {
			prefix = before + "."
		}
		name := strings.TrimPrefix(key.Key, prefix)
		property, ok := blockOf(blocks[prefix])[name]
		require.True(t, ok, "the schema has no property for ai.%s", key.Key)
		seen++
		if key.Type == "integer" {
			assert.Equal(t, "integer", property.Type, key.Key)
			require.NotNil(t, property.Minimum, key.Key)
			require.NotNil(t, property.Maximum, key.Key)
			assert.Equal(t, key.Min, *property.Minimum, key.Key)
			assert.Equal(t, key.Max, *property.Maximum, key.Key)
			assert.Equal(t, key.Default, strconv.Itoa(int(property.Default.(float64))), key.Key)
		} else {
			assert.Equal(t, "string", property.Type, key.Key)
			assert.Equal(t, "", property.Default, key.Key)
		}
	}
	// The schema has no property that the table lacks.
	count := len(blockOf("AIUpstreamDef")) + len(blockOf("AIRelayDef"))
	for _, property := range blockOf("AIDef") {
		if property.Ref == "" {
			count++
		}
	}
	assert.Equal(t, seen, count)
}

func TestConfigSchemaAI(t *testing.T) {
	t.Parallel()

	resolved := mustResolveConfigSchema(t)
	for name, spec := range map[string]string{
		"all keys": `
ai:
  models_refresh_hours: 12
  models_timeout_sec: 10
  upstream:
    proxy_url: "http://proxy.example.com:3128"
  relay:
    max_request_bytes: 4096
    idle_timeout_sec: 60
`,
		"empty proxy": "ai:\n  upstream:\n    proxy_url: \"\"\n",
	} {
		require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, spec)), name)
	}
	for name, spec := range map[string]string{
		"unknown key":       "ai:\n  models_timeout: 3\n",
		"unknown relay key": "ai:\n  relay:\n    max_stream: 3\n",
		"api key":           "ai:\n  api_key: sk-123\n",
		"string integer":    "ai:\n  models_timeout_sec: \"30\"\n",
		"out of range":      "ai:\n  relay:\n    max_streams_per_run: 0\n",
		"above range":       "ai:\n  relay:\n    max_header_bytes: 2000000\n",
		"proxy with user":   "ai:\n  upstream:\n    proxy_url: \"http://u:p@proxy.example.com\"\n",
		"proxy scheme":      "ai:\n  upstream:\n    proxy_url: \"socks5://proxy.example.com\"\n",
	} {
		require.Error(t, resolved.Validate(mustParseYAMLDocument(t, spec)), name)
	}
}
