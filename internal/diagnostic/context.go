// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package diagnostic

import "context"

type contextKey struct{}

// WithSink returns a context that reports diagnostics to sink.
func WithSink(ctx context.Context, sink Sink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, sink)
}

// Report sends a diagnostic to the sink stored in ctx.
func Report(ctx context.Context, d Diagnostic) {
	sink, _ := ctx.Value(contextKey{}).(Sink)
	if sink == nil {
		return
	}
	sink.Report(d)
}
