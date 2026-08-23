// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"time"

	appconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
)

// Config holds configuration for the coordinator client
type Config struct {
	// WorkspaceBundleDir is required when dispatching DAGs with file dependencies.
	WorkspaceBundleDir string

	// TLS configuration
	Insecure      bool   // Use insecure connection (default: true)
	CertFile      string // Client certificate
	KeyFile       string // Client key
	CAFile        string // CA certificate
	SkipTLSVerify bool   // Skip server certificate verification

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
	if peer.MaxRetries > 0 {
		cfg.MaxRetries = peer.MaxRetries
	}
	if peer.RetryInterval > 0 {
		cfg.RetryInterval = peer.RetryInterval
	}
	return cfg
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if !c.Insecure && !c.SkipTLSVerify && c.CertFile == "" && c.KeyFile == "" && c.CAFile == "" {
		return ErrMissingTLSConfig
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
