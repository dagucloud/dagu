// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

func TestNodesField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want ir.NodeTargets
	}{
		{
			name: "String",
			yaml: "steps:\n  - run: \"true\"\n    nodes: web,env=prod\n",
			want: ir.NodeTargets{Selector: "web,env=prod", MaxConcurrent: 1},
		},
		{
			name: "List",
			yaml: "steps:\n  - run: \"true\"\n    nodes: [web, \"!canary\"]\n",
			want: ir.NodeTargets{Selector: "web,!canary", MaxConcurrent: 1},
		},
		{
			name: "Object",
			yaml: "steps:\n  - run: \"true\"\n    nodes:\n      labels: web\n      max_concurrent: 5\n      keep_going: true\n",
			want: ir.NodeTargets{Selector: "web", MaxConcurrent: 5, KeepGoing: true},
		},
		{
			// A reference resolves when the step starts, so validation
			// leaves it alone.
			name: "Reference",
			yaml: "params:\n  - name: env\n    default: prod\nsteps:\n  - run: \"true\"\n    nodes: web,env=${params.env}\n",
			want: ir.NodeTargets{Selector: "web,env=${params.env}", MaxConcurrent: 1},
		},
		{
			name: "DagRun",
			yaml: "steps:\n  - action: dag.run\n    with: {dag: deploy-one}\n    nodes: web\n---\nname: deploy-one\nnode: required\nsteps:\n  - run: \"true\"\n",
			want: ir.NodeTargets{Selector: "web", MaxConcurrent: 1},
		},
		{
			name: "SSHRun",
			yaml: "steps:\n  - action: ssh.run\n    with: {command: \"true\"}\n    nodes: web\n",
			want: ir.NodeTargets{Selector: "web", MaxConcurrent: 1},
		},
		{
			name: "SFTPDownloadPerNode",
			yaml: "steps:\n  - action: sftp.download\n    with: {source: /var/log/app.log, destination: \"logs/${node.name}.log\"}\n    nodes: web\n",
			want: ir.NodeTargets{Selector: "web", MaxConcurrent: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dag, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.NoError(t, err)
			require.NotNil(t, dag.Steps[0].Nodes)
			assert.Equal(t, tt.want, *dag.Steps[0].Nodes)
		})
	}
}

func TestNodesFieldRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"UnknownObjectField", "nodes:\n      labels: web\n      rank_by: name\n", `unknown field "rank_by"`},
		{"MissingLabels", "nodes:\n      max_concurrent: 2\n", "nodes.labels': is required"},
		{"MaxConcurrentZero", "nodes:\n      labels: web\n      max_concurrent: 0\n", "must be an integer from 1 to 1000"},
		{"MaxConcurrentTooHigh", "nodes:\n      labels: web\n      max_concurrent: 1001\n", "must be an integer from 1 to 1000"},
		{"KeepGoingNotBool", "nodes:\n      labels: web\n      keep_going: \"yes\"\n", "must be true or false"},
		{"InvalidTerm", "nodes: env==prod\n", `invalid label filter "env==prod"`},
		{"EmptySelector", "nodes: \",\"\n", "selector is empty"},
		{"NotAString", "nodes: 5\n", "must be a selector string"},
		{"EmptyListTermBesideReference", "nodes: [\"\", \"${TARGETS}\"]\n", "each selector term must be non-empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(context.Background(), []byte("steps:\n  - run: \"true\"\n    "+tt.yaml))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// RFC 013 §Validation: a step with nodes must be a step that can run on each
// node.
func TestNodesStepRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		step    string
		wantErr string
	}{
		{"WithParallel", "action: dag.run\n    with: {dag: child}\n    parallel: [a, b]\n    nodes: web", "cannot be combined with parallel"},
		{"WithWorkerSelector", "run: \"true\"\n    worker_selector: {gpu: \"true\"}\n    nodes: web", "cannot be combined with worker_selector"},
		{"WithContainer", "run: \"true\"\n    container: {image: alpine}\n    nodes: web", "cannot be combined with container"},
		{"WithRepeatPolicy", "run: \"true\"\n    repeat_policy: {repeat: until, condition: \"true\", expected: \"true\"}\n    nodes: web", "cannot be combined with repeat_policy"},
		{"OnHTTPRequest", "action: http.request\n    with: {method: GET, url: \"https://example.com\"}\n    nodes: web", `is allowed only on dag.run and on command steps`},
		{"OnDagEnqueue", "action: dag.enqueue\n    with: {dag: child}\n    nodes: web", `is allowed only on dag.run and on command steps`},
		{"SSHRunWithHost", "action: ssh.run\n    with: {host: example.com, command: \"true\"}\n    nodes: web", "with.host': cannot be set on a step with nodes"},
		{"SFTPDownloadOneDestination", "action: sftp.download\n    with: {source: /var/log/app.log, destination: app.log}\n    nodes: web", "with.destination': must use ${node.name}"},
		{"ShellPackages", "run: \"true\"\n    with: {shell: nix-shell, shell_packages: [jq]}\n    nodes: web", "with.shell_packages': cannot be used on a step with nodes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(context.Background(), []byte("steps:\n  - "+tt.step+"\n---\nname: child\nsteps:\n  - run: \"true\"\n"))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestNodeRequired(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte("node: required\nsteps:\n  - run: \"true\"\n"))
	require.NoError(t, err)
	assert.True(t, dag.NodeRequired)

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"UnknownValue", "node: optional\nsteps:\n  - run: \"true\"\n", `must be "required"`},
		{"WithSSH", "node: required\nssh: {host: example.com, user: deploy}\nsteps:\n  - run: \"true\"\n", "cannot be combined with ssh"},
		{"WithContainer", "node: required\ncontainer: {image: alpine}\nsteps:\n  - run: \"true\"\n", "cannot be combined with container"},
		{"WithWorkerSelector", "node: required\nworker_selector: {gpu: \"true\"}\nsteps:\n  - run: \"true\"\n", "cannot be combined with worker_selector"},
		{"WithWorkerSelectorLocal", "node: required\nworker_selector: local\nsteps:\n  - run: \"true\"\n", "cannot be combined with worker_selector"},
		{"WithNodesStep", "node: required\nsteps:\n  - id: fan\n    run: \"true\"\n    nodes: web\n", "steps.fan.nodes': a node: required DAG runs on one node"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
