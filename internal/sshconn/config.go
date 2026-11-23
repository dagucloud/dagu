// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package sshconn connects to SSH servers, directly or through a bastion. It
// checks each hop's host key and authenticates with credentials resolved at
// run start. SSH steps and node connection tests both connect through it.
package sshconn

import (
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// DefaultTimeout bounds connecting when a connection sets no timeout.
const DefaultTimeout = 30 * time.Second

// Config represents SSH connection info
type Config struct {
	User string
	Host string
	Port string
	// SSHAuth holds the key, password, and passphrase, each a literal or a
	// secret ref resolved at run start.
	ir.SSHAuth
	StrictHostKey bool           // Enable strict host key checking (defaults to true)
	KnownHostFile string         // Path to known_hosts file (defaults to ~/.ssh/known_hosts)
	Shell         string         // Shell for remote command execution (e.g., "/bin/bash")
	ShellArgs     []string       // Additional shell arguments (e.g., -e, -o pipefail)
	Timeout       time.Duration  // Connection timeout (defaults to 30s)
	Bastion       *BastionConfig // Optional bastion/jump host configuration
	// NodeHostKey, when set, checks the host key against an inventory
	// node's keys in place of StrictHostKey and KnownHostFile.
	NodeHostKey *NodeHostKey
}

// BastionConfig represents bastion/jump host connection info
type BastionConfig struct {
	Host string
	Port string
	User string
	ir.SSHAuth
	// NodeHostKey checks the bastion's host key against its node's keys. A
	// connection to a node sets it, since each hop is a node of its own.
	NodeHostKey *NodeHostKey
}
