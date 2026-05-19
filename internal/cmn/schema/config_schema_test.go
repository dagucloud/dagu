// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/require"
)

func TestConfigSchemaTopLevelPropertiesCoverDefinition(t *testing.T) {
	t.Parallel()

	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(ConfigSchemaJSON, &doc))

	defType := reflect.TypeFor[config.Definition]()
	for field := range defType.Fields() {
		key := field.Tag.Get("mapstructure")
		if key == "" || key == "-" {
			continue
		}
		key = strings.Split(key, ",")[0]
		require.Containsf(
			t,
			doc.Properties,
			key,
			"config schema is missing top-level property for Definition.%s (%q)",
			field.Name,
			key,
		)
	}
}

func TestConfigSchemaBotProviderValidation(t *testing.T) {
	t.Parallel()

	resolved := mustResolveConfigSchema(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "ValidTelegramBotConfig",
			spec: `
bots:
  provider: telegram
  safe_mode: true
  telegram:
    token: secret-token
    allowed_chat_ids: [12345]
`,
		},
		{
			name: "ValidSlackBotConfig",
			spec: `
bots:
  provider: slack
  slack:
    bot_token: xoxb-example
    app_token: xapp-example
    allowed_channel_ids: [C12345]
    respond_to_all: true
`,
		},
		{
			name: "ValidLineBotConfig",
			spec: `
bots:
  provider: line
  line:
    channel_access_token: line-channel-token
    channel_secret: line-channel-secret
    allowed_source_ids: [U12345, C67890]
    respond_to_all: false
`,
		},
		{
			name: "ValidDiscordBotConfig",
			spec: `
bots:
  provider: discord
  discord:
    token: discord-token
    allowed_channel_ids: [C12345]
    respond_to_all: true
`,
		},
		{
			name: "RejectInvalidProvider",
			spec: `
bots:
  provider: unknown
`,
			wantErr: "bots",
		},
		{
			name: "RejectTelegramWithoutAllowedChats",
			spec: `
bots:
  provider: telegram
  telegram:
    token: secret-token
`,
			wantErr: "bots",
		},
		{
			name: "RejectSlackWithoutTokens",
			spec: `
bots:
  provider: slack
  slack:
    allowed_channel_ids: [C12345]
`,
			wantErr: "bots",
		},
		{
			name: "RejectLineWithoutChannelSecret",
			spec: `
bots:
  provider: line
  line:
    channel_access_token: line-channel-token
    allowed_source_ids: [U12345]
`,
			wantErr: "bots",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestConfigSchemaCheckUpdatesValidation(t *testing.T) {
	t.Parallel()

	resolved := mustResolveConfigSchema(t)

	tests := []struct {
		name string
		spec string
	}{
		{
			name: "CheckUpdatesTrue",
			spec: `
check_updates: true
`,
		},
		{
			name: "CheckUpdatesFalse",
			spec: `
check_updates: false
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			require.NoError(t, resolved.Validate(doc))
		})
	}
}

func TestConfigSchemaRepoCopyMatchesEmbeddedSchema(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	repoSchemaPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "schemas", "config.schema.json")
	repoSchemaJSON, err := os.ReadFile(repoSchemaPath)
	require.NoError(t, err)
	require.Equal(t, string(ConfigSchemaJSON), string(repoSchemaJSON))
}

func mustResolveConfigSchema(t *testing.T) *jsonschema.Resolved {
	t.Helper()

	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(ConfigSchemaJSON, &schema))

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{})
	require.NoError(t, err)
	return resolved
}
