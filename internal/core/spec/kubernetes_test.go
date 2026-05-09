// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/core/spec"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/kubernetes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKubernetesInheritance(t *testing.T) {
	t.Parallel()

	yaml := `
kubernetes:
  namespace: dag-ns
  resources:
    requests:
      cpu: "100m"
      memory: "128Mi"
  labels:
    team: platform
  annotations:
    owner: platform
  volume_mounts:
    - name: shared
      mount_path: /shared

steps:
  - name: step1
    action: k8s.run
    with:
      image: alpine:3.20
      resources:
        requests:
          cpu: "200m"
      labels:
        app: api
      annotations: {}
      volume_mounts: []
      command: echo hello

  - name: step2
    action: kubernetes.run
    with:
      image: alpine:3.20
      command: echo hello
`

	dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 2)

	step1 := dag.Steps[0]
	assert.Equal(t, "k8s", step1.ExecutorConfig.Type)
	assert.Equal(t, "alpine:3.20", step1.ExecutorConfig.Config["image"])
	assert.Equal(t, "dag-ns", step1.ExecutorConfig.Config["namespace"])

	requests1 := mustMap(t, mustMap(t, step1.ExecutorConfig.Config["resources"])["requests"])
	assert.Equal(t, "200m", requests1["cpu"])
	assert.Equal(t, "128Mi", requests1["memory"])

	labels1 := mustMap(t, step1.ExecutorConfig.Config["labels"])
	assert.Equal(t, "platform", labels1["team"])
	assert.Equal(t, "api", labels1["app"])

	assert.Empty(t, mustMap(t, step1.ExecutorConfig.Config["annotations"]))
	assert.Empty(t, mustSlice(t, step1.ExecutorConfig.Config["volume_mounts"]))

	step2 := dag.Steps[1]
	assert.Equal(t, "kubernetes", step2.ExecutorConfig.Type)
	assert.Equal(t, "dag-ns", step2.ExecutorConfig.Config["namespace"])
	requests2 := mustMap(t, mustMap(t, step2.ExecutorConfig.Config["resources"])["requests"])
	assert.Equal(t, "100m", requests2["cpu"])
	assert.Equal(t, "128Mi", requests2["memory"])
	assert.Len(t, mustSlice(t, step2.ExecutorConfig.Config["volume_mounts"]), 1)
}

func TestKubernetesDoesNotInferExecutorType(t *testing.T) {
	t.Parallel()

	yaml := `
kubernetes:
  namespace: dag-ns

steps:
  - name: step1
    run: echo hello
`

	dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	assert.Empty(t, dag.Steps[0].ExecutorConfig.Type)
	assert.Empty(t, dag.Steps[0].ExecutorConfig.Config)
}

func TestKubernetesRootSchemaValidation(t *testing.T) {
	t.Parallel()

	yaml := `
kubernetes:
  unsupported_field: true

steps:
  - name: step1
    action: k8s.run
    with:
      image: alpine:3.20
      command: echo hello
`

	_, err := spec.LoadYAML(context.Background(), []byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported_field")
}

func TestKubernetesStepRequiresEffectiveImage(t *testing.T) {
	t.Parallel()

	yaml := `
steps:
  - name: step1
    action: k8s.run
    with:
      namespace: jobs
      command: echo hello
`

	_, err := spec.LoadYAML(context.Background(), []byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "image")
}

func TestKubernetesBaseConfigMerge(t *testing.T) {
	t.Parallel()

	base := createTempYAMLFile(t, `
kubernetes:
  namespace: base-ns
  resources:
    requests:
      cpu: "100m"
      memory: "128Mi"
  labels:
    team: platform
  volume_mounts:
    - name: shared
      mount_path: /shared
`)

	child := createTempYAMLFile(t, `
kubernetes:
  namespace: child-ns
  resources:
    requests:
      cpu: "250m"
  labels:
    app: worker
  volume_mounts: []

steps:
  - name: step1
    action: k8s.run
    with:
      image: alpine:3.20
      command: echo hello
`)

	dag, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
	require.NoError(t, err)

	require.NotNil(t, dag.Kubernetes)
	assert.Equal(t, "child-ns", dag.Kubernetes["namespace"])

	rootRequests := mustMap(t, mustMap(t, dag.Kubernetes["resources"])["requests"])
	assert.Equal(t, "250m", rootRequests["cpu"])
	assert.Equal(t, "128Mi", rootRequests["memory"])

	rootLabels := mustMap(t, dag.Kubernetes["labels"])
	assert.Equal(t, "platform", rootLabels["team"])
	assert.Equal(t, "worker", rootLabels["app"])
	assert.Empty(t, mustSlice(t, dag.Kubernetes["volume_mounts"]))

	require.Len(t, dag.Steps, 1)
	assert.Equal(t, "child-ns", dag.Steps[0].ExecutorConfig.Config["namespace"])
}

func TestKubernetesInheritanceSupportsExtendedConfigAndClearing(t *testing.T) {
	t.Parallel()

	yaml := `
kubernetes:
  security_context:
    run_as_non_root: true
    capabilities:
      drop: [ALL]
  pod_security_context:
    supplemental_groups: [2000, 3000]
    seccomp_profile:
      type: RuntimeDefault
  affinity:
    node_affinity:
      required_during_scheduling_ignored_during_execution:
        node_selector_terms:
          - match_expressions:
              - key: kubernetes.io/arch
                operator: In
                values: [amd64]
  pod_failure_policy:
    rules:
      - action: Count
        on_exit_codes:
          operator: In
          values: [42]

steps:
  - name: step1
    action: k8s.run
    with:
      image: alpine:3.20
      security_context:
        capabilities:
          add: [NET_BIND_SERVICE]
      pod_security_context:
        supplemental_groups: []
      affinity: {}
      pod_failure_policy: {}
      command: echo hello
`

	dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]

	securityContext := mustMap(t, step.ExecutorConfig.Config["security_context"])
	capabilities := mustMap(t, securityContext["capabilities"])
	assert.Equal(t, []any{"NET_BIND_SERVICE"}, mustSlice(t, capabilities["add"]))
	assert.Equal(t, []any{"ALL"}, mustSlice(t, capabilities["drop"]))

	podSecurityContext := mustMap(t, step.ExecutorConfig.Config["pod_security_context"])
	assert.Empty(t, mustSlice(t, podSecurityContext["supplemental_groups"]))
	seccompProfile := mustMap(t, podSecurityContext["seccomp_profile"])
	assert.Equal(t, "RuntimeDefault", seccompProfile["type"])

	assert.Empty(t, mustMap(t, step.ExecutorConfig.Config["affinity"]))
	assert.Empty(t, mustMap(t, step.ExecutorConfig.Config["pod_failure_policy"]))
}

func TestKubernetesEmptyRootClearsBaseConfig(t *testing.T) {
	t.Parallel()

	base := createTempYAMLFile(t, `
kubernetes:
  namespace: base-ns
  labels:
    team: platform
`)

	child := createTempYAMLFile(t, `
kubernetes: {}

steps:
  - name: step1
    action: k8s.run
    with:
      image: alpine:3.20
      command: echo hello
`)

	dag, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
	require.NoError(t, err)

	require.NotNil(t, dag.Kubernetes)
	assert.Empty(t, dag.Kubernetes)
	assert.NotContains(t, dag.Steps[0].ExecutorConfig.Config, "namespace")
	assert.NotContains(t, dag.Steps[0].ExecutorConfig.Config, "labels")
}

func mustMap(t *testing.T, value any) map[string]any {
	t.Helper()

	ret, ok := value.(map[string]any)
	require.True(t, ok, "expected map[string]any, got %T", value)
	return ret
}

func mustSlice(t *testing.T, value any) []any {
	t.Helper()

	ret, ok := value.([]any)
	require.True(t, ok, "expected []any, got %T", value)
	return ret
}
