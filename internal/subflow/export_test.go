// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package subflow

import (
	"context"
	osexec "os/exec"

	"github.com/dagucloud/dagu/internal/runtime/executor"
)

func BuildStartCommandForTest(
	runner *LocalCLI,
	ctx context.Context,
	req executor.SubWorkflowRequest,
	workDir string,
	target string,
) (*osexec.Cmd, error) {
	return runner.buildStartCommand(ctx, req, workDir, target)
}

func BuildRetryCommandForTest(
	runner *LocalCLI,
	ctx context.Context,
	req executor.SubWorkflowRetryRequest,
) (*osexec.Cmd, error) {
	return runner.buildRetryCommand(ctx, req)
}
