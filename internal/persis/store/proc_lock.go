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
	close(held.release)
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
	local     *sync.Mutex
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
	acquired := make(chan error, 1)
	go func() {
		err := col.WithLock(ctx, procLockKey(held.groupName), func() error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			acquired <- nil
			<-held.release
			return nil
		})
		select {
		case acquired <- err:
		default:
		}
		close(held.done)
	}()
	return <-acquired
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
