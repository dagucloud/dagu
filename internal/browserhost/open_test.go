// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"github.com/stretchr/testify/require"
)

// slotWait bounds how long a test waits for a slot that should come free.
const slotWait = 10 * time.Second

func TestTakeSlotWithoutLimitNeverWaits(t *testing.T) {
	dir := t.TempDir()
	for range 3 {
		slot, err := browserhost.TakeSlot(context.Background(), dir, 0, func() { t.Error("waited without a limit") })
		require.NoError(t, err)
		require.Nil(t, slot)
	}
}

func TestTakeSlotWaitsUntilAnOpenBrowserReleasesIt(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), slotWait)
	defer cancel()
	first, err := browserhost.TakeSlot(ctx, dir, 1, nil)
	require.NoError(t, err)

	waiting := make(chan struct{})
	taken := make(chan error, 1)
	go func() {
		slot, err := browserhost.TakeSlot(ctx, dir, 1, func() { close(waiting) })
		slot.Release()
		taken <- err
	}()
	select {
	case <-waiting:
	case <-ctx.Done():
		t.Fatal("the second browser did not wait for the first")
	}
	first.Release()
	require.NoError(t, <-taken)
}

// A browser left open after its step keeps its slot until its process exits.
func TestTakeSlotCountsABrowserUntilItsProcessExits(t *testing.T) {
	dir := t.TempDir()
	pid, _, exited := startHungBrowserProcess(t)
	slot, err := browserhost.TakeSlot(context.Background(), dir, 1, nil)
	require.NoError(t, err)
	startedAt, _ := procutil.StartTime(pid)
	require.NoError(t, slot.Hold(pid, startedAt))

	busy, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_, err = browserhost.TakeSlot(busy, dir, 1, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	process, err := os.FindProcess(pid)
	require.NoError(t, err)
	require.NoError(t, process.Kill())
	<-exited
	free, cancel := context.WithTimeout(context.Background(), slotWait)
	defer cancel()
	next, err := browserhost.TakeSlot(free, dir, 1, nil)
	require.NoError(t, err)
	next.Release()
}
