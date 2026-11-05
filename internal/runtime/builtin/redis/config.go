// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package redis provides Redis executor capabilities for Dagu workflows.
package redis

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/workpath"
	"github.com/go-viper/mapstructure/v2"
)

// Config represents the Redis executor configuration.
type Config struct {
	// Connection - Basic
	URL      string `mapstructure:"url"`      // redis://user:pass@host:port/db
	Host     string `mapstructure:"host"`     // Alternative to URL
	Port     int    `mapstructure:"port"`     // Default: 6379
	Password string `mapstructure:"password"` // Auth password
	Username string `mapstructure:"username"` // ACL username (Redis 6+)
	DB       int    `mapstructure:"db"`       // Database number (0-15)

	// Connection - TLS
	TLS           bool   `mapstructure:"tls"`             // Enable TLS
	TLSCert       string `mapstructure:"tls_cert"`        // Client certificate path
	TLSKey        string `mapstructure:"tls_key"`         // Client key path
	TLSCA         string `mapstructure:"tls_ca"`          // CA certificate path
	TLSSkipVerify bool   `mapstructure:"tls_skip_verify"` // Skip verification

	// Connection - High Availability
	Mode           string   `mapstructure:"mode"`            // standalone, sentinel, cluster
	SentinelMaster string   `mapstructure:"sentinel_master"` // Sentinel master name
	SentinelAddrs  []string `mapstructure:"sentinel_addrs"`  // Sentinel addresses
	ClusterAddrs   []string `mapstructure:"cluster_addrs"`   // Cluster node addresses

	// Connection - Retry
	MaxRetries int `mapstructure:"max_retries"` // Max retry attempts

	// Command Execution
	Command string         `mapstructure:"command"` // Redis command
	Key     string         `mapstructure:"key"`     // Primary key
	Keys    []string       `mapstructure:"keys"`    // Multiple keys
	Value   any            `mapstructure:"value"`   // Value for SET
	Values  []any          `mapstructure:"values"`  // Multiple values
	Field   string         `mapstructure:"field"`   // Hash field
	Fields  map[string]any `mapstructure:"fields"`  // Multiple hash fields

	// Command Options
	TTL     int    `mapstructure:"ttl"`      // Expiration in seconds
	NX      bool   `mapstructure:"nx"`       // SET if not exists
	XX      bool   `mapstructure:"xx"`       // SET if exists
	KeepTTL bool   `mapstructure:"keep_ttl"` // Preserve existing TTL
	Count   int    `mapstructure:"count"`    // Count for SCAN, LPOP, etc.
	Match   string `mapstructure:"match"`    // Pattern for SCAN

	// List Options
	Position string `mapstructure:"position"` // BEFORE/AFTER for LINSERT
	Pivot    string `mapstructure:"pivot"`    // Pivot for LINSERT
	Start    int64  `mapstructure:"start"`    // Range start
	Stop     int64  `mapstructure:"stop"`     // Range stop

	// Sorted Set Options
	Score      float64 `mapstructure:"score"`       // Member score
	Min        string  `mapstructure:"min"`         // Range min
	Max        string  `mapstructure:"max"`         // Range max
	WithScores bool    `mapstructure:"with_scores"` // Include scores in output

	// Pub/Sub Options
	Channel  string   `mapstructure:"channel"`  // Pub/Sub channel
	Channels []string `mapstructure:"channels"` // Multiple channels
	Message  any      `mapstructure:"message"`  // Message to publish

	// Stream Options
	Stream       string         `mapstructure:"stream"`        // Stream key
	StreamID     string         `mapstructure:"stream_id"`     // Message ID (* for auto)
	Group        string         `mapstructure:"group"`         // Consumer group
	Consumer     string         `mapstructure:"consumer"`      // Consumer name
	StreamFields map[string]any `mapstructure:"stream_fields"` // Stream entry fields
	MaxLen       int64          `mapstructure:"max_len"`       // MAXLEN for XADD
	Block        int            `mapstructure:"block"`         // Block timeout (ms)
	NoAck        bool           `mapstructure:"no_ack"`        // NOACK for XREADGROUP

	// Scripting
	Script     string   `mapstructure:"script"`      // Lua script
	ScriptFile string   `mapstructure:"script_file"` // Path to Lua script file
	ScriptSHA  string   `mapstructure:"script_sha"`  // Pre-loaded script SHA
	ScriptKeys []string `mapstructure:"script_keys"` // KEYS for script
	ScriptArgs []any    `mapstructure:"script_args"` // ARGV for script

	// Pipeline/Transaction
	Pipeline []PipelineCommand `mapstructure:"pipeline"` // Batch commands
	Watch    []string          `mapstructure:"watch"`    // Keys to WATCH
	Multi    bool              `mapstructure:"multi"`    // Use MULTI/EXEC

	// Distributed Lock
	Lock        string `mapstructure:"lock"`         // Lock name
	LockTimeout int    `mapstructure:"lock_timeout"` // Lock expiry (seconds)
	LockRetry   int    `mapstructure:"lock_retry"`   // Lock retry attempts
	LockWait    int    `mapstructure:"lock_wait"`    // Wait between retries (ms)

	// Output
	OutputFormat string `mapstructure:"output_format"` // json, jsonl, raw, csv
	NullValue    string `mapstructure:"null_value"`    // String for nil values

	// Execution
	Timeout       int   `mapstructure:"timeout"`         // Command timeout (seconds)
	MaxResultSize int64 `mapstructure:"max_result_size"` // Max result size in bytes
}

// PipelineCommand represents a single command in a pipeline.
type PipelineCommand struct {
	Command string         `mapstructure:"command"`
	Key     string         `mapstructure:"key"`
	Keys    []string       `mapstructure:"keys"`
	Value   any            `mapstructure:"value"`
	Values  []any          `mapstructure:"values"`
	Field   string         `mapstructure:"field"`
	Fields  map[string]any `mapstructure:"fields"`
	TTL     int            `mapstructure:"ttl"`
	NX      bool           `mapstructure:"nx"`
	XX      bool           `mapstructure:"xx"`
	Score   float64        `mapstructure:"score"`
}

// DefaultConfig returns a Config with default values.
func DefaultConfig() *Config {
	return &Config{
		Host:          "localhost",
		Port:          6379,
		DB:            0,
		Mode:          "standalone",
		MaxRetries:    3,
		OutputFormat:  "json",
		NullValue:     "null",
		Timeout:       30,
		LockTimeout:   30,
		LockRetry:     10,
		LockWait:      100,
		MaxResultSize: 10 * 1024 * 1024, // 10MB
	}
}

// ParseConfig parses the executor configuration from a map. Relative file
// paths resolve under the step working directory in ctx.
func ParseConfig(ctx context.Context, mapCfg map[string]any) (*Config, error) {
	cfg := DefaultConfig()

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           cfg,
		WeaklyTypedInput: true,
		TagName:          "mapstructure",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create config decoder: %w", err)
	}

	if err := decoder.Decode(mapCfg); err != nil {
		return nil, fmt.Errorf("failed to decode redis config: %w", err)
	}

	// Validate configuration
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	workDir := runtime.GetEnv(ctx).WorkingDir
	cfg.ScriptFile = workpath.Resolve(workDir, cfg.ScriptFile)
	cfg.TLSCA = workpath.Resolve(workDir, cfg.TLSCA)
	cfg.TLSCert = workpath.Resolve(workDir, cfg.TLSCert)
	cfg.TLSKey = workpath.Resolve(workDir, cfg.TLSKey)

	return cfg, nil
}

// validate checks the configuration for errors.
func (c *Config) validate() error {
	// Must have either URL or Host
	if c.URL == "" && c.Host == "" {
		return fmt.Errorf("either url or host is required")
	}

	// Validate mode
	switch c.Mode {
	case "standalone", "sentinel", "cluster":
		// Valid
	default:
		return fmt.Errorf("invalid mode: %s (must be standalone, sentinel, or cluster)", c.Mode)
	}

	// Sentinel mode requires master name and addresses
	if c.Mode == "sentinel" {
		if c.SentinelMaster == "" {
			return fmt.Errorf("sentinel_master is required for sentinel mode")
		}
		if len(c.SentinelAddrs) == 0 {
			return fmt.Errorf("sentinel_addrs is required for sentinel mode")
		}
	}

	// Cluster mode requires addresses
	if c.Mode == "cluster" {
		if len(c.ClusterAddrs) == 0 {
			return fmt.Errorf("cluster_addrs is required for cluster mode")
		}
	}

	// Validate output format
	switch c.OutputFormat {
	case "json", "jsonl", "raw", "csv":
		// Valid
	default:
		return fmt.Errorf("invalid output_format: %s (must be json, jsonl, raw, or csv)", c.OutputFormat)
	}

	// Validate timeout
	if c.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}

	if c.MaxResultSize < 0 {
		return fmt.Errorf("max_result_size must be non-negative")
	}

	// Validate DB number
	if c.DB < 0 || c.DB > 15 {
		return fmt.Errorf("db must be between 0 and 15")
	}

	// Validate port
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}

	// Must have a command, script, script_sha, or pipeline
	if c.Command == "" && c.Script == "" && c.ScriptFile == "" && c.ScriptSHA == "" && len(c.Pipeline) == 0 {
		return fmt.Errorf("command, script, script_file, script_sha, or pipeline is required")
	}

	// Validate TLS certificate pair - both must be provided together
	if (c.TLSCert != "" && c.TLSKey == "") || (c.TLSCert == "" && c.TLSKey != "") {
		return fmt.Errorf("both tls_cert and tls_key must be provided together")
	}

	return nil
}
