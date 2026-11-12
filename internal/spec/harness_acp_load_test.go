// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"

	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/require"
)

// TestACPLoad verifies that base configuration inheritance and cloning preserve ACP.
func TestACPLoad(t *testing.T) {
	base := createTempYAMLFile(t, `harnesses:
  adapter:
    protocol: acp
    transport: websocket
    url: wss://agent.example/acp
    ca_file: /private/ca.pem
    bearer_subprotocol: true
    cwd: /remote/workspace
    headers:
      Authorization: Bearer ${ACP_TOKEN}
`)
	child := createTempYAMLFile(t, `steps:
  - action: harness.run
    with:
      provider: adapter
      prompt: hello
`)
	dag, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
	require.NoError(t, err)
	require.Equal(t, ir.HarnessProtocolACP, dag.Harnesses["adapter"].Protocol)
	require.Equal(t, "adapter", dag.Steps[0].ExecutorConfig.Config["provider"])
	cloned := dag.Clone()
	require.Equal(t, ir.HarnessProtocolACP, cloned.Harnesses["adapter"].Protocol)
	require.Equal(t, ir.HarnessTransportWebSocket, cloned.Harnesses["adapter"].Transport)
	require.Equal(t, "wss://agent.example/acp", cloned.Harnesses["adapter"].URL)
	require.Equal(t, "/remote/workspace", cloned.Harnesses["adapter"].Cwd)
	require.Equal(t, "/private/ca.pem", cloned.Harnesses["adapter"].CAFile)
	require.True(t, cloned.Harnesses["adapter"].BearerSubprotocol)
	cloned.Harnesses["adapter"].Headers["Authorization"] = "changed"
	require.Equal(t, "Bearer ${ACP_TOKEN}", dag.Harnesses["adapter"].Headers["Authorization"])
}

// TestACPRemoteDefaults verifies that remote ACP steps take the DAG-level
// permission policy but no CLI options. A provider chosen by a variable is
// unknown at load time, so its step keeps every default and records which ones
// it inherited for the runtime to filter.
func TestACPRemoteDefaults(t *testing.T) {
	file := createTempYAMLFile(t, `harnesses:
  remote:
    protocol: acp
    transport: websocket
    url: wss://agent.example/acp
    cwd: /remote/workspace
harness:
  provider: claude
  model: sonnet
  acp_permission: allow
steps:
  - action: harness.run
    with:
      provider: remote
      prompt: hello
  - action: harness.run
    with:
      provider: ${PROVIDER}
      prompt: hello
`)
	dag, err := spec.Load(context.Background(), file)
	require.NoError(t, err)
	remote := dag.Steps[0].ExecutorConfig.Config
	require.Equal(t, "remote", remote["provider"])
	require.Equal(t, "allow", remote["acp_permission"])
	require.NotContains(t, remote, "model")
	templated := dag.Steps[1].ExecutorConfig
	require.Equal(t, "${PROVIDER}", templated.Config["provider"])
	require.Equal(t, "allow", templated.Config["acp_permission"])
	require.Equal(t, []string{"acp_permission", "model"}, templated.Metadata[ir.HarnessInheritedOptionsKey])
	require.NotContains(t, dag.Steps[0].ExecutorConfig.Metadata, ir.HarnessInheritedOptionsKey)
}

// TestACPRemoteWorkspace verifies that remote-only harness steps, whose agent
// works in the definition's remote cwd, get no local working_dir warning.
func TestACPRemoteWorkspace(t *testing.T) {
	dag, err := spec.LoadYAML(context.Background(), []byte(`harnesses:
  remote:
    protocol: acp
    transport: http
    url: https://agent.example/acp
    cwd: /remote/workspace
  local:
    protocol: acp
    binary: adapter
steps:
  - name: remote-only
    action: harness.run
    with:
      provider: remote
      prompt: hello
  - name: local-fallback
    action: harness.run
    with:
      provider: remote
      prompt: hello
      fallback:
        - provider: local
`))
	require.NoError(t, err)
	require.Len(t, dag.BuildWarnings, 1)
	require.Contains(t, dag.BuildWarnings[0], `"steps.local-fallback" has no explicit working_dir`)
}
