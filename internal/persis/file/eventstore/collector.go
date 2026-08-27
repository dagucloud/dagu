// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eventstore

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
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
)

const (
	defaultDrainInterval = time.Second
	defaultCleanupEvery  = time.Hour
	defaultBatchSize     = 256
)

type CollectorOption func(*Collector)

func WithDrainInterval(interval time.Duration) CollectorOption {
	return func(c *Collector) {
		if interval > 0 {
			c.drainInterval = interval
		}
	}
}

func WithBatchSize(size int) CollectorOption {
	return func(c *Collector) {
		if size > 0 {
			c.batchSize = size
		}
	}
}

func WithNow(now func() time.Time) CollectorOption {
	return func(c *Collector) {
		if now != nil {
			c.now = now
		}
	}
}

type Collector struct {
	store         *Store
	retentionDays int
	drainInterval time.Duration
	cleanupEvery  time.Duration
	batchSize     int
	now           func() time.Time
}

type pendingInboxEvent struct {
	path  string
	raw   []byte
	event *eventstore.Event
}

func NewCollector(baseDir string, retentionDays int, opts ...CollectorOption) (*Collector, error) {
	store, err := New(baseDir)
	if err != nil {
		return nil, err
	}
	collector := &Collector{
		store:         store,
		retentionDays: retentionDays,
		drainInterval: defaultDrainInterval,
		cleanupEvery:  defaultCleanupEvery,
		batchSize:     defaultBatchSize,
		now:           time.Now,
	}
	for _, opt := range opts {
		opt(collector)
	}
	return collector, nil
}

func (c *Collector) Start(ctx context.Context) {
	c.cleanupExpired()
	if err := c.DrainOnce(ctx); err != nil {
		slog.Warn("fileeventstore: initial drain failed",
			slog.String("dir", c.store.baseDir),
			slog.String("error", err.Error()))
	}

	drainTicker := time.NewTicker(c.drainInterval)
	defer drainTicker.Stop()
	cleanupTicker := time.NewTicker(c.cleanupEvery)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-drainTicker.C:
			if err := c.DrainOnce(ctx); err != nil {
				slog.Warn("fileeventstore: drain failed",
					slog.String("dir", c.store.baseDir),
					slog.String("error", err.Error()))
			}
		case <-cleanupTicker.C:
			c.cleanupExpired()
		}
	}
}

func (c *Collector) DrainOnce(_ context.Context) error {
	entries, err := os.ReadDir(c.store.inboxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read inbox directory: %w", err)
	}

	var pendingByHour = make(map[string][]pendingInboxEvent)
	queuedIDs := make(map[string]struct{})
	processed := 0
	for _, entry := range entries {
		if processed >= c.batchSize {
			break
		}
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), inboxSuffix) {
			continue
		}
		processed++
		path := filepath.Join(c.store.inboxDir, entry.Name())
		pending, err := c.readPendingEvent(path)
		if err != nil {
			c.quarantine(path, entry.Name(), err)
			continue
		}
		if _, ok := queuedIDs[pending.event.ID]; ok {
			if err := fileutil.Remove(path); err != nil && !os.IsNotExist(err) {
				slog.Warn("fileeventstore: failed to delete duplicate inbox file",
					slog.String("file", path),
					slog.String("error", err.Error()))
			}
			continue
		}
		hour := pending.event.OccurredAt.UTC().Format(hourFormat)
		pendingByHour[hour] = append(pendingByHour[hour], pending)
		queuedIDs[pending.event.ID] = struct{}{}
	}

	if len(pendingByHour) == 0 {
		return nil
	}

	hours := make([]string, 0, len(pendingByHour))
	for hour := range pendingByHour {
		hours = append(hours, hour)
	}
	sort.Strings(hours)

	for _, hour := range hours {
		group := pendingByHour[hour]
		if err := c.appendGroup(hour, group); err != nil {
			return err
		}
	}
	return nil
}

func (c *Collector) appendGroup(hour string, group []pendingInboxEvent) error {
	logPath := filepath.Join(c.store.baseDir, logPrefix+hour+logSuffix)
	pendingIDs := make(map[string]struct{}, len(group))
	for _, item := range group {
		pendingIDs[item.event.ID] = struct{}{}
	}

	// Deduplication stays bounded by the current batch and its hourly log.
	committedIDs, err := c.findCommittedIDs(logPath, pendingIDs)
	if err != nil {
		return err
	}
	remaining := group[:0]
	for _, item := range group {
		if _, ok := committedIDs[item.event.ID]; ok {
			c.removeInboxFile(item.path)
			continue
		}
		remaining = append(remaining, item)
	}
	if len(remaining) == 0 {
		return nil
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePermissions) //nolint:gosec // controlled path
	if err != nil {
		return fmt.Errorf("open event log %s: %w", logPath, err)
	}
	defer func() { _ = f.Close() }()

	removePersistedInboxFiles := func() {
		ids, err := c.findCommittedIDs(logPath, pendingIDs)
		if err != nil {
			slog.Warn("fileeventstore: failed to check persisted events after append error",
				slog.String("file", logPath),
				slog.String("error", err.Error()))
			return
		}
		for _, item := range remaining {
			if _, ok := ids[item.event.ID]; ok {
				c.removeInboxFile(item.path)
			}
		}
	}

	writer := bufio.NewWriter(f)
	for _, item := range remaining {
		if _, err := writer.Write(item.raw); err != nil {
			removePersistedInboxFiles()
			return fmt.Errorf("append event log %s: %w", logPath, err)
		}
		if err := writer.WriteByte('\n'); err != nil {
			removePersistedInboxFiles()
			return fmt.Errorf("append newline %s: %w", logPath, err)
		}
	}
	if err := writer.Flush(); err != nil {
		removePersistedInboxFiles()
		return fmt.Errorf("flush event log %s: %w", logPath, err)
	}
	if err := f.Sync(); err != nil {
		removePersistedInboxFiles()
		return fmt.Errorf("sync event log %s: %w", logPath, err)
	}

	for _, item := range remaining {
		c.removeInboxFile(item.path)
	}
	return nil
}

func (c *Collector) findCommittedIDs(filePath string, pendingIDs map[string]struct{}) (map[string]struct{}, error) {
	committedIDs := make(map[string]struct{}, len(pendingIDs))
	f, err := os.Open(filePath) //nolint:gosec // controlled path
	if err != nil {
		if os.IsNotExist(err) {
			return committedIDs, nil
		}
		return nil, fmt.Errorf("open event log %s: %w", filePath, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	fileutil.ConfigureScanner(scanner)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		var event struct {
			eventstore.Event
			Data struct{} `json:"data"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			slog.Warn("fileeventstore: skipping malformed committed event while checking duplicates",
				slog.String("file", filePath),
				slog.Int("line", lineNum),
				slog.String("error", err.Error()))
			continue
		}
		if _, ok := pendingIDs[event.ID]; !ok {
			continue
		}
		committedIDs[event.ID] = struct{}{}
		if len(committedIDs) == len(pendingIDs) {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan event log %s: %w", filePath, err)
	}
	return committedIDs, nil
}

func (c *Collector) removeInboxFile(path string) {
	if err := fileutil.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Warn("fileeventstore: failed to delete processed inbox file",
			slog.String("file", path),
			slog.String("error", err.Error()))
	}
}

func (c *Collector) readPendingEvent(path string) (pendingInboxEvent, error) {
	data, err := fileutil.ReadFile(path)
	if err != nil {
		return pendingInboxEvent{}, err
	}
	// Draining needs event metadata; raw preserves the payload for persistence.
	var event struct {
		eventstore.Event
		Data struct{} `json:"data"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return pendingInboxEvent{}, err
	}
	event.Normalize()
	if err := event.Validate(); err != nil {
		return pendingInboxEvent{}, err
	}
	return pendingInboxEvent{
		path:  path,
		raw:   data,
		event: &event.Event,
	}, nil
}

func (c *Collector) quarantine(path, name string, parseErr error) {
	dest := filepath.Join(c.store.quarantineDir, name)
	if _, err := os.Stat(dest); err == nil {
		dest = filepath.Join(c.store.quarantineDir, fmt.Sprintf("%d-%s", c.now().UTC().UnixNano(), name))
	}
	if err := fileutil.Rename(path, dest); err != nil {
		slog.Warn("fileeventstore: failed to quarantine inbox file",
			slog.String("file", path),
			slog.String("error", err.Error()))
		return
	}
	slog.Warn("fileeventstore: quarantined malformed inbox file",
		slog.String("file", dest),
		slog.String("error", parseErr.Error()))
}

func (c *Collector) cleanupExpired() {
	if c.retentionDays <= 0 {
		return
	}

	now := c.now().UTC()
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, -c.retentionDays)
	baseEntries, err := os.ReadDir(c.store.baseDir)
	if err == nil {
		for _, entry := range baseEntries {
			if entry.IsDir() {
				continue
			}
			window, ok := parseCommittedFileWindow(filepath.Join(c.store.baseDir, entry.Name()), entry.Name())
			if !ok || window.end.After(cutoff) {
				continue
			}
			path := window.path
			if err := fileutil.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Warn("fileeventstore: failed to remove expired event log",
					slog.String("file", path),
					slog.String("error", err.Error()))
			}
		}
	} else if !os.IsNotExist(err) {
		slog.Warn("fileeventstore: failed to read event store directory for cleanup",
			slog.String("dir", c.store.baseDir),
			slog.String("error", err.Error()))
	}

	quarantineEntries, err := os.ReadDir(c.store.quarantineDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("fileeventstore: failed to read quarantine directory for cleanup",
				slog.String("dir", c.store.quarantineDir),
				slog.String("error", err.Error()))
		}
		return
	}
	for _, entry := range quarantineEntries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		modDay, ok := utcDay(info.ModTime())
		if !ok || !modDay.Before(cutoff) {
			continue
		}
		path := filepath.Join(c.store.quarantineDir, entry.Name())
		if err := fileutil.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("fileeventstore: failed to remove expired quarantined event file",
				slog.String("file", path),
				slog.String("error", err.Error()))
		}
	}
}
