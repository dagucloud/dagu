// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package redis

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"strconv"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/redis/go-redis/v9"
)

// createClient creates a Redis client based on the configuration.
func createClient(cfg *Config) (redis.UniversalClient, error) {
	// If URL is provided, parse it
	if cfg.URL != "" {
		return createClientFromURL(cfg)
	}

	// Otherwise, use the mode-specific client
	switch cfg.Mode {
	case "sentinel":
		return createSentinelClient(cfg)
	case "cluster":
		return createClusterClient(cfg)
	default:
		return createStandaloneClient(cfg)
	}
}

// createClientFromURL creates a client from a Redis URL.
func createClientFromURL(cfg *Config) (redis.UniversalClient, error) {
	parsedURL, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid redis URL: %w", err)
	}

	// Extract password from URL or config
	password := cfg.Password
	if parsedURL.User != nil {
		if p, ok := parsedURL.User.Password(); ok {
			password = p
		}
	}

	// Extract username from URL or config
	username := cfg.Username
	if parsedURL.User != nil && parsedURL.User.Username() != "" {
		username = parsedURL.User.Username()
	}

	// Extract DB from URL path or config
	db := cfg.DB
	if parsedURL.Path != "" && parsedURL.Path != "/" {
		if d, err := strconv.Atoi(parsedURL.Path[1:]); err == nil {
			db = d
		}
	}

	// Build options
	opts := &redis.Options{
		Addr:       parsedURL.Host,
		Password:   password,
		Username:   username,
		DB:         db,
		MaxRetries: cfg.MaxRetries,
	}

	// Handle TLS
	if parsedURL.Scheme == "rediss" || cfg.TLS {
		tlsConfig, err := buildTLSConfig(cfg)
		if err != nil {
			return nil, err
		}
		opts.TLSConfig = tlsConfig
	}

	return redis.NewClient(opts), nil
}

// createStandaloneClient creates a standalone Redis client.
func createStandaloneClient(cfg *Config) (redis.UniversalClient, error) {
	opts := &redis.Options{
		Addr:       fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password:   cfg.Password,
		Username:   cfg.Username,
		DB:         cfg.DB,
		MaxRetries: cfg.MaxRetries,
	}

	if cfg.TLS {
		tlsConfig, err := buildTLSConfig(cfg)
		if err != nil {
			return nil, err
		}
		opts.TLSConfig = tlsConfig
	}

	return redis.NewClient(opts), nil
}

// createSentinelClient creates a Redis Sentinel failover client.
func createSentinelClient(cfg *Config) (redis.UniversalClient, error) {
	opts := &redis.FailoverOptions{
		MasterName:    cfg.SentinelMaster,
		SentinelAddrs: cfg.SentinelAddrs,
		Password:      cfg.Password,
		Username:      cfg.Username,
		DB:            cfg.DB,
		MaxRetries:    cfg.MaxRetries,
	}

	if cfg.TLS {
		tlsConfig, err := buildTLSConfig(cfg)
		if err != nil {
			return nil, err
		}
		opts.TLSConfig = tlsConfig
	}

	return redis.NewFailoverClient(opts), nil
}

// createClusterClient creates a Redis Cluster client.
func createClusterClient(cfg *Config) (redis.UniversalClient, error) {
	opts := &redis.ClusterOptions{
		Addrs:      cfg.ClusterAddrs,
		Password:   cfg.Password,
		Username:   cfg.Username,
		MaxRetries: cfg.MaxRetries,
	}

	if cfg.TLS {
		tlsConfig, err := buildTLSConfig(cfg)
		if err != nil {
			return nil, err
		}
		opts.TLSConfig = tlsConfig
	}

	return redis.NewClusterClient(opts), nil
}

// buildTLSConfig creates a TLS configuration from the config.
func buildTLSConfig(cfg *Config) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,  // Enforce TLS 1.2 minimum
		InsecureSkipVerify: cfg.TLSSkipVerify, //nolint:gosec // User-controlled option
	}

	// Load CA certificate if provided
	if cfg.TLSCA != "" {
		caCert, err := fileutil.ReadFile(cfg.TLSCA)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = caCertPool
	}

	// Load client certificate and key if provided
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}
