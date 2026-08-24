// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/stretchr/testify/mock"
)

var _ queue.QueueStore = (*MockQueueStore)(nil)

// MockQueueStore is a configurable queue store for tests.
type MockQueueStore struct {
	mock.Mock
}

func (m *MockQueueStore) Enqueue(ctx context.Context, name string, priority queue.QueuePriority, run ir.DAGRunRef) error {
	return m.Called(ctx, name, priority, run).Error(0)
}

func (m *MockQueueStore) DequeueByDAGRunID(ctx context.Context, name string, run ir.DAGRunRef) ([]queue.QueuedItemData, error) {
	args := m.Called(ctx, name, run)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]queue.QueuedItemData), args.Error(1)
}

func (m *MockQueueStore) DeleteByItemIDs(ctx context.Context, name string, itemIDs []string) (int, error) {
	args := m.Called(ctx, name, itemIDs)
	return args.Int(0), args.Error(1)
}

func (m *MockQueueStore) Len(ctx context.Context, name string) (int, error) {
	args := m.Called(ctx, name)
	return args.Int(0), args.Error(1)
}

func (m *MockQueueStore) List(ctx context.Context, name string) ([]queue.QueuedItemData, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]queue.QueuedItemData), args.Error(1)
}

func (m *MockQueueStore) GetByItemID(ctx context.Context, name, itemID string) (queue.QueuedItemData, error) {
	args := m.Called(ctx, name, itemID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(queue.QueuedItemData), args.Error(1)
}

func (m *MockQueueStore) ListCursor(ctx context.Context, name, cursor string, limit int) (pagination.CursorResult[queue.QueuedItemData], error) {
	args := m.Called(ctx, name, cursor, limit)
	if args.Get(0) == nil {
		return pagination.CursorResult[queue.QueuedItemData]{}, args.Error(1)
	}
	return args.Get(0).(pagination.CursorResult[queue.QueuedItemData]), args.Error(1)
}

func (m *MockQueueStore) All(ctx context.Context) ([]queue.QueuedItemData, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]queue.QueuedItemData), args.Error(1)
}

func (m *MockQueueStore) ListByDAGName(ctx context.Context, name, dagName string) ([]queue.QueuedItemData, error) {
	args := m.Called(ctx, name, dagName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]queue.QueuedItemData), args.Error(1)
}

func (m *MockQueueStore) QueueList(ctx context.Context) ([]string, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockQueueStore) QueueWatcher(ctx context.Context) queue.QueueWatcher {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(queue.QueueWatcher)
}
