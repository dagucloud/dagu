// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"sync"
	"time"
)

type pollingQueueWatcher struct {
	interval time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func newPollingQueueWatcher(interval time.Duration) *pollingQueueWatcher {
	if interval <= 0 {
		interval = queuePollInterval
	}
	return &pollingQueueWatcher{
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

func (w *pollingQueueWatcher) Start(ctx context.Context) (<-chan struct{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	notifyCh := make(chan struct{}, 1)
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stopCh:
				return
			case <-ticker.C:
				select {
				case notifyCh <- struct{}{}:
				default:
				}
			}
		}
	}()
	return notifyCh, nil
}

func (w *pollingQueueWatcher) Stop(ctx context.Context) {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
	case <-done:
	}
}
