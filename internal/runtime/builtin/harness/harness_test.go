// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderBaseArgs(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		prompt   string
		expected []string
	}{
		{"claude", &claudeProvider{}, "hello", []string{"-p", "hello"}},
		{"codex", &codexProvider{}, "hello", []string{"exec", "hello"}},
		{"copilot", &copilotProvider{}, "hello", []string{"-p", "hello"}},
		{"opencode", &opencodeProvider{}, "hello", []string{"run", "hello"}},
		{"pi", &piProvider{}, "hello", []string{"-p", "hello"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.provider.BaseArgs(tt.prompt))
			assert.Equal(t, tt.name, tt.provider.Name())
		})
	}
}

func TestProviderDefaultConfig(t *testing.T) {
	t.Run("codex", func(t *testing.T) {
		provider, ok := any(&codexProvider{}).(defaultConfigProvider)
		require.True(t, ok)
		assert.Equal(t, map[string]any{"skip_git_repo_check": true}, provider.DefaultConfig())
	})
}

func TestHarnessExecutorPushBackContextAugmentsPromptWithLogPath(t *testing.T) {
	t.Parallel()

	exec := &harnessExecutor{prompt: "Fix the implementation"}
	exec.SetPushBackContext(map[string]string{
		"FEEDBACK": "tighten the tests",
		"SCOPE":    "unit only",
	}, 2)
	exec.SetPushBackPreviousStdout("/tmp/dagu/review.out")

	prompt := exec.effectivePrompt()

	assert.Contains(t, prompt, "Fix the implementation")
	assert.Contains(t, prompt, "Push-back iteration: 2")
	assert.Contains(t, prompt, "Previous stdout log: /tmp/dagu/review.out")
	assert.Contains(t, prompt, "- FEEDBACK: tighten the tests")
	assert.Contains(t, prompt, "- SCOPE: unit only")
	assert.NotContains(t, prompt, "previous stdout content")
}

func TestConfigToFlags(t *testing.T) {
	t.Run("reserved_keys_skipped", func(t *testing.T) {
		flags := configToFlags(map[string]any{
			"provider": "claude",
			"fallback": []any{
				map[string]any{"provider": "codex"},
			},
		}, nil)
		assert.Empty(t, flags)
	})

	t.Run("bool_number_and_array", func(t *testing.T) {
		flags := configToFlags(map[string]any{
			"bare":           true,
			"max-turns":      20,
			"max-budget-usd": 5.5,
			"allow-tool":     []any{"shell(git:*)", "write"},
		}, nil)
		assert.Equal(t, []string{
			"--allow-tool", "shell(git:*)",
			"--allow-tool", "write",
			"--bare",
			"--max-budget-usd", "5.5",
			"--max-turns", "20",
		}, flags)
	})

	t.Run("builtin_flags_normalize_underscores", func(t *testing.T) {
		flags := configToFlags(map[string]any{
			"full_auto":           true,
			"max_turns":           20,
			"skip_git_repo_check": true,
		}, nil)
		assert.Equal(t, []string{
			"--full-auto",
			"--max-turns", "20",
			"--skip-git-repo-check",
		}, flags)
	})

	t.Run("definition_overrides_flag_tokens", func(t *testing.T) {
		flags := configToFlags(map[string]any{
			"provider":   "gemini",
			"model":      "gemini-2.5-pro",
			"allow-tool": []any{"shell(git:*)"},
		}, &core.HarnessDefinition{
			FlagStyle:   core.HarnessFlagStyleSingleDash,
			OptionFlags: map[string]string{"allow-tool": "--allowedTool"},
		})
		assert.Equal(t, []string{
			"--allowedTool", "shell(git:*)",
			"-model", "gemini-2.5-pro",
		}, flags)
	})
}

func TestNormalizeConfigMap(t *testing.T) {
	cfg := normalizeConfigMap(map[string]any{
		"provider":    "${PROVIDER}",
		"bare":        "true",
		"max-turns":   "10",
		"temperature": "5.5",
		"model":       "sonnet",
		"fallback": []any{
			map[string]any{
				"provider":  "${FALLBACK_PROVIDER}",
				"full-auto": "true",
			},
		},
	})

	assert.Equal(t, "${PROVIDER}", cfg["provider"])
	assert.Equal(t, true, cfg["bare"])
	assert.EqualValues(t, 10, cfg["max-turns"])
	assert.EqualValues(t, 5.5, cfg["temperature"])
	assert.Equal(t, "sonnet", cfg["model"])

	fallback := mustFallback(t, cfg["fallback"])
	require.Len(t, fallback, 1)
	assert.Equal(t, "${FALLBACK_PROVIDER}", fallback[0]["provider"])
	assert.Equal(t, true, fallback[0]["full-auto"])
}

func TestExtractFallbackConfigs(t *testing.T) {
	primary, fallback, err := extractFallbackConfigs(map[string]any{
		"provider": "claude",
		"model":    "sonnet",
		"fallback": []any{
			map[string]any{"provider": "codex", "full-auto": true},
			map[string]any{"provider": "copilot", "silent": true},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"provider": "claude",
		"model":    "sonnet",
	}, primary)
	require.Len(t, fallback, 2)
	assert.Equal(t, "codex", fallback[0]["provider"])
	assert.Equal(t, "copilot", fallback[1]["provider"])
}

func TestValidateHarnessStep(t *testing.T) {
	t.Run("missing_prompt", func(t *testing.T) {
		err := validateHarnessStep(core.Step{
			ExecutorConfig: core.ExecutorConfig{Config: map[string]any{"provider": "claude"}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "prompt")
	})

	t.Run("missing_config", func(t *testing.T) {
		err := validateHarnessStep(core.Step{
			Commands: []core.CommandEntry{{Command: "prompt"}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "config is required")
	})

	t.Run("missing_provider", func(t *testing.T) {
		err := validateHarnessStep(core.Step{
			Commands:       []core.CommandEntry{{Command: "prompt"}},
			ExecutorConfig: core.ExecutorConfig{Config: map[string]any{}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "config.provider is required")
	})

	t.Run("templated_provider_allowed", func(t *testing.T) {
		err := validateHarnessStep(core.Step{
			Commands:       []core.CommandEntry{{Command: "prompt"}},
			ExecutorConfig: core.ExecutorConfig{Config: map[string]any{"provider": "${PROVIDER}"}},
		})
		assert.NoError(t, err)
	})

	t.Run("templated_fallback_provider_allowed", func(t *testing.T) {
		err := validateHarnessStep(core.Step{
			Commands: []core.CommandEntry{{Command: "prompt"}},
			ExecutorConfig: core.ExecutorConfig{Config: map[string]any{
				"provider": "claude",
				"fallback": []any{
					map[string]any{"provider": "${FALLBACK_PROVIDER}"},
				},
			}},
		})
		assert.NoError(t, err)
	})

	t.Run("multiple_commands_rejected", func(t *testing.T) {
		err := validateHarnessStep(core.Step{
			Commands: []core.CommandEntry{
				{Command: "prompt one"},
				{Command: "prompt two"},
			},
			ExecutorConfig: core.ExecutorConfig{Config: map[string]any{"provider": "claude"}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "field 'command': action \"harness\" supports only one command")
		assert.NotContains(t, err.Error(), "executor")
	})

	t.Run("invalid_fallback_shape", func(t *testing.T) {
		err := validateHarnessStep(core.Step{
			Commands: []core.CommandEntry{{Command: "prompt"}},
			ExecutorConfig: core.ExecutorConfig{Config: map[string]any{
				"provider": "claude",
				"fallback": []any{"codex"},
			}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "fallback[0]")
	})

	t.Run("nested_fallback_rejected", func(t *testing.T) {
		err := validateHarnessStep(core.Step{
			Commands: []core.CommandEntry{{Command: "prompt"}},
			ExecutorConfig: core.ExecutorConfig{Config: map[string]any{
				"provider": "claude",
				"fallback": []any{
					map[string]any{
						"provider": "codex",
						"fallback": []any{
							map[string]any{"provider": "copilot"},
						},
					},
				},
			}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "config.fallback is not supported")
	})
}

func TestResolveProvider(t *testing.T) {
	t.Run("builtin", func(t *testing.T) {
		cfg, err := resolveProvider(map[string]any{"provider": "claude"}, nil)
		require.NoError(t, err)
		assert.Equal(t, "claude", cfg.binaryName())

		args, stdin, err := cfg.buildInvocation("hello", "context")
		require.NoError(t, err)
		assert.Equal(t, []string{"-p", "hello"}, args)
		assert.Equal(t, "context", mustReadAll(t, stdin))
	})

	t.Run("custom_definition", func(t *testing.T) {
		cfg, err := resolveProvider(map[string]any{"provider": "gemini"}, core.HarnessDefinitions{
			"gemini": {
				Binary:     "gemini",
				PrefixArgs: []string{"run"},
				PromptMode: core.HarnessPromptModeFlag,
				PromptFlag: "--prompt",
				FlagStyle:  core.HarnessFlagStyleGNULong,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "gemini", cfg.binaryName())

		cfg.flags = map[string]any{"provider": "gemini", "model": "gemini-2.5-pro"}
		args, stdin, err := cfg.buildInvocation("hello", "context")
		require.NoError(t, err)
		assert.Equal(t, []string{"run", "--prompt", "hello", "--model", "gemini-2.5-pro"}, args)
		assert.Equal(t, "context", mustReadAll(t, stdin))
	})

	t.Run("deleted_definition_is_unknown", func(t *testing.T) {
		_, err := resolveProvider(map[string]any{"provider": "gemini"}, core.HarnessDefinitions{
			"gemini": nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown provider")
	})

	t.Run("templated_provider_runtime_error", func(t *testing.T) {
		_, err := resolveProvider(map[string]any{"provider": "${PROVIDER}"}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unresolved provider template")
	})
}

func TestBuildProviderConfigs(t *testing.T) {
	t.Run("primary_and_fallback", func(t *testing.T) {
		if goruntime.GOOS == "windows" {
			t.Skip("Skipping shell-based test on Windows")
		}

		primary := writeHarnessTestBinary(t, "primary", "#!/bin/sh\nexit 0\n")
		fallback := writeHarnessTestBinary(t, "fallback", "#!/bin/sh\nexit 0\n")
		defs := core.HarnessDefinitions{
			"primary": {
				Binary:     primary,
				PromptMode: core.HarnessPromptModeArg,
				FlagStyle:  core.HarnessFlagStyleGNULong,
			},
			"fallback": {
				Binary:     fallback,
				PromptMode: core.HarnessPromptModeArg,
				FlagStyle:  core.HarnessFlagStyleGNULong,
			},
		}

		configs, err := buildProviderConfigs(map[string]any{
			"provider": "primary",
			"fallback": []any{
				map[string]any{"provider": "fallback"},
			},
		}, defs)
		require.NoError(t, err)
		require.Len(t, configs, 2)
		assert.Equal(t, primary, configs[0].binaryName())
		assert.Equal(t, fallback, configs[1].binaryName())
	})

	t.Run("reject_nested_fallback", func(t *testing.T) {
		_, err := buildProviderConfigs(map[string]any{
			"provider": "claude",
			"fallback": []any{
				map[string]any{
					"provider": "codex",
					"fallback": []any{
						map[string]any{"provider": "copilot"},
					},
				},
			},
		}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config.fallback is not supported")
	})

	t.Run("builtin_provider_defaults_are_applied", func(t *testing.T) {
		configs, err := buildProviderConfigs(map[string]any{
			"provider": "codex",
		}, nil)
		require.NoError(t, err)
		require.Len(t, configs, 1)
		assert.Equal(t, map[string]any{
			"provider":            "codex",
			"skip-git-repo-check": true,
		}, configs[0].flags)
	})

	t.Run("builtin_provider_defaults_can_be_overridden", func(t *testing.T) {
		configs, err := buildProviderConfigs(map[string]any{
			"provider":            "codex",
			"skip_git_repo_check": false,
		}, nil)
		require.NoError(t, err)
		require.Len(t, configs, 1)
		assert.Equal(t, map[string]any{
			"provider":            "codex",
			"skip-git-repo-check": false,
		}, configs[0].flags)
	})

	t.Run("builtin_provider_aliases_are_deduped", func(t *testing.T) {
		configs, err := buildProviderConfigs(map[string]any{
			"provider":            "codex",
			"skip-git-repo-check": false,
		}, nil)
		require.NoError(t, err)
		require.Len(t, configs, 1)
		assert.Equal(t, map[string]any{
			"provider":            "codex",
			"skip-git-repo-check": false,
		}, configs[0].flags)
	})
}

func TestProviderConfigBuildInvocation(t *testing.T) {
	t.Run("arg_mode_before_flags", func(t *testing.T) {
		cfg := providerConfig{
			name: "gemini",
			definition: &core.HarnessDefinition{
				Binary:         "gemini",
				PrefixArgs:     []string{"run"},
				PromptMode:     core.HarnessPromptModeArg,
				PromptPosition: core.HarnessPromptPositionBeforeFlags,
				FlagStyle:      core.HarnessFlagStyleGNULong,
			},
			flags: map[string]any{
				"provider": "gemini",
				"model":    "gemini-2.5-pro",
			},
		}

		args, stdin, err := cfg.buildInvocation("hello", "context")
		require.NoError(t, err)
		assert.Equal(t, []string{"run", "hello", "--model", "gemini-2.5-pro"}, args)
		assert.Equal(t, "context", mustReadAll(t, stdin))
	})

	t.Run("arg_mode_after_flags", func(t *testing.T) {
		cfg := providerConfig{
			name: "aider",
			definition: &core.HarnessDefinition{
				Binary:         "aider",
				PrefixArgs:     []string{"exec"},
				PromptMode:     core.HarnessPromptModeArg,
				PromptPosition: core.HarnessPromptPositionAfterFlags,
				FlagStyle:      core.HarnessFlagStyleSingleDash,
			},
			flags: map[string]any{
				"provider": "aider",
				"model":    "sonnet",
			},
		}

		args, stdin, err := cfg.buildInvocation("hello", "context")
		require.NoError(t, err)
		assert.Equal(t, []string{"exec", "-model", "sonnet", "hello"}, args)
		assert.Equal(t, "context", mustReadAll(t, stdin))
	})

	t.Run("flag_mode", func(t *testing.T) {
		cfg := providerConfig{
			name: "gemini",
			definition: &core.HarnessDefinition{
				Binary:         "gemini",
				PrefixArgs:     []string{"run"},
				PromptMode:     core.HarnessPromptModeFlag,
				PromptFlag:     "--prompt",
				PromptPosition: core.HarnessPromptPositionBeforeFlags,
				FlagStyle:      core.HarnessFlagStyleGNULong,
				OptionFlags:    map[string]string{"allow-tool": "--allowedTool"},
			},
			flags: map[string]any{
				"provider":   "gemini",
				"model":      "gemini-2.5-pro",
				"allow-tool": []any{"shell(git:*)"},
			},
		}

		args, stdin, err := cfg.buildInvocation("hello", "context")
		require.NoError(t, err)
		assert.Equal(t, []string{
			"run",
			"--prompt", "hello",
			"--allowedTool", "shell(git:*)",
			"--model", "gemini-2.5-pro",
		}, args)
		assert.Equal(t, "context", mustReadAll(t, stdin))
	})

	t.Run("stdin_mode", func(t *testing.T) {
		cfg := providerConfig{
			name: "llm",
			definition: &core.HarnessDefinition{
				Binary:     "llm",
				PrefixArgs: []string{"run"},
				PromptMode: core.HarnessPromptModeStdin,
				FlagStyle:  core.HarnessFlagStyleGNULong,
			},
			flags: map[string]any{
				"provider": "llm",
				"model":    "o3",
			},
		}

		args, stdin, err := cfg.buildInvocation("hello", "context")
		require.NoError(t, err)
		assert.Equal(t, []string{"run", "--model", "o3"}, args)
		assert.Equal(t, "hello\n\ncontext", mustReadAll(t, stdin))
	})
}

func TestHarnessExecutorRun_FallbackBuffersStdout(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	primary := writeHarnessTestBinary(t, "primary", `#!/bin/sh
echo "primary stdout"
echo "primary stderr" >&2
exit 1
`)
	fallback := writeHarnessTestBinary(t, "fallback", `#!/bin/sh
echo "fallback stdout"
echo "fallback stderr" >&2
exit 0
`)

	exec := &harnessExecutor{
		stdout: &strings.Builder{},
		stderr: &strings.Builder{},
		configs: []providerConfig{
			{
				name: "primary",
				definition: &core.HarnessDefinition{
					Binary:     primary,
					PromptMode: core.HarnessPromptModeArg,
					FlagStyle:  core.HarnessFlagStyleGNULong,
				},
				flags: map[string]any{"provider": "primary"},
			},
			{
				name: "fallback",
				definition: &core.HarnessDefinition{
					Binary:     fallback,
					PromptMode: core.HarnessPromptModeArg,
					FlagStyle:  core.HarnessFlagStyleGNULong,
				},
				flags: map[string]any{"provider": "fallback"},
			},
		},
		prompt: "hello",
	}

	stdout := exec.stdout.(*strings.Builder)
	stderr := exec.stderr.(*strings.Builder)
	err := exec.Run(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "fallback stdout\n", stdout.String())
	assert.Contains(t, stderr.String(), "primary stderr")
	assert.Contains(t, stderr.String(), "fallback stderr")
	assert.Contains(t, stderr.String(), "trying fallback")
	assert.Equal(t, 0, exec.ExitCode())
}

func TestHarnessExecutorRun_IncludesFailedStdoutInError(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	primary := writeHarnessTestBinary(t, "primary", `#!/bin/sh
echo "provider auth failed"
exit 1
`)

	var stdout strings.Builder
	var stderr strings.Builder
	exec := &harnessExecutor{
		stdout: &stdout,
		stderr: &stderr,
		configs: []providerConfig{
			{
				name: "primary",
				definition: &core.HarnessDefinition{
					Binary:     primary,
					PromptMode: core.HarnessPromptModeArg,
					FlagStyle:  core.HarnessFlagStyleGNULong,
				},
				flags: map[string]any{"provider": "primary"},
			},
		},
		prompt: "hello",
	}

	err := exec.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recent stdout:")
	assert.Contains(t, err.Error(), "provider auth failed")
	assert.Contains(t, stderr.String(), "recent stdout (tail):")
	assert.Contains(t, stderr.String(), "provider auth failed")
	assert.Empty(t, stdout.String())
	assert.Equal(t, 1, exec.ExitCode())
}

func TestHarnessExecutorRun_ContextCancellationSkipsFallback(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	marker := filepath.Join(t.TempDir(), "fallback-ran")
	primary := writeHarnessTestBinary(t, "primary", `#!/bin/sh
sleep 1
echo "primary stderr" >&2
exit 1
`)
	fallback := writeHarnessTestBinary(t, "fallback", "#!/bin/sh\ntouch \""+marker+"\"\nexit 0\n")

	var stdout strings.Builder
	var stderr strings.Builder
	exec := &harnessExecutor{
		stdout: &stdout,
		stderr: &stderr,
		configs: []providerConfig{
			{
				name: "primary",
				definition: &core.HarnessDefinition{
					Binary:     primary,
					PromptMode: core.HarnessPromptModeArg,
					FlagStyle:  core.HarnessFlagStyleGNULong,
				},
				flags: map[string]any{"provider": "primary"},
			},
			{
				name: "fallback",
				definition: &core.HarnessDefinition{
					Binary:     fallback,
					PromptMode: core.HarnessPromptModeArg,
					FlagStyle:  core.HarnessFlagStyleGNULong,
				},
				flags: map[string]any{"provider": "fallback"},
			},
		},
		prompt: "hello",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := exec.Run(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NoFileExists(t, marker)
	assert.NotContains(t, stderr.String(), "trying fallback")
	assert.Equal(t, 124, exec.ExitCode())
}

func TestHarnessExecutorRun_CreatesWorkingDir(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	workDir := filepath.Join(t.TempDir(), "nested", "workdir")
	bin := writeHarnessTestBinary(t, "pwd", "#!/bin/sh\npwd\n")

	var stdout strings.Builder
	var stderr strings.Builder
	exec := &harnessExecutor{
		stdout: &stdout,
		stderr: &stderr,
		configs: []providerConfig{
			{
				name: "pwd",
				definition: &core.HarnessDefinition{
					Binary:     bin,
					PromptMode: core.HarnessPromptModeArg,
					FlagStyle:  core.HarnessFlagStyleGNULong,
				},
				flags: map[string]any{"provider": "pwd"},
			},
		},
		prompt:  "hello",
		workDir: workDir,
	}

	err := exec.Run(context.Background())
	require.NoError(t, err)
	assert.DirExists(t, workDir)
	assert.Contains(t, stdout.String(), workDir)
}

func TestHarnessExecutorRun_UsesPATHFromRuntimeEnv(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	binDir := t.TempDir()
	binName := "path-provider"
	binPath := filepath.Join(binDir, binName)
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\necho \"resolved from path\"\n"), 0o755))

	dag := &core.DAG{
		Name:       "harness-path",
		WorkingDir: t.TempDir(),
		Harnesses: core.HarnessDefinitions{
			"custom": {
				Binary:     binName,
				PromptMode: core.HarnessPromptModeArg,
				FlagStyle:  core.HarnessFlagStyleGNULong,
			},
		},
	}
	step := core.Step{
		Name:     "step1",
		Commands: []core.CommandEntry{{Command: "hello"}},
		ExecutorConfig: core.ExecutorConfig{
			Type:   "harness",
			Config: map[string]any{"provider": "custom"},
		},
	}

	ctx := newHarnessTestContext(t, dag, step, "PATH="+binDir)
	exec, err := newHarness(ctx, step)
	require.NoError(t, err)

	var stdout strings.Builder
	var stderr strings.Builder
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, "resolved from path\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestHarnessExecutorRun_ResolvesRelativeBinaryFromWorkingDir(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	workDir := t.TempDir()
	binPath := filepath.Join(workDir, "bin", "agent")
	require.NoError(t, os.MkdirAll(filepath.Dir(binPath), 0o755))
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\necho \"resolved from workdir\"\n"), 0o755))

	dag := &core.DAG{
		Name:       "harness-workdir",
		WorkingDir: workDir,
		Harnesses: core.HarnessDefinitions{
			"custom": {
				Binary:     "./bin/agent",
				PromptMode: core.HarnessPromptModeArg,
				FlagStyle:  core.HarnessFlagStyleGNULong,
			},
		},
	}
	step := core.Step{
		Name:     "step1",
		Commands: []core.CommandEntry{{Command: "hello"}},
		ExecutorConfig: core.ExecutorConfig{
			Type:   "harness",
			Config: map[string]any{"provider": "custom"},
		},
	}

	ctx := newHarnessTestContext(t, dag, step)
	exec, err := newHarness(ctx, step)
	require.NoError(t, err)

	var stdout strings.Builder
	var stderr strings.Builder
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, "resolved from workdir\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestHarnessExecutorRun_FallbackBinaryOptionalUntilNeeded(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping shell-based test on Windows")
	}

	primary := writeHarnessTestBinary(t, "primary", "#!/bin/sh\necho \"primary ok\"\nexit 0\n")

	dag := &core.DAG{
		Name:       "harness-fallback",
		WorkingDir: t.TempDir(),
		Harnesses: core.HarnessDefinitions{
			"primary": {
				Binary:     primary,
				PromptMode: core.HarnessPromptModeArg,
				FlagStyle:  core.HarnessFlagStyleGNULong,
			},
			"fallback": {
				Binary:     "definitely-missing-harness-binary",
				PromptMode: core.HarnessPromptModeArg,
				FlagStyle:  core.HarnessFlagStyleGNULong,
			},
		},
	}
	step := core.Step{
		Name:     "step1",
		Commands: []core.CommandEntry{{Command: "hello"}},
		ExecutorConfig: core.ExecutorConfig{
			Type: "harness",
			Config: map[string]any{
				"provider": "primary",
				"fallback": []any{
					map[string]any{"provider": "fallback"},
				},
			},
		},
	}

	ctx := newHarnessTestContext(t, dag, step)
	exec, err := newHarness(ctx, step)
	require.NoError(t, err)

	var stdout strings.Builder
	var stderr strings.Builder
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, "primary ok\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestNewHarnessRejectsMultipleCommands(t *testing.T) {
	step := core.Step{
		Name: "step1",
		Commands: []core.CommandEntry{
			{Command: "hello"},
			{Command: "goodbye"},
		},
		ExecutorConfig: core.ExecutorConfig{
			Type:   "harness",
			Config: map[string]any{"provider": "claude"},
		},
	}

	ctx := newHarnessTestContext(t, nil, step)
	_, err := newHarness(ctx, step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "field 'command': action \"harness\" supports only one command")
	assert.NotContains(t, err.Error(), "executor")
}

func TestExtractPrompt(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		assert.Equal(t, "", extractPrompt(core.Step{}))
	})

	t.Run("cmd_with_args", func(t *testing.T) {
		step := core.Step{
			Commands: []core.CommandEntry{{CmdWithArgs: "Write tests for auth"}},
		}
		assert.Equal(t, "Write tests for auth", extractPrompt(step))
	})

	t.Run("command_only", func(t *testing.T) {
		step := core.Step{
			Commands: []core.CommandEntry{{Command: "Refactor"}},
		}
		assert.Equal(t, "Refactor", extractPrompt(step))
	})

	t.Run("command_with_args", func(t *testing.T) {
		step := core.Step{
			Commands: []core.CommandEntry{{Command: "analyze", Args: []string{"--deep", "src/"}}},
		}
		assert.Equal(t, "analyze --deep src/", extractPrompt(step))
	})
}

func TestGetProvider(t *testing.T) {
	for _, name := range []string{"claude", "codex", "copilot", "opencode", "pi"} {
		t.Run(name, func(t *testing.T) {
			p, err := getProvider(name)
			require.NoError(t, err)
			assert.Equal(t, name, p.Name())
		})
	}
}

func TestBuiltinProvidersStayInSyncWithCoreList(t *testing.T) {
	registered := make([]string, 0, len(providers))
	for name := range providers {
		registered = append(registered, name)
	}
	sort.Strings(registered)

	assert.Equal(t, core.BuiltinHarnessProviderNames(), registered)
}

func TestRegisterProviderPanicsOnDuplicate(t *testing.T) {
	dupName := "duplicate-test-provider"
	delete(providers, dupName)
	t.Cleanup(func() {
		delete(providers, dupName)
	})

	registerProvider(stubProvider{name: dupName})
	require.PanicsWithValue(
		t,
		`harness: duplicate provider registration "duplicate-test-provider"`,
		func() {
			registerProvider(stubProvider{name: dupName})
		},
	)
}

func TestExitCodeFromError(t *testing.T) {
	assert.Equal(t, 0, exitCodeFromError(nil))
	assert.Equal(t, 1, exitCodeFromError(assert.AnError))
}

func writeHarnessTestBinary(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	return path
}

func mustReadAll(t *testing.T, reader io.Reader) string {
	t.Helper()

	if reader == nil {
		return ""
	}
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(data)
}

func mustFallback(t *testing.T, value any) []map[string]any {
	t.Helper()

	switch v := value.(type) {
	case []map[string]any:
		return v
	case []any:
		ret := make([]map[string]any, len(v))
		for i := range v {
			item, ok := v[i].(map[string]any)
			require.True(t, ok, "fallback[%d] should be a map[string]any", i)
			ret[i] = item
		}
		return ret
	default:
		t.Fatalf("unexpected fallback type %T", value)
		return nil
	}
}

func newHarnessTestContext(t *testing.T, dag *core.DAG, step core.Step, envs ...string) context.Context {
	t.Helper()

	if dag == nil {
		dag = &core.DAG{Name: "harness-test", WorkingDir: t.TempDir()}
	}
	if dag.Name == "" {
		dag.Name = "harness-test"
	}
	if dag.WorkingDir == "" {
		dag.WorkingDir = t.TempDir()
	}

	ctx := runtime.NewContext(context.Background(), dag, "run-1", "", runtime.WithEnvVars(envs...))
	return runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))
}

type stubProvider struct {
	name string
}

func (p stubProvider) Name() string { return p.name }

func (p stubProvider) BinaryName() string { return p.name }

func (p stubProvider) BaseArgs(prompt string) []string { return []string{prompt} }
