// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/go-viper/mapstructure/v2"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

type nodeConnectionCtxKey struct{}

// nodeConnection is the inventory node that a bound run's SSH steps reach.
type nodeConnection struct {
	cfg    Config
	client *Client
}

// WithNodeConnection returns a context whose SSH steps reach the inventory
// node cfg describes, as steps reach the DAG-level ssh host (RFC 013 §Bound
// runs). The fields under a step's with override the node's, except that a
// step that names a host connects to that host instead. Credentials given as
// secret refs are read from the values resolved at run start.
func WithNodeConnection(ctx context.Context, cfg Config) (context.Context, error) {
	client, err := NewClient(ctx, &cfg)
	if err != nil {
		return ctx, err
	}
	ctx = WithSSHClient(ctx, client)
	return context.WithValue(ctx, nodeConnectionCtxKey{}, &nodeConnection{cfg: cfg, client: client}), nil
}

// nodeConnectionFor returns the node connection a step's with block applies
// to, or nil when the run is not bound to a node or the step names a host.
func nodeConnectionFor(ctx context.Context, with map[string]any) *nodeConnection {
	node, _ := ctx.Value(nodeConnectionCtxKey{}).(*nodeConnection)
	if node == nil {
		return nil
	}
	if _, ok := with["host"]; ok {
		return nil
	}
	if _, ok := with["ip"]; ok {
		return nil
	}
	return node
}

// clientWith returns a client for the node with the fields set under with in
// place of the node's. A key also replaces the node's passphrase, which
// belongs to the key it unlocks.
func (n *nodeConnection) clientWith(ctx context.Context, with map[string]any) (*Client, error) {
	var def sshMapConfig
	md, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{Result: &def, WeaklyTypedInput: true})
	if err != nil {
		return nil, fmt.Errorf("failed to create decoder: %w", err)
	}
	if err := md.Decode(with); err != nil {
		return nil, fmt.Errorf("failed to decode ssh config: %w", err)
	}
	has := func(field string) bool {
		_, ok := with[field]
		return ok
	}
	if has("known_host_file") {
		return nil, errors.New("with.known_host_file does not apply to a node, whose host key is checked against its pinned and trusted keys")
	}
	if has("bastion") {
		return nil, errors.New("with.bastion does not apply to a node, which connects through the bastion its inventory sets")
	}

	cfg := n.cfg
	if has("user") {
		cfg.User = def.User
	}
	if has("port") {
		cfg.Port = def.Port
	}
	if has("key") || has("password") || has("passphrase") {
		auth, err := ir.ParseSSHAuth("with", def.Key, def.Password, def.Passphrase)
		if err != nil {
			return nil, err
		}
		cfg.SSHAuth = overrideAuth(cfg.SSHAuth, auth)
	}
	if has("strict_host_key") && cfg.NodeHostKey != nil {
		hostKey := *cfg.NodeHostKey
		hostKey.AcceptAnyKey = !def.StrictHostKey
		cfg.NodeHostKey = &hostKey
	}
	switch {
	case has("shell"):
		if cfg.Shell, cfg.ShellArgs, err = parseShellConfig(def.Shell, def.ShellArgs); err != nil {
			return nil, fmt.Errorf("failed to parse shell config: %w", err)
		}
	case has("shell_args"):
		// shell_args adds arguments to the node's shell.
		cfg.ShellArgs = append(slices.Clone(cfg.ShellArgs), def.ShellArgs...)
	}
	if has("timeout") {
		if cfg.Timeout, err = parseTimeout(def.Timeout); err != nil {
			return nil, err
		}
	}
	return NewClient(ctx, &cfg)
}

// overrideAuth applies the credentials set in override to base. A credential
// set in override replaces the base one whatever its form.
func overrideAuth(base, override ir.SSHAuth) ir.SSHAuth {
	merged := base
	if override.HasKey() || override.HasPassphrase() {
		merged.Passphrase, merged.PassphraseSecret = override.Passphrase, override.PassphraseSecret
	}
	if override.HasKey() {
		merged.Key, merged.KeySecret = override.Key, override.KeySecret
	}
	if override.HasPassword() {
		merged.Password, merged.PasswordSecret = override.Password, override.PasswordSecret
	}
	return merged
}
