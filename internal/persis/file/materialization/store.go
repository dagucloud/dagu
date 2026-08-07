// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package materialization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/incremental"
	"github.com/gofrs/flock"
)

const fileMode = 0o600

// Store is a local file-backed materialization store.
type Store struct {
	root string
}

// New creates a materialization store rooted at dir.
func New(dir string) *Store {
	return &Store{root: dir}
}

type heldLock struct {
	store    *Store
	locks    []*flock.Flock
	requests []exec.PathLockRequest
	released bool
}

func (l *heldLock) Release() error {
	if l == nil || l.released {
		return nil
	}
	l.released = true
	var result error
	for idx := len(l.locks) - 1; idx >= 0; idx-- {
		result = errors.Join(result, l.locks[idx].Unlock())
	}
	return result
}

// Get returns one committed manifest.
func (s *Store) Get(_ context.Context, key string) (*exec.Materialization, error) {
	data, err := os.ReadFile(s.manifestPath(key)) //nolint:gosec // key is generated internally
	if errors.Is(err, os.ErrNotExist) {
		return nil, exec.ErrMaterializationNotFound
	}
	if err != nil {
		return nil, err
	}
	var manifest exec.Materialization
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode materialization manifest: %w", err)
	}
	return &manifest, nil
}

// AcquirePaths acquires path locks in stable order and recovers incomplete commits.
func (s *Store) AcquirePaths(ctx context.Context, requests []exec.PathLockRequest) (exec.MaterializationLock, error) {
	if s == nil || s.root == "" {
		return nil, fmt.Errorf("materialization store is unavailable")
	}
	if err := s.ensureDirs(); err != nil {
		return nil, err
	}
	requests = normalizeRequests(requests)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		held := &heldLock{store: s, requests: requests}
		restart := false
		for _, request := range requests {
			lock := flock.New(s.lockPath(request.Key))
			locked, err := acquire(ctx, lock, request.Mode)
			if err != nil || !locked {
				_ = held.Release()
				if err != nil {
					return nil, fmt.Errorf("acquire materialization path lock: %w", err)
				}
				return nil, fmt.Errorf("materialization path lock was not acquired")
			}
			held.locks = append(held.locks, lock)

			if request.Mode == exec.PathLockExclusive {
				if err := s.recover(request.Key); err != nil {
					_ = held.Release()
					return nil, fmt.Errorf("%w: %w", exec.ErrMaterializationRecovery, err)
				}
				continue
			}
			if _, err := os.Stat(s.journalPath(request.Key)); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				_ = held.Release()
				return nil, err
			}

			_ = held.Release()
			if err := s.recoverWithExclusiveLock(ctx, request.Key); err != nil {
				return nil, fmt.Errorf("%w: %w", exec.ErrMaterializationRecovery, err)
			}
			restart = true
			break
		}
		if !restart {
			return held, nil
		}
	}
}

func acquire(ctx context.Context, lock *flock.Flock, mode exec.PathLockMode) (bool, error) {
	if mode == exec.PathLockShared {
		return lock.TryRLockContext(ctx, 25*time.Millisecond)
	}
	return lock.TryLockContext(ctx, 25*time.Millisecond)
}

func (s *Store) recoverWithExclusiveLock(ctx context.Context, key string) error {
	lock := flock.New(s.lockPath(key))
	locked, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("materialization recovery lock was not acquired")
	}
	defer func() { _ = lock.Unlock() }()
	return s.recover(key)
}

// Commit publishes the staged file and manifest with rollback on failure.
func (s *Store) Commit(_ context.Context, lock exec.MaterializationLock, req exec.MaterializationCommit) error {
	held, ok := lock.(*heldLock)
	if !ok || held.store != s || held.released {
		return fmt.Errorf("invalid materialization lock")
	}
	outputKey := incremental.ComparisonKey(req.FinalPath)
	hasOutputLock := false
	for _, request := range held.requests {
		if request.Mode == exec.PathLockExclusive && request.Key == outputKey {
			hasOutputLock = true
			break
		}
	}
	if !hasOutputLock {
		return fmt.Errorf("materialization commit requires an exclusive lock for the final output")
	}
	if filepath.Dir(req.StagingPath) != filepath.Dir(req.FinalPath) {
		return fmt.Errorf("staging and final output must be on the same filesystem directory")
	}
	if err := verifyFile(req.StagingPath, req.Manifest.Output); err != nil {
		return err
	}

	manifestPath := s.manifestPath(req.Manifest.MaterializationKey)
	previousManifest, previousManifestErr := os.ReadFile(manifestPath) //nolint:gosec
	if previousManifestErr != nil && !errors.Is(previousManifestErr, os.ErrNotExist) {
		return previousManifestErr
	}
	previousFinal, err := snapshotExisting(req.FinalPath)
	if err != nil {
		return err
	}

	journalPath := s.journalPath(outputKey)
	backupPath := filepath.Join(filepath.Dir(req.FinalPath), ".dagu-backup-"+digestName(req.FinalPath+"\x00"+req.Manifest.CommitID))
	journal := commitJournal{
		FinalPath:        req.FinalPath,
		BackupPath:       backupPath,
		ManifestPath:     manifestPath,
		PreviousFinal:    previousFinal,
		PreviousManifest: previousManifest,
		Proposed:         req.Manifest,
	}
	if err := fileutil.WriteJSONAtomic(journalPath, journal, fileMode); err != nil {
		return fmt.Errorf("write materialization journal: %w", err)
	}

	rollback := func(cause error) error {
		if restoreErr := restorePrevious(journal); restoreErr != nil {
			return errors.Join(cause, fmt.Errorf("rollback materialization: %w", restoreErr))
		}
		_ = fileutil.RemoveFileDurable(journalPath)
		return cause
	}
	if previousFinal != nil {
		if err := preserveFile(req.FinalPath, backupPath); err != nil {
			_ = fileutil.RemoveFileDurable(journalPath)
			return fmt.Errorf("preserve previous materialization: %w", err)
		}
	}
	if err := fileutil.ReplaceFileDurable(req.StagingPath, req.FinalPath); err != nil {
		return rollback(fmt.Errorf("replace materialized output: %w", err))
	}
	if err := fileutil.WriteJSONAtomic(manifestPath, req.Manifest, fileMode); err != nil {
		return rollback(fmt.Errorf("write materialization manifest: %w", err))
	}
	_ = fileutil.RemoveFileDurable(backupPath)
	_ = fileutil.RemoveFileDurable(journalPath)
	return nil
}

type commitJournal struct {
	FinalPath        string               `json:"finalPath"`
	BackupPath       string               `json:"backupPath"`
	ManifestPath     string               `json:"manifestPath"`
	PreviousFinal    *exec.FileSnapshot   `json:"previousFinal,omitempty"`
	PreviousManifest json.RawMessage      `json:"previousManifest,omitempty"`
	Proposed         exec.Materialization `json:"proposed"`
}

func (s *Store) recover(pathKey string) error {
	journalPath := s.journalPath(pathKey)
	data, err := os.ReadFile(journalPath) //nolint:gosec
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var journal commitJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return fmt.Errorf("recover materialization: invalid journal: %w", err)
	}
	var current exec.Materialization
	manifestData, manifestErr := os.ReadFile(journal.ManifestPath) //nolint:gosec
	if manifestErr == nil && json.Unmarshal(manifestData, &current) == nil &&
		current.CommitID == journal.Proposed.CommitID && verifyFile(journal.FinalPath, journal.Proposed.Output) == nil {
		_ = fileutil.RemoveFileDurable(journal.BackupPath)
		return fileutil.RemoveFileDurable(journalPath)
	}
	if err := restorePrevious(journal); err != nil {
		return fmt.Errorf("recover materialization: %w", err)
	}
	_ = fileutil.RemoveFileDurable(journalPath)
	return nil
}

func restorePrevious(journal commitJournal) error {
	if journal.PreviousFinal != nil {
		if err := verifyFile(journal.BackupPath, *journal.PreviousFinal); err == nil {
			if err := fileutil.ReplaceFileDurable(journal.BackupPath, journal.FinalPath); err != nil {
				return err
			}
		} else if currentErr := verifyFile(journal.FinalPath, *journal.PreviousFinal); currentErr == nil {
			_ = fileutil.RemoveFileDurable(journal.BackupPath)
		} else {
			return fmt.Errorf("previous output and backup are unavailable: %w", err)
		}
	} else if _, err := os.Lstat(journal.FinalPath); err == nil {
		if verifyErr := verifyFile(journal.FinalPath, journal.Proposed.Output); verifyErr != nil {
			return fmt.Errorf("current output cannot be identified as the proposed materialization: %w", verifyErr)
		}
		if err := fileutil.RemoveFileDurable(journal.FinalPath); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(journal.PreviousManifest) > 0 {
		return fileutil.WriteFileAtomic(journal.ManifestPath, journal.PreviousManifest, fileMode)
	}
	if err := fileutil.RemoveFileDurable(journal.ManifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func normalizeRequests(requests []exec.PathLockRequest) []exec.PathLockRequest {
	byKey := make(map[string]exec.PathLockMode, len(requests))
	for _, request := range requests {
		if request.Key == "" {
			continue
		}
		mode := byKey[request.Key]
		if mode == "" || request.Mode == exec.PathLockExclusive {
			byKey[request.Key] = request.Mode
		}
	}
	result := make([]exec.PathLockRequest, 0, len(byKey))
	for key, mode := range byKey {
		result = append(result, exec.PathLockRequest{Key: key, Mode: mode})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

func (s *Store) ensureDirs() error {
	for _, dir := range []string{filepath.Join(s.root, "manifests"), filepath.Join(s.root, "locks"), filepath.Join(s.root, "journals")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) manifestPath(key string) string {
	return filepath.Join(s.root, "manifests", key+".json")
}
func (s *Store) lockPath(key string) string {
	return filepath.Join(s.root, "locks", digestName(key)+".lock")
}
func (s *Store) journalPath(key string) string {
	return filepath.Join(s.root, "journals", digestName(key)+".json")
}

func digestName(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func snapshotExisting(path string) (*exec.FileSnapshot, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	snapshot, err := snapshotFile(path)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func snapshotFile(path string) (exec.FileSnapshot, error) {
	return incremental.Snapshot("", path)
}

func verifyFile(path string, expected exec.FileSnapshot) error {
	got, err := snapshotFile(path)
	if err != nil {
		return err
	}
	if got.Size != expected.Size || got.Digest != expected.Digest {
		return fmt.Errorf("file content changed for %s", path)
	}
	return nil
}

func preserveFile(source, target string) error {
	if err := os.Link(source, target); err == nil {
		return fileutil.SyncDir(filepath.Dir(target))
	}
	in, err := os.Open(source) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode) //nolint:gosec
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = fileutil.RemoveFileDurable(target)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = fileutil.RemoveFileDurable(target)
		return err
	}
	if err := out.Close(); err != nil {
		_ = fileutil.RemoveFileDurable(target)
		return err
	}
	return fileutil.SyncDir(filepath.Dir(target))
}
