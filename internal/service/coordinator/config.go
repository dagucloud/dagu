// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import "time"

// Config holds configuration for the coordinator client
type Config struct {
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
