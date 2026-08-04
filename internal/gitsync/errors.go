// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync

import (
	"errors"
	"fmt"
)

// Common errors for Git sync operations.
var (
	// ErrNotEnabled is returned when Git sync is not enabled.
	ErrNotEnabled = errors.New("git sync is not enabled")

	// ErrNotConfigured is returned when Git sync is not properly configured.
	ErrNotConfigured = errors.New("git sync is not properly configured")

	// ErrRepoNotCloned is returned when the repository has not been cloned yet.
	ErrRepoNotCloned = errors.New("repository not cloned")

	// ErrAuthFailed is returned when authentication fails.
	ErrAuthFailed = errors.New("authentication failed")

	// ErrConflict is returned when a conflict is detected.
	ErrConflict = errors.New("conflict detected")

	// ErrOperationInProgress is returned when another sync operation is in progress.
	ErrOperationInProgress = errors.New("another sync operation is in progress")

	// ErrDAGNotFound is returned when the specified sync item is not found.
	ErrDAGNotFound = errors.New("sync item not found")

	// ErrInvalidDAGID is returned when the sync item ID format is invalid.
	ErrInvalidDAGID = errors.New("invalid sync item ID format")

	// ErrPushDisabled is returned when push operations are disabled.
	ErrPushDisabled = errors.New("push operations are disabled")

	// ErrNoChanges is returned when there are no changes to publish.
	ErrNoChanges = errors.New("no changes to publish")

	// ErrCannotForget is returned when a sync item cannot be forgotten.
	ErrCannotForget = errors.New("sync item cannot be forgotten")

	// ErrCannotDeleteUntracked is returned when deleting an untracked sync item.
	ErrCannotDeleteUntracked = errors.New("untracked sync items cannot be deleted from remote — use forget instead")

	// ErrNetworkError is returned when a network operation fails.
	ErrNetworkError = errors.New("network error")
)

// ConflictError represents a conflict error with details.
type ConflictError struct {
	DAGID         string
	RemoteCommit  string
	RemoteAuthor  string
	RemoteMessage string
}

func (e *ConflictError) Error() string {
	commit := e.RemoteCommit
	if len(commit) > 8 {
		commit = commit[:8]
	}
	return fmt.Sprintf("conflict detected for sync item %q: remote commit %s by %s",
		e.DAGID, commit, e.RemoteAuthor)
}

func (e *ConflictError) Unwrap() error {
	return ErrConflict
}

// ValidationError represents a validation error with field details.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error for %s: %s", e.Field, e.Message)
}

// DAGNotFoundError represents a sync item not found error with the ID.
type DAGNotFoundError struct {
	DAGID string
}

func (e *DAGNotFoundError) Error() string {
	return fmt.Sprintf("sync item not found: %s", e.DAGID)
}

func (e *DAGNotFoundError) Unwrap() error {
	return ErrDAGNotFound
}

// NetworkError wraps network-related errors with context.
type NetworkError struct {
	Operation string
	Cause     error
}

func (e *NetworkError) Error() string {
	return fmt.Sprintf("network error during %s: %v", e.Operation, e.Cause)
}

func (e *NetworkError) Unwrap() error {
	return errors.Join(ErrNetworkError, e.Cause)
}

// InvalidDAGIDError represents an invalid sync item identifier.
type InvalidDAGIDError struct {
	DAGID  string
	Reason string
}

func (e *InvalidDAGIDError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("invalid sync item ID %q", e.DAGID)
	}
	return fmt.Sprintf("invalid sync item ID %q: %s", e.DAGID, e.Reason)
}

func (e *InvalidDAGIDError) Unwrap() error {
	return ErrInvalidDAGID
}

// IsConflict checks if the error is a conflict error.
func IsConflict(err error) bool {
	return errors.Is(err, ErrConflict)
}

// IsNotEnabled checks if the error indicates Git sync is not enabled.
func IsNotEnabled(err error) bool {
	return errors.Is(err, ErrNotEnabled)
}

// IsDAGNotFound checks if the error indicates a sync item was not found.
func IsDAGNotFound(err error) bool {
	return errors.Is(err, ErrDAGNotFound)
}

// IsInvalidDAGID checks if the error indicates a sync item ID is invalid.
func IsInvalidDAGID(err error) bool {
	return errors.Is(err, ErrInvalidDAGID)
}
