// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package redis

import (
	"context"
	"crypto/sha1" //nolint:gosec // SHA1 used for Redis EVALSHA, not security
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	goredis "github.com/redis/go-redis/v9"
)

// ScriptExecutor executes Lua scripts on Redis.
type ScriptExecutor struct {
	client goredis.UniversalClient
	cfg    *Config
}

// NewScriptExecutor creates a new script executor.
func NewScriptExecutor(client goredis.UniversalClient, cfg *Config) *ScriptExecutor {
	return &ScriptExecutor{client: client, cfg: cfg}
}

// Execute executes the Lua script and returns the result.
func (e *ScriptExecutor) Execute(ctx context.Context) (any, error) {
	// Prepare keys and args
	keys := e.cfg.ScriptKeys
	args := e.cfg.ScriptArgs

	// If SHA is provided directly, try EVALSHA first
	sha := e.cfg.ScriptSHA
	if sha != "" {
		result, err := e.client.EvalSha(ctx, sha, keys, args...).Result()
		if err != nil {
			// Check if it's a NOSCRIPT error
			if isNoScriptError(err) {
				// Try to get script content for fallback
				script, scriptErr := e.getScript()
				if scriptErr != nil {
					return nil, fmt.Errorf("NOSCRIPT error and no script content available for fallback: %w", err)
				}
				// Fall back to EVAL
				result, err = e.client.Eval(ctx, script, keys, args...).Result()
				if err != nil {
					return nil, fmt.Errorf("script execution failed: %w", err)
				}
				return result, nil
			}
			return nil, fmt.Errorf("evalsha failed: %w", err)
		}
		return result, nil
	}

	// No SHA provided, get script content and calculate SHA
	script, err := e.getScript()
	if err != nil {
		return nil, err
	}

	sha = calculateSHA1(script)

	// Try EVALSHA first for better performance (script may be cached)
	result, err := e.client.EvalSha(ctx, sha, keys, args...).Result()
	if err != nil {
		// Check if it's a NOSCRIPT error
		if isNoScriptError(err) {
			// Fall back to EVAL
			result, err = e.client.Eval(ctx, script, keys, args...).Result()
			if err != nil {
				return nil, fmt.Errorf("script execution failed: %w", err)
			}
		} else {
			return nil, fmt.Errorf("evalsha failed: %w", err)
		}
	}

	return result, nil
}

// getScript returns the script content from config.
func (e *ScriptExecutor) getScript() (string, error) {
	// If script content is provided directly
	if e.cfg.Script != "" {
		return e.cfg.Script, nil
	}

	// If script file is provided
	if e.cfg.ScriptFile != "" {
		content, err := fileutil.ReadFile(e.cfg.ScriptFile)
		if err != nil {
			return "", fmt.Errorf("failed to read script file %s: %w", e.cfg.ScriptFile, err)
		}
		return string(content), nil
	}

	return "", fmt.Errorf("no script or scriptFile provided")
}

// LoadScript loads a script into Redis and returns its SHA.
func (e *ScriptExecutor) LoadScript(ctx context.Context, script string) (string, error) {
	sha, err := e.client.ScriptLoad(ctx, script).Result()
	if err != nil {
		return "", fmt.Errorf("failed to load script: %w", err)
	}
	return sha, nil
}

// ScriptExists checks if a script exists in Redis.
func (e *ScriptExecutor) ScriptExists(ctx context.Context, sha string) (bool, error) {
	results, err := e.client.ScriptExists(ctx, sha).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check script existence: %w", err)
	}
	if len(results) == 0 {
		return false, nil
	}
	return results[0], nil
}

// FlushScripts flushes the script cache.
func (e *ScriptExecutor) FlushScripts(ctx context.Context) error {
	return e.client.ScriptFlush(ctx).Err()
}

// calculateSHA1 calculates the SHA1 hash of a string.
func calculateSHA1(s string) string {
	h := sha1.New() //nolint:gosec // SHA1 used for Redis EVALSHA, not security
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// isNoScriptError checks if the error is a NOSCRIPT error.
func isNoScriptError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "NOSCRIPT")
}

// Common Lua scripts for typical operations.
var (
	// RateLimitScript implements a sliding window rate limiter.
	RateLimitScript = `
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

-- Remove old entries
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)

-- Count current entries
local count = redis.call('ZCARD', key)

if count < limit then
    -- Add new entry
    redis.call('ZADD', key, now, now .. '-' .. math.random())
    redis.call('EXPIRE', key, window)
    return 1
else
    return 0
end
`

	// AtomicIncrWithMaxScript atomically increments a value up to a maximum.
	AtomicIncrWithMaxScript = `
local key = KEYS[1]
local max = tonumber(ARGV[1])
local current = tonumber(redis.call('GET', key) or 0)

if current < max then
    return redis.call('INCR', key)
else
    return current
end
`

	// CompareAndSwapScript implements compare-and-swap.
	CompareAndSwapScript = `
local key = KEYS[1]
local expected = ARGV[1]
local new = ARGV[2]
local current = redis.call('GET', key)

if current == expected then
    redis.call('SET', key, new)
    return 1
else
    return 0
end
`

	// LockWithTimeoutScript implements a lock with automatic expiry.
	LockWithTimeoutScript = `
local key = KEYS[1]
local value = ARGV[1]
local timeout = tonumber(ARGV[2])

if redis.call('SETNX', key, value) == 1 then
    redis.call('EXPIRE', key, timeout)
    return 1
else
    return 0
end
`
)

// GetBuiltinScript returns a built-in script by name.
func GetBuiltinScript(name string) (string, bool) {
	scripts := map[string]string{
		"rate_limit":        RateLimitScript,
		"atomic_incr_max":   AtomicIncrWithMaxScript,
		"compare_and_swap":  CompareAndSwapScript,
		"lock_with_timeout": LockWithTimeoutScript,
	}
	script, ok := scripts[name]
	return script, ok
}
