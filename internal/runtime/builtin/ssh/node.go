// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/go-viper/mapstructure/v2"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/sshconn"
)

type nodeConnectionCtxKey struct{}

// nodeConnection is the inventory node that a bound run's SSH steps reach.
type nodeConnection struct {
	cfg    sshconn.Config
	client *sshconn.Client
}

// WithNodeConnection returns a context whose SSH steps reach the inventory
// node cfg describes, as steps reach the DAG-level ssh host (RFC 013 §Bound
// runs). The fields under a step's with override the node's, except that a
// step that names a host connects to that host instead. Credentials given as
// secret refs are read from the values resolved at run start.
func WithNodeConnection(ctx context.Context, cfg sshconn.Config) (context.Context, error) {
	client, err := sshconn.NewClient(ctx, &cfg)
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
	if namesHost(with, "host") || namesHost(with, "ip") {
		return nil
	}
	return node
}

// namesHost reports whether with sets key to a host. An empty value names
// none, so the step connects to the node rather than to whatever host an
// empty address dials. The agent applies the same rule before a bound run
// starts.
func namesHost(with map[string]any, key string) bool {
	value, ok := with[key]
	if text, isText := value.(string); isText {
		return strings.TrimSpace(text) != ""
	}
	return ok
}

// clientWith returns a client for the node with the fields set under with in
// place of the node's.
func (n *nodeConnection) clientWith(ctx context.Context, with map[string]any) (*sshconn.Client, error) {
	cfg, err := n.configWith(with)
	if err != nil {
		return nil, err
	}
	return sshconn.NewClient(ctx, &cfg)
}

// configWith returns the node's connection with the fields set under with in
// place of the node's. A key or a password replaces all of the node's
// credentials.
func (n *nodeConnection) configWith(with map[string]any) (sshconn.Config, error) {
	var def sshMapConfig
	md, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{Result: &def, WeaklyTypedInput: true})
	if err != nil {
		return sshconn.Config{}, fmt.Errorf("failed to create decoder: %w", err)
	}
	if err := md.Decode(with); err != nil {
		return sshconn.Config{}, fmt.Errorf("failed to decode ssh config: %w", err)
	}
	has := func(field string) bool {
		_, ok := with[field]
		return ok
	}
	if has("known_host_file") {
		return sshconn.Config{}, errors.New("with.known_host_file does not apply to a node, whose host key is checked against its pinned and trusted keys")
	}
	if has("strict_host_key") {
		return sshconn.Config{}, errors.New("with.strict_host_key does not apply to a node, whose inventory decides which host keys it accepts")
	}
	if has("bastion") {
		return sshconn.Config{}, errors.New("with.bastion does not apply to a node, which connects through the bastion its inventory sets")
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
			return sshconn.Config{}, err
		}
		cfg.SSHAuth = overrideAuth(cfg.SSHAuth, auth)
	}
	switch {
	case has("shell"):
		if cfg.Shell, cfg.ShellArgs, err = parseShellConfig(def.Shell, def.ShellArgs); err != nil {
			return sshconn.Config{}, fmt.Errorf("failed to parse shell config: %w", err)
		}
	case has("shell_args"):
		// shell_args adds arguments to the node's shell.
		cfg.ShellArgs = append(slices.Clone(cfg.ShellArgs), def.ShellArgs...)
	}
	if has("timeout") {
		if cfg.Timeout, err = parseTimeout(def.Timeout); err != nil {
			return sshconn.Config{}, err
		}
	}
	return cfg, nil
}

// overrideAuth applies the credentials set in override to base. A key or a
// password in override replaces all of base's credentials, so the connection
// signs in with it. A passphrase alone unlocks base's key.
func overrideAuth(base, override ir.SSHAuth) ir.SSHAuth {
	if override.HasKey() || override.HasPassword() {
		return override
	}
	base.Passphrase, base.PassphraseSecret = override.Passphrase, override.PassphraseSecret
	return base
}
