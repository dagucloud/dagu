// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"math/big"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

const (
	base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	base62UUIDLen  = 22
	maxDAGRunIDLen = 64
)

var (
	reDAGRunID            = regexp.MustCompile(`^[-a-zA-Z0-9_]+$`)
	ErrInvalidRunRefFormat = errors.New("invalid dag-run reference format")
)

// DAGRunRef identifies a DAG run.
type DAGRunRef struct {
	Name string `json:"name,omitempty"`
	ID   string `json:"id,omitempty"`
}

// NewDAGRunRef creates a DAG-run reference.
func NewDAGRunRef(name, runID string) DAGRunRef {
	return DAGRunRef{Name: name, ID: runID}
}

// String returns the reference in name:runId form.
func (r DAGRunRef) String() string {
	return r.Name + ":" + r.ID
}

// Zero reports whether the reference is empty.
func (r DAGRunRef) Zero() bool {
	return r == DAGRunRef{}
}

// NewDAGRunID returns a compact UUIDv7-derived DAG-run ID.
func NewDAGRunID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return encodeBase62UUID(id), nil
}

// ValidateDAGRunID validates a DAG-run identifier.
func ValidateDAGRunID(dagRunID string) error {
	if dagRunID == "" {
		return fmt.Errorf("dag-run ID must not be empty")
	}
	if !reDAGRunID.MatchString(dagRunID) {
		return fmt.Errorf("dag-run ID must only contain alphanumeric characters, dashes, and underscores")
	}
	if len(dagRunID) > maxDAGRunIDLen {
		return fmt.Errorf("dag-run ID length must be less than %d characters", maxDAGRunIDLen)
	}
	return nil
}

// ParseDAGRunRef parses a name:runId reference.
func ParseDAGRunRef(value string) (DAGRunRef, error) {
	name, dagRunID, found := strings.Cut(value, ":")
	if !found {
		return DAGRunRef{}, ErrInvalidRunRefFormat
	}
	if name == "" {
		return DAGRunRef{}, fmt.Errorf("%w: DAG name must not be empty", ErrInvalidRunRefFormat)
	}
	if err := ValidateDAGRunID(dagRunID); err != nil {
		return DAGRunRef{}, fmt.Errorf("%w: %w", ErrInvalidRunRefFormat, err)
	}
	return NewDAGRunRef(name, dagRunID), nil
}

// GenerateAttemptKey creates a globally unique attempt identifier.
func GenerateAttemptKey(rootName, rootID, dagName, dagRunID, attemptID string) string {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(rootName + "\x00" + rootID + "\x00" + dagName + "\x00" + dagRunID))
	return hex.EncodeToString(hash.Sum(nil)) + ":" + attemptID
}

func encodeBase62UUID(id uuid.UUID) string {
	var value big.Int
	value.SetBytes(id[:])

	base := big.NewInt(int64(len(base62Alphabet)))
	var remainder big.Int
	result := make([]byte, base62UUIDLen)
	for i := len(result) - 1; i >= 0; i-- {
		value.DivMod(&value, base, &remainder)
		result[i] = base62Alphabet[remainder.Int64()]
	}
	return string(result)
}
