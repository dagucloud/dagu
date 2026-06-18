// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"os"

	"github.com/dagucloud/dagu/internal/core"
)

func AgentConfigFromBuiltinHarnessConfigForTest(cfg map[string]any) (*core.AgentStepConfig, error) {
	return agentConfigFromBuiltinHarnessConfig(cfg)
}

func BuiltinRunCanceledForTest(stopped bool, runCtxErr, parentCtxErr error) bool {
	return builtinRunCanceled(stopped, runCtxErr, parentCtxErr)
}

func NewTestExecutorForTest(step core.Step, prompt string, script string, workDir string) *harnessExecutor {
	return &harnessExecutor{
		step:    step,
		prompt:  prompt,
		script:  script,
		workDir: workDir,
	}
}

func NewTestExecutorWithProviderConfigsForTest(step core.Step, prompt string, script string, workDir string, configs ...providerConfig) *harnessExecutor {
	return &harnessExecutor{
		step:    step,
		configs: configs,
		prompt:  prompt,
		script:  script,
		workDir: workDir,
	}
}

func NewTestBuiltinProviderConfigForTest(name string) providerConfig {
	return providerConfig{name: name, builtin: true}
}

func NewTestProviderConfigForTest(name string, definition core.HarnessDefinition, flags map[string]any) providerConfig {
	return providerConfig{
		name:       name,
		definition: &definition,
		flags:      flags,
	}
}

func (e *harnessExecutor) RunOnceForTest(ctx context.Context, cfg providerConfig) (*os.File, error) {
	return e.runOnce(ctx, cfg)
}

func SharedContainerHarnessEnvForTest(userEnv map[string]string) []string {
	return sharedContainerHarnessEnv(userEnv)
}
