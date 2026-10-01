// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package signalctx

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

type osSignalsDisabledKey struct{}

// WithOSSignalsDisabled marks a context so in-process test helpers can skip
// subscribing to process-wide OS signals.
func WithOSSignalsDisabled(ctx context.Context) context.Context {
	return context.WithValue(ctx, osSignalsDisabledKey{}, true)
}

// OSSignalsDisabled reports whether OS signal subscriptions should be skipped.
func OSSignalsDisabled(ctx context.Context) bool {
	disabled, _ := ctx.Value(osSignalsDisabledKey{}).(bool)
	return disabled
}

var absorbTerminate sync.Once

// AbsorbRepeatedTerminate keeps SIGTERM handled for the rest of the process.
// Supervisors call it when a shutdown signal arrives, before unsubscribing the
// channel that received it, so a repeated SIGTERM cannot kill the process
// during graceful shutdown. Process managers can deliver SIGTERM more than
// once, for example to a whole process group and again through a relay such
// as sudo, and they escalate with SIGKILL when shutdown takes too long. A
// second SIGINT still forces an interactive exit. It does nothing when ctx
// disables OS signal subscriptions.
func AbsorbRepeatedTerminate(ctx context.Context) {
	if OSSignalsDisabled(ctx) {
		return
	}
	absorbTerminate.Do(func() {
		// Signals beyond the buffer are dropped, which is all absorbing needs.
		signal.Notify(make(chan os.Signal, 1), syscall.SIGTERM)
	})
}
