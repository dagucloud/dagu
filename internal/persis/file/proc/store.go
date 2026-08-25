// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package proc

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/proc"
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
	errInvalidProcFile  = errors.New("invalid proc file")
	procFileRegex       = regexp.MustCompile(`^proc_(\d{8}_\d{6}Z)_([0-9a-f]+)_([0-9a-f]+)\.proc$`)
	procLegacyFileRegex = regexp.MustCompile(`^proc_(\d{8}_\d{6}Z)_([-a-zA-Z0-9_]+)\.proc$`)
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
	entry      proc.ProcEntry
	observedAt time.Time
}

var (
	_ persis.ProcStore = (*Store)(nil)
	_ proc.ProcHandle  = (*ProcHandle)(nil)
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
	meta              proc.ProcMeta
	heartbeatInterval time.Duration
	started           atomic.Bool
	canceled          atomic.Bool
	cancel            context.CancelFunc
	mu                sync.Mutex
	wg                sync.WaitGroup
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

// WithLock runs fn while holding the process-group lock.
func (s *Store) WithLock(ctx context.Context, groupName string, fn func() error) error {
	basePolicy := backoff.NewExponentialBackoffPolicy(500 * time.Millisecond)
	basePolicy.BackoffFactor = 2.0
	basePolicy.MaxInterval = time.Minute
	basePolicy.MaxRetries = 10

	policy := backoff.WithJitter(basePolicy, backoff.Jitter)
	if err := backoff.Retry(ctx, func(_ context.Context) error {
		return s.groupLock(groupName).TryLock()
	}, policy, func(_ error) bool {
		return ctx.Err() == nil
	}); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		return persis.NewProcLockError(err)
	}
	defer func() {
		if err := s.groupLock(groupName).Unlock(); err != nil {
			logger.Error(ctx, "Failed to unlock the proc group", tag.Error(err))
		}
	}()
	return fn()
}

// Acquire creates and starts a proc heartbeat.
func (s *Store) Acquire(ctx context.Context, groupName string, meta proc.ProcMeta) (proc.ProcHandle, error) {
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

// Validate fails if the proc directory cannot be read. Individual proc files
// are not decoded here, so a damaged one does not make the store unusable.
func (s *Store) Validate(_ context.Context) error {
	if _, err := fileutil.ReadDir(s.root); err != nil && !errors.Is(err, os.ErrNotExist) {
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

func procRecordName(meta proc.ProcMeta, t time.Time) string {
	return fmt.Sprintf("%s%sZ_%s_%s",
		procFilePrefix,
		t.UTC().Format(procDateTimeUTC),
		hex.EncodeToString([]byte(meta.DAGRunID)),
		hex.EncodeToString([]byte(meta.AttemptID)),
	)
}

func (s *Store) filePath(groupName string, meta proc.ProcMeta, t time.Time) string {
	return filepath.Join(s.root, groupName, meta.Name, procRecordName(meta, t)+procFileExt)
}

func writeProcFile(path string, heartbeatUnix int64, meta proc.ProcMeta) error {
	if err := meta.Validate(); err != nil {
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
	entries, err := fileutil.ReadDir(dir)
	if err != nil || len(entries) > 0 {
		return
	}
	_ = fileutil.Remove(dir)
}

// ListEntries returns proc entries for a group.
func (s *Store) ListEntries(_ context.Context, groupName string) ([]proc.ProcEntry, error) {
	groupDir := filepath.Join(s.root, groupName)
	if _, err := fileutil.Stat(groupDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	files, err := procFilesInGroup(groupDir)
	if err != nil {
		return nil, err
	}
	return s.entriesFromFiles(groupName, files)
}

// ListAllEntries returns all proc entries under the store root.
func (s *Store) ListAllEntries(_ context.Context) ([]proc.ProcEntry, error) {
	dirEntries, err := fileutil.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var entries []proc.ProcEntry
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
func (s *Store) LatestHeartbeat(_ context.Context, groupName string, dagRun ir.DAGRunRef) (*proc.ProcHeartbeat, error) {
	groupDir := filepath.Join(s.root, groupName)
	if _, err := fileutil.Stat(groupDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	files, err := procFilesInGroup(groupDir)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var latest *proc.ProcHeartbeat
	for _, file := range files {
		if !procFileMayBelongTo(file, dagRun) {
			continue
		}
		observed, err := readProcEntryWithRetry(file, groupName, s.staleTime, now)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if errors.Is(err, errInvalidProcFile) && s.abandoned(file, now) {
				continue
			}
			// The file may be this run's and is still being written, so report
			// that rather than an absence the caller reads as an exit.
			return nil, err
		}
		entry := observed.entry
		if entry.Meta.Name != dagRun.Name || entry.Meta.DAGRunID != dagRun.ID {
			continue
		}
		heartbeat := entry.Heartbeat(observed.observedAt)
		if latest == nil || heartbeat.PreferredTo(*latest) {
			latest = &heartbeat
		}
	}
	return latest, nil
}

func procFilesInGroup(groupDir string) ([]string, error) {
	dagEntries, err := fileutil.ReadDir(groupDir)
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
		procEntries, err := fileutil.ReadDir(filepath.Join(groupDir, dagEntry.Name()))
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

func (s *Store) entriesFromFiles(groupName string, files []string) ([]proc.ProcEntry, error) {
	now := time.Now().UTC()
	entries := make([]proc.ProcEntry, 0, len(files))
	for _, file := range files {
		observed, err := readProcEntryWithRetry(file, groupName, s.staleTime, now)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if errors.Is(err, errInvalidProcFile) && s.abandoned(file, now) {
				continue
			}
			return nil, err
		}
		entries = append(entries, observed.entry)
	}
	return entries, nil
}

// procFileMayBelongTo reports whether path can hold an entry for dagRun,
// judging by the DAG directory and the identifiers carried in the file name.
// A name that cannot be parsed is a possible match, because attributing such a
// file needs its contents.
func procFileMayBelongTo(path string, dagRun ir.DAGRunRef) bool {
	if filepath.Base(filepath.Dir(path)) != dagRun.Name {
		return false
	}
	parsed, err := parseProcFileName(filepath.Base(path))
	if err != nil {
		return true
	}
	return parsed.dagRunID == dagRun.ID
}

// abandoned reports whether path has gone untouched for at least the stale
// threshold. A damaged file that is still being written may belong to a run
// that is alive, and reporting the group without it would undercount.
func (s *Store) abandoned(path string, now time.Time) bool {
	info, err := fileutil.Stat(path)
	return err == nil && now.Sub(info.ModTime()) >= s.staleTime
}

// RemoveIfStale deletes entry when the on-disk proc file is still stale.
func (s *Store) RemoveIfStale(ctx context.Context, entry proc.ProcEntry) error {
	path, ok := entry.Identity.StoreValue(procEntryIdentityFile)
	if !ok {
		return nil
	}
	observed, err := readProcEntryWithRetry(path, entry.GroupName, s.staleTime, time.Now().UTC())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if observed.entry.Fresh || !observed.entry.SameObservation(entry) {
		return nil
	}
	if err := removeProcFile(path); err != nil {
		return err
	}
	logger.Info(ctx, "Removed stale proc file", tag.File(path))
	return nil
}

func readProcEntryWithRetry(path, groupName string, staleTime time.Duration, now time.Time) (observedProcEntry, error) {
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
		if attempt < procFileRetries-1 {
			time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
		}
	}
	return observedProcEntry{}, lastErr
}

func readProcEntryObserved(path, groupName string, staleTime time.Duration, now time.Time) (observedProcEntry, error) {
	filename := filepath.Base(path)
	parsedName, err := parseProcFileName(filename)
	if err != nil {
		return observedProcEntry{}, err
	}

	info, err := fileutil.Stat(path)
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

	meta, err := procMetaFromData(path, parsedName, data[procHeartbeatSize:], heartbeatTime, info)
	if err != nil {
		return observedProcEntry{}, err
	}

	// The heartbeat carries the writing process's clock, so it is only trusted
	// when it is not ahead of the reader. A skewed or garbled timestamp leaves
	// the entry stale rather than alive forever.
	fresh := now.Sub(info.ModTime()) < staleTime
	if !fresh {
		age := now.Sub(heartbeatTime)
		fresh = age >= 0 && age < staleTime
	}
	entry := proc.ProcEntry{
		GroupName:       groupName,
		Identity:        fileProcEntryID(path),
		Meta:            meta,
		LastHeartbeatAt: lastHeartbeatAt,
		Fresh:           fresh,
	}
	return observedProcEntry{entry: entry, observedAt: info.ModTime()}, nil
}

func procMetaFromData(path string, parsedName procFileName, payload []byte, heartbeatTime time.Time, info os.FileInfo) (proc.ProcMeta, error) {
	switch parsedName.format {
	case procFileFormatCurrent:
		if len(payload) == 0 {
			return proc.ProcMeta{}, fmt.Errorf("%w: proc file %s is missing metadata payload", errInvalidProcFile, path)
		}
		var diskMeta procDiskMeta
		if err := json.Unmarshal(payload, &diskMeta); err != nil {
			return proc.ProcMeta{}, fmt.Errorf("%w: decode proc metadata: %w", errInvalidProcFile, err)
		}
		if diskMeta.Version != procFileVersion {
			return proc.ProcMeta{}, fmt.Errorf("%w: unsupported proc version %d", errInvalidProcFile, diskMeta.Version)
		}
		meta := proc.ProcMeta{
			StartedAt:    diskMeta.StartedAt,
			Name:         diskMeta.DAGName,
			DAGRunID:     diskMeta.DAGRunID,
			AttemptID:    diskMeta.AttemptID,
			RootName:     diskMeta.RootName,
			RootDAGRunID: diskMeta.RootDAGRunID,
		}
		if err := meta.Validate(); err != nil {
			return proc.ProcMeta{}, fmt.Errorf("%w: %w", errInvalidProcFile, err)
		}
		if parsedName.dagRunID != meta.DAGRunID || parsedName.attemptID != meta.AttemptID {
			return proc.ProcMeta{}, fmt.Errorf("%w: proc filename/body mismatch for %s", errInvalidProcFile, path)
		}
		if filepath.Base(filepath.Dir(path)) != meta.Name {
			return proc.ProcMeta{}, fmt.Errorf("%w: proc path/body DAG name mismatch for %s", errInvalidProcFile, path)
		}
		return meta, nil
	case procFileFormatLegacy:
		if len(payload) != 0 {
			return proc.ProcMeta{}, fmt.Errorf("%w: legacy proc file %s must only contain the heartbeat header", errInvalidProcFile, path)
		}
		return legacyProcMeta(path, parsedName, heartbeatTime, info)
	default:
		return proc.ProcMeta{}, fmt.Errorf("%w: unsupported proc filename format for %s", errInvalidProcFile, path)
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
		if err := ir.ValidateDAGRunID(matches[2]); err != nil {
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

func fileProcEntryID(path string) proc.ProcEntryID {
	return proc.NewStoreEntryID(procEntryIdentityFile, path)
}

func legacyProcMeta(path string, parsedName procFileName, heartbeatTime time.Time, info os.FileInfo) (proc.ProcMeta, error) {
	dagName := filepath.Base(filepath.Dir(path))
	if dagName == "" || dagName == "." || dagName == string(filepath.Separator) {
		return proc.ProcMeta{}, fmt.Errorf("%w: invalid legacy proc path %s", errInvalidProcFile, path)
	}

	startedAt := parsedName.createdAt.UTC().Unix()
	if startedAt <= 0 {
		startedAt = heartbeatTime.UTC().Unix()
	}
	if startedAt <= 0 {
		startedAt = info.ModTime().UTC().Unix()
	}

	meta := proc.ProcMeta{
		StartedAt:    startedAt,
		Name:         dagName,
		DAGRunID:     parsedName.dagRunID,
		AttemptID:    parsedName.attemptID,
		RootName:     dagName,
		RootDAGRunID: parsedName.dagRunID,
	}
	if err := meta.Validate(); err != nil {
		return proc.ProcMeta{}, fmt.Errorf("%w: %w", errInvalidProcFile, err)
	}
	return meta, nil
}
