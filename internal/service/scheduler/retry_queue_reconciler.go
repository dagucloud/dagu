// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

const retryQueueReconcileInterval = 30 * time.Second

func (p *QueueProcessor) retryQueueReconcileLoop(ctx context.Context) {
	reconcile := func() {
		if err := p.reconcileRetryQueueIntents(ctx); err != nil {
			logger.Error(ctx, "Failed to reconcile retry queue intents", tag.Error(err))
		}
	}
	reconcile()

	ticker := time.NewTicker(retryQueueReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.quit:
			return
		case <-ticker.C:
			reconcile()
		}
	}
}

func (p *QueueProcessor) reconcileRetryQueueIntents(ctx context.Context) error {
	if p.dagRunStore == nil || p.queueStore == nil {
		return nil
	}
	statuses, err := p.dagRunStore.ListStatuses(
		ctx,
		exec.WithStatuses([]core.Status{core.Queued}),
		exec.WithAllHistory(),
		exec.WithoutLimit(),
	)
	if err != nil {
		return fmt.Errorf("list queued retry intents: %w", err)
	}

	var reconcileErrors []error
	for _, status := range statuses {
		if status == nil || status.RetryQueueKey == "" || status.RetryQueuePublished {
			continue
		}
		if err := exec.PublishRetryQueueIntent(ctx, p.dagRunStore, p.queueStore, status); err != nil && !errors.Is(err, exec.ErrRetryStaleLatest) {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile retry %s: %w", status.DAGRun(), err))
		}
	}
	return errors.Join(reconcileErrors...)
}
