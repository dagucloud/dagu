// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"sync/atomic"
)

type dagRunListReadBatchContextKey struct{}

var nextDAGRunListReadBatchID atomic.Uint64

// DAGRunListReadBatch identifies list reads that may share the same storage work.
type DAGRunListReadBatch struct {
	id uint64
}

// NewDAGRunListReadBatch creates a new list-read batch.
func NewDAGRunListReadBatch() *DAGRunListReadBatch {
	return &DAGRunListReadBatch{id: nextDAGRunListReadBatchID.Add(1)}
}

// WithDAGRunListReadBatch associates a list-read batch with a context.
func WithDAGRunListReadBatch(ctx context.Context, batch *DAGRunListReadBatch) context.Context {
	return context.WithValue(ctx, dagRunListReadBatchContextKey{}, batch)
}

// DAGRunListReadBatchID returns the list-read batch ID associated with ctx.
func DAGRunListReadBatchID(ctx context.Context) (uint64, bool) {
	batch, ok := ctx.Value(dagRunListReadBatchContextKey{}).(*DAGRunListReadBatch)
	if !ok {
		return 0, false
	}
	return batch.id, true
}
