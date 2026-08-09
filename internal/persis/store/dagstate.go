// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagstate"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

const dagStateRecordIDVersion = "v1"

var _ dagstate.Store = (*DAGStateStore)(nil)

// DAGStateStore persists DAG state entries in a persis collection.
type DAGStateStore struct {
	col persis.Collection
	mu  sync.Mutex
}

type lockableCollection interface {
	WithLock(ctx context.Context, key string, fn func() error) error
}

type recordIDCollection interface {
	RecordIDs(ctx context.Context, prefix string) ([]string, error)
}

// NewDAGStateStore returns a DAG state store backed by the provided collection.
func NewDAGStateStore(col persis.Collection) *DAGStateStore {
	return &DAGStateStore{col: col}
}

func (s *DAGStateStore) Get(ctx context.Context, ref dagstate.Ref) (*dagstate.Entry, error) {
	id, err := dagStateRecordID(ref)
	if err != nil {
		return nil, err
	}
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		return nil, mapDAGStateStoreError(err)
	}
	return decodeDAGStateRecord(rec)
}

func (s *DAGStateStore) Put(ctx context.Context, ref dagstate.Ref, value json.RawMessage, opts dagstate.PutOptions) (*dagstate.Entry, error) {
	id, err := dagStateRecordID(ref)
	if err != nil {
		return nil, err
	}
	normalized, err := dagstate.NormalizeValue(value)
	if err != nil {
		return nil, err
	}

	var out *dagstate.Entry
	err = s.withRecordLock(ctx, id, func() error {
		now := time.Now().UTC()
		rec, getErr := s.col.Get(ctx, id)
		if getErr != nil {
			if !errors.Is(getErr, persis.ErrNotFound) {
				return mapDAGStateStoreError(getErr)
			}
			if opts.ExpectedVersion != nil && *opts.ExpectedVersion != 0 {
				return dagstate.ErrConflict
			}
			entry := &dagstate.Entry{
				Ref:       ref,
				Value:     append(json.RawMessage(nil), normalized...),
				Version:   1,
				Hash:      dagstate.HashValue(normalized),
				CreatedAt: now,
				UpdatedAt: now,
				UpdatedBy: opts.UpdatedBy.Clone(),
			}
			if err := s.putEntry(ctx, id, entry, now, now); err != nil {
				return err
			}
			out = entry.Clone()
			return nil
		}

		existing, err := decodeDAGStateRecord(rec)
		if err != nil {
			return err
		}
		if opts.CreateOnly {
			return dagstate.ErrConflict
		}
		if opts.ExpectedVersion != nil && existing.Version != *opts.ExpectedVersion {
			return dagstate.ErrConflict
		}

		entry := &dagstate.Entry{
			Ref:       ref,
			Value:     append(json.RawMessage(nil), normalized...),
			Version:   existing.Version + 1,
			Hash:      dagstate.HashValue(normalized),
			CreatedAt: existing.CreatedAt,
			UpdatedAt: now,
			UpdatedBy: opts.UpdatedBy.Clone(),
		}
		if err := s.putEntry(ctx, id, entry, existing.CreatedAt, now); err != nil {
			return err
		}
		out = entry.Clone()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *DAGStateStore) Delete(ctx context.Context, ref dagstate.Ref) (bool, error) {
	id, err := dagStateRecordID(ref)
	if err != nil {
		return false, err
	}

	var deleted bool
	err = s.withRecordLock(ctx, id, func() error {
		if _, err := s.col.Get(ctx, id); err != nil {
			if errors.Is(err, persis.ErrNotFound) {
				deleted = false
				return nil
			}
			return mapDAGStateStoreError(err)
		}
		if err := s.col.Delete(ctx, id); err != nil {
			return mapDAGStateStoreError(err)
		}
		deleted = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}

func (s *DAGStateStore) List(ctx context.Context, opts dagstate.ListOptions) ([]*dagstate.Entry, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	prefix, err := dagStateRecordIDPrefix(opts)
	if err != nil {
		return nil, err
	}
	if idCol, ok := s.col.(recordIDCollection); ok {
		ids, err := idCol.RecordIDs(ctx, prefix)
		if err != nil {
			return nil, mapDAGStateStoreError(err)
		}
		sort.Strings(ids)
		if opts.Limit > 0 && len(ids) > opts.Limit {
			ids = ids[:opts.Limit]
		}
		entries := make([]*dagstate.Entry, 0, len(ids))
		for _, id := range ids {
			rec, err := s.col.Get(ctx, id)
			if err != nil {
				if errors.Is(err, persis.ErrNotFound) {
					continue
				}
				return nil, mapDAGStateStoreError(err)
			}
			entry, err := decodeDAGStateRecord(rec)
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
		}
		return entries, nil
	}

	recs, err := listAll(ctx, s.col, persis.ListQuery{Prefix: prefix})
	if err != nil {
		return nil, mapDAGStateStoreError(err)
	}
	entries := make([]*dagstate.Entry, 0, len(recs))
	for _, rec := range recs {
		entry, err := decodeDAGStateRecord(rec)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Scope != entries[j].Scope {
			return entries[i].Scope < entries[j].Scope
		}
		if entries[i].Namespace != entries[j].Namespace {
			return entries[i].Namespace < entries[j].Namespace
		}
		return entries[i].Key < entries[j].Key
	})
	if opts.Limit > 0 && len(entries) > opts.Limit {
		entries = entries[:opts.Limit]
	}
	return entries, nil
}

func (s *DAGStateStore) withRecordLock(ctx context.Context, id string, fn func() error) error {
	if locked, ok := s.col.(lockableCollection); ok {
		return locked.WithLock(ctx, id, fn)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn()
}

func (s *DAGStateStore) putEntry(ctx context.Context, id string, entry *dagstate.Entry, createdAt, updatedAt time.Time) error {
	data, err := persis.Encode(entry)
	if err != nil {
		return err
	}
	return mapDAGStateStoreError(s.col.Put(ctx, &persis.Record{
		ID:        id,
		Data:      data,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}))
}

func decodeDAGStateRecord(rec *persis.Record) (*dagstate.Entry, error) {
	var entry dagstate.Entry
	if err := persis.Decode(rec, &entry); err != nil {
		return nil, fmt.Errorf("dag state store: decode %q: %w", rec.ID, err)
	}
	ref, err := dagStateRefFromRecordID(rec.ID)
	if err != nil {
		return nil, fmt.Errorf("dag state store: decode %q: %w", rec.ID, err)
	}
	entry.Ref = ref
	return entry.Clone(), nil
}

func dagStateRecordID(ref dagstate.Ref) (string, error) {
	if err := ref.Validate(); err != nil {
		return "", err
	}
	return dagStateRecordIDVersion + "/" + string(ref.Scope) + "/" + encodeDAGStateRecordIDPart(ref.Namespace) + "/" + encodeDAGStateRecordIDPart(ref.Key), nil
}

func dagStateRefFromRecordID(id string) (dagstate.Ref, error) {
	parts := strings.SplitN(id, "/", 4)
	if len(parts) != 4 || parts[0] != dagStateRecordIDVersion {
		return dagstate.Ref{}, fmt.Errorf("%w: malformed record id", dagstate.ErrInvalidRef)
	}
	namespace, err := decodeDAGStateRecordIDPart(parts[2])
	if err != nil {
		return dagstate.Ref{}, err
	}
	key, err := decodeDAGStateRecordIDPart(parts[3])
	if err != nil {
		return dagstate.Ref{}, err
	}
	ref := dagstate.Ref{Scope: dagstate.Scope(parts[1]), Namespace: namespace, Key: key}
	return ref, ref.Validate()
}

func dagStateRecordIDPrefix(opts dagstate.ListOptions) (string, error) {
	if err := opts.Validate(); err != nil {
		return "", err
	}
	return dagStateRecordIDVersion + "/" + string(opts.Scope) + "/" + encodeDAGStateRecordIDPart(opts.Namespace) + "/" + encodeDAGStateRecordIDPart(opts.KeyPrefix), nil
}

func encodeDAGStateRecordIDPart(value string) string {
	return hex.EncodeToString([]byte(value))
}

func decodeDAGStateRecordIDPart(value string) (string, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("%w: malformed record id", dagstate.ErrInvalidRef)
	}
	return string(decoded), nil
}

func mapDAGStateStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, persis.ErrNotFound):
		return dagstate.ErrNotFound
	case errors.Is(err, persis.ErrConflict):
		return dagstate.ErrConflict
	default:
		return err
	}
}
