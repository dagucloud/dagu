// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
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

// cleanupRecord is the details of an audit_cleanup entry. It is written
// before the files it lists are removed, so a reader that tracks byte offsets
// can tell whether a removed file held entries it never read. A file whose
// removal fails stays on disk and is listed again by the next run.
type cleanupRecord struct {
	ExpiredFrom   string        `json:"expired_from"`
	ExpiredTo     string        `json:"expired_to"`
	Files         []cleanupFile `json:"files"`
	RetentionDays int           `json:"retention_days"`
}

// cleanupFile is an expired audit file the cleaner is about to remove, with
// its size.
type cleanupFile struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// newCleaner creates and starts a cleaner that purges expired audit log files.
// It runs purgeExpiredFiles immediately, then every 24 hours.
// The appendFn records each cleanup in the audit log before its files are
// removed, and must return only once the entry is on disk.
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
// keeps today plus the 7 previous days (8 calendar days of files). It records
// the files and their sizes first, and removes nothing if the record fails.
func (c *cleaner) purgeExpiredFiles() {
	if c.retentionDays <= 0 {
		return
	}

	files := c.expiredFiles()
	if len(files) == 0 {
		return
	}
	if err := c.recordCleanup(files); err != nil {
		slog.Warn("fileaudit: kept expired audit files, the cleanup entry failed",
			slog.Int("files", len(files)),
			slog.String("error", err.Error()))
		return
	}

	removed := 0
	for _, file := range files {
		filePath := filepath.Join(c.baseDir, file.Name)
		if err := fileutil.Remove(filePath); err != nil {
			slog.Warn("fileaudit: failed to remove expired audit file",
				slog.String("file", filePath),
				slog.String("error", err.Error()))
			continue
		}
		removed++
	}
	slog.Info("fileaudit: purged expired audit log files",
		slog.Int("removed", removed),
		slog.Int("retentionDays", c.retentionDays))
}

// expiredFiles lists the audit files dated before the retention cutoff,
// oldest first, with their sizes.
func (c *cleaner) expiredFiles() []cleanupFile {
	entries, err := os.ReadDir(c.baseDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("fileaudit: failed to read audit directory for cleanup",
				slog.String("dir", c.baseDir),
				slog.String("error", err.Error()))
		}
		return nil
	}

	now := time.Now().UTC()
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, -c.retentionDays)

	var files []cleanupFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, auditFileExtension) {
			continue
		}

		fileDate, err := time.Parse(dateFormat, strings.TrimSuffix(name, auditFileExtension))
		if err != nil || !fileDate.Before(cutoff) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			// Its size cannot go in the record, so the file is not removed.
			continue
		}
		files = append(files, cleanupFile{Name: name, Bytes: info.Size()})
	}
	return files
}

// recordCleanup appends the audit_cleanup entry that lists files.
func (c *cleaner) recordCleanup(files []cleanupFile) error {
	details, err := json.Marshal(cleanupRecord{
		ExpiredFrom:   strings.TrimSuffix(files[0].Name, auditFileExtension),
		ExpiredTo:     strings.TrimSuffix(files[len(files)-1].Name, auditFileExtension),
		Files:         files,
		RetentionDays: c.retentionDays,
	})
	if err != nil {
		return err
	}
	entry := audit.NewEntry(audit.CategorySystem, "audit_cleanup", "", "system").
		WithDetails(string(details))
	return c.appendFn(context.Background(), entry)
}
