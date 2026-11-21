// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 092 "Reserved labels": a key of worker_selector that starts with agent.
// in a DAG or a step is an error. A key of a step selector that is a value
// reference would resolve only at run time, so a run parameter could set the
// label of an agent worker on the child run. The loader refuses such a key.
func TestStepSelectorKeyFromAParameter(t *testing.T) {
	t.Parallel()

	const yamlText = `
params:
  - K: zone
steps:
  - id: child
    action: dag.run
    worker_selector:
      "${K}": "true"
    with:
      dag: child
`
	dag, err := spec.LoadYAML(t.Context(), []byte(yamlText),
		spec.WithDAGsDir(t.TempDir()), spec.WithParams([]string{"K=agent.provider.claude"}))
	if err != nil {
		assert.Contains(t, err.Error(), "agent.")
		return
	}
	require.Len(t, dag.Steps, 1)
	for key := range dag.Steps[0].WorkerSelector {
		assert.NotContains(t, key, "${", "the key %q of the step selector is resolved only at run time, with no reserved-prefix check", key)
	}
}

// The same reference at the level of the DAG is resolved before the check, so
// the loader refuses it. This is the control of the test above.
func TestDAGSelectorKeyFromAParameter(t *testing.T) {
	t.Parallel()

	const yamlText = `
params:
  - K: zone
worker_selector:
  "${K}": "true"
steps:
  - id: a
    run: echo hi
`
	_, err := spec.LoadYAML(t.Context(), []byte(yamlText),
		spec.WithDAGsDir(t.TempDir()), spec.WithParams([]string{"K=agent.provider.claude"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `worker selector "agent.provider.claude"`)
}
