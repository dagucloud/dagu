// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"crypto/tls"
	"crypto/x509"
	"time"

	appconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// Config holds configuration for the coordinator client
type Config struct {
	// WorkspaceBundleDir is required when dispatching DAGs with file dependencies.
	WorkspaceBundleDir string

	// TLS configuration
	Insecure      bool   // h2c, unless InsecureUnset and a TLS source select built-in TLS
	CertFile      string // Client certificate
	KeyFile       string // Client key
	CAFile        string // CA certificate
	SkipTLSVerify bool   // Skip server certificate verification

	// InsecureUnset reports that Insecure came from no setting, so a TLS
	// source still selects built-in TLS.
	InsecureUnset bool
	// BuiltinCAFile is the CA certificate, peer.ca_cert_file, that every
	// coordinator certificate on built-in TLS chains to.
	BuiltinCAFile string
	// PublishedCAFile is the CA certificate that coordinators publish in
	// this process's data directory.
	PublishedCAFile string
	// FallbackFile keeps the h2c windows that the processes sharing the data
	// directory opened.
	FallbackFile string
	// LocalRegistry is the service registry in this process's data
	// directory. The published CA certificate selects built-in TLS only
	// while it lists a coordinator that serves built-in TLS.
	LocalRegistry serviceregistry.ServiceRegistry

	// invite is the worker.invite of an invite worker that holds no worker
	// certificate yet. Its connections verify coordinators against the
	// invite's pin, and serve enrollment only.
	invite *Invite
	// workerCert is the worker certificate and key of an invite worker.
	// Its connections present it in place of the peer token and verify
	// coordinators against workerCA, the CA certificate that enrollment
	// returned.
	workerCert *tls.Certificate
	workerCA   *x509.Certificate
	// onRemoved, when set, is called each time a coordinator refuses
	// workerCert as revoked, whatever the call.
	onRemoved func()

	// Peer token sent on every call: Token when set, otherwise the content of
	// TokenFile when this process can read it.
	Token     string
	TokenFile string

	// Timeouts
	DialTimeout      time.Duration // Connection timeout (default: 10s)
	RequestTimeout   time.Duration // Per-request timeout (default: 5m)
	HeartbeatTimeout time.Duration // Worker heartbeat timeout (default: 10s)

	// Retry configuration
	MaxRetries    int           // Max dispatch retries (default: 3)
	RetryInterval time.Duration // Base retry interval (default: 1s)
}

// DefaultConfig returns a Config with default values
func DefaultConfig() *Config {
	return &Config{
		Insecure:         true,
		DialTimeout:      10 * time.Second,
		RequestTimeout:   5 * time.Minute,
		HeartbeatTimeout: 10 * time.Second,
		MaxRetries:       3,
		RetryInterval:    time.Second,
	}
}

// ConfigFromPeer maps application peer settings to coordinator client settings.
func ConfigFromPeer(peer appconfig.Peer) *Config {
	cfg := DefaultConfig()
	cfg.CAFile = peer.ClientCaFile
	cfg.CertFile = peer.CertFile
	cfg.KeyFile = peer.KeyFile
	cfg.SkipTLSVerify = peer.SkipTLSVerify
	cfg.Insecure = peer.Insecure
	cfg.InsecureUnset = peer.InsecureUnset
	cfg.BuiltinCAFile = peer.CACertFile
	cfg.PublishedCAFile = peer.PublishedCAFile
	cfg.FallbackFile = peer.FallbackFile
	cfg.Token = peer.Token
	cfg.TokenFile = peer.TokenFile
	if peer.MaxRetries > 0 {
		cfg.MaxRetries = peer.MaxRetries
	}
	if peer.RetryInterval > 0 {
		cfg.RetryInterval = peer.RetryInterval
	}
	return cfg
}

// WithInvite returns a copy of c for an invite worker that holds no worker
// certificate: its connections use built-in TLS verified against the pin of
// invite, never fall back to h2c, and send no peer token. Such a client only
// enrolls, through Enroll.
func (c *Config) WithInvite(invite Invite) *Config {
	clone := *c
	clone.invite = &invite
	clone.workerCert, clone.workerCA, clone.onRemoved = nil, nil, nil
	return &clone
}

// WithWorkerCertificate returns a copy of c for an invite worker that holds
// cert, its worker certificate and key: its connections use built-in TLS,
// present cert in place of the peer token, verify coordinators against ca,
// the CA certificate that enrollment returned, and never fall back to h2c.
// The client calls onRemoved, unless nil, each time a coordinator refuses
// cert as revoked, on any call.
func (c *Config) WithWorkerCertificate(cert tls.Certificate, ca *x509.Certificate, onRemoved func()) *Config {
	clone := *c
	clone.invite = nil
	clone.workerCert, clone.workerCA = &cert, ca
	clone.onRemoved = onRemoved
	return &clone
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if !c.Insecure {
		if _, err := c.startTransport(); err != nil {
			return err
		}
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = 10 * time.Second
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = 5 * time.Minute
	}
	if c.HeartbeatTimeout <= 0 {
		c.HeartbeatTimeout = 10 * time.Second
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	if c.RetryInterval <= 0 {
		c.RetryInterval = time.Second
	}
	return nil
}
