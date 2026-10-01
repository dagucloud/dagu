// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"os"
	"os/signal"

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

// notifyShutdownContext preserves the received signal as the cancellation
// cause so services can forward it even after their context becomes done.
// Once a signal arrives, a repeated SIGTERM is absorbed for the rest of the
// process; a repeated SIGINT takes its default action after stop.
func notifyShutdownContext(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	if signalctx.OSSignalsDisabled(parent) {
		return ctx, func() { cancel(nil) }
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, signals...)
	go func() {
		select {
		case sig := <-quit:
			signalctx.AbsorbRepeatedTerminate(parent)
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
