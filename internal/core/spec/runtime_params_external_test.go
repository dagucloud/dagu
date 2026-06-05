// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/stretchr/testify/require"
)

func TestResolveRuntimeParamsListPreservesValueWithSpaces(t *testing.T) {
	t.Parallel()

	yamlData := []byte(`
name: runtime-params
params:
  - topic: ""
steps:
  - name: test
    run: echo "$topic"
`)
	dag, err := spec.LoadYAML(context.Background(), yamlData, spec.WithoutEval())
	require.NoError(t, err)
	dag.YamlData = yamlData

	resolved, err := spec.ResolveRuntimeParams(context.Background(), dag, []string{"topic=hello world"}, spec.ResolveRuntimeParamsOptions{})
	require.NoError(t, err)
	require.Contains(t, resolved.Params, "topic=hello world")
}
