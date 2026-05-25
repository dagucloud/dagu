// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileeventstore

import (
	"bufio"
	"context"
	"encoding/json"
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
)

var _ eventstore.DAGRunReader = (*Store)(nil)
var _ eventstore.NotificationReader = (*Store)(nil)

func (s *Store) DAGRunHeadCursor(_ context.Context) (eventstore.DAGRunCursor, error) {
	cursor := eventstore.DAGRunCursor{
		CommittedOffsets: make(map[string]int64),
	}

	files, err := s.listCommittedLogNames()
	if err != nil {
		return cursor, err
	}
	for _, name := range files {
		info, err := os.Stat(filepath.Join(s.baseDir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return cursor, fmt.Errorf("fileeventstore: stat %s: %w", name, err)
		}
		cursor.CommittedOffsets[name] = info.Size()
	}

	lastInbox, err := s.latestInboxFilename()
	if err != nil {
		return cursor, err
	}
	cursor.LastInboxFile = lastInbox
	return cursor.Normalize(), nil
}

func (s *Store) NotificationHeadCursor(ctx context.Context) (eventstore.NotificationCursor, error) {
	cursor, err := s.DAGRunHeadCursor(ctx)
	return eventstore.NotificationCursor(cursor), err
}

func (s *Store) ReadDAGRunEvents(_ context.Context, cursor eventstore.DAGRunCursor) ([]*eventstore.Event, eventstore.DAGRunCursor, error) {
	cursor = cursor.Normalize()
	nextCursor := eventstore.DAGRunCursor{
		LastInboxFile:    cursor.LastInboxFile,
		CommittedOffsets: make(map[string]int64),
	}

	eventsByID := make(map[string]*eventstore.Event)

	files, err := s.listCommittedLogNames()
	if err != nil {
		return nil, nextCursor, err
	}
	for _, name := range files {
		offset := cursor.CommittedOffsets[name]
		events, size, err := s.readCommittedEventsFromOffset(name, offset)
		if err != nil {
			return nil, nextCursor, err
		}
		nextCursor.CommittedOffsets[name] = size
		for _, event := range events {
			selectNewestDAGRunEvent(eventsByID, event)
		}
	}

	inboxFiles, err := s.listInboxFilenames()
	if err != nil {
		return nil, nextCursor, err
	}
	for _, name := range inboxFiles {
		if cursor.LastInboxFile != "" && name <= cursor.LastInboxFile {
			continue
		}
		event, err := s.readInboxDAGRunEvent(name)
		if err != nil {
			path := filepath.Join(s.inboxDir, name)
			slog.Warn("fileeventstore: quarantining unreadable inbox dag-run event file",
				slog.String("file", path),
				slog.String("cursor_last_inbox_file", cursor.LastInboxFile),
				slog.String("error", err.Error()))
			if quarantineErr := s.quarantineInboxFile(path, name, err); quarantineErr != nil {
				return nil, nextCursor, quarantineErr
			}
			continue
		}
		selectNewestDAGRunEvent(eventsByID, event)
		nextCursor.LastInboxFile = name
	}

	events := make([]*eventstore.Event, 0, len(eventsByID))
	for _, event := range eventsByID {
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool {
		if !events[i].RecordedAt.Equal(events[j].RecordedAt) {
			return events[i].RecordedAt.Before(events[j].RecordedAt)
		}
		return events[i].ID < events[j].ID
	})
	return events, nextCursor.Normalize(), nil
}

func (s *Store) ReadNotificationEvents(ctx context.Context, cursor eventstore.NotificationCursor) ([]*eventstore.Event, eventstore.NotificationCursor, error) {
	events, nextCursor, err := s.ReadDAGRunEvents(ctx, eventstore.DAGRunCursor(cursor))
	if err != nil {
		return nil, eventstore.NotificationCursor{}, err
	}
	filtered := make([]*eventstore.Event, 0, len(events))
	for _, event := range events {
		if event == nil || !eventstore.IsNotificationEventType(event.Kind, event.Type) {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered, eventstore.NotificationCursor(nextCursor), nil
}

func (s *Store) listCommittedLogNames() ([]string, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fileeventstore: read directory %s: %w", s.baseDir, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if _, ok := parseCommittedFileWindow(filepath.Join(s.baseDir, entry.Name()), entry.Name()); !ok {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func (s *Store) listInboxFilenames() ([]string, error) {
	entries, err := os.ReadDir(s.inboxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fileeventstore: read inbox directory %s: %w", s.inboxDir, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), inboxSuffix) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func (s *Store) latestInboxFilename() (string, error) {
	names, err := s.listInboxFilenames()
	if err != nil || len(names) == 0 {
		return "", err
	}
	return names[len(names)-1], nil
}

func (s *Store) readCommittedEventsFromOffset(name string, offset int64) ([]*eventstore.Event, int64, error) {
	path := filepath.Join(s.baseDir, name)
	file, err := os.Open(path) //nolint:gosec // controlled path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("fileeventstore: open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("fileeventstore: stat %s: %w", path, err)
	}
	size := info.Size()
	if offset < 0 || offset > size {
		offset = 0
	}
	if offset == size {
		return nil, size, nil
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, 0, fmt.Errorf("fileeventstore: seek %s: %w", path, err)
	}

	reader := io.LimitReader(file, size-offset)
	events, err := readDAGRunEventsFromReader(path, reader)
	if err != nil {
		return nil, 0, err
	}
	return events, size, nil
}

func (s *Store) readInboxDAGRunEvent(name string) (*eventstore.Event, error) {
	path := filepath.Join(s.inboxDir, name)
	data, err := fileutil.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fileeventstore: read inbox file %s: %w", path, err)
	}

	event := new(eventstore.Event)
	if err := json.Unmarshal(data, event); err != nil {
		return nil, fmt.Errorf("fileeventstore: decode inbox file %s: %w", path, err)
	}
	event.Normalize()
	if err := event.Validate(); err != nil {
		return nil, fmt.Errorf("fileeventstore: validate inbox file %s: %w", path, err)
	}
	if !eventstore.IsDAGRunEventType(event.Kind, event.Type) {
		return nil, nil
	}
	return event, nil
}

func (s *Store) quarantineInboxFile(path, name string, parseErr error) error {
	dest := filepath.Join(s.quarantineDir, name)
	if _, err := os.Stat(dest); err == nil {
		dest = filepath.Join(s.quarantineDir, fmt.Sprintf("%d-%s", time.Now().UTC().UnixNano(), name))
	}
	if err := fileutil.Rename(path, dest); err != nil {
		return fmt.Errorf("fileeventstore: quarantine inbox file %s: %w", path, err)
	}
	slog.Warn("fileeventstore: quarantined malformed inbox file",
		slog.String("file", dest),
		slog.String("error", parseErr.Error()))
	return nil
}

func readDAGRunEventsFromReader(path string, reader io.Reader) ([]*eventstore.Event, error) {
	scanner := bufio.NewScanner(reader)
	fileutil.ConfigureScanner(scanner)

	var events []*eventstore.Event
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		event := new(eventstore.Event)
		if err := json.Unmarshal(scanner.Bytes(), event); err != nil {
			slog.Warn("fileeventstore: skipping malformed dag-run event line",
				slog.String("file", path),
				slog.Int("line", lineNum),
				slog.String("error", err.Error()))
			continue
		}
		event.Normalize()
		if err := event.Validate(); err != nil {
			slog.Warn("fileeventstore: skipping invalid dag-run event line",
				slog.String("file", path),
				slog.Int("line", lineNum),
				slog.String("error", err.Error()))
			continue
		}
		if !eventstore.IsDAGRunEventType(event.Kind, event.Type) {
			continue
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("fileeventstore: scan %s: %w", path, err)
	}
	return events, nil
}

func selectNewestDAGRunEvent(eventsByID map[string]*eventstore.Event, event *eventstore.Event) {
	if event == nil {
		return
	}
	current, ok := eventsByID[event.ID]
	if !ok {
		eventsByID[event.ID] = event
		return
	}
	if event.RecordedAt.After(current.RecordedAt) {
		eventsByID[event.ID] = event
		return
	}
	if event.RecordedAt.Equal(current.RecordedAt) && event.OccurredAt.After(current.OccurredAt) {
		eventsByID[event.ID] = event
	}
}
