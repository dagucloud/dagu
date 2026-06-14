// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"fmt"
	"sync"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/stretchr/testify/assert"
)

func TestExecutorCapabilities_Get(t *testing.T) {
	registry := &executorCapabilitiesRegistry{
		caps: make(map[string]ExecutorCapabilities),
	}

	// Test case 1: Registered executor
	caps := ExecutorCapabilities{Command: true, MultipleCommands: true}
	registry.Register("test-executor", caps)
	assert.Equal(t, caps, registry.Get("test-executor"))

	// Test case 2: Unregistered executor should return empty capabilities (strict default)
	assert.Equal(t, ExecutorCapabilities{}, registry.Get("unregistered"))
}

func TestSupportsHelpers(t *testing.T) {
	// Register a test executor with specific capabilities
	caps := ExecutorCapabilities{
		Command:        true,
		Script:         false,
		WorkerSelector: true,
	}
	RegisterExecutorCapabilities("helper-test", caps)

	assert.True(t, SupportsCommand("helper-test"))
	assert.False(t, SupportsScript("helper-test"))
	assert.True(t, SupportsWorkerSelector("helper-test"))

	// Unregistered executor should return false for everything
	assert.False(t, SupportsCommand("unknown"))
	assert.False(t, SupportsScript("unknown"))
	assert.False(t, SupportsShell("unknown"))
}

func TestExecutorCapabilities_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	registry := &executorCapabilitiesRegistry{
		caps: make(map[string]ExecutorCapabilities),
	}

	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("executor-%d", i)
			registry.Register(name, ExecutorCapabilities{Command: true})
			assert.True(t, registry.Get(name).Command)
		}(i)
	}

	for i := range 64 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = registry.Get(fmt.Sprintf("executor-%d", i))
			_ = registry.Get("missing")
		}(i)
	}

	wg.Wait()
	assert.True(t, registry.Get("executor-63").Command)
}

func optionsFromSlice(opts []cmnvalue.Option) *cmnvalue.Options {
	out := cmnvalue.NewOptions()
	for _, opt := range opts {
		opt(out)
	}
	return out
}

func TestStep_EvalOptions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("CommandUsesFieldSpecificHook", func(t *testing.T) {
		RegisterExecutorCapabilities("command-eval-opts-test", ExecutorCapabilities{
			Command: true,
			GetCommandEvalOptions: func(_ context.Context, _ Step) []cmnvalue.Option {
				return []cmnvalue.Option{cmnvalue.WithoutExpandShell()}
			},
		})
		t.Cleanup(func() { UnregisterExecutorCapabilities("command-eval-opts-test") })

		step := Step{ExecutorConfig: ExecutorConfig{Type: "command-eval-opts-test"}}
		opts := optionsFromSlice(step.CommandEvalOptions(ctx))
		assert.False(t, opts.Substitute)
		assert.False(t, opts.ExpandShell)
	})

	t.Run("ScriptUsesFieldSpecificHook", func(t *testing.T) {
		RegisterExecutorCapabilities("script-eval-opts-test", ExecutorCapabilities{
			Command: true,
			Script:  true,
			GetScriptEvalOptions: func(_ context.Context, _ Step) []cmnvalue.Option {
				return []cmnvalue.Option{cmnvalue.WithNoExpansion()}
			},
		})
		t.Cleanup(func() { UnregisterExecutorCapabilities("script-eval-opts-test") })

		step := Step{ExecutorConfig: ExecutorConfig{Type: "script-eval-opts-test"}}
		opts := optionsFromSlice(step.ScriptEvalOptions(ctx))
		assert.False(t, opts.Substitute)
		assert.True(t, opts.NoExpansion)
	})

	t.Run("ConfigDefaultsDisableSubstitute", func(t *testing.T) {
		RegisterExecutorCapabilities("config-eval-opts-test", ExecutorCapabilities{
			Command: true,
		})
		t.Cleanup(func() { UnregisterExecutorCapabilities("config-eval-opts-test") })

		step := Step{ExecutorConfig: ExecutorConfig{Type: "config-eval-opts-test"}}
		opts := optionsFromSlice(step.ConfigEvalOptions(ctx))
		assert.False(t, opts.Substitute)
	})

	t.Run("LegacyEvalHookFallsBackForCommandAndScript", func(t *testing.T) {
		RegisterExecutorCapabilities("legacy-eval-opts-test", ExecutorCapabilities{
			Command: true,
			Script:  true,
			GetEvalOptions: func(_ context.Context, _ Step) []cmnvalue.Option {
				return []cmnvalue.Option{cmnvalue.WithoutExpandShell()}
			},
		})
		t.Cleanup(func() { UnregisterExecutorCapabilities("legacy-eval-opts-test") })

		step := Step{ExecutorConfig: ExecutorConfig{Type: "legacy-eval-opts-test"}}
		commandOpts := optionsFromSlice(step.CommandEvalOptions(ctx))
		scriptOpts := optionsFromSlice(step.ScriptEvalOptions(ctx))
		assert.False(t, commandOpts.Substitute)
		assert.False(t, commandOpts.ExpandShell)
		assert.False(t, scriptOpts.Substitute)
		assert.False(t, scriptOpts.ExpandShell)
	})

	t.Run("UnregisteredExecutorStillDisablesSubstitute", func(t *testing.T) {
		step := Step{ExecutorConfig: ExecutorConfig{Type: "unregistered-executor"}}
		commandOpts := optionsFromSlice(step.CommandEvalOptions(ctx))
		scriptOpts := optionsFromSlice(step.ScriptEvalOptions(ctx))
		configOpts := optionsFromSlice(step.ConfigEvalOptions(ctx))
		assert.False(t, commandOpts.Substitute)
		assert.False(t, scriptOpts.Substitute)
		assert.False(t, configOpts.Substitute)
	})
}
