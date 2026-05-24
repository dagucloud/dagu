// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
)

// Lock locks a process group until Unlock is called.
func (s *ProcStore) Lock(ctx context.Context, groupName string) error {
	held := &procHeldLock{
		groupName: groupName,
		release:   make(chan struct{}),
		done:      make(chan struct{}),
		released:  make(chan struct{}),
	}

	for {
		s.mu.Lock()
		existing := s.locks[groupName]
		if existing == nil {
			s.locks[groupName] = held
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()

		select {
		case <-existing.released:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if err := s.acquireLock(ctx, held); err != nil {
		s.mu.Lock()
		if s.locks[groupName] == held {
			delete(s.locks, groupName)
		}
		s.mu.Unlock()
		held.signalRelease()
		close(held.released)
		return err
	}
	return nil
}

// Unlock unlocks a process group.
func (s *ProcStore) Unlock(ctx context.Context, groupName string) {
	s.mu.Lock()
	held := s.locks[groupName]
	delete(s.locks, groupName)
	s.mu.Unlock()
	if held == nil {
		return
	}
	defer close(held.released)
	if held.local != nil {
		held.local.Unlock()
		return
	}
	held.signalRelease()
	select {
	case <-held.done:
	case <-ctx.Done():
		logger.Warn(ctx, "Timed out waiting for proc group unlock", tag.Name(groupName), tag.Error(ctx.Err()))
	}
}

type procHeldLock struct {
	groupName string
	release   chan struct{}
	done      chan struct{}
	released  chan struct{}
	once      sync.Once
	local     *sync.Mutex
}

func (h *procHeldLock) signalRelease() {
	h.once.Do(func() {
		close(h.release)
	})
}

type procLockCollection interface {
	WithLock(ctx context.Context, key string, fn func() error) error
}

func (s *ProcStore) acquireLock(ctx context.Context, held *procHeldLock) error {
	if col, ok := s.col.(procLockCollection); ok {
		return s.acquireBackendLock(ctx, col, held)
	}
	local := s.localLock(held.groupName)
	local.Lock()
	held.local = local
	close(held.done)
	return nil
}

func (s *ProcStore) acquireBackendLock(ctx context.Context, col procLockCollection, held *procHeldLock) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	acquired := make(chan error, 1)
	callbackStarted := make(chan struct{})
	done := make(chan struct{})
	lockCtx, cancelLock := context.WithCancel(context.WithoutCancel(ctx))

	go func() {
		select {
		case <-ctx.Done():
			select {
			case <-callbackStarted:
			default:
				cancelLock()
			}
		case <-callbackStarted:
		case <-done:
		}
	}()

	go func() {
		defer close(done)
		defer cancelLock()

		err := col.WithLock(lockCtx, procLockKey(held.groupName), func() error {
			close(callbackStarted)
			select {
			default:
			case <-held.release:
				return nil
			}
			select {
			case acquired <- nil:
			case <-held.release:
				return nil
			}
			<-held.release
			return nil
		})
		select {
		case acquired <- err:
		default:
		}
		close(held.done)
	}()
	select {
	case err := <-acquired:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *ProcStore) localLock(groupName string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, ok := s.localLocks[groupName]
	if !ok {
		lock = &sync.Mutex{}
		s.localLocks[groupName] = lock
	}
	return lock
}

func procLockKey(groupName string) string {
	return strings.TrimSuffix(procGroupPrefix(groupName), "/") + "/_lock"
}
