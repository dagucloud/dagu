// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// Nested navigation follows a path from the root schema into the
// definitions its properties reference.
func TestSchemaDrillsIntoNestedPath(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	root := dagu.Run("schema", "dag")
	root.ExpectExitCode(0)
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal([]byte(root.Stdout()), &schema))
	require.Contains(t, schema.Properties, "steps")

	// A nested path is shown with the references that define its shape
	// expanded, so the fields of the referenced definition are visible.
	nested := dagu.Run("schema", "dag", "steps.container")
	nested.ExpectExitCode(0)
	var container struct {
		OneOf []struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"oneOf"`
	}
	require.NoError(t, json.Unmarshal([]byte(nested.Stdout()), &container))
	var fields []string
	for _, branch := range container.OneOf {
		for name := range branch.Properties {
			fields = append(fields, name)
		}
	}
	require.Contains(t, fields, "image")
}

// TestSchemaShowsConfigRootFields asserts on "coordinator", a property that
// exists only on the config root schema and not on the DAG schema, so the
// test can't pass against the wrong schema.
func TestSchemaShowsConfigRootFields(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("schema", "config")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), `"coordinator"`)
}

func TestSchemaRejectsUnknownName(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("schema", "bogus")
	result.ExpectNonZeroExitCode()
}
