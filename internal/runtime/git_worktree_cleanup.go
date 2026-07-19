// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/core/exec"
)

type gitWorktreeCleanupSinkKey struct{}

type gitWorktreeCleanupSink func(exec.GitWorktreeCleanup) error

func withGitWorktreeCleanupSink(ctx context.Context, sink gitWorktreeCleanupSink) context.Context {
	return context.WithValue(ctx, gitWorktreeCleanupSinkKey{}, sink)
}

// RegisterGitWorktreeCleanup attaches an owned worktree to the current DAG run.
func RegisterGitWorktreeCleanup(ctx context.Context, cleanup exec.GitWorktreeCleanup) error {
	sink, ok := ctx.Value(gitWorktreeCleanupSinkKey{}).(gitWorktreeCleanupSink)
	if !ok || sink == nil {
		return fmt.Errorf("git worktree cleanup is unavailable outside a DAG run")
	}
	return sink(cleanup)
}
