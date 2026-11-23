// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkflowTestSchemaRepoCopyMatchesEmbeddedSchema(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	repoSchemaPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "schemas", "workflow-test.schema.json")
	repoSchemaJSON, err := os.ReadFile(repoSchemaPath)
	require.NoError(t, err)
	require.Equal(t, string(WorkflowTestSchemaJSON), string(repoSchemaJSON))
}
