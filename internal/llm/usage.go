// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import "context"

// UsageRecord is the usage of one model call: the provider, the model the
// request named, and what the provider reported.
type UsageRecord struct {
	Provider string
	Model    string
	Usage    Usage
}

// UsageSink receives the usage of each model call made with a context that
// carries it.
type UsageSink interface {
	RecordLLMUsage(UsageRecord)
}

type usageSinkKey struct{}

// WithUsageSink returns a context whose model calls report their usage to
// sink. A nil sink hides any sink ctx carries, so calls made with the
// returned context report nowhere.
func WithUsageSink(ctx context.Context, sink UsageSink) context.Context {
	return context.WithValue(ctx, usageSinkKey{}, sink)
}

// RecordUsage reports a call's usage to the sink ctx carries, if any. A
// provider that calls its API apart from Chat and ChatStream reports its
// calls this way.
func RecordUsage(ctx context.Context, record UsageRecord) {
	if sink := usageSink(ctx); sink != nil {
		sink.RecordLLMUsage(record)
	}
}

func usageSink(ctx context.Context) UsageSink {
	sink, _ := ctx.Value(usageSinkKey{}).(UsageSink)
	return sink
}
