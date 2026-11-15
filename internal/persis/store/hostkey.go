// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ inventory.HostKeyStore = (*HostKeyStore)(nil)

// HostKeyStore implements [inventory.HostKeyStore] over a [persis.Collection],
// one record per node keyed by its lowercased name.
type HostKeyStore struct {
	col persis.Collection
}

type hostKeyStoredRecord struct {
	// SchemaVersion is the record's format.
	SchemaVersion int       `json:"schema_version"`
	Node          string    `json:"node"`
	Key           string    `json:"key"`
	Fingerprint   string    `json:"fingerprint"`
	TrustedBy     string    `json:"trusted_by"`
	TrustedAt     time.Time `json:"trusted_at"`
}

// hostKeySchemaVersion is the record format this version writes. A newer
// format is refused on read.
const hostKeySchemaVersion = 1

// NewHostKeyStore creates a HostKeyStore backed by col.
func NewHostKeyStore(col persis.Collection) (*HostKeyStore, error) {
	if col == nil {
		return nil, errors.New("host key store: collection cannot be nil")
	}
	return &HostKeyStore{col: col}, nil
}

func (s *HostKeyStore) Get(ctx context.Context, node string) (inventory.TrustedHostKey, error) {
	rec, err := s.col.Get(ctx, hostKeyRecordID(node))
	if errors.Is(err, persis.ErrNotFound) {
		return inventory.TrustedHostKey{}, inventory.ErrNoTrustedHostKey
	}
	if err != nil {
		return inventory.TrustedHostKey{}, fmt.Errorf("host key store: read the key of node %q: %w", node, err)
	}
	var payload hostKeyStoredRecord
	if err := persis.Decode(rec, &payload); err != nil {
		return inventory.TrustedHostKey{}, fmt.Errorf("host key store: decode the key of node %q: %w", node, err)
	}
	if payload.SchemaVersion > hostKeySchemaVersion {
		return inventory.TrustedHostKey{}, fmt.Errorf("host key store: the key of node %q has schema version %d, newer than %d, which this version of Dagu reads",
			node, payload.SchemaVersion, hostKeySchemaVersion)
	}
	return inventory.TrustedHostKey{
		Node:        payload.Node,
		Key:         payload.Key,
		Fingerprint: payload.Fingerprint,
		TrustedBy:   payload.TrustedBy,
		TrustedAt:   payload.TrustedAt,
	}, nil
}

func (s *HostKeyStore) Trust(ctx context.Context, node string, key ssh.PublicKey, by string) (inventory.TrustedHostKey, error) {
	trusted, err := inventory.NewTrustedHostKey(node, key, by, time.Now().UTC())
	if err != nil {
		return inventory.TrustedHostKey{}, err
	}
	h := recordHelper{col: s.col, name: "host key store"}
	if err := h.put(ctx, hostKeyRecordID(node), hostKeyStoredRecord{
		SchemaVersion: hostKeySchemaVersion,
		Node:          trusted.Node,
		Key:           trusted.Key,
		Fingerprint:   trusted.Fingerprint,
		TrustedBy:     trusted.TrustedBy,
		TrustedAt:     trusted.TrustedAt,
	}); err != nil {
		return inventory.TrustedHostKey{}, err
	}
	return trusted, nil
}

// hostKeyRecordID keys a node's record by its name without case, as node
// names are compared, so file systems that ignore case keep one file.
func hostKeyRecordID(node string) string {
	return strings.ToLower(node)
}
