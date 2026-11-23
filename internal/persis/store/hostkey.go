// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"cmp"
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
	return decodeHostKey(rec, node)
}

func decodeHostKey(rec *persis.Record, node string) (inventory.TrustedHostKey, error) {
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

func (s *HostKeyStore) Trust(ctx context.Context, node string, key ssh.PublicKey, by, replacing string) (inventory.TrustedHostKey, error) {
	trusted, err := inventory.NewTrustedHostKey(node, key, by, time.Now().UTC())
	if err != nil {
		return inventory.TrustedHostKey{}, err
	}
	data, err := persis.Encode(hostKeyStoredRecord{
		SchemaVersion: hostKeySchemaVersion,
		Node:          trusted.Node,
		Key:           trusted.Key,
		Fingerprint:   trusted.Fingerprint,
		TrustedBy:     trusted.TrustedBy,
		TrustedAt:     trusted.TrustedAt,
	})
	if err != nil {
		return inventory.TrustedHostKey{}, fmt.Errorf("host key store: encode the key of node %q: %w", node, err)
	}
	id := hostKeyRecordID(node)
	// The trusted key is compared and swapped in one write, so two trusts of
	// one node cannot both replace a key neither saw.
	err = retryConflict(ctx, func(ctx context.Context) error {
		existing, err := s.col.Get(ctx, id)
		if err != nil && !errors.Is(err, persis.ErrNotFound) {
			return err
		}
		var current string
		createdAt := trusted.TrustedAt
		if existing != nil {
			stored, err := decodeHostKey(existing, node)
			if err != nil {
				return err
			}
			current, createdAt = stored.Fingerprint, existing.CreatedAt
		}
		if current != replacing {
			return fmt.Errorf("%w: node %q trusts %s", inventory.ErrTrustedHostKeyChanged, node, cmp.Or(current, "no key"))
		}
		return createOrSwap(ctx, s.col, existing, &persis.Record{ID: id, Data: data, CreatedAt: createdAt, UpdatedAt: trusted.TrustedAt})
	})
	if err != nil {
		return inventory.TrustedHostKey{}, fmt.Errorf("host key store: trust the key of node %q: %w", node, err)
	}
	return trusted, nil
}

func (s *HostKeyStore) Forget(ctx context.Context, node string) error {
	if err := s.col.Delete(ctx, hostKeyRecordID(node)); err != nil {
		return fmt.Errorf("host key store: forget the key of node %q: %w", node, err)
	}
	return nil
}

// hostKeyRecordID keys a node's record by its name without case, as node
// names are compared, so file systems that ignore case keep one file.
func hostKeyRecordID(node string) string {
	return strings.ToLower(node)
}
