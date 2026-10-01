// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/dagucloud/dagu/v2/internal/cmn/signalctx"
)

type shutdownSignalError struct {
	signal os.Signal
}

func (e shutdownSignalError) Error() string {
	return e.signal.String() + " signal received"
}

func (e shutdownSignalError) Is(target error) bool {
	return target == context.Canceled
}

func (e shutdownSignalError) As(target any) bool {
	sig, ok := target.(*os.Signal)
	if ok {
		*sig = e.signal
	}
	return ok
}

// absorbRepeatedTerminate keeps SIGTERM handled until the returned function
// runs, so a repeated SIGTERM cannot kill a supervisor during graceful
// shutdown. Process managers can deliver SIGTERM more than once, for example to
// a whole process group and again through a relay such as sudo, and they
// escalate with SIGKILL when shutdown takes too long. A second SIGINT still
// forces an interactive exit.
func absorbRepeatedTerminate(ctx context.Context) func() {
	if signalctx.OSSignalsDisabled(ctx) {
		return func() {}
	}
	// Signals beyond the buffer are dropped, which is all absorbing needs.
	absorbed := make(chan os.Signal, 1)
	signal.Notify(absorbed, syscall.SIGTERM)
	return func() { signal.Stop(absorbed) }
}

// notifyShutdownContext preserves the received signal as the cancellation
// cause so services can forward it even after their context becomes done.
func notifyShutdownContext(parent context.Context, propagate bool, signals ...os.Signal) (context.Context, context.CancelFunc) {
	if !propagate {
		return signal.NotifyContext(parent, signals...)
	}
	ctx, cancel := context.WithCancelCause(parent)
	if signalctx.OSSignalsDisabled(parent) {
		return ctx, func() { cancel(nil) }
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, signals...)
	go func() {
		select {
		case sig := <-quit:
			signal.Stop(quit)
			cancel(shutdownSignalError{signal: sig})
		case <-ctx.Done():
			signal.Stop(quit)
		}
	}()
	return ctx, func() {
		cancel(nil)
		signal.Stop(quit)
	}
}
