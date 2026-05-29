// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestFile creates a file with the given name in the directory.
func createTestFile(t *testing.T, dir, name string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, name), []byte("test data\n"), 0640)
	require.NoError(t, err)
}

// newTestCleaner creates a cleaner for testing without starting the background goroutine.
func newTestCleaner(baseDir string, retentionDays int) *cleaner {
	return &cleaner{baseDir: baseDir, retentionDays: retentionDays, stopCh: make(chan struct{})}
}

// fileExists returns true if the named file exists in the directory.
func fileExists(t *testing.T, dir, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, name))
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatalf("unexpected error checking file %s: %v", name, err)
	return false
}

func TestPurgeExpiredFiles_DeletesExpired(t *testing.T) {
	dir := t.TempDir()

	// Create an expired file (30 days ago, well beyond 7-day retention)
	expiredDate := time.Now().UTC().AddDate(0, 0, -30).Format(dateFormat)
	createTestFile(t, dir, expiredDate+auditFileExtension)

	c := newTestCleaner(dir, 7)
	c.purgeExpiredFiles()

	assert.False(t, fileExists(t, dir, expiredDate+auditFileExtension),
		"expired file should have been deleted")
}

func TestPurgeExpiredFiles_PreservesRecent(t *testing.T) {
	dir := t.TempDir()

	// Create a recent file (1 day ago, within 7-day retention)
	recentDate := time.Now().UTC().AddDate(0, 0, -1).Format(dateFormat)
	createTestFile(t, dir, recentDate+auditFileExtension)

	// Create today's file
	todayDate := time.Now().UTC().Format(dateFormat)
	createTestFile(t, dir, todayDate+auditFileExtension)

	c := newTestCleaner(dir, 7)
	c.purgeExpiredFiles()

	assert.True(t, fileExists(t, dir, recentDate+auditFileExtension),
		"recent file should be preserved")
	assert.True(t, fileExists(t, dir, todayDate+auditFileExtension),
		"today's file should be preserved")
}

func TestPurgeExpiredFiles_MixedFiles(t *testing.T) {
	dir := t.TempDir()

	// Create a mix of expired and recent files
	expiredDate := time.Now().UTC().AddDate(0, 0, -20).Format(dateFormat)
	recentDate := time.Now().UTC().AddDate(0, 0, -3).Format(dateFormat)

	createTestFile(t, dir, expiredDate+auditFileExtension)
	createTestFile(t, dir, recentDate+auditFileExtension)

	c := newTestCleaner(dir, 7)
	c.purgeExpiredFiles()

	assert.False(t, fileExists(t, dir, expiredDate+auditFileExtension),
		"expired file should be deleted")
	assert.True(t, fileExists(t, dir, recentDate+auditFileExtension),
		"recent file should be preserved")
}

func TestPurgeExpiredFiles_IgnoresNonJsonl(t *testing.T) {
	dir := t.TempDir()

	// Create non-.jsonl files with old-looking dates
	createTestFile(t, dir, "2020-01-01.txt")
	createTestFile(t, dir, "2020-01-01.log")
	createTestFile(t, dir, "notes.jsonl") // .jsonl but not a date name
	createTestFile(t, dir, "README.md")

	c := newTestCleaner(dir, 7)
	c.purgeExpiredFiles()

	assert.True(t, fileExists(t, dir, "2020-01-01.txt"),
		"non-jsonl file should not be deleted")
	assert.True(t, fileExists(t, dir, "2020-01-01.log"),
		"non-jsonl file should not be deleted")
	assert.True(t, fileExists(t, dir, "notes.jsonl"),
		"jsonl file with unparsable date name should not be deleted")
	assert.True(t, fileExists(t, dir, "README.md"),
		"non-jsonl file should not be deleted")
}

func TestPurgeExpiredFiles_SkipsUnparsableNames(t *testing.T) {
	dir := t.TempDir()

	// Create .jsonl files with unparsable date names
	createTestFile(t, dir, "not-a-date.jsonl")
	createTestFile(t, dir, "2020-13-01.jsonl") // invalid month
	createTestFile(t, dir, "2020-01-32.jsonl") // invalid day
	createTestFile(t, dir, "random.jsonl")
	createTestFile(t, dir, "01-02-2020.jsonl") // wrong format

	c := newTestCleaner(dir, 7)
	c.purgeExpiredFiles()

	// All files should still exist — unparsable names are skipped
	assert.True(t, fileExists(t, dir, "not-a-date.jsonl"))
	assert.True(t, fileExists(t, dir, "2020-13-01.jsonl"))
	assert.True(t, fileExists(t, dir, "2020-01-32.jsonl"))
	assert.True(t, fileExists(t, dir, "random.jsonl"))
	assert.True(t, fileExists(t, dir, "01-02-2020.jsonl"))
}

func TestPurgeExpiredFiles_ZeroRetentionSkipsCleanup(t *testing.T) {
	dir := t.TempDir()

	// Create old files that would normally be deleted
	oldDate := time.Now().UTC().AddDate(0, 0, -30).Format(dateFormat)
	createTestFile(t, dir, oldDate+auditFileExtension)

	c := newTestCleaner(dir, 0)
	c.purgeExpiredFiles()

	assert.True(t, fileExists(t, dir, oldDate+auditFileExtension),
		"files should not be deleted when retentionDays is 0")
}

func TestCleaner_StopIsIdempotent(t *testing.T) {
	c := newTestCleaner(t.TempDir(), 7)

	// Calling stop multiple times should not panic
	assert.NotPanics(t, func() {
		c.stop()
		c.stop()
		c.stop()
	})
}

func TestPurgeExpiredFiles_NonexistentDirectory(t *testing.T) {
	c := newTestCleaner(filepath.Join(t.TempDir(), "nonexistent"), 7)

	// Should not panic on nonexistent directory
	assert.NotPanics(t, func() {
		c.purgeExpiredFiles()
	})
}

func TestPurgeExpiredFiles_LogsCleanupAuditEntry(t *testing.T) {
	dir := t.TempDir()

	// Create two expired files with known dates
	oldDate := time.Now().UTC().AddDate(0, 0, -30).Format(dateFormat)
	olderDate := time.Now().UTC().AddDate(0, 0, -31).Format(dateFormat)
	createTestFile(t, dir, oldDate+auditFileExtension)
	createTestFile(t, dir, olderDate+auditFileExtension)

	var mu sync.Mutex
	var entries []*audit.Entry
	fn := func(_ context.Context, entry *audit.Entry) error {
		mu.Lock()
		defer mu.Unlock()
		entries = append(entries, entry)
		return nil
	}

	c := &cleaner{baseDir: dir, retentionDays: 7, appendFn: fn, stopCh: make(chan struct{})}
	c.purgeExpiredFiles()

	require.Len(t, entries, 1)
	assert.Equal(t, audit.CategorySystem, entries[0].Category)
	assert.Equal(t, "audit_cleanup", entries[0].Action)
	assert.Equal(t, "", entries[0].UserID)
	assert.Equal(t, "system", entries[0].Username)
	assert.Contains(t, entries[0].Details, `"files_removed":2`)
	assert.Contains(t, entries[0].Details, `"retention_days":7`)
	assert.Contains(t, entries[0].Details, `"purged_from"`)
	assert.Contains(t, entries[0].Details, `"purged_to"`)
}

func TestPurgeExpiredFiles_NoEntryWhenNothingPurged(t *testing.T) {
	dir := t.TempDir()

	// Create only a recent file — nothing to purge
	recentDate := time.Now().UTC().AddDate(0, 0, -1).Format(dateFormat)
	createTestFile(t, dir, recentDate+auditFileExtension)

	called := false
	fn := func(_ context.Context, _ *audit.Entry) error {
		called = true
		return nil
	}

	c := &cleaner{baseDir: dir, retentionDays: 7, appendFn: fn, stopCh: make(chan struct{})}
	c.purgeExpiredFiles()

	assert.False(t, called, "appendFn should not be called when no files are purged")
}

func TestPurgeExpiredFiles_BoundaryDate(t *testing.T) {
	dir := t.TempDir()

	// Create a file exactly at the retention boundary
	// With retentionDays=7, cutoff = today - 7 days
	// A file dated exactly (today - 7 days) should NOT be deleted (it's not strictly before cutoff)
	boundaryDate := time.Now().UTC().AddDate(0, 0, -7).Format(dateFormat)
	// A file one day older than the boundary should be deleted
	expiredDate := time.Now().UTC().AddDate(0, 0, -8).Format(dateFormat)

	createTestFile(t, dir, boundaryDate+auditFileExtension)
	createTestFile(t, dir, expiredDate+auditFileExtension)

	c := newTestCleaner(dir, 7)
	c.purgeExpiredFiles()

	assert.True(t, fileExists(t, dir, boundaryDate+auditFileExtension),
		"file at exact boundary should be preserved")
	assert.False(t, fileExists(t, dir, expiredDate+auditFileExtension),
		"file one day past boundary should be deleted")
}
