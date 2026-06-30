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
