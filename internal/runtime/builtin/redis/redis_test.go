// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package redis_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	redisexec "github.com/dagucloud/dagu/v2/internal/runtime/builtin/redis"
)

// Helper function to create an executor via the registry
func newRedisExecutor(ctx context.Context, step ir.Step) (executor.Executor, error) {
	return executor.NewExecutor(ctx, step)
}

// skipIfNoRedis skips the test if REDIS_TEST_HOST is not set
func skipIfNoRedis(t *testing.T) string {
	host := os.Getenv("REDIS_TEST_HOST")
	if host == "" {
		t.Skip("REDIS_TEST_HOST not set, skipping integration test")
	}
	return host
}

func TestRedisExecutor_Registration(t *testing.T) {
	host := skipIfNoRedis(t)

	// Verify redis executor is registered
	ctx := context.Background()
	step := ir.Step{
		Name: "test-registration",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "PING",
			},
		},
	}

	exec, err := newRedisExecutor(ctx, step)
	require.NoError(t, err)
	require.NotNil(t, exec)

	// Check it implements the expected interfaces
	_, ok := exec.(executor.Executor)
	assert.True(t, ok, "should implement Executor interface")
}

func TestRedisExecutor_SetStdout(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()
	step := ir.Step{
		Name: "test-stdout",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "PING",
			},
		},
	}

	exec, err := newRedisExecutor(ctx, step)
	require.NoError(t, err)

	var buf bytes.Buffer
	exec.SetStdout(&buf)
	// SetStdout should not error
}

func TestRedisExecutor_SetStderr(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()
	step := ir.Step{
		Name: "test-stderr",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "PING",
			},
		},
	}

	exec, err := newRedisExecutor(ctx, step)
	require.NoError(t, err)

	var buf bytes.Buffer
	exec.SetStderr(&buf)
	// SetStderr should not error
}

func TestRedisExecutor_ConfigValidation_Invalid(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Test cases that should return errors during config parsing
	tests := []struct {
		name   string
		config map[string]any
	}{
		{
			name: "missing host and url",
			config: map[string]any{
				"host":    "",
				"command": "PING",
			},
		},
		{
			name: "invalid mode",
			config: map[string]any{
				"host":    "localhost",
				"mode":    "invalid",
				"command": "PING",
			},
		},
		{
			name: "missing command for non-pipeline/script",
			config: map[string]any{
				"host": "localhost",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			step := ir.Step{
				Name: "test-config",
				ExecutorConfig: ir.ExecutorConfig{
					Type:   "redis",
					Config: tt.config,
				},
			}

			_, err := newRedisExecutor(ctx, step)
			assert.Error(t, err)
		})
	}
}

func TestRedisExecutor_ConfigValidation_Valid(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()

	// Test cases that should succeed (require Redis connection)
	tests := []struct {
		name   string
		config map[string]any
	}{
		{
			name: "valid config with host",
			config: map[string]any{
				"host":    host,
				"command": "PING",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := ir.Step{
				Name: "test-config",
				ExecutorConfig: ir.ExecutorConfig{
					Type:   "redis",
					Config: tt.config,
				},
			}

			_, err := newRedisExecutor(ctx, step)
			assert.NoError(t, err)
		})
	}
}

// Integration tests - require REDIS_TEST_HOST environment variable

func TestRedisExecutor_PING_Integration(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()
	step := ir.Step{
		Name: "test-ping",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "PING",
			},
		},
	}

	exec, err := newRedisExecutor(ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "PONG")

	// Check metrics in stderr
	metrics := stderr.String()
	assert.Contains(t, metrics, `"status":"success"`)
}

func TestRedisExecutor_SET_GET_Integration(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()

	// SET a value
	setStep := ir.Step{
		Name: "test-set",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "SET",
				"key":     "test:integration:key",
				"value":   "hello-world",
			},
		},
	}

	setExec, err := newRedisExecutor(ctx, setStep)
	require.NoError(t, err)

	err = setExec.Run(ctx)
	require.NoError(t, err)

	// GET the value
	getStep := ir.Step{
		Name: "test-get",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "GET",
				"key":     "test:integration:key",
			},
		},
	}

	getExec, err := newRedisExecutor(ctx, getStep)
	require.NoError(t, err)

	var stdout bytes.Buffer
	getExec.SetStdout(&stdout)

	err = getExec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "hello-world")

	// Cleanup
	delStep := ir.Step{
		Name: "test-del",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "DEL",
				"key":     "test:integration:key",
			},
		},
	}

	delExec, err := newRedisExecutor(ctx, delStep)
	require.NoError(t, err)
	_ = delExec.Run(ctx)
}

func TestRedisExecutor_Pipeline_Integration(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()
	step := ir.Step{
		Name: "test-pipeline",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host": host,
				"pipeline": []map[string]any{
					{"command": "SET", "key": "test:pipe:k1", "value": "v1"},
					{"command": "SET", "key": "test:pipe:k2", "value": "v2"},
					{"command": "GET", "key": "test:pipe:k1"},
					{"command": "GET", "key": "test:pipe:k2"},
					{"command": "DEL", "key": "test:pipe:k1"},
					{"command": "DEL", "key": "test:pipe:k2"},
				},
			},
		},
	}

	exec, err := newRedisExecutor(ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "v1")
	assert.Contains(t, output, "v2")

	metrics := stderr.String()
	assert.Contains(t, metrics, `"command":"PIPELINE"`)
	assert.Contains(t, metrics, `"status":"success"`)
}

func TestRedisExecutor_Script_Integration(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()
	step := ir.Step{
		Name: "test-script",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":        host,
				"script":      "return redis.call('SET', KEYS[1], ARGV[1])",
				"script_keys": []string{"test:script:key"},
				"script_args": []any{"script-value"},
			},
		},
	}

	exec, err := newRedisExecutor(ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.NoError(t, err)

	metrics := stderr.String()
	assert.Contains(t, metrics, `"command":"EVAL"`)
	assert.Contains(t, metrics, `"status":"success"`)

	// Cleanup
	delStep := ir.Step{
		Name: "cleanup",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "DEL",
				"key":     "test:script:key",
			},
		},
	}
	delExec, _ := newRedisExecutor(ctx, delStep)
	_ = delExec.Run(ctx)
}

func TestRedisExecutor_OutputFormats_Integration(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()

	// Set up test data
	setup := ir.Step{
		Name: "setup",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "SET",
				"key":     "test:format:key",
				"value":   "format-test",
			},
		},
	}
	setupExec, _ := newRedisExecutor(ctx, setup)
	_ = setupExec.Run(ctx)

	defer func() {
		// Cleanup
		cleanup := ir.Step{
			Name: "cleanup",
			ExecutorConfig: ir.ExecutorConfig{
				Type: "redis",
				Config: map[string]any{
					"host":    host,
					"command": "DEL",
					"key":     "test:format:key",
				},
			},
		}
		cleanupExec, _ := newRedisExecutor(ctx, cleanup)
		_ = cleanupExec.Run(ctx)
	}()

	tests := []struct {
		name         string
		outputFormat string
		contains     string
	}{
		{"json format", "json", "format-test"},
		{"raw format", "raw", "format-test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := ir.Step{
				Name: "test-format",
				ExecutorConfig: ir.ExecutorConfig{
					Type: "redis",
					Config: map[string]any{
						"host":          host,
						"command":       "GET",
						"key":           "test:format:key",
						"output_format": tt.outputFormat,
					},
				},
			}

			exec, err := newRedisExecutor(ctx, step)
			require.NoError(t, err)

			var stdout bytes.Buffer
			exec.SetStdout(&stdout)

			err = exec.Run(ctx)
			require.NoError(t, err)

			output := stdout.String()
			assert.Contains(t, output, tt.contains)
		})
	}
}

func TestRedisExecutor_Hash_Integration(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()

	// HSET
	hsetStep := ir.Step{
		Name: "test-hset",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "HSET",
				"key":     "test:hash:key",
				"fields": map[string]any{
					"field1": "value1",
					"field2": "value2",
				},
			},
		},
	}

	hsetExec, err := newRedisExecutor(ctx, hsetStep)
	require.NoError(t, err)
	err = hsetExec.Run(ctx)
	require.NoError(t, err)

	// HGETALL
	hgetallStep := ir.Step{
		Name: "test-hgetall",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "HGETALL",
				"key":     "test:hash:key",
			},
		},
	}

	hgetallExec, err := newRedisExecutor(ctx, hgetallStep)
	require.NoError(t, err)

	var stdout bytes.Buffer
	hgetallExec.SetStdout(&stdout)

	err = hgetallExec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "field1")
	assert.Contains(t, output, "value1")
	assert.Contains(t, output, "field2")
	assert.Contains(t, output, "value2")

	// Cleanup
	delStep := ir.Step{
		Name: "cleanup",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "DEL",
				"key":     "test:hash:key",
			},
		},
	}
	delExec, _ := newRedisExecutor(ctx, delStep)
	_ = delExec.Run(ctx)
}

func TestRedisExecutor_List_Integration(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()

	// RPUSH
	rpushStep := ir.Step{
		Name: "test-rpush",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "RPUSH",
				"key":     "test:list:key",
				"values":  []any{"item1", "item2", "item3"},
			},
		},
	}

	rpushExec, err := newRedisExecutor(ctx, rpushStep)
	require.NoError(t, err)
	err = rpushExec.Run(ctx)
	require.NoError(t, err)

	// LRANGE
	lrangeStep := ir.Step{
		Name: "test-lrange",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "LRANGE",
				"key":     "test:list:key",
				"start":   0,
				"stop":    -1,
			},
		},
	}

	lrangeExec, err := newRedisExecutor(ctx, lrangeStep)
	require.NoError(t, err)

	var stdout bytes.Buffer
	lrangeExec.SetStdout(&stdout)

	err = lrangeExec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "item1")
	assert.Contains(t, output, "item2")
	assert.Contains(t, output, "item3")

	// Cleanup
	delStep := ir.Step{
		Name: "cleanup",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "DEL",
				"key":     "test:list:key",
			},
		},
	}
	delExec, _ := newRedisExecutor(ctx, delStep)
	_ = delExec.Run(ctx)
}

func TestRedisExecutor_Set_Integration(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()

	// SADD
	saddStep := ir.Step{
		Name: "test-sadd",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "SADD",
				"key":     "test:set:key",
				"values":  []any{"member1", "member2", "member3"},
			},
		},
	}

	saddExec, err := newRedisExecutor(ctx, saddStep)
	require.NoError(t, err)
	err = saddExec.Run(ctx)
	require.NoError(t, err)

	// SMEMBERS
	smembersStep := ir.Step{
		Name: "test-smembers",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "SMEMBERS",
				"key":     "test:set:key",
			},
		},
	}

	smembersExec, err := newRedisExecutor(ctx, smembersStep)
	require.NoError(t, err)

	var stdout bytes.Buffer
	smembersExec.SetStdout(&stdout)

	err = smembersExec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "member1")
	assert.Contains(t, output, "member2")
	assert.Contains(t, output, "member3")

	// Cleanup
	delStep := ir.Step{
		Name: "cleanup",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "DEL",
				"key":     "test:set:key",
			},
		},
	}
	delExec, _ := newRedisExecutor(ctx, delStep)
	_ = delExec.Run(ctx)
}

func TestRedisExecutor_SortedSet_Integration(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()

	// ZADD
	zaddStep := ir.Step{
		Name: "test-zadd",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "ZADD",
				"key":     "test:zset:key",
				"score":   1.0,
				"value":   "member1",
			},
		},
	}

	zaddExec, err := newRedisExecutor(ctx, zaddStep)
	require.NoError(t, err)
	err = zaddExec.Run(ctx)
	require.NoError(t, err)

	// ZRANGE with scores
	zrangeStep := ir.Step{
		Name: "test-zrange",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":        host,
				"command":     "ZRANGE",
				"key":         "test:zset:key",
				"start":       0,
				"stop":        -1,
				"with_scores": true,
			},
		},
	}

	zrangeExec, err := newRedisExecutor(ctx, zrangeStep)
	require.NoError(t, err)

	var stdout bytes.Buffer
	zrangeExec.SetStdout(&stdout)

	err = zrangeExec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "member1")

	// Cleanup
	delStep := ir.Step{
		Name: "cleanup",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "DEL",
				"key":     "test:zset:key",
			},
		},
	}
	delExec, _ := newRedisExecutor(ctx, delStep)
	_ = delExec.Run(ctx)
}

func TestRedisExecutor_Timeout_Integration(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()
	step := ir.Step{
		Name: "test-timeout",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "PING",
				"timeout": 5, // 5 second timeout
			},
		},
	}

	exec, err := newRedisExecutor(ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "PONG")
}

func TestRedisExecutor_Kill(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()
	step := ir.Step{
		Name: "test-kill",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "PING",
			},
		},
	}

	exec, err := newRedisExecutor(ctx, step)
	require.NoError(t, err)

	// Kill should not error even before Run is called
	err = exec.Kill(os.Interrupt)
	assert.NoError(t, err)
}

func TestRedisExecutor_Close(t *testing.T) {
	host := skipIfNoRedis(t)

	ctx := context.Background()
	step := ir.Step{
		Name: "test-close",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "PING",
			},
		},
	}

	exec, err := newRedisExecutor(ctx, step)
	require.NoError(t, err)

	// Check if implements io.Closer
	closer, ok := exec.(interface{ Close() error })
	if ok {
		err = closer.Close()
		assert.NoError(t, err)

		// Double close should not error
		err = closer.Close()
		assert.NoError(t, err)
	}
}

// --- Unit Tests for Helper Functions ---

func TestResultWriter_Formats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		format   string
		value    any
		contains string
	}{
		{"json string", "json", "hello", "hello"},
		{"json number", "json", 42, "42"},
		{"json array", "json", []string{"a", "b"}, "a"},
		{"jsonl string", "jsonl", "hello", "hello"},
		{"raw string", "raw", "hello", "hello"},
		{"raw array", "raw", []string{"a", "b"}, "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			writer := redisexec.NewResultWriter(&buf, tt.format, "null")

			err := writer.Write(tt.value)
			require.NoError(t, err)

			err = writer.Flush()
			require.NoError(t, err)

			assert.Contains(t, buf.String(), tt.contains)
		})
	}
}

func TestResultWriter_CSV(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writer := redisexec.NewResultWriter(&buf, "csv", "NULL")

	err := writer.Write([]string{"value1", "value2", "value3"})
	require.NoError(t, err)

	err = writer.Flush()
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "value1")
	assert.Contains(t, output, "value2")
}

func TestResultWriter_NilValue(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writer := redisexec.NewResultWriter(&buf, "raw", "<nil>")

	err := writer.Write(nil)
	require.NoError(t, err)

	err = writer.Flush()
	require.NoError(t, err)

	assert.Contains(t, buf.String(), "<nil>")
}

func TestTruncateString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "he..."},
		{"hi", 2, "hi"},
		{"hello", 3, "hel"}, // When maxLen <= 3, no room for ellipsis
		{"ab", 1, "a"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			result := redisexec.TruncateString(tt.input, tt.maxLen)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSanitizeKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"normal-key", "normal-key"},
		{"key\nwith\nnewlines", "key\\nwith\\nnewlines"},
		{"key\rwith\rreturns", "key\\rwith\\rreturns"},
		{"key\twith\ttabs", "key\\twith\\ttabs"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			result := redisexec.SanitizeKey(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    int64
		expected string
	}{
		{-1, "no expiry"},
		{-100, "no expiry"},
		{0, "0 seconds"},
		{60, "60 seconds"},
		{3600, "3600 seconds"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			t.Parallel()
			result := redisexec.FormatDuration(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetBuiltinScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expected bool
	}{
		{"rate_limit", true},
		{"atomic_incr_max", true},
		{"compare_and_swap", true},
		{"lock_with_timeout", true},
		{"unknown_script", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script, ok := redisexec.GetBuiltinScript(tt.name)
			assert.Equal(t, tt.expected, ok)
			if tt.expected {
				assert.NotEmpty(t, script)
			}
		})
	}
}

// --- Global Pool Manager Tests ---

func TestGlobalRedisPoolManager_NewAndClose(t *testing.T) {
	t.Parallel()

	cfg := redisexec.GlobalPoolConfig{
		MaxClients: 10,
	}

	pm := redisexec.NewGlobalRedisPoolManager(cfg)
	require.NotNil(t, pm)

	// Close should not error
	err := pm.Close()
	assert.NoError(t, err)

	// Double close should not error
	err = pm.Close()
	assert.NoError(t, err)
}

func TestGlobalRedisPoolManager_Stats(t *testing.T) {
	t.Parallel()

	pm := redisexec.NewGlobalRedisPoolManager(redisexec.GlobalPoolConfig{})
	t.Cleanup(func() { _ = pm.Close() })

	stats := pm.Stats()
	require.NotNil(t, stats)
	assert.Equal(t, 0, stats["clientCount"])
	assert.Equal(t, false, stats["closed"])
}

func TestGlobalRedisPoolManager_Context(t *testing.T) {
	t.Parallel()

	pm := redisexec.NewGlobalRedisPoolManager(redisexec.GlobalPoolConfig{})
	t.Cleanup(func() { _ = pm.Close() })

	ctx := context.Background()

	// Without pool manager
	assert.Nil(t, redisexec.GetRedisPoolManager(ctx))

	// With pool manager
	ctx = redisexec.WithRedisPoolManager(ctx, pm)
	assert.Equal(t, pm, redisexec.GetRedisPoolManager(ctx))
}

func TestGlobalRedisPoolManager_Integration(t *testing.T) {
	host := skipIfNoRedis(t)

	pm := redisexec.NewGlobalRedisPoolManager(redisexec.GlobalPoolConfig{
		MaxClients: 5,
	})
	t.Cleanup(func() { _ = pm.Close() })

	ctx := redisexec.WithRedisPoolManager(context.Background(), pm)

	// Create executor using global pool
	step := ir.Step{
		Name: "test-pool",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "redis",
			Config: map[string]any{
				"host":    host,
				"command": "PING",
			},
		},
	}

	exec, err := newRedisExecutor(ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "PONG")

	// Check pool stats
	stats := pm.Stats()
	assert.GreaterOrEqual(t, stats["clientCount"], 1)
}
