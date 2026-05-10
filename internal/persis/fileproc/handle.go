// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileproc

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// Error messages
var (
	ErrHeartbeatAlreadyStarted = fmt.Errorf("heartbeat already started")
)

var _ exec.ProcHandle = (*ProcHandle)(nil)

// ProcHandle is a struct that implements the ProcHandle interface.
type ProcHandle struct {
	fileName          string
	started           atomic.Bool
	canceled          atomic.Bool
	cancel            context.CancelFunc
	mu                sync.Mutex
	wg                sync.WaitGroup
	meta              exec.ProcMeta
	heartbeatInterval time.Duration
	syncInterval      time.Duration
}

// GetMeta implements models.ProcHandle.
func (p *ProcHandle) GetMeta() exec.ProcMeta {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.meta
}

// NewProcHandler creates a new instance of Proc with the specified file name.
func NewProcHandler(file string, meta exec.ProcMeta, heartbeatInterval, syncInterval time.Duration) *ProcHandle {
	if heartbeatInterval <= 0 {
		heartbeatInterval = 5 * time.Second
	}
	if syncInterval <= 0 {
		syncInterval = 10 * time.Second
	}
	return &ProcHandle{
		fileName:          file,
		meta:              meta,
		heartbeatInterval: heartbeatInterval,
		syncInterval:      syncInterval,
	}
}

// Stop implements models.Proc.
func (p *ProcHandle) Stop(_ context.Context) error {
	if !p.started.Load() {
		return fmt.Errorf("heartbeat not started")
	}
	if p.canceled.CompareAndSwap(false, true) {
		p.mu.Lock()
		cancel := p.cancel
		p.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		// Wait for the heartbeat goroutine to finish
		p.wg.Wait()
	}
	// Remove parent directory if it's empty
	_ = os.Remove(filepath.Dir(p.fileName))
	return nil
}

// startHeartbeat starts the heartbeat for the process.
func (p *ProcHandle) startHeartbeat(ctx context.Context) error {
	if !p.started.CompareAndSwap(false, true) {
		return ErrHeartbeatAlreadyStarted
	}

	if err := validateProcMeta(p.meta); err != nil {
		p.started.Store(false)
		return fmt.Errorf("invalid proc metadata: %w", err)
	}

	// Ensure the directory exists
	dir := filepath.Dir(p.fileName)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	heartbeatUnix := time.Now().Unix()
	fd, err := p.openInitializedProcFile(heartbeatUnix)
	if err != nil {
		p.started.Store(false)
		return err
	}

	hbCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()
	p.canceled.Store(false)

	p.wg.Add(1)

	// Start the heartbeat goroutine.
	// Writes timestamp every heartbeatInterval (default 5s), syncs every syncInterval (default 10s).
	// Stops when context is cancelled. Proc files are stale after staleThreshold (default 90s).
	go func() {
		// recovery
		defer func() {
			if r := recover(); r != nil {
				var err error
				switch v := r.(type) {
				case error:
					err = v
				case string:
					err = fmt.Errorf("%s", v)
				default:
					err = fmt.Errorf("%v", v)
				}
				logger.Error(ctx, "Heartbeat goroutine panicked",
					tag.Error(err))
			}
		}()

		defer func() {
			_ = fd.Close()
			// On Windows, another process (e.g., the scheduler) may hold a
			// transient read handle on the proc file, causing os.Remove to fail
			// with "access denied". Retry a few times with a brief sleep before
			// giving up and logging.
			const maxAttempts = 5
			for i := range maxAttempts {
				err := os.Remove(p.fileName)
				if err == nil || errors.Is(err, os.ErrNotExist) {
					break
				}
				if i < maxAttempts-1 {
					time.Sleep(50 * time.Millisecond)
					continue
				}
				logger.Error(ctx, "Failed to remove heartbeat file",
					tag.Error(err))
			}
			p.mu.Lock()
			p.cancel = nil
			p.mu.Unlock()
			p.started.Store(false)
			p.wg.Done()
		}()

		ticker := time.NewTicker(p.heartbeatInterval)
		flush := time.NewTicker(p.syncInterval)
		defer ticker.Stop()
		defer flush.Stop()

		for {
			select {
			case <-hbCtx.Done():
				_ = fd.Sync()
				return
			case <-ticker.C:
				heartbeatUnix := time.Now().Unix()
				if err := writeHeartbeat(fd, heartbeatUnix); err != nil {
					logger.Error(ctx, "Failed to write heartbeat", tag.Error(err))
				}
				// Self-healing: detect if file was unlinked externally
				if _, statErr := os.Stat(p.fileName); errors.Is(statErr, os.ErrNotExist) {
					if hbCtx.Err() != nil {
						return // Context cancelled — don't resurrect
					}
					logger.Warn(ctx, "Heartbeat file deleted externally, recreating",
						tag.File(p.fileName))
					_ = fd.Close()
					newFd, err := p.recreateFile(heartbeatUnix)
					if err != nil {
						logger.Error(ctx, "Failed to recreate heartbeat file",
							tag.File(p.fileName),
							tag.Error(err))
						return
					}
					fd = newFd
				}

			case <-flush.C:
				_ = fd.Sync()
			}
		}
	}()

	return nil
}

// recreateFile creates a new heartbeat file after the original was deleted externally.
func (p *ProcHandle) recreateFile(heartbeatUnix int64) (*os.File, error) {
	dir := filepath.Dir(p.fileName)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}
	fd, err := p.openInitializedProcFile(heartbeatUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to recreate file: %w", err)
	}
	return fd, nil
}

func writeHeartbeat(fd *os.File, heartbeatUnix int64) error {
	buf := make([]byte, heartbeatSize)
	binary.BigEndian.PutUint64(buf, uint64(heartbeatUnix)) //nolint:gosec // caller uses local unix timestamp
	_, err := fd.WriteAt(buf, 0)
	return err
}

func (p *ProcHandle) openInitializedProcFile(heartbeatUnix int64) (*os.File, error) {
	dir := filepath.Dir(p.fileName)
	// Keep the temporary name short. Proc file names already include encoded
	// run and attempt IDs, and deriving the temp name from them can exceed
	// Windows path limits even when the final proc path is still valid.
	tmpFile, err := createTempProcFile(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp proc file: %w", err)
	}
	tmpName := tmpFile.Name()
	cleanupTemp := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
	}

	if err := writeProcFile(tmpFile, heartbeatUnix, p.meta); err != nil {
		cleanupTemp()
		return nil, fmt.Errorf("failed to write proc file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		cleanupTemp()
		return nil, fmt.Errorf("failed to sync temp proc file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("failed to close temp proc file: %w", err)
	}
	if err := os.Rename(tmpName, p.fileName); err != nil {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("failed to publish proc file: %w", err)
	}

	fd, err := os.OpenFile(p.fileName, os.O_RDWR, 0600)
	if err != nil {
		_ = os.Remove(p.fileName)
		return nil, fmt.Errorf("failed to reopen proc file: %w", err)
	}
	return fd, nil
}

func createTempProcFile(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	tmpFile, err := os.CreateTemp(dir, ".proc-*.tmp")
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return tmpFile, err
	}
	// Another proc can remove an empty DAG proc directory between MkdirAll and
	// CreateTemp while a restart publishes its new heartbeat.
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, ".proc-*.tmp")
}
