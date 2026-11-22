// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Fields under a step's with replace the node's, except shell_args alone,
// which adds arguments to the node's shell.
func TestNodeConnectionWithOverrides(t *testing.T) {
	t.Parallel()

	node := &nodeConnection{cfg: Config{
		User:        "deploy",
		Host:        "127.0.0.1",
		Port:        "2222",
		SSHAuth:     ir.SSHAuth{Password: "node-password"},
		Shell:       "bash",
		ShellArgs:   []string{"-e"},
		NodeHostKey: &NodeHostKey{Node: "web01"},
	}}

	for _, tc := range []struct {
		name      string
		with      map[string]any
		user      string
		shell     string
		shellArgs []string
	}{
		{name: "NodeFields", with: map[string]any{"timeout": "5s"}, user: "deploy", shell: "bash", shellArgs: []string{"-e"}},
		{name: "User", with: map[string]any{"user": "root"}, user: "root", shell: "bash", shellArgs: []string{"-e"}},
		{name: "ShellArgs", with: map[string]any{"shell_args": []string{"-o", "pipefail"}}, user: "deploy", shell: "bash", shellArgs: []string{"-e", "-o", "pipefail"}},
		{name: "Shell", with: map[string]any{"shell": "zsh -u"}, user: "deploy", shell: "zsh", shellArgs: []string{"-u"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client, err := node.clientWith(context.Background(), tc.with)
			require.NoError(t, err)
			require.Equal(t, tc.user, client.cfg.User)
			require.Equal(t, "127.0.0.1:2222", client.hostPort)
			require.Equal(t, tc.shell, client.Shell)
			require.Equal(t, tc.shellArgs, client.ShellArgs)
		})
	}
	require.Equal(t, []string{"-e"}, node.cfg.ShellArgs, "the node's own arguments are unchanged")

	_, err := node.clientWith(context.Background(), map[string]any{"known_host_file": "/tmp/known_hosts"})
	require.ErrorContains(t, err, "with.known_host_file does not apply to a node")
}
