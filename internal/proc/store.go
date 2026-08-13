// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package proc

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

var procSafeAttemptIDPattern = regexp.MustCompile(`^[-a-zA-Z0-9_]+$`)

// Store persists process heartbeats and provides the backend operations needed
// by Repository.
type Store interface {
	Validate(ctx context.Context) error
	WithLock(ctx context.Context, groupName string, fn func() error) error
	Acquire(ctx context.Context, groupName string, meta ProcMeta) (ProcHandle, error)
	ListEntries(ctx context.Context, groupName string) ([]ProcEntry, error)
	LatestHeartbeat(ctx context.Context, groupName string, dagRun ir.DAGRunRef) (*ProcHeartbeat, error)
	ListAllEntries(ctx context.Context) ([]ProcEntry, error)
	RemoveIfStale(ctx context.Context, entry ProcEntry) error
}

// LockError reports a failure to acquire a process-group lock.
type LockError struct {
	err error
}

// NewLockError classifies a backend lock-acquisition failure.
func NewLockError(err error) error {
	if err == nil {
		return nil
	}
	return &LockError{err: err}
}

func (e *LockError) Error() string { return e.err.Error() }
func (e *LockError) Unwrap() error { return e.err }

// IsLockError reports whether err represents lock acquisition failure.
func IsLockError(err error) bool {
	var target *LockError
	return errors.As(err, &target)
}

// ProcHandle represents a process that is associated with a dag-run.
type ProcHandle interface {
	// Stop stops the heartbeat for the process.
	Stop(ctx context.Context) error
}

// ProcMeta is a struct that holds metadata for a process.
type ProcMeta struct {
	StartedAt    int64
	Name         string
	DAGRunID     string
	AttemptID    string
	RootName     string
	RootDAGRunID string
}

// Validate checks whether the process metadata is complete and safe to persist.
func (m ProcMeta) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("proc meta name is required")
	}
	if err := ir.ValidateDAGRunID(m.DAGRunID); err != nil {
		return fmt.Errorf("invalid proc meta dag run id: %w", err)
	}
	if m.AttemptID == "" {
		return fmt.Errorf("proc meta attempt id is required")
	}
	if !procSafeAttemptIDPattern.MatchString(m.AttemptID) {
		return fmt.Errorf("proc meta attempt id must only contain alphanumeric characters, dashes, and underscores")
	}
	if m.StartedAt <= 0 {
		return fmt.Errorf("proc meta started at must be > 0")
	}
	if (m.RootName == "") != (m.RootDAGRunID == "") {
		return fmt.Errorf("proc meta root name and root dag run id must both be set or both be empty")
	}
	if m.RootDAGRunID != "" {
		if err := ir.ValidateDAGRunID(m.RootDAGRunID); err != nil {
			return fmt.Errorf("invalid proc meta root dag run id: %w", err)
		}
	}
	return nil
}

// Root returns the root DAG-run reference if present.
func (m ProcMeta) Root() ir.DAGRunRef {
	if m.RootName == "" || m.RootDAGRunID == "" {
		return ir.DAGRunRef{}
	}
	return ir.NewDAGRunRef(m.RootName, m.RootDAGRunID)
}

// DAGRun returns the DAG-run reference for the proc entry.
func (m ProcMeta) DAGRun() ir.DAGRunRef {
	return ir.NewDAGRunRef(m.Name, m.DAGRunID)
}

// ProcEntry represents a storage-independent proc heartbeat observation.
type ProcEntry struct {
	GroupName       string
	Identity        ProcEntryID
	Meta            ProcMeta
	LastHeartbeatAt int64
	Fresh           bool
}

// ProcEntryID is an opaque identity returned by Repository for exact stale-entry
// removal. Callers must not interpret it as a filesystem path or record key.
type ProcEntryID struct {
	token string
}

// NewProcEntryID creates an opaque proc entry identity token.
func NewProcEntryID(token string) ProcEntryID {
	return ProcEntryID{token: token}
}

// NewStoreEntryID creates an opaque identity for a backend record.
func NewStoreEntryID(kind, value string) ProcEntryID {
	if kind == "" || value == "" {
		return ProcEntryID{}
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(value))
	return ProcEntryID{token: kind + ":" + encoded}
}

// IsZero reports whether the identity is empty.
func (id ProcEntryID) IsZero() bool {
	return id.token == ""
}

// String returns an opaque display token. Callers must not parse it.
func (id ProcEntryID) String() string {
	return id.token
}

// StoreValue returns the backend value when the identity belongs to kind.
func (id ProcEntryID) StoreValue(kind string) (string, bool) {
	actualKind, encoded, found := strings.Cut(id.token, ":")
	if !found || actualKind != kind || encoded == "" {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 {
		return "", false
	}
	return string(decoded), true
}

// ProcHeartbeat is a storage-independent observation of a proc heartbeat.
type ProcHeartbeat struct {
	GroupName       string
	DAGRun          ir.DAGRunRef
	AttemptID       string
	StartedAt       int64
	LastHeartbeatAt int64
	ObservedAt      time.Time
	Fresh           bool
}

// AdvancedSince reports whether this observation is newer than previous.
func (h ProcHeartbeat) AdvancedSince(previous ProcHeartbeat) bool {
	if h.GroupName != previous.GroupName || h.DAGRun != previous.DAGRun {
		return false
	}
	if h.AttemptID != previous.AttemptID {
		return h.StartedAt > previous.StartedAt || h.ObservedAt.After(previous.ObservedAt)
	}
	return h.LastHeartbeatAt > previous.LastHeartbeatAt || h.ObservedAt.After(previous.ObservedAt)
}

// PreferredTo reports whether this is the preferred latest observation.
func (h ProcHeartbeat) PreferredTo(other ProcHeartbeat) bool {
	if h.Fresh != other.Fresh {
		return h.Fresh
	}
	if h.StartedAt != other.StartedAt {
		return h.StartedAt > other.StartedAt
	}
	if h.LastHeartbeatAt != other.LastHeartbeatAt {
		return h.LastHeartbeatAt > other.LastHeartbeatAt
	}
	if !h.ObservedAt.Equal(other.ObservedAt) {
		return h.ObservedAt.After(other.ObservedAt)
	}
	return h.AttemptID < other.AttemptID
}

// DAGRun returns the DAG-run reference for the proc entry.
func (e ProcEntry) DAGRun() ir.DAGRunRef {
	return e.Meta.DAGRun()
}

// Heartbeat returns a heartbeat observation for the entry.
func (e ProcEntry) Heartbeat(observedAt time.Time) ProcHeartbeat {
	return ProcHeartbeat{
		GroupName:       e.GroupName,
		DAGRun:          e.Meta.DAGRun(),
		AttemptID:       e.Meta.AttemptID,
		StartedAt:       e.Meta.StartedAt,
		LastHeartbeatAt: e.LastHeartbeatAt,
		ObservedAt:      observedAt,
		Fresh:           e.Fresh,
	}
}

// IsRoot reports whether the proc entry belongs to a root DAG run.
func (e ProcEntry) IsRoot() bool {
	return e.Meta.RootName == e.Meta.Name && e.Meta.RootDAGRunID == e.Meta.DAGRunID
}

// AttemptKey returns a stable identifier for the exact proc-backed attempt.
func (e ProcEntry) AttemptKey() string {
	return e.GroupName + "|" + e.Meta.Root().String() + "|" + e.Meta.Name + "|" + e.Meta.DAGRunID + "|" + e.Meta.AttemptID
}

// RunScopeKey returns a stable identifier for the DAG-run scope across attempts.
func (e ProcEntry) RunScopeKey() string {
	return e.GroupName + "|" + e.Meta.Root().String() + "|" + e.Meta.Name + "|" + e.Meta.DAGRunID
}

// SameObservation reports whether two entries describe the same stored heartbeat observation.
func (e ProcEntry) SameObservation(other ProcEntry) bool {
	return e.GroupName == other.GroupName &&
		e.Identity == other.Identity &&
		e.LastHeartbeatAt == other.LastHeartbeatAt &&
		e.Meta == other.Meta
}
