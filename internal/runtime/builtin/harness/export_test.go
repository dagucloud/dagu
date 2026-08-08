// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"os"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

func NewTestExecutorForTest(step ir.Step, prompt string, script string, workDir string) *harnessExecutor {
	return &harnessExecutor{
		step:    step,
		prompt:  prompt,
		script:  script,
		workDir: workDir,
	}
}

func NewTestExecutorWithProviderConfigsForTest(step ir.Step, prompt string, script string, workDir string, configs ...providerConfig) *harnessExecutor {
	return &harnessExecutor{
		step:    step,
		configs: configs,
		prompt:  prompt,
		script:  script,
		workDir: workDir,
	}
}

func NewTestProviderConfigForTest(name string, definition ir.HarnessDefinition, flags map[string]any) providerConfig {
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
