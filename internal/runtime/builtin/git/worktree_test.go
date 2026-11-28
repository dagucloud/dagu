// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWorktreeRepositoryLockWaitIsCancelable(t *testing.T) {
	t.Parallel()

	commonDir := t.TempDir()
	holder := &executorImpl{}
	require.NoError(t, holder.lockRepository(context.Background(), commonDir))
	t.Cleanup(func() { require.NoError(t, holder.Close()) })

	waitCtx, cancel := context.WithCancel(context.Background())
	waiter := &executorImpl{}
	errCh := make(chan error, 1)
	go func() {
		errCh <- waiter.lockRepository(waitCtx, commonDir)
	}()

	select {
	case err := <-errCh:
		require.FailNow(t, "lock wait ended before cancellation", "error: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	cancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		require.FailNow(t, "lock wait did not observe cancellation")
	}
	require.Nil(t, waiter.repoLock)
}

// Git runs for a workflow, so it gets neither Git plumbing variables that
// redirect it nor any spelling of Dagu's own credentials.
func TestSanitizedGitEnvironment(t *testing.T) {
	for _, name := range []string{"GIT_DIR", "DAGU_PEER_TOKEN", "DAGU_WORKER_INVITE", "DAGU_WORKER.INVITE"} {
		t.Setenv(name, "hidden")
	}
	t.Setenv("DAGU_CONFORMANCE_SETTING", "visible")

	env := sanitizedGitEnvironment()
	require.Contains(t, env, "DAGU_CONFORMANCE_SETTING=visible")
	for _, entry := range env {
		require.NotContains(t, entry, "hidden")
	}
}
