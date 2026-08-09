// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	// StateScopeDAG stores state under the current DAG name.
	StateScopeDAG StateScope = "dag"
	// StateScopeRootDAG stores state under the root DAG name for nested runs.
	StateScopeRootDAG StateScope = "root_dag"
	// StateScopeGlobal stores state in a process-wide namespace.
	StateScopeGlobal StateScope = "global"
	// StateScopeCustom stores state in an explicitly provided namespace.
	StateScopeCustom StateScope = "custom"

	// DefaultGlobalStateNamespace is used when global state does not need a user namespace.
	DefaultGlobalStateNamespace = "_"
	// MaxStateValueBytes is the maximum normalized JSON payload size for one state entry.
	MaxStateValueBytes = 1 << 20
)

var (
	ErrStateNotFound      = errors.New("dag state: not found")
	ErrStateConflict      = errors.New("dag state: conflict")
	ErrInvalidStateRef    = errors.New("dag state: invalid ref")
	ErrInvalidStateValue  = errors.New("dag state: invalid value")
	ErrStateValueTooLarge = errors.New("dag state: value too large")
)

// StateScope identifies the namespace strategy for a state entry.
type StateScope string

// StateRef identifies one persistent DAG state entry.
type StateRef struct {
	Scope     StateScope `json:"scope"`
	Namespace string     `json:"namespace"`
	Key       string     `json:"key"`
}

// StateUpdateSource records the DAG run and step that last updated an entry.
type StateUpdateSource struct {
	DAGName   string `json:"dag_name,omitempty"`
	DAGRunID  string `json:"dag_run_id,omitempty"`
	AttemptID string `json:"attempt_id,omitempty"`
	StepName  string `json:"step_name,omitempty"`
}

// StateEntry is a versioned JSON value stored for a state reference.
type StateEntry struct {
	StateRef
	Value     json.RawMessage    `json:"value"`
	Version   int64              `json:"version"`
	Hash      string             `json:"hash"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	UpdatedBy *StateUpdateSource `json:"updated_by,omitempty"`
}

// StatePutOptions controls optimistic concurrency and audit metadata for writes.
type StatePutOptions struct {
	ExpectedVersion *int64
	CreateOnly      bool
	UpdatedBy       *StateUpdateSource
}

// StateListOptions filters state entries by scope, namespace, and key prefix.
type StateListOptions struct {
	Scope     StateScope
	Namespace string
	KeyPrefix string
	Limit     int
}

// Validate rejects malformed list filters before store access.
func (o StateListOptions) Validate() error {
	if !o.Scope.Valid() {
		return fmt.Errorf("%w: unsupported scope %q", ErrInvalidStateRef, o.Scope)
	}
	if err := validateStatePathPart("namespace", o.Namespace); err != nil {
		return err
	}
	if o.Limit < 0 {
		return fmt.Errorf("%w: limit must be greater than or equal to zero", ErrInvalidStateRef)
	}
	if o.Limit > 1<<31-1 {
		return fmt.Errorf("%w: limit exceeds %d", ErrInvalidStateRef, 1<<31-1)
	}
	return validateStateKeyPrefix(o.KeyPrefix)
}

// StateStore persists JSON state entries across DAG runs.
type StateStore interface {
	Get(ctx context.Context, ref StateRef) (*StateEntry, error)
	Put(ctx context.Context, ref StateRef, value json.RawMessage, opts StatePutOptions) (*StateEntry, error)
	Delete(ctx context.Context, ref StateRef) (bool, error)
	List(ctx context.Context, opts StateListOptions) ([]*StateEntry, error)
}

// Validate rejects malformed state references.
func (r StateRef) Validate() error {
	if !r.Scope.Valid() {
		return fmt.Errorf("%w: unsupported scope %q", ErrInvalidStateRef, r.Scope)
	}
	if err := validateStatePathPart("namespace", r.Namespace); err != nil {
		return err
	}
	return validateStateKey("key", r.Key)
}

// Valid reports whether the scope is supported.
func (s StateScope) Valid() bool {
	switch s {
	case StateScopeDAG, StateScopeRootDAG, StateScopeGlobal, StateScopeCustom:
		return true
	default:
		return false
	}
}

// NormalizeStateValue validates and compacts a JSON value before storage.
func NormalizeStateValue(data []byte) (json.RawMessage, error) {
	if len(data) > MaxStateValueBytes {
		return nil, fmt.Errorf("%w: %d bytes exceeds %d", ErrStateValueTooLarge, len(data), MaxStateValueBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidStateValue, err)
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidStateValue, err)
	}

	normalized, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidStateValue, err)
	}
	if len(normalized) > MaxStateValueBytes {
		return nil, fmt.Errorf("%w: %d bytes exceeds %d", ErrStateValueTooLarge, len(normalized), MaxStateValueBytes)
	}
	return json.RawMessage(normalized), nil
}

// HashStateValue returns the SHA-256 hash of a normalized state value.
func HashStateValue(value json.RawMessage) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

// Clone returns a deep copy of the entry.
func (e *StateEntry) Clone() *StateEntry {
	if e == nil {
		return nil
	}
	cp := *e
	if e.Value != nil {
		cp.Value = append(json.RawMessage(nil), e.Value...)
	}
	if e.UpdatedBy != nil {
		updatedBy := *e.UpdatedBy
		cp.UpdatedBy = &updatedBy
	}
	return &cp
}

// Clone returns a copy of the update source.
func (u *StateUpdateSource) Clone() *StateUpdateSource {
	if u == nil {
		return nil
	}
	cp := *u
	return &cp
}

func validateStatePathPart(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidStateRef, name)
	}
	if strings.ContainsAny(value, `/\`) || value == "." || value == ".." {
		return fmt.Errorf("%w: invalid %s %q", ErrInvalidStateRef, name, value)
	}
	return nil
}

func validateStateKey(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidStateRef, name)
	}
	if strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, `\`) {
		return fmt.Errorf("%w: invalid %s %q", ErrInvalidStateRef, name, value)
	}
	for part := range strings.SplitSeq(value, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%w: invalid %s %q", ErrInvalidStateRef, name, value)
		}
	}
	return nil
}

func validateStateKeyPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if strings.HasPrefix(prefix, "/") || strings.Contains(prefix, `\`) {
		return fmt.Errorf("%w: invalid key prefix %q", ErrInvalidStateRef, prefix)
	}
	trimmed := strings.TrimSuffix(prefix, "/")
	if trimmed == "" {
		return nil
	}
	return validateStateKey("key_prefix", trimmed)
}
