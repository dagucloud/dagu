// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
)

const (
	openDirName = "open"
	// openPollInterval is how often a browser waiting for a slot looks
	// again.
	openPollInterval = time.Second
)

// Slot is a place for one open browser among the few a host allows. Until
// the browser starts, the process opening it holds the slot; after Hold,
// the browser process does, so a browser kept open after its step still
// counts until it exits.
type Slot struct {
	path string
}

// slotHolder is the process a slot's file names.
type slotHolder struct {
	PID       int   `json:"pid"`
	StartedAt int64 `json:"startedAt,omitempty"`
}

// TakeSlot reserves a place for one more browser under browserDir, waiting
// while limit browsers are open there. A limit below one sets no limit and
// returns a nil Slot, whose methods do nothing. waiting runs once, when the
// browser first has to wait.
func TakeSlot(ctx context.Context, browserDir string, limit int, waiting func()) (*Slot, error) {
	if limit < 1 {
		return nil, nil
	}
	dir := filepath.Join(browserDir, openDirName)
	if err := os.MkdirAll(dir, recordDirMode); err != nil {
		return nil, fmt.Errorf("create open browser slots: %w", err)
	}
	lock := dirlock.New(dir, nil)
	for {
		slot, err := trySlot(ctx, lock, dir, limit)
		if slot != nil || err != nil {
			return slot, err
		}
		if waiting != nil {
			waiting()
			waiting = nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for another browser to close: %w", ctx.Err())
		case <-time.After(openPollInterval):
		}
	}
}

// trySlot takes a free slot, or returns nil when limit slots are held.
func trySlot(ctx context.Context, lock dirlock.DirLock, dir string, limit int) (*Slot, error) {
	if err := lock.Lock(ctx); err != nil {
		return nil, fmt.Errorf("lock open browser slots: %w", err)
	}
	defer func() { _ = lock.Unlock() }()
	held, err := heldSlots(dir)
	if err != nil || held >= limit {
		return nil, err
	}
	pid := os.Getpid()
	startedAt, _ := procutil.StartTime(pid)
	slot := &Slot{path: filepath.Join(dir, fmt.Sprintf("%d-%d%s", pid, time.Now().UnixNano(), recordFileExt))}
	if err := slot.write(slotHolder{PID: pid, StartedAt: startedAt}); err != nil {
		return nil, err
	}
	return slot, nil
}

// heldSlots counts the slots whose holder still runs and removes the rest.
func heldSlots(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read open browser slots: %w", err)
	}
	held := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != recordFileExt {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		var holder slotHolder
		data, err := os.ReadFile(path) //nolint:gosec // path is a slot file under the browser data directory.
		if err == nil && json.Unmarshal(data, &holder) == nil && processAlive(holder.PID, holder.StartedAt) {
			held++
			continue
		}
		_ = os.Remove(path)
	}
	return held, nil
}

// Hold hands the slot to the browser process, so it stays taken while the
// browser runs, after the process that opened it is gone.
func (s *Slot) Hold(pid int, startedAt int64) error {
	if s == nil {
		return nil
	}
	return s.write(slotHolder{PID: pid, StartedAt: startedAt})
}

// Release frees the slot of a browser that did not start, or that closed
// without holding the slot itself.
func (s *Slot) Release() {
	if s == nil {
		return
	}
	_ = os.Remove(s.path)
}

func (s *Slot) write(holder slotHolder) error {
	if err := fileutil.WriteJSONAtomic(s.path, holder, recordFileMode); err != nil {
		return fmt.Errorf("record open browser slot: %w", err)
	}
	return nil
}
