// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package redis

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, "localhost", cfg.Host)
	assert.Equal(t, 6379, cfg.Port)
	assert.Equal(t, 0, cfg.DB)
	assert.Equal(t, "standalone", cfg.Mode)
	assert.Equal(t, 3, cfg.MaxRetries)
	assert.Equal(t, "json", cfg.OutputFormat)
	assert.Equal(t, "null", cfg.NullValue)
	assert.Equal(t, 30, cfg.Timeout)
	assert.Equal(t, 30, cfg.LockTimeout)
	assert.Equal(t, 10, cfg.LockRetry)
	assert.Equal(t, 100, cfg.LockWait)
	assert.Equal(t, int64(10*1024*1024), cfg.MaxResultSize)
}

func TestParseConfig_Valid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    map[string]any
		expected func(*testing.T, *Config)
	}{
		{
			name: "minimal with URL",
			input: map[string]any{
				"url":     "redis://localhost:6379/0",
				"command": "PING",
			},
			expected: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "redis://localhost:6379/0", cfg.URL)
				assert.Equal(t, "PING", cfg.Command)
			},
		},
		{
			name: "minimal with host",
			input: map[string]any{
				"host":    "127.0.0.1",
				"port":    6380,
				"command": "GET",
				"key":     "mykey",
			},
			expected: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "127.0.0.1", cfg.Host)
				assert.Equal(t, 6380, cfg.Port)
				assert.Equal(t, "GET", cfg.Command)
				assert.Equal(t, "mykey", cfg.Key)
			},
		},
		{
			name: "sentinel mode",
			input: map[string]any{
				"mode":            "sentinel",
				"sentinel_master": "mymaster",
				"sentinel_addrs":  []string{"sentinel1:26379", "sentinel2:26379"},
				"command":         "PING",
			},
			expected: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "sentinel", cfg.Mode)
				assert.Equal(t, "mymaster", cfg.SentinelMaster)
				assert.Len(t, cfg.SentinelAddrs, 2)
			},
		},
		{
			name: "cluster mode",
			input: map[string]any{
				"mode":          "cluster",
				"cluster_addrs": []string{"node1:6379", "node2:6379", "node3:6379"},
				"command":       "PING",
			},
			expected: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "cluster", cfg.Mode)
				assert.Len(t, cfg.ClusterAddrs, 3)
			},
		},
		{
			name: "with TLS",
			input: map[string]any{
				"host":            "localhost",
				"tls":             true,
				"tls_skip_verify": true,
				"command":         "PING",
			},
			expected: func(t *testing.T, cfg *Config) {
				assert.True(t, cfg.TLS)
				assert.True(t, cfg.TLSSkipVerify)
			},
		},
		{
			name: "with pipeline",
			input: map[string]any{
				"host": "localhost",
				"pipeline": []map[string]any{
					{"command": "SET", "key": "k1", "value": "v1"},
					{"command": "GET", "key": "k1"},
				},
			},
			expected: func(t *testing.T, cfg *Config) {
				assert.Len(t, cfg.Pipeline, 2)
				assert.Equal(t, "SET", cfg.Pipeline[0].Command)
				assert.Equal(t, "GET", cfg.Pipeline[1].Command)
			},
		},
		{
			name: "with script",
			input: map[string]any{
				"host":        "localhost",
				"script":      "return redis.call('GET', KEYS[1])",
				"script_keys": []string{"mykey"},
			},
			expected: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "return redis.call('GET', KEYS[1])", cfg.Script)
				assert.Len(t, cfg.ScriptKeys, 1)
			},
		},
		{
			name: "output formats",
			input: map[string]any{
				"host":          "localhost",
				"command":       "PING",
				"output_format": "jsonl",
				"null_value":    "<nil>",
			},
			expected: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "jsonl", cfg.OutputFormat)
				assert.Equal(t, "<nil>", cfg.NullValue)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := ParseConfig(context.Background(), tt.input)
			require.NoError(t, err)
			tt.expected(t, cfg)
		})
	}
}

func TestParseConfig_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       map[string]any
		errContains string
	}{
		{
			name:        "missing host and url",
			input:       map[string]any{"host": "", "command": "PING"},
			errContains: "either url or host is required",
		},
		{
			name: "invalid mode",
			input: map[string]any{
				"host":    "localhost",
				"mode":    "invalid",
				"command": "PING",
			},
			errContains: "invalid mode",
		},
		{
			name: "sentinel without master",
			input: map[string]any{
				"host":           "localhost",
				"mode":           "sentinel",
				"sentinel_addrs": []string{"s1:26379"},
				"command":        "PING",
			},
			errContains: "sentinel_master is required",
		},
		{
			name: "sentinel without addrs",
			input: map[string]any{
				"host":            "localhost",
				"mode":            "sentinel",
				"sentinel_master": "mymaster",
				"command":         "PING",
			},
			errContains: "sentinel_addrs is required",
		},
		{
			name: "cluster without addrs",
			input: map[string]any{
				"host":    "localhost",
				"mode":    "cluster",
				"command": "PING",
			},
			errContains: "cluster_addrs is required",
		},
		{
			name: "invalid output format",
			input: map[string]any{
				"host":          "localhost",
				"command":       "PING",
				"output_format": "xml",
			},
			errContains: "invalid output_format",
		},
		{
			name: "negative timeout",
			input: map[string]any{
				"host":    "localhost",
				"command": "PING",
				"timeout": -1,
			},
			errContains: "timeout must be non-negative",
		},
		{
			name: "invalid db",
			input: map[string]any{
				"host":    "localhost",
				"command": "PING",
				"db":      16,
			},
			errContains: "db must be between 0 and 15",
		},
		{
			name: "invalid port",
			input: map[string]any{
				"host":    "localhost",
				"port":    0,
				"command": "PING",
			},
			errContains: "port must be between 1 and 65535",
		},
		{
			name: "missing command",
			input: map[string]any{
				"host": "localhost",
			},
			errContains: "command, script, script_file, script_sha, or pipeline is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseConfig(context.Background(), tt.input)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestHashConfig(t *testing.T) {
	t.Parallel()

	cfg1 := &Config{Host: "localhost", Port: 6379, DB: 0}
	cfg2 := &Config{Host: "localhost", Port: 6379, DB: 0}
	cfg3 := &Config{Host: "localhost", Port: 6380, DB: 0}

	hash1 := hashConfig(cfg1)
	hash2 := hashConfig(cfg2)
	hash3 := hashConfig(cfg3)

	// Same config should produce same hash
	assert.Equal(t, hash1, hash2)

	// Different config should produce different hash
	assert.NotEqual(t, hash1, hash3)

	// Hash should be 16 chars (8 bytes hex encoded)
	assert.Len(t, hash1, 16)
}

func TestPoolManagerContextFunctions(t *testing.T) {
	t.Parallel()

	pm := NewGlobalRedisPoolManager(GlobalPoolConfig{})
	ctx := context.Background()

	// Without pool manager
	assert.Nil(t, GetRedisPoolManager(ctx))

	// With pool manager
	ctx = WithRedisPoolManager(ctx, pm)
	assert.Equal(t, pm, GetRedisPoolManager(ctx))
}

// Relative script and TLS file paths name files under the step working
// directory, not the Dagu process directory.
func TestParseConfig_RelativePathsUseWorkingDir(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	step := ir.Step{Name: "redis-step"}
	dag := &ir.DAG{Name: "redis-test", WorkingDir: workDir, WorkingDirExplicit: true}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "")
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))

	cfg, err := ParseConfig(ctx, map[string]any{
		"host":        "localhost",
		"command":     "EVAL",
		"script_file": "scripts/count.lua",
		"tls":         true,
		"tls_ca":      "certs/ca.pem",
		"tls_cert":    "certs/client.pem",
		"tls_key":     "certs/client.key",
	})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(workDir, "scripts", "count.lua"), cfg.ScriptFile)
	assert.Equal(t, filepath.Join(workDir, "certs", "ca.pem"), cfg.TLSCA)
	assert.Equal(t, filepath.Join(workDir, "certs", "client.pem"), cfg.TLSCert)
	assert.Equal(t, filepath.Join(workDir, "certs", "client.key"), cfg.TLSKey)
}

// A client that cannot be built, here because the CA file is missing, fails at
// once: no connection was attempted, so retrying cannot help.
func TestCreateClientWithRetry_ConfigErrorFailsAtOnce(t *testing.T) {
	t.Parallel()

	cfg, err := ParseConfig(context.Background(), map[string]any{
		"host":    "localhost",
		"command": "PING",
		"tls":     true,
		"tls_ca":  filepath.Join(t.TempDir(), "missing-ca.pem"),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = createClientWithRetry(ctx, cfg)
	require.ErrorContains(t, err, "failed to read CA certificate")
}

// An unknown with key must fail validation instead of being silently dropped
// by the redis executor (#3440). host/key/command remain accepted.
func TestRedisRejectsUnknownWithKey(t *testing.T) {
	t.Parallel()

	err := registry.ValidateExecutorConfig("redis", map[string]any{
		"host": "127.0.0.1", "hoost": "typo",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `did you mean "host" instead of "hoost"?`)

	require.NoError(t, registry.ValidateExecutorConfig("redis", map[string]any{
		"host": "127.0.0.1", "port": 6379, "command": "GET", "key": "mykey",
	}))
}
