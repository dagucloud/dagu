// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sshconn

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
)

// NodeConfig returns the connection to an inventory node, through bastion
// when the node has one. Each hop accepts only its own node's pinned or
// trusted host key, unless that node sets strict_host_key: false.
func NodeConfig(node inventory.ResolvedNode, bastion *inventory.ResolvedNode, trusted TrustedHostKeys) (Config, error) {
	shell, shellArgs, err := splitShell(node.Shell)
	if err != nil {
		return Config{}, fmt.Errorf("node %q: invalid shell: %w", node.Name, err)
	}
	cfg := Config{
		User:        node.User,
		Host:        node.Host,
		Port:        nodePort(node),
		SSHAuth:     node.Auth,
		Shell:       shell,
		ShellArgs:   shellArgs,
		Timeout:     node.Timeout,
		NodeHostKey: nodeHostKey(node, trusted),
	}
	if bastion != nil {
		cfg.Bastion = &BastionConfig{
			Host:        bastion.Host,
			Port:        nodePort(*bastion),
			User:        bastion.User,
			SSHAuth:     bastion.Auth,
			NodeHostKey: nodeHostKey(*bastion, trusted),
		}
	}
	return cfg, nil
}

// NodeSecretRefs returns the secret refs of the credentials of node and of
// bastion, named nodes.<node>.<field> and nodes.<node>.bastion.<field>.
func NodeSecretRefs(node inventory.ResolvedNode, bastion *inventory.ResolvedNode) []secretref.Ref {
	cfg := ir.SSHConfig{SSHAuth: node.Auth}
	if bastion != nil {
		cfg.Bastion = &ir.BastionConfig{SSHAuth: bastion.Auth}
	}
	return cfg.SecretRefs("nodes." + node.Name)
}

func nodeHostKey(node inventory.ResolvedNode, trusted TrustedHostKeys) *NodeHostKey {
	return &NodeHostKey{
		Node:         node.Name,
		Pinned:       node.HostKey,
		AcceptAnyKey: !node.StrictHostKey,
		Trusted:      trusted,
	}
}

// nodePort returns the node's SSH port, or empty for the default.
func nodePort(node inventory.ResolvedNode) string {
	if node.Port == 0 {
		return ""
	}
	return strconv.Itoa(node.Port)
}

// splitShell splits a node's shell, such as "bash -e", into the shell and
// its arguments.
func splitShell(shell string) (string, []string, error) {
	if strings.TrimSpace(shell) == "" {
		return "", nil, nil
	}
	return cmdutil.SplitCommand(shell)
}
