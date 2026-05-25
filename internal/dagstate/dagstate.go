// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package dagstate defines persistent state shared across DAG runs.
package dagstate

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
	recordIDVersion = "v1"

	// ScopeDAG stores state under the current DAG name.
	ScopeDAG Scope = "dag"
	// ScopeRootDAG stores state under the root DAG name for nested runs.
	ScopeRootDAG Scope = "root_dag"
	// ScopeGlobal stores state in a process-wide namespace.
	ScopeGlobal Scope = "global"
	// ScopeCustom stores state in an explicitly provided namespace.
	ScopeCustom Scope = "custom"

	// DefaultGlobalNamespace is used when global state does not need a user namespace.
	DefaultGlobalNamespace = "_"
	// DefaultMaxValueBytes is the maximum normalized JSON payload size for one state entry.
	DefaultMaxValueBytes = 1 << 20
)

var (
	ErrNotFound      = errors.New("dag state: not found")
	ErrConflict      = errors.New("dag state: conflict")
	ErrInvalidRef    = errors.New("dag state: invalid ref")
	ErrInvalidValue  = errors.New("dag state: invalid value")
	ErrValueTooLarge = errors.New("dag state: value too large")
)

// Scope identifies the namespace strategy for a state entry.
type Scope string

// Ref identifies one persistent DAG state entry.
type Ref struct {
	Scope     Scope  `json:"scope"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

// UpdateSource records the DAG run and step that last updated an entry.
type UpdateSource struct {
	DAGName   string `json:"dag_name,omitempty"`
	DAGRunID  string `json:"dag_run_id,omitempty"`
	AttemptID string `json:"attempt_id,omitempty"`
	StepName  string `json:"step_name,omitempty"`
}

// Entry is a versioned JSON value stored for a state reference.
type Entry struct {
	Ref
	Value     json.RawMessage `json:"value"`
	Version   int64           `json:"version"`
	Hash      string          `json:"hash"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	UpdatedBy *UpdateSource   `json:"updated_by,omitempty"`
}

// PutOptions controls optimistic concurrency and audit metadata for writes.
type PutOptions struct {
	ExpectedVersion *int64
	CreateOnly      bool
	UpdatedBy       *UpdateSource
}

// ListOptions filters state entries by scope, namespace, and key prefix.
type ListOptions struct {
	Scope     Scope
	Namespace string
	KeyPrefix string
	Limit     int
}

// Validate rejects malformed list filters before store access.
func (o ListOptions) Validate() error {
	if !o.Scope.Valid() {
		return fmt.Errorf("%w: unsupported scope %q", ErrInvalidRef, o.Scope)
	}
	if err := validatePathPart("namespace", o.Namespace); err != nil {
		return err
	}
	if o.Limit < 0 {
		return fmt.Errorf("%w: limit must be greater than or equal to zero", ErrInvalidRef)
	}
	if o.Limit > 1<<31-1 {
		return fmt.Errorf("%w: limit exceeds %d", ErrInvalidRef, 1<<31-1)
	}
	return ValidateKeyPrefix(o.KeyPrefix)
}

// Store persists JSON state entries across DAG runs.
type Store interface {
	Get(ctx context.Context, ref Ref) (*Entry, error)
	Put(ctx context.Context, ref Ref, value json.RawMessage, opts PutOptions) (*Entry, error)
	Delete(ctx context.Context, ref Ref) (bool, error)
	List(ctx context.Context, opts ListOptions) ([]*Entry, error)
}

// Validate rejects malformed state references.
func (r Ref) Validate() error {
	if !r.Scope.Valid() {
		return fmt.Errorf("%w: unsupported scope %q", ErrInvalidRef, r.Scope)
	}
	if err := validatePathPart("namespace", r.Namespace); err != nil {
		return err
	}
	return validateKey("key", r.Key)
}

// RecordID returns the stable storage key for the reference.
func (r Ref) RecordID() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	return recordIDVersion + "/" + string(r.Scope) + "/" + encodeRecordIDPart(r.Namespace) + "/" + encodeRecordIDPart(r.Key), nil
}

// RefFromRecordID parses a storage key back into a state reference.
func RefFromRecordID(id string) (Ref, error) {
	if ref, ok, err := refFromVersionedRecordID(id); ok || err != nil {
		return ref, err
	}

	parts := strings.SplitN(id, "/", 3)
	if len(parts) != 3 {
		return Ref{}, fmt.Errorf("%w: malformed record id", ErrInvalidRef)
	}
	ref := Ref{Scope: Scope(parts[0]), Namespace: parts[1], Key: parts[2]}
	return ref, ref.Validate()
}

func refFromVersionedRecordID(id string) (Ref, bool, error) {
	parts := strings.SplitN(id, "/", 4)
	if len(parts) != 4 || parts[0] != recordIDVersion {
		return Ref{}, false, nil
	}
	namespace, err := decodeRecordIDPart(parts[2])
	if err != nil {
		return Ref{}, true, err
	}
	key, err := decodeRecordIDPart(parts[3])
	if err != nil {
		return Ref{}, true, err
	}
	ref := Ref{Scope: Scope(parts[1]), Namespace: namespace, Key: key}
	return ref, true, ref.Validate()
}

// RecordIDPrefix returns the storage prefix for a list filter.
func (o ListOptions) RecordIDPrefix() (string, error) {
	if err := o.Validate(); err != nil {
		return "", err
	}
	return recordIDVersion + "/" + string(o.Scope) + "/" + encodeRecordIDPart(o.Namespace) + "/" + encodeRecordIDPart(o.KeyPrefix), nil
}

// Valid reports whether the scope is supported.
func (s Scope) Valid() bool {
	switch s {
	case ScopeDAG, ScopeRootDAG, ScopeGlobal, ScopeCustom:
		return true
	default:
		return false
	}
}

// NormalizeValue validates and compacts a JSON value before storage.
func NormalizeValue(data []byte) (json.RawMessage, error) {
	if len(data) > DefaultMaxValueBytes {
		return nil, fmt.Errorf("%w: %d bytes exceeds %d", ErrValueTooLarge, len(data), DefaultMaxValueBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidValue, err)
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidValue, err)
	}

	normalized, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidValue, err)
	}
	if len(normalized) > DefaultMaxValueBytes {
		return nil, fmt.Errorf("%w: %d bytes exceeds %d", ErrValueTooLarge, len(normalized), DefaultMaxValueBytes)
	}
	return json.RawMessage(normalized), nil
}

// HashValue returns the SHA-256 hash of a normalized state value.
func HashValue(value json.RawMessage) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

// Clone returns a deep copy of the entry.
func (e *Entry) Clone() *Entry {
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
func (u *UpdateSource) Clone() *UpdateSource {
	if u == nil {
		return nil
	}
	cp := *u
	return &cp
}

func validatePathPart(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidRef, name)
	}
	if strings.ContainsAny(value, `/\`) || value == "." || value == ".." {
		return fmt.Errorf("%w: invalid %s %q", ErrInvalidRef, name, value)
	}
	return nil
}

func validateKey(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidRef, name)
	}
	if strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, `\`) {
		return fmt.Errorf("%w: invalid %s %q", ErrInvalidRef, name, value)
	}
	for part := range strings.SplitSeq(value, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%w: invalid %s %q", ErrInvalidRef, name, value)
		}
	}
	return nil
}

func encodeRecordIDPart(value string) string {
	return hex.EncodeToString([]byte(value))
}

func decodeRecordIDPart(value string) (string, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("%w: malformed record id", ErrInvalidRef)
	}
	return string(decoded), nil
}

// ValidateKeyPrefix rejects malformed key prefixes for list filters.
func ValidateKeyPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if strings.HasPrefix(prefix, "/") || strings.Contains(prefix, `\`) {
		return fmt.Errorf("%w: invalid key prefix %q", ErrInvalidRef, prefix)
	}
	trimmed := strings.TrimSuffix(prefix, "/")
	if trimmed == "" {
		return nil
	}
	return validateKey("key_prefix", trimmed)
}
