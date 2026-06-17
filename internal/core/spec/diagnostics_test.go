// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/diagnostic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadYAMLWithResultReturnsDiagnostics(t *testing.T) {
	t.Parallel()

	result, err := spec.LoadYAMLWithResult(context.Background(), []byte(`
name: diagnostics
consts:
  - image: ${consts.missing}
steps:
  - run: echo ok
`))

	require.NoError(t, err)
	require.NotNil(t, result.DAG)
	require.Len(t, result.Diagnostics, 1)

	got := result.Diagnostics[0]
	assert.Equal(t, diagnostic.SeverityNotice, got.Severity)
	assert.Equal(t, cmnvalue.DiagnosticKindValueResolution, got.Kind)
	assert.Equal(t, cmnvalue.CodeValueReferenceUnresolved, got.Code)
	assert.Equal(t, "consts.image", got.Location.FieldPath)
	assert.Equal(t, "${consts.missing}", got.Attributes["token"])
	assert.Contains(t, got.Message, "was left unchanged")
}
