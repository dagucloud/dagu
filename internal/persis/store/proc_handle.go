// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
)

// ProcHandle is a collection-backed process heartbeat handle.
type ProcHandle struct {
	store      *ProcStore
	groupName  string
	recordID   string
	legacyPath string
	createdAt  time.Time
	meta       exec.ProcMeta

	started  atomic.Bool
	canceled atomic.Bool
	cancel   context.CancelFunc
	mu       sync.Mutex
	wg       sync.WaitGroup
}

var _ exec.ProcHandle = (*ProcHandle)(nil)

// GetMeta returns this process metadata.
func (p *ProcHandle) GetMeta() exec.ProcMeta {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.meta
}

// Stop stops the heartbeat and removes the proc entry.
func (p *ProcHandle) Stop(ctx context.Context) error {
	if p.canceled.CompareAndSwap(false, true) {
		p.mu.Lock()
		cancel := p.cancel
		p.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		p.wg.Wait()
	}
	return p.cleanup(procCleanupContext(ctx))
}

func (p *ProcHandle) cleanup(ctx context.Context) error {
	var errs []error
	if err := p.store.col.Delete(ctx, p.recordID); err != nil && !errors.Is(err, persis.ErrNotFound) {
		errs = append(errs, err)
	}
	if p.legacyPath != "" {
		if err := removeLegacyProcFile(p.legacyPath); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func procCleanupContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	if ctx.Err() != nil {
		return context.WithoutCancel(ctx)
	}
	return ctx
}

func (p *ProcHandle) startHeartbeat(ctx context.Context) error {
	if !p.started.CompareAndSwap(false, true) {
		return fmt.Errorf("heartbeat already started")
	}
	if err := p.writeHeartbeat(ctx, time.Now().UTC()); err != nil {
		if cleanupErr := p.cleanup(context.WithoutCancel(ctx)); cleanupErr != nil {
			err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
		}
		p.started.Store(false)
		return err
	}

	hbCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()

	p.wg.Go(func() {
		defer func() {
			p.started.Store(false)
			if !p.canceled.Load() {
				if err := p.cleanup(context.WithoutCancel(ctx)); err != nil {
					logger.Error(ctx, "Failed to clean up proc heartbeat", tag.Error(err))
				}
			}
		}()

		ticker := time.NewTicker(p.store.heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case now := <-ticker.C:
				if err := p.writeHeartbeat(hbCtx, now.UTC()); err != nil {
					logger.Error(ctx, "Failed to write proc heartbeat", tag.Error(err))
				}
			}
		}
	})
	return nil
}

func (p *ProcHandle) writeHeartbeat(ctx context.Context, now time.Time) error {
	payload := procPayload{
		Version:         procStoreVersion,
		GroupName:       p.groupName,
		Meta:            p.meta,
		LastHeartbeatAt: now.Unix(),
		LegacyPath:      p.legacyPath,
	}
	data, enc, err := persis.Encode(payload)
	if err != nil {
		return err
	}
	expiresAt := now.Add(p.store.staleTime)
	if err := p.store.col.Put(ctx, &persis.Record{
		ID:        p.recordID,
		Data:      data,
		Encoding:  enc,
		CreatedAt: p.createdAt,
		UpdatedAt: now,
		ExpiresAt: &expiresAt,
	}); err != nil {
		return err
	}
	if p.legacyPath != "" {
		return writeLegacyProcFile(p.legacyPath, now.Unix(), p.meta)
	}
	return nil
}

type procPayload struct {
	Version         int           `json:"version"`
	GroupName       string        `json:"groupName"`
	Meta            exec.ProcMeta `json:"meta"`
	LastHeartbeatAt int64         `json:"lastHeartbeatAt"`
	LegacyPath      string        `json:"legacyPath,omitempty"`
}
