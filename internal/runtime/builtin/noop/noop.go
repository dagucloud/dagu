// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package noop

import (
	"context"
	"io"
	"os"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

type noopExecutor struct{}

func newNoop(_ context.Context, _ core.Step) (executor.Executor, error) {
	return &noopExecutor{}, nil
}

func (*noopExecutor) SetStdout(_ io.Writer) {}

func (*noopExecutor) SetStderr(_ io.Writer) {}

func (*noopExecutor) Kill(_ os.Signal) error { return nil }

func (*noopExecutor) Run(_ context.Context) error { return nil }

func init() {
	executor.RegisterExecutor("noop", newNoop, nil, core.ExecutorCapabilities{})
}
