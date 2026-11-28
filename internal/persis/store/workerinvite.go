// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ persis.WorkerInviteStore = (*WorkerInviteStore)(nil)

// WorkerInviteStore implements [persis.WorkerInviteStore] over a
// [persis.Collection], one record per invite keyed by its ID.
type WorkerInviteStore struct {
	col persis.Collection
}

type workerInviteStoredRecord struct {
	// SchemaVersion is the record's format.
	SchemaVersion int                     `json:"schema_version"`
	ID            string                  `json:"id"`
	SecretHash    []byte                  `json:"secret_hash"`
	Kind          persis.WorkerInviteKind `json:"kind"`
	ExpiresAt     time.Time               `json:"expires_at,omitzero"`
	Address       string                  `json:"address,omitempty"`
	CreatedBy     string                  `json:"created_by"`
	CreatedAt     time.Time               `json:"created_at"`
	UseCount      int                     `json:"use_count"`
	LastUsedAt    time.Time               `json:"last_used_at,omitzero"`
}

const (
	// workerInviteSchemaVersion is the record format this version writes.
	workerInviteSchemaVersion = 1

	// workerInviteRetention is how long an ended invite's record stays.
	workerInviteRetention = 7 * 24 * time.Hour
)

// NewWorkerInviteStore creates a WorkerInviteStore backed by col.
func NewWorkerInviteStore(col persis.Collection) (*WorkerInviteStore, error) {
	if col == nil {
		return nil, errors.New("worker invite store: collection cannot be nil")
	}
	return &WorkerInviteStore{col: col}, nil
}

func (s *WorkerInviteStore) Create(ctx context.Context, invite persis.WorkerInvite) (persis.WorkerInvite, error) {
	if !invite.Kind.Valid() {
		return persis.WorkerInvite{}, fmt.Errorf("worker invite store: unknown kind %q", invite.Kind)
	}
	if len(invite.SecretHash) != sha256.Size {
		return persis.WorkerInvite{}, errors.New("worker invite store: secret hash is not a SHA-256")
	}
	invite.ID = uuid.NewString()
	rec, err := encodeWorkerInvite(invite)
	if err != nil {
		return persis.WorkerInvite{}, err
	}
	if err := s.col.Create(ctx, rec); err != nil {
		return persis.WorkerInvite{}, fmt.Errorf("worker invite store: create invite: %w", err)
	}
	return invite, nil
}

func (s *WorkerInviteStore) Get(ctx context.Context, id string) (persis.WorkerInvite, error) {
	rec, err := s.col.Get(ctx, id)
	if errors.Is(err, persis.ErrNotFound) {
		return persis.WorkerInvite{}, persis.ErrWorkerInviteNotFound
	}
	if err != nil {
		return persis.WorkerInvite{}, fmt.Errorf("worker invite store: read invite %q: %w", id, err)
	}
	return decodeWorkerInvite(rec)
}

func (s *WorkerInviteStore) GetBySecretHash(ctx context.Context, hash []byte) (persis.WorkerInvite, error) {
	invites, err := s.List(ctx)
	if err != nil {
		return persis.WorkerInvite{}, err
	}
	for _, invite := range invites {
		if subtle.ConstantTimeCompare(invite.SecretHash, hash) == 1 {
			return invite, nil
		}
	}
	return persis.WorkerInvite{}, persis.ErrWorkerInviteNotFound
}

func (s *WorkerInviteStore) List(ctx context.Context) ([]persis.WorkerInvite, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, fmt.Errorf("worker invite store: list: %w", err)
	}
	invites := make([]persis.WorkerInvite, 0, len(recs))
	for _, rec := range recs {
		// A record that cannot be read admits no enrollment, so it is
		// left out like a deleted invite.
		invite, err := decodeWorkerInvite(rec)
		if err != nil {
			continue
		}
		invites = append(invites, invite)
	}
	slices.SortFunc(invites, func(a, b persis.WorkerInvite) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	return invites, nil
}

func (s *WorkerInviteStore) Delete(ctx context.Context, id string) error {
	return recordHelper{col: s.col, name: "worker invite store"}.delete(ctx, id, persis.ErrWorkerInviteNotFound, "invite")
}

func (s *WorkerInviteStore) Use(ctx context.Context, id string, at time.Time) (persis.WorkerInvite, error) {
	var used persis.WorkerInvite
	// A conflict means another enrollment used the invite meanwhile, so
	// the invite is read again and checked again.
	err := retryConflict(ctx, func(ctx context.Context) error {
		current, err := s.col.Get(ctx, id)
		if errors.Is(err, persis.ErrNotFound) {
			return persis.ErrWorkerInviteNotFound
		}
		if err != nil {
			return fmt.Errorf("worker invite store: read invite %q: %w", id, err)
		}
		invite, err := decodeWorkerInvite(current)
		if err != nil {
			return err
		}
		if invite.Ended(at) {
			return persis.ErrWorkerInviteEnded
		}
		invite.UseCount++
		if at.After(invite.LastUsedAt) {
			invite.LastUsedAt = at
		}
		next, err := encodeWorkerInvite(invite)
		if err != nil {
			return err
		}
		err = s.col.CompareAndSwap(ctx, id, current.Data, next.Data)
		if errors.Is(err, persis.ErrNotFound) {
			return persis.ErrWorkerInviteNotFound
		}
		if err != nil {
			return err
		}
		used = invite
		return nil
	})
	if err != nil {
		return persis.WorkerInvite{}, err
	}
	return used, nil
}

func (s *WorkerInviteStore) Prune(ctx context.Context, now time.Time) (int, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return 0, fmt.Errorf("worker invite store: list: %w", err)
	}
	removed := 0
	var errs []error
	for _, rec := range recs {
		invite, err := decodeWorkerInvite(rec)
		if err != nil {
			continue
		}
		endedAt, ends := inviteEndedAt(invite)
		if !ends || now.Sub(endedAt) <= workerInviteRetention {
			continue
		}
		// Only the record as read is deleted. An ended invite changes only
		// when deleted, so a conflict means it is gone.
		err = s.col.CompareAndDelete(ctx, rec)
		if errors.Is(err, persis.ErrConflict) || errors.Is(err, persis.ErrNotFound) {
			continue
		}
		if err != nil {
			// One record that cannot be deleted leaves the others to go.
			errs = append(errs, fmt.Errorf("worker invite store: delete invite %q: %w", invite.ID, err))
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

// inviteEndedAt returns when invite ended or ends: at its use for a used
// single-use invite, else at its expiry. It returns false for an invite
// that never ends.
func inviteEndedAt(invite persis.WorkerInvite) (time.Time, bool) {
	if invite.Kind == persis.WorkerInviteSingleUse && invite.UseCount > 0 {
		return invite.LastUsedAt, true
	}
	return invite.ExpiresAt, !invite.ExpiresAt.IsZero()
}

func encodeWorkerInvite(invite persis.WorkerInvite) (*persis.Record, error) {
	data, err := persis.Encode(workerInviteStoredRecord{
		SchemaVersion: workerInviteSchemaVersion,
		ID:            invite.ID,
		SecretHash:    invite.SecretHash,
		Kind:          invite.Kind,
		ExpiresAt:     invite.ExpiresAt.UTC(),
		Address:       invite.Address,
		CreatedBy:     invite.CreatedBy,
		CreatedAt:     invite.CreatedAt.UTC(),
		UseCount:      invite.UseCount,
		LastUsedAt:    invite.LastUsedAt.UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("worker invite store: encode invite %q: %w", invite.ID, err)
	}
	return &persis.Record{ID: invite.ID, Data: data, CreatedAt: invite.CreatedAt, UpdatedAt: invite.CreatedAt}, nil
}

func decodeWorkerInvite(rec *persis.Record) (persis.WorkerInvite, error) {
	var payload workerInviteStoredRecord
	if err := persis.Decode(rec, &payload); err != nil {
		return persis.WorkerInvite{}, fmt.Errorf("worker invite store: decode %q: %w", rec.ID, err)
	}
	// An unknown kind could read as reusable, so the record is refused.
	if payload.ID == "" || !payload.Kind.Valid() {
		return persis.WorkerInvite{}, fmt.Errorf("worker invite store: record %q is not an invite", rec.ID)
	}
	return persis.WorkerInvite{
		ID:         payload.ID,
		SecretHash: payload.SecretHash,
		Kind:       payload.Kind,
		ExpiresAt:  payload.ExpiresAt,
		Address:    payload.Address,
		CreatedBy:  payload.CreatedBy,
		CreatedAt:  payload.CreatedAt,
		UseCount:   payload.UseCount,
		LastUsedAt: payload.LastUsedAt,
	}, nil
}
