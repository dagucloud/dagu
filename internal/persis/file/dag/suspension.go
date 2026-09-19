// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dag/dagindex"
	"github.com/gofrs/flock"
)

const (
	flagPermission       os.FileMode = 0o750
	suspendFlagExtension             = ".suspend"
	suspendLockFile                  = ".suspend.lock"
	suspendLockRetry                 = 25 * time.Millisecond
)

func (store *Store) SetSuspended(ctx context.Context, id string, suspended bool) error {
	return store.withSuspendFlagsLock(ctx, func() error {
		defer store.invalidateIndex()
		flag := fileName(id)
		target := filepath.Join(store.flagsBaseDir, flag)
		if suspended {
			if err := fileutil.WriteFileAtomic(target, nil, flagPermission); err != nil {
				return err
			}
		}
		legacyDir, err := store.legacySuspendFlagsDir()
		if err != nil {
			return err
		}
		if legacyDir != "" {
			if err := removeSuspendFlag(filepath.Join(legacyDir, flag)); err != nil {
				return err
			}
		}
		if !suspended {
			return removeSuspendFlag(target)
		}
		return nil
	})
}

func (store *Store) IsSuspended(_ context.Context, id string) (bool, error) {
	for _, dir := range store.suspendFlagsDirs() {
		_, err := os.Stat(filepath.Join(dir, fileName(id)))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		if _, err := suspendFlagsDirExists(dir); err != nil {
			return false, err
		}
	}
	return false, nil
}

// MigrateSuspensionState copies legacy suspension state into primary storage
// while retaining legacy flags and existing primary flags.
func (store *Store) MigrateSuspensionState(ctx context.Context) error {
	legacyDir, err := store.legacySuspendFlagsDir()
	if err != nil || legacyDir == "" {
		return err
	}
	return store.withSuspendFlagsLock(ctx, func() error {
		entries, err := os.ReadDir(legacyDir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read legacy suspend flags: %w", err)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != suspendFlagExtension {
				continue
			}
			err := fileutil.WriteFileAtomicExclusive(filepath.Join(store.flagsBaseDir, entry.Name()), nil, flagPermission)
			if err != nil && !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("copy suspend flag %s: %w", entry.Name(), err)
			}
		}
		return nil
	})
}

func (store *Store) withSuspendFlagsLock(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(store.flagsBaseDir, flagPermission); err != nil {
		return err
	}
	// Migration and state changes share a lock so a resume cannot be undone
	// by a scheduler copying a legacy flag it read before the resume.
	lock := flock.New(filepath.Join(store.flagsBaseDir, suspendLockFile))
	locked, err := lock.TryLockContext(ctx, suspendLockRetry)
	if err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("suspend flags lock was not acquired")
	}
	defer func() { _ = lock.Unlock() }()
	return fn()
}

func (store *Store) suspendFlagsDirs() []string {
	// Read legacy first so promoting a flag to primary storage cannot make
	// a continuously suspended DAG appear active during the transition.
	if store.legacyFlagsBaseDir != "" && store.legacyFlagsBaseDir != store.flagsBaseDir {
		return []string{store.legacyFlagsBaseDir, store.flagsBaseDir}
	}
	return []string{store.flagsBaseDir}
}

func (store *Store) legacySuspendFlagsDir() (string, error) {
	legacyDir := store.legacyFlagsBaseDir
	if legacyDir == "" || legacyDir == store.flagsBaseDir {
		return "", nil
	}
	legacy, err := os.Stat(legacyDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !legacy.IsDir() {
		return "", fmt.Errorf("suspend flags path %s is not a directory", legacyDir)
	}
	primary, err := os.Stat(store.flagsBaseDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err == nil && os.SameFile(legacy, primary) {
		return "", nil
	}
	return legacyDir, nil
}

func (store *Store) readSuspendFlags(ctx context.Context) (dagindex.SuspendFlags, error) {
	flags := make(dagindex.SuspendFlags)
	for _, dir := range store.suspendFlagsDirs() {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			exists, statErr := suspendFlagsDirExists(dir)
			if statErr != nil {
				return nil, statErr
			}
			if !exists {
				logger.Debug(ctx, "Suspend flags directory does not exist", tag.Dir(dir))
				continue
			}
		}
		if err != nil {
			return nil, fmt.Errorf("read suspend flags directory %s: %w", dir, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == suspendFlagExtension {
				flags[entry.Name()] = struct{}{}
			}
		}
	}
	return flags, nil
}

func suspendFlagsDirExists(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("suspend flags path %s is not a directory", dir)
	}
	return true, nil
}

func removeSuspendFlag(path string) error {
	err := fileutil.RemoveFileDurable(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
