// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileaudit

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/service/audit"
)

// appendFn is a function that appends an audit entry to the store.
type appendFn func(ctx context.Context, entry *audit.Entry) error

// cleaner handles periodic cleanup of expired audit log files.
type cleaner struct {
	baseDir       string
	retentionDays int
	appendFn      appendFn
	stopCh        chan struct{}
	stopOnce      sync.Once
}

// newCleaner creates and starts a cleaner that purges expired audit log files.
// It runs purgeExpiredFiles immediately, then every 24 hours.
// The appendFn is called to record cleanup events in the audit log.
func newCleaner(baseDir string, retentionDays int, fn appendFn) *cleaner {
	c := &cleaner{
		baseDir:       baseDir,
		retentionDays: retentionDays,
		appendFn:      fn,
		stopCh:        make(chan struct{}),
	}
	go c.run()
	return c
}

// run executes the cleanup loop.
func (c *cleaner) run() {
	c.purgeExpiredFiles()

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.purgeExpiredFiles()
		case <-c.stopCh:
			return
		}
	}
}

// stop stops the cleaner goroutine. Safe to call multiple times.
func (c *cleaner) stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
}

// purgeExpiredFiles removes audit log files whose date is strictly before
// the cutoff (today minus retentionDays, UTC). For example, retentionDays=7
// keeps today plus the 7 previous days (8 calendar days of files).
func (c *cleaner) purgeExpiredFiles() {
	if c.retentionDays <= 0 {
		return
	}

	entries, err := os.ReadDir(c.baseDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("fileaudit: failed to read audit directory for cleanup",
				slog.String("dir", c.baseDir),
				slog.String("error", err.Error()))
		}
		return
	}

	now := time.Now().UTC()
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, -c.retentionDays)

	var purgedDates []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, auditFileExtension) {
			continue
		}

		datePart := strings.TrimSuffix(name, auditFileExtension)
		fileDate, err := time.Parse(dateFormat, datePart)
		if err != nil {
			continue
		}

		if fileDate.Before(cutoff) {
			filePath := filepath.Join(c.baseDir, name)
			if err := fileutil.Remove(filePath); err != nil {
				slog.Warn("fileaudit: failed to remove expired audit file",
					slog.String("file", filePath),
					slog.String("error", err.Error()))
				continue
			}
			purgedDates = append(purgedDates, datePart)
		}
	}

	if len(purgedDates) > 0 {
		slog.Info("fileaudit: purged expired audit log files",
			slog.Int("removed", len(purgedDates)),
			slog.Int("retentionDays", c.retentionDays))

		if c.appendFn != nil {
			details, err := json.Marshal(map[string]any{
				"purged_from":    purgedDates[0],
				"purged_to":      purgedDates[len(purgedDates)-1],
				"files_removed":  len(purgedDates),
				"retention_days": c.retentionDays,
			})
			if err != nil {
				slog.Warn("fileaudit: failed to marshal cleanup audit details",
					slog.String("error", err.Error()))
				details = []byte("{}")
			}
			entry := audit.NewEntry(audit.CategorySystem, "audit_cleanup", "", "system").
				WithDetails(string(details))
			if err := c.appendFn(context.Background(), entry); err != nil {
				slog.Warn("fileaudit: failed to log cleanup audit entry",
					slog.String("error", err.Error()))
			}
		}
	}
}
