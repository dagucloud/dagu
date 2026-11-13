// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/schema"
	"github.com/stretchr/testify/require"
)

// The DAG schema's xlsx with block and the registry schema describe the
// same options, so an editor and a parse error say the same thing.
func TestDAGSchemaMatchesConfigSchema(t *testing.T) {
	t.Parallel()
	var dagSchema struct {
		Definitions struct {
			XlsxActionConfig struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
			} `json:"xlsxActionConfig"`
		} `json:"definitions"`
	}
	require.NoError(t, json.Unmarshal(schema.DAGSchemaJSON, &dagSchema))
	published := dagSchema.Definitions.XlsxActionConfig.Properties
	require.NotEmpty(t, published)

	registered := map[string]string{}
	for name, prop := range configSchema.Properties {
		registered[name] = prop.Description
	}
	descriptions := map[string]string{}
	for name, prop := range published {
		descriptions[name] = prop.Description
	}
	require.Equal(t, registered, descriptions)
}
