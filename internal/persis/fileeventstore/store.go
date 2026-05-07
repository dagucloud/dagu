// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package fileeventstore provides a file-based implementation of the event store.
package fileeventstore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/google/uuid"
)

const (
	dirPermissions  = 0o750
	filePermissions = 0o640
	dayFormat       = "20060102"
	hourFormat      = "2006010215"
	logSuffix       = ".jsonl"
	inboxSuffix     = ".json"
	logPrefix       = "_"
)

type committedFileWindow struct {
	path  string
	start time.Time
	end   time.Time
}

type Store struct {
	baseDir       string
	inboxDir      string
	quarantineDir string
}

var _ eventstore.Store = (*Store)(nil)

func New(baseDir string) (*Store, error) {
	if baseDir == "" {
		return nil, errors.New("fileeventstore: baseDir cannot be empty")
	}
	store := &Store{
		baseDir:       baseDir,
		inboxDir:      filepath.Join(baseDir, "inbox"),
		quarantineDir: filepath.Join(baseDir, "quarantine"),
	}
	for _, dir := range []string{store.baseDir, store.inboxDir, store.quarantineDir} {
		if err := os.MkdirAll(dir, dirPermissions); err != nil {
			return nil, fmt.Errorf("fileeventstore: create directory %s: %w", dir, err)
		}
	}
	return store, nil
}

func (s *Store) Emit(_ context.Context, event *eventstore.Event) error {
	if event == nil {
		return errors.New("fileeventstore: event cannot be nil")
	}
	payload := *event
	recordedAt := payload.RecordedAt
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
		payload.RecordedAt = recordedAt
	}
	data, err := json.Marshal(&payload)
	if err != nil {
		return fmt.Errorf("fileeventstore: marshal event: %w", err)
	}
	name := fmt.Sprintf("%020d-%s%s", recordedAt.UnixNano(), uuid.Must(uuid.NewV7()).String(), inboxSuffix)
	path := filepath.Join(s.inboxDir, name)
	if err := fileutil.WriteFileAtomic(path, data, filePermissions); err != nil {
		return fmt.Errorf("fileeventstore: write inbox file: %w", err)
	}
	return nil
}

func (s *Store) Query(_ context.Context, filter eventstore.QueryFilter) (*eventstore.QueryResult, error) {
	files, err := s.listCommittedFiles(filter.StartTime, filter.EndTime)
	if err != nil {
		return nil, err
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}

	if usesCursorPagination(filter) {
		return s.queryWithCursor(files, filter, limit)
	}
	return s.queryWithOffset(files, filter, limit)
}

func usesCursorPagination(filter eventstore.QueryFilter) bool {
	return filter.PaginationMode == eventstore.QueryPaginationModeCursor || filter.Cursor != ""
}

func (s *Store) queryWithCursor(files []string, filter eventstore.QueryFilter, limit int) (*eventstore.QueryResult, error) {

	cursor, err := decodeQueryCursor(filter.Cursor, filter)
	if err != nil {
		return nil, err
	}

	startIndex := 0
	startOffset := int64(-1)
	if cursor.File != "" {
		found := false
		for i, file := range files {
			if filepath.Base(file) != cursor.File {
				continue
			}
			startIndex = i
			startOffset = cursor.Offset
			found = true
			break
		}
		if !found {
			return &eventstore.QueryResult{Entries: []*eventstore.Event{}}, nil
		}
	}

	matches := make([]scannedQueryEvent, 0, limit+1)
	for i := startIndex; i < len(files) && len(matches) < limit+1; i++ {
		offset := int64(-1)
		if i == startIndex && cursor.File != "" {
			offset = startOffset
		}
		loaded, err := s.readCommittedEventsReverse(files[i], filter, limit+1-len(matches), offset)
		if err != nil {
			return nil, err
		}
		matches = append(matches, loaded...)
	}

	entries := make([]*eventstore.Event, 0, min(limit, len(matches)))
	for i := 0; i < len(matches) && i < limit; i++ {
		entries = append(entries, matches[i].Event)
	}

	result := &eventstore.QueryResult{Entries: entries}
	if len(matches) > limit {
		nextCursor, err := encodeQueryCursor(filter, matches[limit-1].File, matches[limit-1].LineStart)
		if err != nil {
			return nil, err
		}
		result.NextCursor = nextCursor
	}

	return result, nil
}

func (s *Store) queryWithOffset(files []string, filter eventstore.QueryFilter, limit int) (*eventstore.QueryResult, error) {
	var entries []*eventstore.Event
	for _, file := range files {
		loaded, err := s.readCommittedEvents(file, filter)
		if err != nil {
			return nil, err
		}
		entries = append(entries, loaded...)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].OccurredAt.Equal(entries[j].OccurredAt) {
			if entries[i].RecordedAt.Equal(entries[j].RecordedAt) {
				return entries[i].ID > entries[j].ID
			}
			return entries[i].RecordedAt.After(entries[j].RecordedAt)
		}
		return entries[i].OccurredAt.After(entries[j].OccurredAt)
	})

	total := len(entries)
	offset := max(filter.Offset, 0)
	if offset >= total {
		return &eventstore.QueryResult{
			Entries: []*eventstore.Event{},
			Total:   &total,
		}, nil
	}

	entries = entries[offset:]
	if limit < len(entries) {
		entries = entries[:limit]
	}

	return &eventstore.QueryResult{
		Entries: entries,
		Total:   &total,
	}, nil
}

func (s *Store) listCommittedFiles(startTime, endTime time.Time) ([]string, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fileeventstore: read directory %s: %w", s.baseDir, err)
	}

	startTime = startTime.UTC()
	endTime = endTime.UTC()
	hasStart := !startTime.IsZero()
	hasEnd := !endTime.IsZero()
	var files []committedFileWindow
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		window, ok := parseCommittedFileWindow(filepath.Join(s.baseDir, entry.Name()), entry.Name())
		if !ok {
			continue
		}
		if hasStart && !window.end.After(startTime) {
			continue
		}
		if hasEnd && window.start.After(endTime) {
			continue
		}
		files = append(files, window)
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].start.Equal(files[j].start) {
			return files[i].path > files[j].path
		}
		return files[i].start.After(files[j].start)
	})

	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.path)
	}
	return paths, nil
}

type scannedQueryEvent struct {
	Event     *eventstore.Event
	File      string
	LineStart int64
}

func (s *Store) readCommittedEvents(filePath string, filter eventstore.QueryFilter) ([]*eventstore.Event, error) {
	f, err := os.Open(filePath) //nolint:gosec // controlled path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fileeventstore: open %s: %w", filePath, err)
	}
	defer func() { _ = f.Close() }()

	var entries []*eventstore.Event
	scanner := bufio.NewScanner(f)
	fileutil.ConfigureScanner(scanner)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		event := new(eventstore.Event)
		if err := json.Unmarshal(scanner.Bytes(), event); err != nil {
			slog.Warn("fileeventstore: skipping malformed event log line",
				slog.String("file", filePath),
				slog.Int("line", lineNum),
				slog.String("error", err.Error()))
			continue
		}
		event.Normalize()
		if err := event.Validate(); err != nil {
			slog.Warn("fileeventstore: skipping invalid event log line",
				slog.String("file", filePath),
				slog.Int("line", lineNum),
				slog.String("error", err.Error()))
			continue
		}
		if !matchesFilter(event, filter) {
			continue
		}
		entries = append(entries, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("fileeventstore: scan %s: %w", filePath, err)
	}
	return entries, nil
}

func (s *Store) readCommittedEventsReverse(filePath string, filter eventstore.QueryFilter, limit int, offset int64) ([]scannedQueryEvent, error) {
	if limit <= 0 {
		return nil, nil
	}

	f, err := os.Open(filePath) //nolint:gosec // controlled path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fileeventstore: open %s: %w", filePath, err)
	}
	defer func() { _ = f.Close() }()

	reader, err := newReverseLineReader(f, offset)
	if err != nil {
		return nil, fmt.Errorf("fileeventstore: open reverse reader for %s: %w", filePath, err)
	}

	entries := make([]scannedQueryEvent, 0, limit)
	for {
		line, lineStart, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if errors.Is(err, eventstore.ErrInvalidQueryCursor) {
				return nil, err
			}
			return nil, fmt.Errorf("fileeventstore: reverse scan %s: %w", filePath, err)
		}

		event := new(eventstore.Event)
		if err := json.Unmarshal(line, event); err != nil {
			slog.Warn("fileeventstore: skipping malformed event log line",
				slog.String("file", filePath),
				slog.String("error", err.Error()))
			continue
		}
		event.Normalize()
		if err := event.Validate(); err != nil {
			slog.Warn("fileeventstore: skipping invalid event log line",
				slog.String("file", filePath),
				slog.String("error", err.Error()))
			continue
		}
		if !matchesFilter(event, filter) {
			continue
		}
		entries = append(entries, scannedQueryEvent{
			Event:     event,
			File:      filepath.Base(filePath),
			LineStart: lineStart,
		})
		if len(entries) >= limit {
			break
		}
	}
	return entries, nil
}

func matchesFilter(event *eventstore.Event, filter eventstore.QueryFilter) bool {
	if event == nil {
		return false
	}
	if filter.Kind != "" && event.Kind != filter.Kind {
		return false
	}
	if filter.Type != "" && event.Type != filter.Type {
		return false
	}
	if filter.DAGName != "" && event.DAGName != filter.DAGName {
		return false
	}
	if filter.DAGRunID != "" && event.DAGRunID != filter.DAGRunID {
		return false
	}
	if filter.AttemptID != "" && event.AttemptID != filter.AttemptID {
		return false
	}
	if filter.SessionID != "" && event.SessionID != filter.SessionID {
		return false
	}
	if filter.UserID != "" && event.UserID != filter.UserID {
		return false
	}
	if filter.Model != "" && event.Model != filter.Model {
		return false
	}
	if filter.Status != "" && event.Status != filter.Status {
		return false
	}
	if !filter.StartTime.IsZero() && event.OccurredAt.Before(filter.StartTime) {
		return false
	}
	if !filter.EndTime.IsZero() && event.OccurredAt.After(filter.EndTime) {
		return false
	}
	return true
}

func parseCommittedFileWindow(path, name string) (committedFileWindow, bool) {
	if !strings.HasPrefix(name, logPrefix) || !strings.HasSuffix(name, logSuffix) {
		return committedFileWindow{}, false
	}
	datePart := strings.TrimSuffix(strings.TrimPrefix(name, logPrefix), logSuffix)
	switch len(datePart) {
	case len(hourFormat):
		hour, err := time.Parse(hourFormat, datePart)
		if err != nil {
			return committedFileWindow{}, false
		}
		hour = hour.UTC()
		return committedFileWindow{
			path:  path,
			start: hour,
			end:   hour.Add(time.Hour),
		}, true
	case len(dayFormat):
		day, err := time.Parse(dayFormat, datePart)
		if err != nil {
			return committedFileWindow{}, false
		}
		day = day.UTC()
		return committedFileWindow{
			path:  path,
			start: day,
			end:   day.Add(24 * time.Hour),
		}, true
	default:
		return committedFileWindow{}, false
	}
}

func utcDay(t time.Time) (time.Time, bool) {
	if t.IsZero() {
		return time.Time{}, false
	}
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC), true
}
