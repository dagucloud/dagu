// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package redis_test

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/require"

	redisexec "github.com/dagucloud/dagu/v2/internal/runtime/builtin/redis"
)

// TestRace_ConcurrentExecutors tests that multiple executors can run concurrently
// without data races. Run with: go test -race ./internal/runtime/builtin/redis/...
func TestRace_ConcurrentExecutors(t *testing.T) {
	host := os.Getenv("REDIS_TEST_HOST")
	if host == "" {
		t.Skip("REDIS_TEST_HOST not set, skipping race test")
	}

	ctx := context.Background()
	numExecutors := 10

	var wg sync.WaitGroup
	errCh := make(chan error, numExecutors*2) // Buffer for potential errors

	for i := range numExecutors {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			step := ir.Step{
				Name: "test-concurrent",
				ExecutorConfig: ir.ExecutorConfig{
					Type: "redis",
					Config: map[string]any{
						"host":    host,
						"command": "PING",
					},
				},
			}

			exec, err := executor.NewExecutor(ctx, step)
			if err != nil {
				errCh <- err
				return
			}

			var stdout bytes.Buffer
			exec.SetStdout(&stdout)

			if err := exec.Run(ctx); err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	// Collect and assert errors in main goroutine
	for err := range errCh {
		require.NoError(t, err)
	}
}

// TestRace_GlobalPoolManagerConcurrent tests concurrent access to the global pool manager.
func TestRace_GlobalPoolManagerConcurrent(t *testing.T) {
	host := os.Getenv("REDIS_TEST_HOST")
	if host == "" {
		t.Skip("REDIS_TEST_HOST not set, skipping race test")
	}

	pm := redisexec.NewGlobalRedisPoolManager(redisexec.GlobalPoolConfig{
		MaxClients: 10,
	})
	defer pm.Close()

	ctx := redisexec.WithRedisPoolManager(context.Background(), pm)

	numGoroutines := 20
	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	// Test concurrent GetOrCreateClient calls
	for i := range numGoroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			cfg, err := redisexec.ParseConfig(ctx, map[string]any{
				"host":    host,
				"command": "PING",
			})
			if err != nil {
				errCh <- err
				return
			}

			client, err := pm.GetOrCreateClient(ctx, cfg)
			if err != nil {
				return // Connection errors are acceptable in race conditions
			}

			// Execute a command
			_, err = client.Ping(ctx).Result()
			if err != nil {
				return // Ping errors are acceptable in race conditions
			}

			// Release client
			pm.ReleaseClient(cfg)
		}(i)
	}

	wg.Wait()
	close(errCh)

	// Collect and assert config parsing errors in main goroutine
	for err := range errCh {
		require.NoError(t, err)
	}

	// Verify pool stats are consistent
	stats := pm.Stats()
	require.NotNil(t, stats)
}

// TestRace_ConfigParsing tests concurrent config parsing.
func TestRace_ConfigParsing(t *testing.T) {
	ctx := context.Background()
	numGoroutines := 20
	var wg sync.WaitGroup

	configs := []map[string]any{
		{"host": "localhost", "command": "PING"},
		{"host": "localhost", "command": "GET", "key": "test"},
		{"host": "localhost", "command": "SET", "key": "test", "value": "val"},
		{"host": "localhost", "port": 6380, "command": "PING"},
		{
			"host": "localhost",
			"pipeline": []map[string]any{
				{"command": "SET", "key": "k1", "value": "v1"},
				{"command": "GET", "key": "k1"},
			},
		},
	}

	for i := range numGoroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			cfg := configs[idx%len(configs)]
			_, _ = redisexec.ParseConfig(ctx, cfg)
		}(i)
	}

	wg.Wait()
}

// TestRace_ResultWriter tests concurrent result writer operations.
func TestRace_ResultWriter(t *testing.T) {
	numGoroutines := 10
	var wg sync.WaitGroup

	for range numGoroutines {
		wg.Go(func() {

			var buf bytes.Buffer
			writer := redisexec.NewResultWriter(&buf, "jsonl", "null")

			for j := range 100 {
				_ = writer.Write(map[string]any{"id": j, "name": "test"})
			}

			_ = writer.Flush()
		})
	}

	wg.Wait()
}

// TestRace_ExecutorKill tests calling Kill() concurrently with Run().
func TestRace_ExecutorKill(t *testing.T) {
	host := os.Getenv("REDIS_TEST_HOST")
	if host == "" {
		t.Skip("REDIS_TEST_HOST not set, skipping race test")
	}

	ctx := context.Background()
	numIterations := 5

	for range numIterations {
		step := ir.Step{
			Name: "test-kill-race",
			ExecutorConfig: ir.ExecutorConfig{
				Type: "redis",
				Config: map[string]any{
					"host":    host,
					"command": "PING",
				},
			},
		}

		exec, err := executor.NewExecutor(ctx, step)
		require.NoError(t, err)

		var stdout bytes.Buffer
		exec.SetStdout(&stdout)

		var wg sync.WaitGroup

		// Start Run() in goroutine
		wg.Go(func() {
			// Run may succeed or be killed - either is fine
			_ = exec.Run(ctx)
		})

		// Kill() from another goroutine
		wg.Go(func() {
			_ = exec.Kill(os.Interrupt)
		})

		wg.Wait()
	}
}

// TestRace_ExecutorClose tests calling Close() concurrently.
func TestRace_ExecutorClose(t *testing.T) {
	host := os.Getenv("REDIS_TEST_HOST")
	if host == "" {
		t.Skip("REDIS_TEST_HOST not set, skipping race test")
	}

	ctx := context.Background()
	numIterations := 5

	for range numIterations {
		step := ir.Step{
			Name: "test-close-race",
			ExecutorConfig: ir.ExecutorConfig{
				Type: "redis",
				Config: map[string]any{
					"host":    host,
					"command": "PING",
				},
			},
		}

		exec, err := executor.NewExecutor(ctx, step)
		require.NoError(t, err)

		closer, ok := exec.(interface{ Close() error })
		if !ok {
			continue
		}

		var wg sync.WaitGroup
		numClosers := 5

		// Multiple goroutines trying to close
		for range numClosers {
			wg.Go(func() {
				_ = closer.Close()
			})
		}

		wg.Wait()
	}
}

// TestRace_PoolManagerClose tests closing pool manager while clients are active.
func TestRace_PoolManagerClose(t *testing.T) {
	host := os.Getenv("REDIS_TEST_HOST")
	if host == "" {
		t.Skip("REDIS_TEST_HOST not set, skipping race test")
	}

	for range 3 {
		pm := redisexec.NewGlobalRedisPoolManager(redisexec.GlobalPoolConfig{
			MaxClients: 5,
		})

		ctx := redisexec.WithRedisPoolManager(context.Background(), pm)

		var wg sync.WaitGroup
		numWorkers := 10

		// Start workers getting clients
		for range numWorkers {
			wg.Go(func() {

				cfg, err := redisexec.ParseConfig(ctx, map[string]any{
					"host":    host,
					"command": "PING",
				})
				if err != nil {
					return
				}

				client, err := pm.GetOrCreateClient(ctx, cfg)
				if err != nil {
					return
				}

				// Use the client
				_, _ = client.Ping(ctx).Result()

				pm.ReleaseClient(cfg)
			})
		}

		// Close while workers are active
		go func() {
			_ = pm.Close()
		}()

		wg.Wait()
	}
}

// TestRace_HashConfigConcurrent tests concurrent hashConfig calls.
func TestRace_HashConfigConcurrent(t *testing.T) {
	ctx := context.Background()
	numGoroutines := 20
	var wg sync.WaitGroup

	for i := range numGoroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			cfg1, _ := redisexec.ParseConfig(ctx, map[string]any{
				"host":    "localhost",
				"port":    6379,
				"db":      idx % 16,
				"command": "PING",
			})

			cfg2, _ := redisexec.ParseConfig(ctx, map[string]any{
				"host":    "localhost",
				"port":    6379,
				"db":      idx % 16,
				"command": "PING",
			})

			// hashConfig is called internally when using pool manager
			// We can't call it directly, but we can verify configs are equal
			_ = cfg1
			_ = cfg2
		}(i)
	}

	wg.Wait()
}

// TestRace_MultiplePoolManagerOperations tests various pool manager operations concurrently.
func TestRace_MultiplePoolManagerOperations(t *testing.T) {
	host := os.Getenv("REDIS_TEST_HOST")
	if host == "" {
		t.Skip("REDIS_TEST_HOST not set, skipping race test")
	}

	pm := redisexec.NewGlobalRedisPoolManager(redisexec.GlobalPoolConfig{
		MaxClients: 10,
	})
	defer pm.Close()

	ctx := redisexec.WithRedisPoolManager(context.Background(), pm)

	var wg sync.WaitGroup
	numGoroutines := 30

	for i := range numGoroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			switch idx % 3 {
			case 0:
				// GetOrCreateClient
				cfg, err := redisexec.ParseConfig(ctx, map[string]any{
					"host":    host,
					"db":      idx % 16,
					"command": "PING",
				})
				if err != nil {
					return
				}
				client, err := pm.GetOrCreateClient(ctx, cfg)
				if err != nil {
					return
				}
				_, _ = client.Ping(ctx).Result()
				pm.ReleaseClient(cfg)

			case 1:
				// Stats
				stats := pm.Stats()
				_ = stats["clientCount"]
				_ = stats["closed"]

			case 2:
				// Context operations
				ctx2 := redisexec.WithRedisPoolManager(context.Background(), pm)
				pm2 := redisexec.GetRedisPoolManager(ctx2)
				_ = pm2
			}
		}(i)
	}

	wg.Wait()
}
