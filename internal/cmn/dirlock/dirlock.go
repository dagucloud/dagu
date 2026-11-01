// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package dirlock provides a directory-based locking mechanism for coordinating
// access to shared resources across multiple processes.
package dirlock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// Error types for lock operations
var (
	// ErrLockConflict indicates the lock is held by another process
	ErrLockConflict = errors.New("directory is locked by another process")

	// ErrNotLocked indicates unlock was called but lock is not held
	ErrNotLocked = errors.New("directory is not locked")

	// ErrLockNotHeld indicates operation requires holding the lock
	ErrLockNotHeld = errors.New("lock is not held by this instance")

	// ErrInvalidLockFile indicates the lock file is corrupted
	ErrInvalidLockFile = errors.New("lock file is invalid or corrupted")
)

// DirLock represents a directory lock instance
type DirLock interface {
	// TryLock attempts to acquire lock without blocking
	// Returns ErrLockConflict if lock is held by another process
	TryLock() error

	// Lock acquires lock, blocking until available or context is cancelled
	Lock(ctx context.Context) error

	// Unlock releases the lock
	// Returns error if lock is not held by this instance
	Unlock() error

	// IsLocked checks if directory is currently locked
	// Returns true if locked (by any process)
	IsLocked() bool

	// IsHeldByMe checks if this instance holds the lock
	IsHeldByMe() bool

	// Info returns information about current lock holder
	// Returns nil if not locked
	Info() (*LockInfo, error)

	// Heartbeat updates the lock timestamp to prevent staleness
	// Must be called periodically while holding the lock
	Heartbeat(ctx context.Context) error
}

// LockOptions configures lock behavior
type LockOptions struct {
	// StaleThreshold after which lock is considered stale (default: 30s)
	StaleThreshold time.Duration

	// RetryInterval for lock acquisition attempts (default: 50ms)
	RetryInterval time.Duration

	// OnWait is called once when Lock transitions into retry/wait mode after
	// encountering a lock conflict.
	OnWait func()
}

// LockInfo contains information about a lock
type LockInfo struct {
	AcquiredAt  time.Time
	LockDirName string
}

// dirLock implements the DirLock interface
type dirLock struct {
	targetDir  string
	lockPath   string
	opts       *LockOptions
	isHeld     bool
	fenceToken string // unique token written at acquire time
	ownerFile  string // name of the owner file holding fenceToken
	mu         sync.Mutex

	// beforeStaleClaim, when set by tests, runs after a stale lock has been
	// observed and before it is claimed.
	beforeStaleClaim func()
	// afterClaim, when set by tests, runs after a lock instance has been
	// claimed for removal and before it is removed.
	afterClaim func()
	// beforeOwnerWrite, when set by tests, runs after a new lock directory is
	// created and before its owner file is written.
	beforeOwnerWrite func()
	// beforeSweep, when set by tests, runs when a takeover starts sweeping
	// released lock directories.
	beforeSweep func()
}

var _ DirLock = (*dirLock)(nil)

const (
	// LockDirectoryName is the reserved directory name for lock metadata.
	LockDirectoryName           = ".dagu_lock"
	releasedLockDirectoryPrefix = LockDirectoryName + ".releasing."

	// lockOwnerFileName is the owner file written by older releases.
	lockOwnerFileName = "owner"
	// ownerFilePrefix names the owner file of a live lock. The suffix is unique
	// per lock instance, so a rename of it can only ever hit that instance.
	ownerFilePrefix = "owner."
	// claimedFilePrefix names the owner file after a releaser has claimed the
	// lock instance for removal.
	claimedFilePrefix = "claimed."
)

// IsLockDirectoryName reports whether name is reserved for lock metadata.
func IsLockDirectoryName(name string) bool {
	return name == LockDirectoryName || strings.HasPrefix(name, releasedLockDirectoryPrefix)
}

// New creates a new directory lock instance
func New(directory string, opts *LockOptions) DirLock {
	// Set default options if not provided
	if opts == nil {
		opts = &LockOptions{}
	}
	if opts.StaleThreshold == 0 {
		opts.StaleThreshold = 30 * time.Second
	}
	if opts.RetryInterval == 0 {
		opts.RetryInterval = 50 * time.Millisecond
	}

	return &dirLock{
		targetDir: directory,
		lockPath:  filepath.Join(directory, LockDirectoryName),
		opts:      opts,
	}
}

// Heartbeat updates the lock's modification time to prevent it from being
// considered stale. This is an atomic operation that should be called
// periodically while the lock is held to keep it alive.
func (l *dirLock) Heartbeat(_ context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.isHeld {
		return ErrNotLocked
	}

	// Verify we still own the lock by checking the fence token
	tokenPath := filepath.Join(l.lockPath, l.ownerFile)
	data, err := fileutil.ReadFile(tokenPath)
	if err != nil {
		l.isHeld = false
		return fmt.Errorf("%w: failed to read lock token: %v", ErrLockNotHeld, err)
	}
	if string(data) != l.fenceToken {
		l.isHeld = false
		return fmt.Errorf("%w: token mismatch (lock was acquired by another process)", ErrLockNotHeld)
	}

	// Touch the lock directory to update its modification time
	now := time.Now()
	if err := os.Chtimes(l.lockPath, now, now); err != nil {
		return fmt.Errorf("failed to update lock timestamp: %w", err)
	}

	return nil
}

// TryLock attempts to acquire lock without blocking
func (l *dirLock) TryLock() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.isHeld {
		return nil // Already held by us
	}

	// Check if lock exists
	info, err := os.Stat(l.lockPath)
	if err == nil {
		if !l.isStaleInfo(info) {
			return ErrLockConflict
		}
		if err := l.removeStaleLock(); err != nil {
			return err
		}
		// A stale lock means a holder stopped without releasing it, so it may
		// also have left released lock directories behind. Sweep them before
		// creating the new lock, so the sweep does not eat into its lease.
		l.removeAbandonedTempDirs()
		return l.createLock()
	} else if !os.IsNotExist(err) {
		if isRetryableLockStateError(err) {
			return ErrLockConflict
		}
		return fmt.Errorf("failed to check lock status: %w", err)
	}

	return l.createLock()
}

// removeAbandonedTempDirs removes released lock directories older than the
// stale threshold, left by a process that stopped while removing them. Errors
// are ignored; a later takeover retries.
func (l *dirLock) removeAbandonedTempDirs() {
	if l.beforeSweep != nil {
		l.beforeSweep()
	}
	entries, err := os.ReadDir(l.targetDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, releasedLockDirectoryPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || !l.isStaleInfo(info) {
			continue
		}
		_ = fileutil.RemoveAll(filepath.Join(l.targetDir, name))
	}
}

// removeStaleLock removes the lock directory if it is still the stale instance
// observed by the caller. Contenders that observe the same stale lock race to
// claim it with a single atomic operation bound to that instance, so a lock
// created after the stale one was removed is never taken over. It returns
// ErrLockConflict when another contender won.
func (l *dirLock) removeStaleLock() error {
	l.restoreLockDirAccess()

	// List the entries before re-checking staleness: a lock created after this
	// listing is fresh, so the entries always belong to the stale instance.
	entries, err := os.ReadDir(l.lockPath)
	if err != nil {
		return staleLockError(err)
	}
	info, err := os.Stat(l.lockPath)
	if err != nil {
		return staleLockError(err)
	}
	if !l.isStaleInfo(info) {
		return ErrLockConflict
	}
	if l.beforeStaleClaim != nil {
		l.beforeStaleClaim()
	}

	if len(entries) == 0 {
		// An empty directory holds no lock: a lock is held only once its owner
		// file is written and verified. A contender still creating its lock
		// here finds the directory gone or shared, and backs off.
		return staleLockError(os.Remove(l.lockPath))
	}

	claimName := claimedFilePrefix + newUniqueID()
	claimPath := filepath.Join(l.lockPath, claimName)
	if marker := staleLockMarker(entries); marker != "" {
		// Only one contender can rename a given marker away.
		if err := fileutil.Rename(filepath.Join(l.lockPath, marker), claimPath); err != nil {
			if isLostClaimError(err) {
				return ErrLockConflict
			}
			return staleLockError(err)
		}
		l.runAfterClaim()
		return staleLockError(releaseClaimedLock(l.lockPath, claimName))
	}

	// No marker to rename: create one exclusively and give up if any other
	// marker shows up, which means a live lock or a competing claim.
	// #nosec G304 -- claimPath is the lock directory plus a generated name.
	f, err := os.OpenFile(claimPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return staleLockError(err)
	}
	_ = f.Close()
	entries, err = os.ReadDir(l.lockPath)
	if err == nil && staleLockMarker(withoutEntry(entries, claimName)) != "" {
		err = ErrLockConflict
	}
	if err != nil {
		_ = os.Remove(claimPath)
		return staleLockError(err)
	}
	l.runAfterClaim()
	return staleLockError(releaseClaimedLock(l.lockPath, claimName))
}

// restoreLockDirAccess gives the owner full access to the lock directory again
// when it has lost some, for example through a restore that did not keep file
// modes. Taking over a stale lock lists and renames entries inside it, which
// needs that access. Group and other permissions are kept, and the change
// never leaves the target directory, even through a symbolic link.
func (l *dirLock) restoreLockDirAccess() {
	info, err := os.Lstat(l.lockPath)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0700 == 0700 {
		return
	}
	root, err := os.OpenRoot(l.targetDir)
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	_ = root.Chmod(LockDirectoryName, info.Mode().Perm()|0700)
}

// staleLockMarker returns the entry that identifies a lock instance, preferring
// a live owner file, then the legacy owner file, then an abandoned claim.
func staleLockMarker(entries []os.DirEntry) string {
	for _, prefix := range []string{ownerFilePrefix, lockOwnerFileName, claimedFilePrefix} {
		for _, e := range entries {
			if name := e.Name(); name == prefix || (strings.HasSuffix(prefix, ".") && strings.HasPrefix(name, prefix)) {
				return name
			}
		}
	}
	return ""
}

func withoutEntry(entries []os.DirEntry, name string) []os.DirEntry {
	out := entries[:0:0]
	for _, e := range entries {
		if e.Name() != name {
			out = append(out, e)
		}
	}
	return out
}

// staleLockError maps errors caused by losing a stale-lock race to
// ErrLockConflict.
func staleLockError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrLockConflict), os.IsNotExist(err), os.IsExist(err), isRetryableLockStateError(err):
		return ErrLockConflict
	default:
		return fmt.Errorf("failed to remove stale lock: %w", err)
	}
}

// releaseClaimedLock deletes a lock directory claimed under claimName. It
// never renames the directory: a claimer that stalls may lose its claim to a
// later takeover, and moving the live lock name away would hand it to a third
// contender. Instead it deletes the entries it listed by name. Those names are
// unique to the claimed instance, so a newer lock at the same path keeps its
// files, and the claim file itself is removed last as the final ownership
// check. It returns ErrLockConflict if the claim was lost.
func releaseClaimedLock(lockPath, claimName string) error {
	entries, err := os.ReadDir(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrLockConflict
		}
		return err
	}
	if !containsEntry(entries, claimName) {
		return ErrLockConflict
	}
	for _, e := range entries {
		if e.Name() != claimName {
			// An entry that still cannot be deleted keeps the lock occupied
			// until someone removes it; the error names it.
			if err := removeEntry(lockPath, e.Name()); err != nil {
				return err
			}
		}
	}
	if err := fileutil.Remove(filepath.Join(lockPath, claimName)); err != nil {
		if os.IsNotExist(err) {
			return ErrLockConflict
		}
		return err
	}
	// A non-empty directory here holds the owner file of a contender whose own
	// directory was removed; that contender decides whether it holds the lock.
	if err := fileutil.Remove(lockPath); err != nil && !os.IsNotExist(err) && !os.IsExist(err) {
		return err
	}
	return nil
}

// removeEntry deletes the entry name of the lock directory and everything
// below it. When permissions deny that, it gives the owner write access to the
// directories and files below the entry and tries again, which succeeds for
// entries this user owns. The permission changes never leave the lock
// directory, even through symbolic links.
func removeEntry(lockPath, name string) error {
	err := fileutil.RemoveAll(filepath.Join(lockPath, name))
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	root, rootErr := os.OpenRoot(lockPath)
	if rootErr != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	_ = fs.WalkDir(root.FS(), name, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
		case d.IsDir():
			_ = root.Chmod(p, 0700)
		case d.Type().IsRegular():
			_ = root.Chmod(p, 0600)
		}
		return nil
	})
	return fileutil.RemoveAll(filepath.Join(lockPath, name))
}

func containsEntry(entries []os.DirEntry, name string) bool {
	for _, e := range entries {
		if e.Name() == name {
			return true
		}
	}
	return false
}

// createLock creates the lock directory and writes this instance's owner file
// into it. Creating the directory is exclusive, as in older releases, so a new
// lock never replaces another one.
func (l *dirLock) createLock() error {
	if err := fileutil.MkdirAll(l.targetDir, 0750); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}
	if err := os.Mkdir(l.lockPath, 0700); err != nil {
		if os.IsExist(err) || isRetryableLockStateError(err) {
			return ErrLockConflict
		}
		return fmt.Errorf("failed to create lock directory: %w", err)
	}
	if l.beforeOwnerWrite != nil {
		l.beforeOwnerWrite()
	}

	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown-host"
	}
	token := newFenceToken(hostname)
	ownerFile := ownerFilePrefix + newUniqueID()
	ownerPath := filepath.Join(l.lockPath, ownerFile)
	if err := os.WriteFile(ownerPath, []byte(token), 0600); err != nil {
		if isLostClaimError(err) {
			// Another process removed or moved the directory, so nothing at
			// the lock path is this instance's to clean up.
			return ErrLockConflict
		}
		// The directory is still this instance's. Remove it rather than leave
		// it empty, which would block others until it goes stale; removing
		// only an empty directory never removes a held lock.
		_ = fileutil.Remove(ownerPath)
		_ = fileutil.Remove(l.lockPath)
		if isRetryableLockStateError(err) {
			return ErrLockConflict
		}
		return fmt.Errorf("failed to write lock token: %w", err)
	}

	// Until the owner file exists the directory looks abandoned, so stale-lock
	// recovery may have removed it, and the owner file may have landed in a
	// directory another contender created or is taking over. Of the contenders
	// that wrote into one directory, only one can list it before the others
	// write, so the lock is held only if the listing shows this owner file and
	// no other owner or claim marker.
	entries, err := os.ReadDir(l.lockPath)
	if err != nil || !containsEntry(entries, ownerFile) || staleLockMarker(withoutEntry(entries, ownerFile)) != "" {
		l.abandonLock(ownerPath)
		if err != nil && !isLostClaimError(err) && !isRetryableLockStateError(err) {
			return fmt.Errorf("failed to verify lock directory: %w", err)
		}
		return ErrLockConflict
	}

	l.fenceToken = token
	l.ownerFile = ownerFile
	l.isHeld = true
	return nil
}

// abandonLock removes the owner file of a lock this instance failed to create.
// Only when that file was still in place is the directory this instance's to
// clean up, and then it is removed if empty. A held lock always contains its
// owner file, so removing an empty directory never removes a held lock.
func (l *dirLock) abandonLock(ownerPath string) {
	if fileutil.Remove(ownerPath) == nil {
		_ = fileutil.Remove(l.lockPath)
	}
}

func isRetryableLockStateError(err error) bool {
	if err == nil || runtime.GOOS != "windows" {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "access is denied") ||
		strings.Contains(msg, "cannot access the file") ||
		strings.Contains(msg, "sharing violation") ||
		strings.Contains(msg, "used by another process")
}

// Lock acquires lock, blocking until available or context is cancelled
func (l *dirLock) Lock(ctx context.Context) error {
	// Try once without blocking
	if err := l.TryLock(); err == nil {
		return nil
	} else if err != ErrLockConflict {
		return err
	}
	if l.opts.OnWait != nil {
		l.opts.OnWait()
	}

	// Set up retry with context
	ticker := time.NewTicker(l.opts.RetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := l.TryLock(); err == nil {
				return nil
			} else if err != ErrLockConflict {
				return err
			}
			// Continue retrying on lock conflict
		}
	}
}

// Unlock releases the lock
func (l *dirLock) Unlock() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.isHeld {
		return nil
	}

	// Releasing reads, renames and deletes entries of the lock directory and
	// then removes it, so give the owner access back first if it was lost.
	l.restoreLockDirAccess()

	// Verify we still own the lock before removing
	tokenPath := filepath.Join(l.lockPath, l.ownerFile)
	data, err := fileutil.ReadFile(tokenPath)
	if err != nil {
		l.isHeld = false
		return fmt.Errorf("%w: failed to read lock token: %v", ErrLockNotHeld, err)
	}
	if string(data) != l.fenceToken {
		// Lock was taken by another process — do NOT remove it
		l.isHeld = false
		return fmt.Errorf("%w: token mismatch (lock was acquired by another process)", ErrLockNotHeld)
	}

	// Claim our own instance so that only it can be removed, even if a stale
	// takeover replaces it concurrently.
	claimName := claimedFilePrefix + newUniqueID()
	if err := fileutil.Rename(tokenPath, filepath.Join(l.lockPath, claimName)); err != nil {
		if isLostClaimError(err) {
			l.isHeld = false
			return fmt.Errorf("%w: lock was taken over: %v", ErrLockNotHeld, err)
		}
		return fmt.Errorf("failed to remove lock directory: %w", err)
	}
	l.isHeld = false
	l.runAfterClaim()

	if err := releaseClaimedLock(l.lockPath, claimName); err != nil {
		if errors.Is(err, ErrLockConflict) {
			return fmt.Errorf("%w: lock was taken over while releasing", ErrLockNotHeld)
		}
		return fmt.Errorf("failed to remove lock directory: %w", err)
	}
	return nil
}

// IsLocked checks if directory is currently locked.
// This is an observe-only operation; stale locks are only cleaned up by TryLock.
func (l *dirLock) IsLocked() bool {
	info, err := os.Stat(l.lockPath)
	if err != nil {
		return false
	}
	return !l.isStaleInfo(info)
}

// IsHeldByMe checks if this instance holds the lock
func (l *dirLock) IsHeldByMe() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.isHeld {
		return false
	}

	// Verify the fence token still matches
	tokenPath := filepath.Join(l.lockPath, l.ownerFile)
	data, err := fileutil.ReadFile(tokenPath)
	if err != nil || string(data) != l.fenceToken {
		l.isHeld = false
		return false
	}
	return true
}

// Info returns information about current lock holder
func (l *dirLock) Info() (*LockInfo, error) {
	info, err := os.Stat(l.lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get lock info: %w", err)
	}

	if l.isStaleInfo(info) {
		// Lock is stale
		return nil, nil
	}

	return &LockInfo{
		AcquiredAt:  info.ModTime(),
		LockDirName: LockDirectoryName,
	}, nil
}

// ForceUnlock forcibly removes a lock (administrative operation)
func ForceUnlock(directory string) error {
	lockPath := filepath.Join(directory, LockDirectoryName)
	if err := removeLockDir(lockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to force unlock: %w", err)
	}
	return nil
}

func removeLockDir(lockPath string) error {
	releasedPath := releasedLockPath(lockPath)
	if err := fileutil.Rename(lockPath, releasedPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	// The live lock name is released after the rename. Cleanup failures should
	// not keep other lock contenders blocked on a lock this process no longer
	// owns.
	_ = fileutil.RemoveAll(releasedPath)
	return nil
}

func releasedLockPath(lockPath string) string {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err == nil {
		return fmt.Sprintf("%s.releasing.%d.%s", lockPath, os.Getpid(), hex.EncodeToString(suffix[:]))
	}
	return fmt.Sprintf("%s.releasing.%d.%d", lockPath, os.Getpid(), time.Now().UnixNano())
}

// isStaleInfo checks if a lock is stale based on file info
func (l *dirLock) isStaleInfo(info os.FileInfo) bool {
	age := time.Since(info.ModTime())
	return age > l.opts.StaleThreshold
}

// isLostClaimError reports whether an operation inside the lock directory
// failed because another process moved or removed the directory first. macOS
// reports EINVAL when the directory is renamed or deleted during the operation.
func isLostClaimError(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, syscall.EINVAL)
}

func (l *dirLock) runAfterClaim() {
	if l.afterClaim != nil {
		l.afterClaim()
	}
}

func newUniqueID() string {
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err == nil {
		return hex.EncodeToString(suffix[:])
	}
	return fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
}

func newFenceToken(hostname string) string {
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err == nil {
		return fmt.Sprintf("%s-%d-%s", hostname, os.Getpid(), hex.EncodeToString(suffix[:]))
	}

	return fmt.Sprintf("%s-%d-%d", hostname, os.Getpid(), time.Now().UnixNano())
}
