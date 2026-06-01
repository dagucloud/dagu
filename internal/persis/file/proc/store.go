// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package proc

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
)

const (
	procFileVersion   = 1
	procFilePrefix    = "proc_"
	procFileExt       = ".proc"
	procHeartbeatSize = 8
	procDateTimeUTC   = "20060102_150405"
	procFileTimeFmt   = procDateTimeUTC + "Z"
	procFileRetries   = 12
)

var (
	errInvalidProcFile       = errors.New("invalid proc file")
	procFileRegex            = regexp.MustCompile(`^proc_(\d{8}_\d{6}Z)_([0-9a-f]+)_([0-9a-f]+)\.proc$`)
	procLegacyFileRegex      = regexp.MustCompile(`^proc_(\d{8}_\d{6}Z)_([-a-zA-Z0-9_]+)\.proc$`)
	procSafeAttemptIDPattern = regexp.MustCompile(`^[-a-zA-Z0-9_]+$`)
)

type procDiskMeta struct {
	Version      int    `json:"version"`
	DAGName      string `json:"dag_name"`
	DAGRunID     string `json:"dag_run_id"`
	AttemptID    string `json:"attempt_id"`
	RootName     string `json:"root_name,omitempty"`
	RootDAGRunID string `json:"root_dag_run_id,omitempty"`
	StartedAt    int64  `json:"started_at"`
}

type procFileFormat int

const (
	procFileFormatCurrent procFileFormat = iota + 1
	procFileFormatLegacy
)

type procFileName struct {
	format    procFileFormat
	createdAt time.Time
	dagRunID  string
	attemptID string
}

type observedProcEntry struct {
	entry      exec.ProcEntry
	observedAt time.Time
}

var (
	_ exec.ProcStore  = (*Store)(nil)
	_ exec.ProcHandle = (*ProcHandle)(nil)
)

// Store reads and writes the file-backed .proc layout.
type Store struct {
	root              string
	staleTime         time.Duration
	heartbeatInterval time.Duration
	groupLocks        sync.Map
}

// StoreOption configures a Store.
type StoreOption func(*Store)

// WithStaleThreshold sets the duration after which a proc file is stale.
func WithStaleThreshold(d time.Duration) StoreOption {
	return func(s *Store) {
		if d > 0 {
			s.staleTime = d
		}
	}
}

// WithHeartbeatInterval sets the heartbeat write interval.
func WithHeartbeatInterval(d time.Duration) StoreOption {
	return func(s *Store) {
		if d > 0 {
			s.heartbeatInterval = d
		}
	}
}

// WithHeartbeatSyncInterval preserves the file proc store configuration surface.
func WithHeartbeatSyncInterval(_ time.Duration) StoreOption {
	return func(_ *Store) {}
}

// New creates a Store rooted at dir.
func New(root string, opts ...StoreOption) *Store {
	s := &Store{
		root:              root,
		staleTime:         90 * time.Second,
		heartbeatInterval: 5 * time.Second,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ProcHandle is a file-backed process heartbeat handle.
type ProcHandle struct {
	fileName          string
	meta              exec.ProcMeta
	heartbeatInterval time.Duration
	started           atomic.Bool
	canceled          atomic.Bool
	cancel            context.CancelFunc
	mu                sync.Mutex
	wg                sync.WaitGroup
}

// GetMeta returns this process metadata.
func (p *ProcHandle) GetMeta() exec.ProcMeta {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.meta
}

// Stop stops the heartbeat and removes the proc file.
func (p *ProcHandle) Stop(_ context.Context) error {
	if p.canceled.CompareAndSwap(false, true) {
		p.mu.Lock()
		cancel := p.cancel
		p.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		p.wg.Wait()
	}
	return removeProcFile(p.fileName)
}

func (p *ProcHandle) startHeartbeat(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !p.started.CompareAndSwap(false, true) {
		return fmt.Errorf("heartbeat already started")
	}
	if err := p.writeHeartbeat(time.Now().UTC()); err != nil {
		p.started.Store(false)
		return err
	}

	hbCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()

	p.wg.Go(func() {
		defer func() {
			p.started.Store(false)
			if !p.canceled.Load() {
				if err := removeProcFile(p.fileName); err != nil {
					logger.Error(ctx, "Failed to remove proc heartbeat file", tag.File(p.fileName), tag.Error(err))
				}
			}
		}()

		ticker := time.NewTicker(p.heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case now := <-ticker.C:
				if err := p.writeHeartbeat(now.UTC()); err != nil {
					logger.Error(ctx, "Failed to write proc heartbeat", tag.File(p.fileName), tag.Error(err))
				}
			}
		}
	})
	return nil
}

func (p *ProcHandle) writeHeartbeat(now time.Time) error {
	return writeProcFile(p.fileName, now.Unix(), p.meta)
}

// Lock locks a process group.
func (s *Store) Lock(ctx context.Context, groupName string) error {
	basePolicy := backoff.NewExponentialBackoffPolicy(500 * time.Millisecond)
	basePolicy.BackoffFactor = 2.0
	basePolicy.MaxInterval = time.Minute
	basePolicy.MaxRetries = 10

	policy := backoff.WithJitter(basePolicy, backoff.Jitter)
	return backoff.Retry(ctx, func(_ context.Context) error {
		return s.groupLock(groupName).TryLock()
	}, policy, func(_ error) bool {
		return ctx.Err() == nil
	})
}

// Unlock unlocks a process group.
func (s *Store) Unlock(ctx context.Context, groupName string) {
	if err := s.groupLock(groupName).Unlock(); err != nil {
		logger.Error(ctx, "Failed to unlock the proc group", tag.Error(err))
	}
}

// Acquire creates and starts a proc heartbeat.
func (s *Store) Acquire(ctx context.Context, groupName string, meta exec.ProcMeta) (exec.ProcHandle, error) {
	if meta.StartedAt <= 0 {
		meta.StartedAt = time.Now().UTC().Unix()
	}
	if err := validateProcMeta(meta); err != nil {
		return nil, err
	}
	handle := &ProcHandle{
		fileName:          s.filePath(groupName, meta, time.Now().UTC()),
		meta:              meta,
		heartbeatInterval: s.heartbeatInterval,
	}
	if err := handle.startHeartbeat(ctx); err != nil {
		return nil, err
	}
	return handle, nil
}

// CountAlive returns the number of fresh DAG runs in a group.
func (s *Store) CountAlive(ctx context.Context, groupName string) (int, error) {
	entries, err := s.ListEntries(ctx, groupName)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if entry.Fresh {
			seen[entry.Meta.DAGRun().String()] = struct{}{}
		}
	}
	return len(seen), nil
}

// CountAliveByDAGName returns the number of fresh DAG runs for dagName in a group.
func (s *Store) CountAliveByDAGName(ctx context.Context, groupName, dagName string) (int, error) {
	entries, err := s.ListEntries(ctx, groupName)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if entry.Fresh && entry.Meta.Name == dagName {
			seen[entry.Meta.DAGRun().String()] = struct{}{}
		}
	}
	return len(seen), nil
}

// IsRunAlive reports whether dagRun has a fresh proc entry in groupName.
func (s *Store) IsRunAlive(ctx context.Context, groupName string, dagRun exec.DAGRunRef) (bool, error) {
	entries, err := s.ListEntries(ctx, groupName)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Fresh && entry.Meta.Name == dagRun.Name && entry.Meta.DAGRunID == dagRun.ID {
			return true, nil
		}
	}
	return false, nil
}

// IsAttemptAlive reports whether a specific attempt has a fresh proc entry.
func (s *Store) IsAttemptAlive(ctx context.Context, groupName string, dagRun exec.DAGRunRef, attemptID string) (bool, error) {
	entries, err := s.ListEntries(ctx, groupName)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Fresh && entry.Meta.Name == dagRun.Name && entry.Meta.DAGRunID == dagRun.ID && entry.Meta.AttemptID == attemptID {
			return true, nil
		}
	}
	return false, nil
}

// ListAlive returns fresh DAG runs in a group.
func (s *Store) ListAlive(ctx context.Context, groupName string) ([]exec.DAGRunRef, error) {
	entries, err := s.ListEntries(ctx, groupName)
	if err != nil {
		return nil, err
	}
	return freshRefs(entries), nil
}

// LatestFreshEntryByDAGName returns the newest fresh proc entry for dagName.
func (s *Store) LatestFreshEntryByDAGName(ctx context.Context, groupName, dagName string) (*exec.ProcEntry, error) {
	entries, err := s.ListEntries(ctx, groupName)
	if err != nil {
		return nil, err
	}
	var freshest *exec.ProcEntry
	for i := range entries {
		entry := entries[i]
		if !entry.Fresh || entry.Meta.Name != dagName {
			continue
		}
		if freshest == nil ||
			entry.Meta.StartedAt > freshest.Meta.StartedAt ||
			(entry.Meta.StartedAt == freshest.Meta.StartedAt && entry.LastHeartbeatAt > freshest.LastHeartbeatAt) {
			copy := entry
			freshest = &copy
		}
	}
	return freshest, nil
}

// ListAllAlive returns all fresh DAG runs grouped by process group.
func (s *Store) ListAllAlive(ctx context.Context) (map[string][]exec.DAGRunRef, error) {
	entries, err := s.ListAllEntries(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]exec.DAGRunRef)
	seen := make(map[string]map[string]struct{})
	for _, entry := range entries {
		if !entry.Fresh {
			continue
		}
		if _, ok := seen[entry.GroupName]; !ok {
			seen[entry.GroupName] = make(map[string]struct{})
		}
		ref := entry.Meta.DAGRun()
		key := ref.String()
		if _, ok := seen[entry.GroupName][key]; ok {
			continue
		}
		seen[entry.GroupName][key] = struct{}{}
		result[entry.GroupName] = append(result[entry.GroupName], ref)
	}
	for groupName := range result {
		sort.Slice(result[groupName], func(i, j int) bool {
			if result[groupName][i].Name == result[groupName][j].Name {
				return result[groupName][i].ID < result[groupName][j].ID
			}
			return result[groupName][i].Name < result[groupName][j].Name
		})
	}
	return result, nil
}

// Validate fails if any proc entry cannot be decoded.
func (s *Store) Validate(ctx context.Context) error {
	_, err := s.ListAllEntries(ctx)
	if err != nil {
		return fmt.Errorf("validate proc store: %w", err)
	}
	return nil
}

func (s *Store) groupLock(groupName string) dirlock.DirLock {
	baseDir := filepath.Join(s.root, groupName)
	if lock, ok := s.groupLocks.Load(baseDir); ok {
		return lock.(dirlock.DirLock)
	}
	lock := dirlock.New(baseDir, &dirlock.LockOptions{
		StaleThreshold: 5 * time.Second,
		RetryInterval:  100 * time.Millisecond,
	})
	actual, _ := s.groupLocks.LoadOrStore(baseDir, lock)
	return actual.(dirlock.DirLock)
}

func validateProcMeta(meta exec.ProcMeta) error {
	if meta.Name == "" {
		return fmt.Errorf("proc meta name is required")
	}
	if err := exec.ValidateDAGRunID(meta.DAGRunID); err != nil {
		return fmt.Errorf("invalid proc meta dag run id: %w", err)
	}
	if meta.AttemptID == "" {
		return fmt.Errorf("proc meta attempt id is required")
	}
	if !procSafeAttemptIDPattern.MatchString(meta.AttemptID) {
		return fmt.Errorf("proc meta attempt id must only contain alphanumeric characters, dashes, and underscores")
	}
	if meta.StartedAt <= 0 {
		return fmt.Errorf("proc meta started at must be > 0")
	}
	if (meta.RootName == "") != (meta.RootDAGRunID == "") {
		return fmt.Errorf("proc meta root name and root dag run id must both be set or both be empty")
	}
	if meta.RootDAGRunID != "" {
		if err := exec.ValidateDAGRunID(meta.RootDAGRunID); err != nil {
			return fmt.Errorf("invalid proc meta root dag run id: %w", err)
		}
	}
	return nil
}

func procRecordName(meta exec.ProcMeta, t time.Time) string {
	return fmt.Sprintf("%s%sZ_%s_%s",
		procFilePrefix,
		t.UTC().Format(procDateTimeUTC),
		hex.EncodeToString([]byte(meta.DAGRunID)),
		hex.EncodeToString([]byte(meta.AttemptID)),
	)
}

func (s *Store) filePath(groupName string, meta exec.ProcMeta, t time.Time) string {
	return filepath.Join(s.root, groupName, meta.Name, procRecordName(meta, t)+procFileExt)
}

func writeProcFile(path string, heartbeatUnix int64, meta exec.ProcMeta) error {
	if err := validateProcMeta(meta); err != nil {
		return err
	}
	metaBytes, err := json.Marshal(procDiskMeta{
		Version:      procFileVersion,
		DAGName:      meta.Name,
		DAGRunID:     meta.DAGRunID,
		AttemptID:    meta.AttemptID,
		RootName:     meta.RootName,
		RootDAGRunID: meta.RootDAGRunID,
		StartedAt:    meta.StartedAt,
	})
	if err != nil {
		return err
	}
	data := make([]byte, procHeartbeatSize+len(metaBytes))
	binary.BigEndian.PutUint64(data[:procHeartbeatSize], uint64(heartbeatUnix)) //nolint:gosec // heartbeat unix time is validated by caller.
	copy(data[procHeartbeatSize:], metaBytes)
	return writeProcFileAtomic(path, data)
}

func writeProcFileAtomic(path string, data []byte) error {
	return writeProcFileAtomicWithCreateTemp(path, data, os.CreateTemp)
}

type createProcTempFileFunc func(dir, pattern string) (*os.File, error)

func writeProcFileAtomicWithCreateTemp(path string, data []byte, createTemp createProcTempFileFunc) error {
	var lastErr error
	for attempt := range procFileRetries {
		if err := writeProcFileAtomicOnce(path, data, createTemp); err != nil {
			if !errors.Is(err, os.ErrNotExist) && !fileutil.IsTransientFileError(err) {
				return err
			}
			lastErr = err
			time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
			continue
		}
		return nil
	}
	return lastErr
}

func writeProcFileAtomicOnce(path string, data []byte, createTemp createProcTempFileFunc) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmpFile, err := createTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return err
	}
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return err
	}
	if err := fileutil.ReplaceFile(tmpPath, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

func removeProcFile(path string) error {
	err := fileutil.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		removeEmptyProcDirs(filepath.Dir(path))
		return nil
	}
	return err
}

func removeEmptyProcDirs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) > 0 {
		return
	}
	_ = fileutil.Remove(dir)
}

// ListEntries returns proc entries for a group.
func (s *Store) ListEntries(_ context.Context, groupName string) ([]exec.ProcEntry, error) {
	groupDir := filepath.Join(s.root, groupName)
	if _, err := os.Stat(groupDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	files, err := procFilesInGroup(groupDir)
	if err != nil {
		return nil, err
	}
	return s.entriesFromFiles(groupName, files)
}

// ListAllEntries returns all proc entries under the store root.
func (s *Store) ListAllEntries(_ context.Context) ([]exec.ProcEntry, error) {
	dirEntries, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var entries []exec.ProcEntry
	for _, entry := range dirEntries {
		if !entry.IsDir() {
			continue
		}
		groupName := entry.Name()
		files, err := procFilesInGroup(filepath.Join(s.root, groupName))
		if err != nil {
			return nil, err
		}
		groupEntries, err := s.entriesFromFiles(groupName, files)
		if err != nil {
			return nil, err
		}
		entries = append(entries, groupEntries...)
	}
	return entries, nil
}

// LatestHeartbeat returns the latest heartbeat for dagRun.
func (s *Store) LatestHeartbeat(_ context.Context, groupName string, dagRun exec.DAGRunRef) (*exec.ProcHeartbeat, error) {
	groupDir := filepath.Join(s.root, groupName)
	if _, err := os.Stat(groupDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	files, err := procFilesInGroup(groupDir)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var latest *exec.ProcHeartbeat
	for _, file := range files {
		observed, err := readProcEntryObservedWithRetry(file, groupName, s.staleTime, now)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, errInvalidProcFile) {
				// Heartbeat observation should not fail because an unrelated
				// proc file is concurrently removed or corrupt.
				continue
			}
			return nil, err
		}
		entry := observed.entry
		if entry.Meta.Name != dagRun.Name || entry.Meta.DAGRunID != dagRun.ID {
			continue
		}
		heartbeat := procHeartbeatFromEntry(entry, observed.observedAt)
		if latest == nil || procHeartbeatPreferred(heartbeat, *latest) {
			latest = &heartbeat
		}
	}
	return latest, nil
}

func procFilesInGroup(groupDir string) ([]string, error) {
	dagEntries, err := os.ReadDir(groupDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, dagEntry := range dagEntries {
		if !dagEntry.IsDir() || dagEntry.Name() == "" || dagEntry.Name()[0] == '.' {
			continue
		}
		procEntries, err := os.ReadDir(filepath.Join(groupDir, dagEntry.Name()))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, procEntry := range procEntries {
			if procEntry.IsDir() || filepath.Ext(procEntry.Name()) != procFileExt {
				continue
			}
			files = append(files, filepath.Join(groupDir, dagEntry.Name(), procEntry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func (s *Store) entriesFromFiles(groupName string, files []string) ([]exec.ProcEntry, error) {
	now := time.Now().UTC()
	entries := make([]exec.ProcEntry, 0, len(files))
	for _, file := range files {
		entry, err := readProcEntryWithRetry(file, groupName, s.staleTime, now)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// RemoveIfStale deletes entry when the on-disk proc file is still stale.
func (s *Store) RemoveIfStale(ctx context.Context, entry exec.ProcEntry) error {
	path, ok := procEntryIdentityValue(entry, procEntryIdentityFile)
	if !ok {
		return nil
	}
	current, err := readProcEntryWithRetry(path, entry.GroupName, s.staleTime, time.Now().UTC())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Fresh || !sameProcEntry(current, entry) {
		return nil
	}
	if err := removeProcFile(path); err != nil {
		return err
	}
	logger.Info(ctx, "Removed stale proc file", tag.File(path))
	return nil
}

func readProcEntry(path, groupName string, staleTime time.Duration, now time.Time) (exec.ProcEntry, error) {
	observed, err := readProcEntryObserved(path, groupName, staleTime, now)
	if err != nil {
		return exec.ProcEntry{}, err
	}
	return observed.entry, nil
}

func readProcEntryWithRetry(path, groupName string, staleTime time.Duration, now time.Time) (exec.ProcEntry, error) {
	var lastErr error
	for attempt := range procFileRetries {
		entry, err := readProcEntry(path, groupName, staleTime, now)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return entry, err
		}
		if !fileutil.IsTransientFileError(err) {
			return exec.ProcEntry{}, err
		}
		lastErr = err
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
	return exec.ProcEntry{}, lastErr
}

func readProcEntryObservedWithRetry(path, groupName string, staleTime time.Duration, now time.Time) (observedProcEntry, error) {
	var lastErr error
	for attempt := range procFileRetries {
		observed, err := readProcEntryObserved(path, groupName, staleTime, now)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return observed, err
		}
		if !fileutil.IsTransientFileError(err) {
			return observedProcEntry{}, err
		}
		lastErr = err
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
	return observedProcEntry{}, lastErr
}

func readProcEntryObserved(path, groupName string, staleTime time.Duration, now time.Time) (observedProcEntry, error) {
	filename := filepath.Base(path)
	parsedName, err := parseProcFileName(filename)
	if err != nil {
		return observedProcEntry{}, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return observedProcEntry{}, err
	}

	data, err := fileutil.ReadFile(path)
	if err != nil {
		return observedProcEntry{}, err
	}
	if len(data) < procHeartbeatSize {
		return observedProcEntry{}, fmt.Errorf("%w: proc file %s is shorter than the %d-byte heartbeat header", errInvalidProcFile, path, procHeartbeatSize)
	}

	lastHeartbeatAt := int64(binary.BigEndian.Uint64(data[:procHeartbeatSize])) //nolint:gosec // heartbeat unix time.
	heartbeatTime := time.Unix(lastHeartbeatAt, 0).UTC()
	if heartbeatTime.After(now.Add(5 * time.Minute)) {
		return observedProcEntry{}, fmt.Errorf("%w: proc heartbeat timestamp is in the future for %s", errInvalidProcFile, path)
	}

	meta, err := procMetaFromLegacyData(path, parsedName, data[procHeartbeatSize:], heartbeatTime, info)
	if err != nil {
		return observedProcEntry{}, err
	}

	fresh := now.Sub(info.ModTime()) < staleTime
	if !fresh {
		fresh = now.Sub(heartbeatTime) < staleTime
	}
	entry := exec.ProcEntry{
		GroupName:       groupName,
		Identity:        fileProcEntryID(path),
		Meta:            meta,
		LastHeartbeatAt: lastHeartbeatAt,
		Fresh:           fresh,
	}
	return observedProcEntry{entry: entry, observedAt: info.ModTime()}, nil
}

func procMetaFromLegacyData(path string, parsedName procFileName, payload []byte, heartbeatTime time.Time, info os.FileInfo) (exec.ProcMeta, error) {
	switch parsedName.format {
	case procFileFormatCurrent:
		if len(payload) == 0 {
			return exec.ProcMeta{}, fmt.Errorf("%w: proc file %s is missing metadata payload", errInvalidProcFile, path)
		}
		var diskMeta procDiskMeta
		if err := json.Unmarshal(payload, &diskMeta); err != nil {
			return exec.ProcMeta{}, fmt.Errorf("%w: decode proc metadata: %w", errInvalidProcFile, err)
		}
		if diskMeta.Version != procFileVersion {
			return exec.ProcMeta{}, fmt.Errorf("%w: unsupported proc version %d", errInvalidProcFile, diskMeta.Version)
		}
		meta := exec.ProcMeta{
			StartedAt:    diskMeta.StartedAt,
			Name:         diskMeta.DAGName,
			DAGRunID:     diskMeta.DAGRunID,
			AttemptID:    diskMeta.AttemptID,
			RootName:     diskMeta.RootName,
			RootDAGRunID: diskMeta.RootDAGRunID,
		}
		if err := validateProcMeta(meta); err != nil {
			return exec.ProcMeta{}, fmt.Errorf("%w: %w", errInvalidProcFile, err)
		}
		if parsedName.dagRunID != meta.DAGRunID || parsedName.attemptID != meta.AttemptID {
			return exec.ProcMeta{}, fmt.Errorf("%w: proc filename/body mismatch for %s", errInvalidProcFile, path)
		}
		if filepath.Base(filepath.Dir(path)) != meta.Name {
			return exec.ProcMeta{}, fmt.Errorf("%w: proc path/body DAG name mismatch for %s", errInvalidProcFile, path)
		}
		return meta, nil
	case procFileFormatLegacy:
		if len(payload) != 0 {
			return exec.ProcMeta{}, fmt.Errorf("%w: legacy proc file %s must only contain the heartbeat header", errInvalidProcFile, path)
		}
		return legacyProcMeta(path, parsedName, heartbeatTime, info)
	default:
		return exec.ProcMeta{}, fmt.Errorf("%w: unsupported proc filename format for %s", errInvalidProcFile, path)
	}
}

func parseProcFileName(filename string) (procFileName, error) {
	if matches := procFileRegex.FindStringSubmatch(filename); len(matches) == 4 {
		createdAt, err := time.Parse(procFileTimeFmt, matches[1])
		if err != nil {
			return procFileName{}, fmt.Errorf("%w: parse proc timestamp: %w", errInvalidProcFile, err)
		}
		dagRunID, err := hex.DecodeString(matches[2])
		if err != nil {
			return procFileName{}, fmt.Errorf("%w: decode dag-run id: %w", errInvalidProcFile, err)
		}
		attemptID, err := hex.DecodeString(matches[3])
		if err != nil {
			return procFileName{}, fmt.Errorf("%w: decode attempt id: %w", errInvalidProcFile, err)
		}
		return procFileName{
			format:    procFileFormatCurrent,
			createdAt: createdAt.UTC(),
			dagRunID:  string(dagRunID),
			attemptID: string(attemptID),
		}, nil
	}
	if matches := procLegacyFileRegex.FindStringSubmatch(filename); len(matches) == 3 {
		createdAt, err := time.Parse(procFileTimeFmt, matches[1])
		if err != nil {
			return procFileName{}, fmt.Errorf("%w: parse legacy proc timestamp: %w", errInvalidProcFile, err)
		}
		if err := exec.ValidateDAGRunID(matches[2]); err != nil {
			return procFileName{}, fmt.Errorf("%w: invalid legacy dag-run id: %w", errInvalidProcFile, err)
		}
		return procFileName{
			format:    procFileFormatLegacy,
			createdAt: createdAt.UTC(),
			dagRunID:  matches[2],
			attemptID: legacyProcAttemptID(matches[2]),
		}, nil
	}
	return procFileName{}, fmt.Errorf("%w: invalid proc filename %q", errInvalidProcFile, filename)
}

func legacyProcAttemptID(dagRunID string) string {
	return "legacy_" + hex.EncodeToString([]byte(dagRunID))
}

const procEntryIdentityFile = "file"

func fileProcEntryID(path string) exec.ProcEntryID {
	return procEntryID(procEntryIdentityFile, path)
}

func procEntryID(kind, value string) exec.ProcEntryID {
	if kind == "" || value == "" {
		return exec.ProcEntryID{}
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(value))
	return exec.NewProcEntryID(kind + ":" + encoded)
}

func procEntryIdentityValue(entry exec.ProcEntry, expectedKind string) (string, bool) {
	kind, value, ok := splitProcEntryID(entry.Identity)
	if !ok || kind != expectedKind {
		return "", false
	}
	return value, true
}

func splitProcEntryID(id exec.ProcEntryID) (kind, value string, ok bool) {
	if id.IsZero() {
		return "", "", false
	}
	raw := id.String()
	kind, encoded, found := strings.Cut(raw, ":")
	if !found || kind == "" || encoded == "" {
		return "", "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 {
		return "", "", false
	}
	return kind, string(decoded), true
}

func sameProcEntry(a, b exec.ProcEntry) bool {
	return a.GroupName == b.GroupName &&
		a.Identity == b.Identity &&
		a.LastHeartbeatAt == b.LastHeartbeatAt &&
		a.Meta == b.Meta
}

func freshRefs(entries []exec.ProcEntry) []exec.DAGRunRef {
	seen := make(map[string]exec.DAGRunRef)
	for _, entry := range entries {
		if !entry.Fresh {
			continue
		}
		ref := entry.Meta.DAGRun()
		seen[ref.String()] = ref
	}
	refs := make([]exec.DAGRunRef, 0, len(seen))
	for _, ref := range seen {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Name == refs[j].Name {
			return refs[i].ID < refs[j].ID
		}
		return refs[i].Name < refs[j].Name
	})
	return refs
}

func procHeartbeatFromEntry(entry exec.ProcEntry, observedAt time.Time) exec.ProcHeartbeat {
	return exec.ProcHeartbeat{
		GroupName:       entry.GroupName,
		DAGRun:          entry.Meta.DAGRun(),
		AttemptID:       entry.Meta.AttemptID,
		StartedAt:       entry.Meta.StartedAt,
		LastHeartbeatAt: entry.LastHeartbeatAt,
		ObservedAt:      observedAt,
		Fresh:           entry.Fresh,
	}
}

func procHeartbeatPreferred(candidate, existing exec.ProcHeartbeat) bool {
	if candidate.Fresh != existing.Fresh {
		return candidate.Fresh
	}
	if candidate.StartedAt != existing.StartedAt {
		return candidate.StartedAt > existing.StartedAt
	}
	if candidate.LastHeartbeatAt != existing.LastHeartbeatAt {
		return candidate.LastHeartbeatAt > existing.LastHeartbeatAt
	}
	if !candidate.ObservedAt.Equal(existing.ObservedAt) {
		return candidate.ObservedAt.After(existing.ObservedAt)
	}
	return candidate.AttemptID < existing.AttemptID
}

func legacyProcMeta(path string, parsedName procFileName, heartbeatTime time.Time, info os.FileInfo) (exec.ProcMeta, error) {
	dagName := filepath.Base(filepath.Dir(path))
	if dagName == "" || dagName == "." || dagName == string(filepath.Separator) {
		return exec.ProcMeta{}, fmt.Errorf("%w: invalid legacy proc path %s", errInvalidProcFile, path)
	}

	startedAt := parsedName.createdAt.UTC().Unix()
	if startedAt <= 0 {
		startedAt = heartbeatTime.UTC().Unix()
	}
	if startedAt <= 0 {
		startedAt = info.ModTime().UTC().Unix()
	}

	meta := exec.ProcMeta{
		StartedAt:    startedAt,
		Name:         dagName,
		DAGRunID:     parsedName.dagRunID,
		AttemptID:    parsedName.attemptID,
		RootName:     dagName,
		RootDAGRunID: parsedName.dagRunID,
	}
	if err := validateProcMeta(meta); err != nil {
		return exec.ProcMeta{}, fmt.Errorf("%w: %w", errInvalidProcFile, err)
	}
	return meta, nil
}
