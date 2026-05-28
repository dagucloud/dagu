// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package audit provides a file-based implementation of the audit Store interface.
package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/service/audit"
)

const (
	// auditFileExtension is the file extension for audit log files.
	auditFileExtension = ".jsonl"
	// auditDirPermissions is the permission mode for the audit logs directory.
	auditDirPermissions = 0750
	// auditFilePermissions is the permission mode for audit log files.
	auditFilePermissions = 0640
	// dateFormat is the format used for daily log file names.
	dateFormat = "2006-01-02"
)

// Store implements audit.Store using the local filesystem.
// Audit entries are stored as JSON lines in daily log files.
type Store struct {
	baseDir string
	mu      sync.Mutex
	cleaner *cleaner
}

var _ audit.Store = (*Store)(nil)

// New creates a new file-based audit store.
// If retentionDays > 0, a background cleaner goroutine is started to purge
// expired audit log files. Use Close() to stop the cleaner on shutdown.
func New(baseDir string, retentionDays int) (*Store, error) {
	if baseDir == "" {
		return nil, errors.New("fileaudit: baseDir cannot be empty")
	}

	// Create base directory if it doesn't exist
	if err := os.MkdirAll(baseDir, auditDirPermissions); err != nil {
		return nil, fmt.Errorf("fileaudit: failed to create directory %s: %w", baseDir, err)
	}

	s := &Store{baseDir: baseDir}

	if retentionDays > 0 {
		s.cleaner = newCleaner(baseDir, retentionDays, s.Append)
	}

	return s, nil
}

// Close stops the background cleaner if running.
func (s *Store) Close() error {
	if s.cleaner != nil {
		s.cleaner.stop()
	}
	return nil
}

// auditFilePath returns the file path for a given date.
func (s *Store) auditFilePath(date time.Time) string {
	return filepath.Join(s.baseDir, date.Format(dateFormat)+auditFileExtension)
}

// Append adds a new audit entry to the store.
func (s *Store) Append(_ context.Context, entry *audit.Entry) error {
	if entry == nil {
		return errors.New("fileaudit: entry cannot be nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Determine file path based on entry timestamp
	filePath := s.auditFilePath(entry.Timestamp)

	// Marshal entry to JSON
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("fileaudit: failed to marshal entry: %w", err)
	}

	// Append to file
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, auditFilePermissions) //nolint:gosec // controlled path
	if err != nil {
		return fmt.Errorf("fileaudit: failed to open file %s: %w", filePath, err)
	}
	defer func() { _ = f.Close() }()

	// Write JSON line
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("fileaudit: failed to write entry: %w", err)
	}

	return nil
}

// Query retrieves audit entries matching the filter.
func (s *Store) Query(_ context.Context, filter audit.QueryFilter) (*audit.QueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Determine date range to scan
	startDate := filter.StartTime
	endDate := filter.EndTime

	// Default to last 7 days if no time range specified
	if startDate.IsZero() && endDate.IsZero() {
		endDate = time.Now().UTC()
		startDate = endDate.AddDate(0, 0, -7)
	} else if startDate.IsZero() {
		startDate = endDate.AddDate(0, 0, -7)
	} else if endDate.IsZero() {
		endDate = time.Now().UTC()
	}

	// Collect all matching entries
	var allEntries []*audit.Entry

	// Get day boundaries for file iteration, preserving the original timezone.
	// Using time.Date() instead of Truncate(24h) ensures correct day boundaries
	// for non-UTC timezones. Convert endDate to the same location as startDate
	// before extracting date components to ensure consistent day boundaries.
	// Individual entries are still filtered by exact timestamps in readEntriesFromFile.
	loc := startDate.Location()
	endDateInLoc := endDate.In(loc)
	fileStartDate := time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 0, 0, 0, 0, loc)
	fileEndDate := time.Date(endDateInLoc.Year(), endDateInLoc.Month(), endDateInLoc.Day(), 0, 0, 0, 0, loc)

	// Iterate through each day in the range
	for d := fileStartDate; !d.After(fileEndDate); d = d.AddDate(0, 0, 1) {
		filePath := s.auditFilePath(d)
		entries, err := s.readEntriesFromFile(filePath, filter)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // Skip missing files
			}
			return nil, err
		}
		allEntries = append(allEntries, entries...)
	}

	// Sort by timestamp descending (newest first)
	sort.Slice(allEntries, func(i, j int) bool {
		return allEntries[i].Timestamp.After(allEntries[j].Timestamp)
	})

	total := len(allEntries)

	// Apply pagination
	limit := filter.Limit
	if limit <= 0 {
		limit = 100 // Default limit
	}
	offset := max(filter.Offset, 0)

	// Apply offset
	if offset >= len(allEntries) {
		return &audit.QueryResult{
			Entries: []*audit.Entry{},
			Total:   total,
		}, nil
	}
	allEntries = allEntries[offset:]

	// Apply limit
	if limit < len(allEntries) {
		allEntries = allEntries[:limit]
	}

	return &audit.QueryResult{
		Entries: allEntries,
		Total:   total,
	}, nil
}

// readEntriesFromFile reads and filters entries from a single file.
func (s *Store) readEntriesFromFile(filePath string, filter audit.QueryFilter) ([]*audit.Entry, error) {
	f, err := os.Open(filePath) //nolint:gosec // controlled path
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []*audit.Entry
	scanner := bufio.NewScanner(f)
	fileutil.ConfigureScanner(scanner)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		entry := new(audit.Entry) // Allocate on heap to avoid pointer escaping issues
		if err := json.Unmarshal(scanner.Bytes(), entry); err != nil {
			slog.Warn("fileaudit: skipping malformed entry",
				slog.String("file", filePath),
				slog.Int("line", lineNum),
				slog.String("error", err.Error()))
			continue
		}

		// Apply filters
		if filter.Category != "" && entry.Category != filter.Category {
			continue
		}
		if filter.Action != "" && entry.Action != filter.Action {
			continue
		}
		if filter.Source != "" && entry.Source != filter.Source {
			continue
		}
		if filter.Surface != "" && entry.Surface != filter.Surface {
			continue
		}
		if filter.Result != "" && entry.Result != filter.Result {
			continue
		}
		if filter.CorrelationID != "" && entry.CorrelationID != filter.CorrelationID {
			continue
		}
		if filter.ResourceType != "" && entry.ResourceType != filter.ResourceType {
			continue
		}
		if filter.ResourceID != "" && entry.ResourceID != filter.ResourceID {
			continue
		}
		if filter.Workspace != "" && entry.Workspace != filter.Workspace {
			continue
		}
		if filter.CredentialID != "" && entry.CredentialID != filter.CredentialID {
			continue
		}
		if filter.CredentialType != "" && entry.CredentialType != filter.CredentialType {
			continue
		}
		if filter.MCPTool != "" && entry.MCPTool != filter.MCPTool {
			continue
		}
		if filter.UserID != "" && entry.UserID != filter.UserID {
			continue
		}
		if filter.IPAddress != "" && entry.IPAddress != filter.IPAddress {
			continue
		}
		if !filter.StartTime.IsZero() && entry.Timestamp.Before(filter.StartTime) {
			continue
		}
		if !filter.EndTime.IsZero() && entry.Timestamp.After(filter.EndTime) {
			continue
		}

		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("fileaudit: failed to read file %s: %w", filePath, err)
	}

	return entries, nil
}
