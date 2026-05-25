// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileserviceregistry

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
)

var quarantineSeq uint64

const (
	quarantineMarker    = ".gc"
	cleanupMinInterval  = 5 * time.Minute  // Minimum time between cleanups
	cleanupMaxInterval  = 60 * time.Minute // Maximum time between cleanups
	cleanupRetryBackoff = 200 * time.Millisecond
	cleanupRetryMax     = 2 * time.Second
	cleanupMaxRetries   = 5
)

// cleaner handles periodic cleanup of quarantined service registry files.
// Uses randomized intervals to avoid conflicts when many processes clean simultaneously.
type cleaner struct {
	baseDir     string
	serviceName exec.ServiceName
	stopCh      chan struct{}
	stopOnce    sync.Once
}

// newCleaner creates a new cleaner that runs with randomized intervals
// to avoid conflicts between multiple coordinator processes
func newCleaner(baseDir string, serviceName exec.ServiceName) *cleaner {
	c := &cleaner{
		baseDir:     baseDir,
		serviceName: serviceName,
		stopCh:      make(chan struct{}),
	}
	go c.run()
	return c
}

// run executes the cleanup loop with randomized intervals to avoid
// thundering herd when multiple coordinators clean simultaneously
func (c *cleaner) run() {
	ctx := context.Background()

	for {
		// Calculate random interval between min and max to spread out cleanup
		// across multiple coordinator processes
		interval := c.randomInterval()

		select {
		case <-time.After(interval):
			c.cleanupQuarantinedFiles(ctx)
		case <-c.stopCh:
			return
		}
	}
}

// randomInterval returns a random duration between min and max cleanup intervals
func (c *cleaner) randomInterval() time.Duration {
	// Use time.Now().UnixNano() for seed to get different values per instance
	// Note: Using math/rand is fine here - crypto randomness not needed
	maxNanos := int64(cleanupMaxInterval - cleanupMinInterval)
	randomNanos := time.Now().UnixNano() % maxNanos
	return cleanupMinInterval + time.Duration(randomNanos)
}

// stop stops the cleaner goroutine (safe to call multiple times)
func (c *cleaner) stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
}

// cleanupQuarantinedFiles finds and removes all quarantined files in the service directory
func (c *cleaner) cleanupQuarantinedFiles(ctx context.Context) {
	serviceDir := filepath.Join(c.baseDir, string(c.serviceName))

	entries, err := os.ReadDir(serviceDir)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn(ctx, "Failed to read service directory for cleanup",
				tag.Dir(serviceDir),
				tag.Error(err))
		}
		return
	}

	policy := backoff.NewExponentialBackoffPolicy(cleanupRetryBackoff)
	policy.MaxInterval = cleanupRetryMax
	policy.MaxRetries = cleanupMaxRetries

	for _, entry := range entries {
		if entry.IsDir() || !isQuarantinedFile(entry.Name()) {
			continue
		}

		filePath := filepath.Join(serviceDir, entry.Name())
		if err := c.removeWithRetry(ctx, filePath, policy); err != nil {
			logger.Warn(ctx, "Failed to cleanup quarantined file",
				tag.File(filePath),
				tag.Error(err))
		}
	}
}

// removeWithRetry attempts to remove a file with exponential backoff
func (c *cleaner) removeWithRetry(ctx context.Context, path string, policy *backoff.ExponentialBackoffPolicy) error {
	return backoff.Retry(ctx, func(_ context.Context) error {
		return removeInstanceFile(path)
	}, policy, nil)
}

// quarantine renames a stale file to mark it for deletion
type quarantine struct {
	staleTimeout time.Duration
}

// newQuarantine creates a new quarantine handler
func newQuarantine(staleTimeout time.Duration) *quarantine {
	return &quarantine{
		staleTimeout: staleTimeout,
	}
}

// markStaleFile renames a stale instance file to a quarantined name
// Returns true if the file was quarantined, false otherwise
func (q *quarantine) markStaleFile(ctx context.Context, path string, observedModTime time.Time) bool {
	// Check if file still exists and hasn't been modified by another process
	if !q.shouldQuarantine(ctx, path, observedModTime) {
		return false
	}

	quarantinePath := q.generateQuarantinePath(path)
	if err := fileutil.Rename(path, quarantinePath); err != nil {
		if !os.IsNotExist(err) {
			logger.Warn(ctx, "Failed to quarantine stale file",
				tag.File(path),
				tag.Error(err))
		}
		return false
	}

	return true
}

// shouldQuarantine checks if a file should be quarantined
func (q *quarantine) shouldQuarantine(ctx context.Context, path string, observedModTime time.Time) bool {
	currentInfo, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn(ctx, "Failed to stat file before quarantine",
				tag.File(path),
				tag.Error(err))
		}
		return false
	}

	// If file was recently touched by another process, don't quarantine
	if !currentInfo.ModTime().Equal(observedModTime) && time.Since(currentInfo.ModTime()) <= q.staleTimeout {
		return false
	}

	return true
}

// generateQuarantinePath creates a unique quarantine path for a file
func (q *quarantine) generateQuarantinePath(path string) string {
	return fmt.Sprintf("%s%s.%d.%d.%d", path, quarantineMarker, os.Getpid(), time.Now().UnixNano(), atomic.AddUint64(&quarantineSeq, 1))
}

// isQuarantinedFile checks if a filename indicates it's quarantined
func isQuarantinedFile(name string) bool {
	return strings.Contains(name, ".json"+quarantineMarker)
}
