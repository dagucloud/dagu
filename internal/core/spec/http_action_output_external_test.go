// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPRequestWithOutputKeepsActionOutputSeparateFromStepOutput(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
steps:
  - id: download
    action: http.request
    with:
      method: GET
      url: https://example.com/data.bin
      output: "${context.paths.artifacts_dir}/data.bin"
    output: RESPONSE
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)
	require.NotNil(t, dag.Artifacts)
	assert.True(t, dag.Artifacts.Enabled)

	step := dag.Steps[0]
	assert.Equal(t, "RESPONSE", step.Output)
	assert.Equal(t, "${context.paths.artifacts_dir}/data.bin", step.ExecutorConfig.Config["output"])
}
