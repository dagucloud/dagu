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

func TestStepResolutionDeclarations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("CommandUsesCommandContextHook", func(t *testing.T) {
		RegisterExecutorCapabilities("command-resolution-test", ExecutorCapabilities{
			Command: true,
			CommandContext: func(_ context.Context, _ Step) cmnvalue.CommandContext {
				return cmnvalue.CommandContext{Target: cmnvalue.CommandTargetSSH, ShellConfigured: true}
			},
		})
		t.Cleanup(func() { UnregisterExecutorCapabilities("command-resolution-test") })

		step := Step{ExecutorConfig: ExecutorConfig{Type: "command-resolution-test"}}
		command := step.CommandResolution(ctx)
		assert.Equal(t, cmnvalue.CommandTargetSSH, command.Target)
		assert.True(t, command.ShellConfigured)
	})

	t.Run("ScriptUsesScriptContextHook", func(t *testing.T) {
		RegisterExecutorCapabilities("script-resolution-test", ExecutorCapabilities{
			Command: true,
			Script:  true,
			CommandContext: func(_ context.Context, _ Step) cmnvalue.CommandContext {
				return cmnvalue.CommandContext{Target: cmnvalue.CommandTargetDocker}
			},
			ScriptContext: func(_ context.Context, _ Step) cmnvalue.CommandContext {
				return cmnvalue.CommandContext{Target: cmnvalue.CommandTargetSSH}
			},
		})
		t.Cleanup(func() { UnregisterExecutorCapabilities("script-resolution-test") })

		step := Step{ExecutorConfig: ExecutorConfig{Type: "script-resolution-test"}}
		assert.Equal(t, cmnvalue.CommandTargetSSH, step.ScriptResolution(ctx).Target)
	})

	t.Run("ConfigUsesProfileHook", func(t *testing.T) {
		RegisterExecutorCapabilities("config-resolution-test", ExecutorCapabilities{
			Command: true,
			ConfigProfile: func(_ context.Context, _ Step) cmnvalue.ConfigProfile {
				return cmnvalue.ConfigProfileTemplate
			},
		})
		t.Cleanup(func() { UnregisterExecutorCapabilities("config-resolution-test") })

		step := Step{ExecutorConfig: ExecutorConfig{Type: "config-resolution-test"}}
		assert.Equal(t, cmnvalue.ConfigProfileTemplate, step.ConfigResolutionProfile(ctx))
	})

	t.Run("ScriptFallsBackToCommandContext", func(t *testing.T) {
		RegisterExecutorCapabilities("script-command-fallback-test", ExecutorCapabilities{
			Command: true,
			Script:  true,
			CommandContext: func(_ context.Context, _ Step) cmnvalue.CommandContext {
				return cmnvalue.CommandContext{Target: cmnvalue.CommandTargetDocker}
			},
		})
		t.Cleanup(func() { UnregisterExecutorCapabilities("script-command-fallback-test") })

		step := Step{ExecutorConfig: ExecutorConfig{Type: "script-command-fallback-test"}}
		assert.Equal(t, cmnvalue.CommandTargetDocker, step.ScriptResolution(ctx).Target)
	})

	t.Run("UnregisteredExecutorUsesDefaults", func(t *testing.T) {
		step := Step{ExecutorConfig: ExecutorConfig{Type: "unregistered-executor"}}
		assert.Equal(t, cmnvalue.CommandTargetLocal, step.CommandResolution(ctx).Target)
		assert.False(t, step.CommandResolution(ctx).ShellConfigured)
		assert.Equal(t, cmnvalue.ConfigProfileDefault, step.ConfigResolutionProfile(ctx))
	})
}
