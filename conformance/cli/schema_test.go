// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestSchemaShowsDAGRootFields(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("schema", "dag")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "steps")
}

// TestSchemaDrillsIntoNestedPath proves the command actually traverses into
// the "steps" sub-schema rather than falling back to the full root schema
// (which is also non-empty and would satisfy a bare NotEmpty check): it
// asserts on the steps property's own description text, which only the
// drilled-down output emits on its own, and asserts the root-schema-only
// "$schema" marker is absent, since the full root document always starts
// with one.
func TestSchemaDrillsIntoNestedPath(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("schema", "dag", "steps")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "List of steps that define the DAG")
	require.NotContains(t, result.Stdout(), `"$schema"`)
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
