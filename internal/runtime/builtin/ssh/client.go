// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Client struct {
	hostPort   string
	cfg        *ssh.ClientConfig
	Shell      string   // Shell for remote command execution
	ShellArgs  []string // Shell arguments for remote command execution
	bastionCfg *bastionClientConfig
}

// bastionClientConfig holds bastion connection configuration
type bastionClientConfig struct {
	hostPort string
	cfg      *ssh.ClientConfig
}

// NewClient creates a client for cfg. Credentials given as secret refs are
// read from the values resolved at run start (see WithSecretValues).
func NewClient(ctx context.Context, cfg *Config) (*Client, error) {
	authMethod, err := selectSSHAuthMethod(ctx, cfg.SSHAuth)
	if err != nil {
		return nil, err
	}

	hostKeyCallback, err := getHostKeyCallback(cfg.StrictHostKey, cfg.KnownHostFile)
	if err != nil {
		return nil, fmt.Errorf("failed to setup host key verification: %w", err)
	}

	timeout := defaultIfZero(cfg.Timeout, defaultSSHTimeout)
	port := defaultIfEmpty(cfg.Port, "22")

	var bastionCfg *bastionClientConfig
	if cfg.Bastion != nil {
		// Bastion inherits host key verification settings from main config
		bastionCfg, err = newBastionClientConfig(ctx, cfg.Bastion, timeout, hostKeyCallback)
		if err != nil {
			return nil, fmt.Errorf("failed to setup bastion client: %w", err)
		}
	}

	return &Client{
		hostPort: net.JoinHostPort(cfg.Host, port),
		cfg: &ssh.ClientConfig{
			User:            cfg.User,
			Auth:            []ssh.AuthMethod{authMethod},
			HostKeyCallback: hostKeyCallback,
			Timeout:         timeout,
		},
		Shell:      cfg.Shell,
		ShellArgs:  slices.Clone(cfg.ShellArgs),
		bastionCfg: bastionCfg,
	}, nil
}

// defaultIfZero returns val if non-zero, otherwise returns defaultVal.
func defaultIfZero(val, defaultVal time.Duration) time.Duration {
	if val == 0 {
		return defaultVal
	}
	return val
}

// defaultIfEmpty returns val if non-empty and not "0", otherwise returns defaultVal.
func defaultIfEmpty(val, defaultVal string) string {
	if val == "" || val == "0" {
		return defaultVal
	}
	return val
}

func (c *Client) NewSession(ctx context.Context) (*ssh.Client, *ssh.Session, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, nil, err
	}

	session, err := conn.NewSession()
	if err != nil {
		_ = conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		return nil, nil, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		_ = session.Close()
		_ = conn.Close()
		return nil, nil, ctxErr
	}

	return conn, session, nil
}

// dial establishes an SSH connection either directly or via bastion host.
func (c *Client) dial(ctx context.Context) (*ssh.Client, error) {
	if c.bastionCfg != nil {
		return c.dialViaBastion(ctx)
	}
	return dialContext(ctx, c.hostPort, c.cfg)
}

// dialViaBastion establishes an SSH connection through a bastion/jump host.
func (c *Client) dialViaBastion(ctx context.Context) (*ssh.Client, error) {
	// Connect to bastion host first
	bastionConn, err := dialContext(ctx, c.bastionCfg.hostPort, c.bastionCfg.cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to bastion host: %w", err)
	}

	// Create a tunnel through bastion to the target host
	targetConn, err := bastionConn.Dial("tcp", c.hostPort)
	if err != nil {
		_ = bastionConn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("failed to dial target through bastion: %w", err)
	}

	// Perform SSH handshake over the tunnel
	ncc, chans, reqs, err := ssh.NewClientConn(targetConn, c.hostPort, c.cfg)
	if err != nil {
		_ = targetConn.Close()
		_ = bastionConn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("failed to establish SSH connection through bastion: %w", err)
	}

	// Wrap the connection to ensure bastion is closed when target is closed
	wrappedConn := &bastionWrappedConn{
		Conn:    ncc,
		bastion: bastionConn,
	}

	client := ssh.NewClient(wrappedConn, chans, reqs)
	if ctxErr := ctx.Err(); ctxErr != nil {
		_ = client.Close()
		return nil, ctxErr
	}
	return client, nil
}

func dialContext(ctx context.Context, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	dialer := net.Dialer{Timeout: cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}

	contextConn := newContextConn(ctx, conn)
	ncc, chans, reqs, err := ssh.NewClientConn(contextConn, addr, cfg)
	if err != nil {
		_ = contextConn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}

	client := ssh.NewClient(ncc, chans, reqs)
	if ctxErr := ctx.Err(); ctxErr != nil {
		_ = client.Close()
		return nil, ctxErr
	}
	return client, nil
}

type contextConn struct {
	net.Conn
	stop     func() bool
	closeErr error
	close    sync.Once
}

func newContextConn(ctx context.Context, conn net.Conn) *contextConn {
	ret := &contextConn{Conn: conn}
	ret.stop = context.AfterFunc(ctx, ret.closeConn)
	return ret
}

func (c *contextConn) Close() error {
	c.stop()
	c.closeConn()
	return c.closeErr
}

func (c *contextConn) closeConn() {
	c.close.Do(func() {
		c.closeErr = c.Conn.Close()
	})
}

// bastionWrappedConn wraps an SSH client connection and ensures the bastion
// connection is closed when the target connection is closed.
type bastionWrappedConn struct {
	ssh.Conn
	bastion *ssh.Client
}

// Close closes both the target connection and the bastion connection.
func (c *bastionWrappedConn) Close() error {
	targetErr := c.Conn.Close()
	bastionErr := c.bastion.Close()
	return errors.Join(targetErr, bastionErr)
}

// newBastionClientConfig creates the bastion client configuration.
// It inherits the host key callback from the main SSH config for security.
func newBastionClientConfig(ctx context.Context, bastion *BastionConfig, timeout time.Duration, hostKeyCallback ssh.HostKeyCallback) (*bastionClientConfig, error) {
	authMethod, err := selectBastionAuthMethod(ctx, bastion.SSHAuth)
	if err != nil {
		return nil, err
	}

	return &bastionClientConfig{
		hostPort: net.JoinHostPort(bastion.Host, defaultIfEmpty(bastion.Port, "22")),
		cfg: &ssh.ClientConfig{
			User:            bastion.User,
			Auth:            []ssh.AuthMethod{authMethod},
			HostKeyCallback: hostKeyCallback,
			Timeout:         timeout,
		},
	}, nil
}

// selectBastionAuthMethod determines the authentication method for bastion host.
// Priority: explicit key > password > default keys.
func selectBastionAuthMethod(ctx context.Context, auth ir.SSHAuth) (ssh.AuthMethod, error) {
	if auth.HasKey() {
		signer, err := keySigner(ctx, auth, "bastion.")
		if err != nil {
			return nil, fmt.Errorf("failed to load bastion SSH key: %w", err)
		}
		return ssh.PublicKeys(signer), nil
	}

	if auth.HasPassword() {
		password, err := credentialValue(ctx, "bastion.password", auth.Password, auth.PasswordSecret)
		if err != nil {
			return nil, err
		}
		return ssh.Password(password), nil
	}

	// Try default keys for bastion
	for _, defaultKey := range getDefaultSSHKeys() {
		if _, err := os.Stat(defaultKey); err == nil {
			auth.Key = defaultKey
			signer, err := keySigner(ctx, auth, "bastion.")
			if err == nil {
				return ssh.PublicKeys(signer), nil
			}
		}
	}

	return nil, fmt.Errorf("no authentication method available for bastion: provide either SSH key or password")
}

// getHostKeyCallback returns the appropriate host key callback based on configuration
func getHostKeyCallback(strictHostKey bool, knownHostFile string) (ssh.HostKeyCallback, error) {
	if !strictHostKey {
		// User explicitly opted out of host key checking
		return ssh.InsecureIgnoreHostKey(), nil // nolint: gosec
	}

	// Default to ~/.ssh/known_hosts if not specified
	if knownHostFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %w", err)
		}
		knownHostFile = filepath.Join(home, ".ssh", "known_hosts")
	}

	// Expand path if it starts with ~
	knownHostFile, err := fileutil.ResolvePath(knownHostFile)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve known_hosts path: %w", err)
	}

	return knownhosts.New(knownHostFile)
}

// getDefaultSSHKeys returns a list of default SSH key paths to try
func getDefaultSSHKeys() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{}
	}

	sshDir := filepath.Join(home, ".ssh")
	return []string{
		filepath.Join(sshDir, "id_rsa"),
		filepath.Join(sshDir, "id_ecdsa"),
		filepath.Join(sshDir, "id_ed25519"),
		filepath.Join(sshDir, "id_dsa"),
	}
}

// selectSSHAuthMethod selects the authentication method based on the configuration.
// Priority: explicit key > default keys when no password is set > password.
func selectSSHAuthMethod(ctx context.Context, auth ir.SSHAuth) (ssh.AuthMethod, error) {
	if !auth.HasKey() && !auth.HasPassword() {
		auth.Key = findDefaultSSHKey()
		if auth.Key == "" {
			return nil, fmt.Errorf("no SSH key specified and no default keys found (~/.ssh/id_rsa, id_ecdsa, id_ed25519, or id_dsa)")
		}
	}

	if auth.HasKey() {
		signer, err := keySigner(ctx, auth, "")
		if err != nil {
			return nil, err
		}
		return ssh.PublicKeys(signer), nil
	}

	password, err := credentialValue(ctx, "password", auth.Password, auth.PasswordSecret)
	if err != nil {
		return nil, err
	}
	return ssh.Password(password), nil
}

// keySigner loads the key of auth from its path or its secret, unlocking it
// with the passphrase when the key is encrypted. prefix names the hop in
// errors, which never include a secret value.
func keySigner(ctx context.Context, auth ir.SSHAuth, prefix string) (ssh.Signer, error) {
	passphrase, err := credentialValue(ctx, prefix+"passphrase", auth.Passphrase, auth.PassphraseSecret)
	if err != nil {
		return nil, err
	}

	if auth.KeySecret != "" {
		pem, err := secretValue(ctx, prefix+"key", auth.KeySecret)
		if err != nil {
			return nil, err
		}
		signer, err := cachedSigner(ctx, []byte(pem), passphrase)
		if err != nil {
			return nil, fmt.Errorf("%skey: secret %q: %w", prefix, auth.KeySecret, secretKeyError(err))
		}
		return signer, nil
	}

	resolvedPath, err := fileutil.ResolvePath(auth.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve %skey path: %w", prefix, err)
	}
	pem, err := fileutil.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load SSH key from %s: %w", resolvedPath, err)
	}
	signer, err := cachedSigner(ctx, pem, passphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to load SSH key from %s: %w", resolvedPath, err)
	}
	return signer, nil
}

// cachedSigner parses a private key once per run (see WithSecretValues), or
// on every call outside a run.
func cachedSigner(ctx context.Context, pem []byte, passphrase string) (ssh.Signer, error) {
	if creds := credentialsFromContext(ctx); creds != nil {
		return creds.signers.signer(pem, passphrase, parseSigner)
	}
	return parseSigner(pem, passphrase)
}

// parseSigner parses a private key. The passphrase is used only when the key
// is encrypted.
func parseSigner(pem []byte, passphrase string) (ssh.Signer, error) {
	signer, err := ssh.ParsePrivateKey(pem)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) && passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase(pem, []byte(passphrase))
	}
	return signer, err
}

// secretKeyError describes why a key read from a secret failed to parse
// without repeating any part of the value.
func secretKeyError(err error) error {
	var missing *ssh.PassphraseMissingError
	switch {
	case errors.As(err, &missing):
		return errors.New("the key is encrypted and no passphrase is set")
	case errors.Is(err, x509.IncorrectPasswordError):
		return errors.New("the passphrase does not unlock the key")
	default:
		return errors.New("the value is not a private key; check that its line breaks were kept")
	}
}

// findDefaultSSHKey returns the first existing default SSH key path, or empty string.
func findDefaultSSHKey() string {
	for _, keyPath := range getDefaultSSHKeys() {
		if _, err := os.Stat(keyPath); err == nil {
			return keyPath
		}
	}
	return ""
}
