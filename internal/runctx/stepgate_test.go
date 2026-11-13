// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runctx_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 092, "Isolation": an isolated untrusted step waits for the running
// steps of its run, then holds the run alone. A trusted step that comes while
// it waits or runs waits for its end.
func TestStepGate(t *testing.T) {
	t.Parallel()
	gate := runctx.NewStepGate()
	ctx := context.Background()

	leaveReader, err := gate.Read(ctx, nil)
	require.NoError(t, err)

	var writerWaited, readerWaited atomic.Bool
	writerIn := make(chan func())
	go func() {
		leave, err := gate.Write(ctx, func() { writerWaited.Store(true) })
		if err == nil {
			writerIn <- leave
		}
	}()
	require.Eventually(t, writerWaited.Load, time.Second, 5*time.Millisecond, "the writer waits for the running reader")

	readerIn := make(chan func())
	go func() {
		leave, err := gate.Read(ctx, func() { readerWaited.Store(true) })
		if err == nil {
			readerIn <- leave
		}
	}()
	require.Eventually(t, readerWaited.Load, time.Second, 5*time.Millisecond, "a new reader waits for the waiting writer")

	leaveReader()
	leaveWriter := <-writerIn
	select {
	case <-readerIn:
		t.Fatal("a reader runs while the writer holds the run")
	case <-time.After(50 * time.Millisecond):
	}
	leaveWriter()
	leave := <-readerIn
	leave()
	leave() // a second call does nothing

	cancelled, cancel := context.WithCancel(ctx)
	hold, err := gate.Read(ctx, nil)
	require.NoError(t, err)
	cancel()
	_, err = gate.Write(cancelled, nil)
	assert.ErrorIs(t, err, context.Canceled)
	hold()
	again, err := gate.Read(ctx, nil)
	require.NoError(t, err, "a cancelled writer does not block the readers")
	again()

	var nilGate *runctx.StepGate
	release, err := nilGate.Write(ctx, nil)
	require.NoError(t, err)
	release()
}
