// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ persis.WorkerCertificateStore = (*WorkerCertificateStore)(nil)

// WorkerCertificateStore implements [persis.WorkerCertificateStore] over a
// [persis.Collection], one record per certificate keyed by its ID.
type WorkerCertificateStore struct {
	col persis.Collection
}

type workerCertificateStoredRecord struct {
	// SchemaVersion is the record's format.
	SchemaVersion   int                           `json:"schema_version"`
	ID              string                        `json:"id"`
	InviteID        string                        `json:"invite_id"`
	KeyFingerprint  []byte                        `json:"key_fingerprint"`
	State           persis.WorkerCertificateState `json:"state"`
	WorkerID        string                        `json:"worker_id,omitempty"`
	Host            string                        `json:"host,omitempty"`
	Environment     persis.WorkerEnvironment      `json:"environment,omitempty"`
	CreatedAt       time.Time                     `json:"created_at"`
	LastHeartbeatAt time.Time                     `json:"last_heartbeat_at,omitzero"`
}

const (
	// workerCertificateSchemaVersion is the record format this version
	// writes.
	workerCertificateSchemaVersion = 1

	// workerCertificateHeartbeatInterval bounds heartbeat writes to one a
	// day per certificate.
	workerCertificateHeartbeatInterval = 24 * time.Hour

	// workerCertificateRetention is how long an active certificate's
	// record stays without a heartbeat.
	workerCertificateRetention = 90 * 24 * time.Hour
)

// workerCertificateStateRank orders the states a certificate moves through.
var workerCertificateStateRank = map[persis.WorkerCertificateState]int{
	persis.WorkerCertificateActive:   0,
	persis.WorkerCertificateRemoving: 1,
	persis.WorkerCertificateRevoked:  2,
}

// NewWorkerCertificateStore creates a WorkerCertificateStore backed by col.
func NewWorkerCertificateStore(col persis.Collection) (*WorkerCertificateStore, error) {
	if col == nil {
		return nil, errors.New("worker certificate store: collection cannot be nil")
	}
	return &WorkerCertificateStore{col: col}, nil
}

func (s *WorkerCertificateStore) Create(ctx context.Context, cert persis.WorkerCertificate) error {
	cert.State = persis.WorkerCertificateActive
	rec, err := encodeWorkerCertificate(cert)
	if err != nil {
		return err
	}
	err = s.col.Create(ctx, rec)
	if errors.Is(err, persis.ErrConflict) {
		return persis.ErrWorkerCertificateExists
	}
	if err != nil {
		return fmt.Errorf("worker certificate store: create certificate %q: %w", cert.ID, err)
	}
	return nil
}

func (s *WorkerCertificateStore) Get(ctx context.Context, id string) (persis.WorkerCertificate, error) {
	rec, err := s.col.Get(ctx, id)
	if errors.Is(err, persis.ErrNotFound) {
		return persis.WorkerCertificate{}, persis.ErrWorkerCertificateNotFound
	}
	if err != nil {
		return persis.WorkerCertificate{}, fmt.Errorf("worker certificate store: read certificate %q: %w", id, err)
	}
	return decodeWorkerCertificate(rec)
}

func (s *WorkerCertificateStore) List(ctx context.Context) ([]persis.WorkerCertificate, error) {
	recs, err := listAllStrict(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, fmt.Errorf("worker certificate store: list: %w", err)
	}
	certs := make([]persis.WorkerCertificate, 0, len(recs))
	for _, rec := range recs {
		cert, err := decodeWorkerCertificate(rec)
		if err != nil {
			return nil, err
		}
		certs = append(certs, cert)
	}
	return certs, nil
}

func (s *WorkerCertificateStore) RecordHeartbeat(ctx context.Context, cert persis.WorkerCertificate) error {
	err := retryConflict(ctx, func(ctx context.Context) error {
		current, err := s.col.Get(ctx, cert.ID)
		if err != nil && !errors.Is(err, persis.ErrNotFound) {
			return err
		}
		next := cert
		next.State = persis.WorkerCertificateActive
		if current != nil {
			stored, err := decodeWorkerCertificate(current)
			if err != nil {
				return err
			}
			if !heartbeatWriteDue(stored, cert) {
				return nil
			}
			next = stored
			next.WorkerID = cert.WorkerID
			next.Host = cert.Host
			next.Environment = cert.Environment
			if cert.LastHeartbeatAt.After(stored.LastHeartbeatAt) {
				next.LastHeartbeatAt = cert.LastHeartbeatAt
			}
		}
		rec, err := encodeWorkerCertificate(next)
		if err != nil {
			return err
		}
		return createOrSwap(ctx, s.col, current, rec)
	})
	if err != nil {
		return fmt.Errorf("worker certificate store: record heartbeat of %q: %w", cert.ID, err)
	}
	return nil
}

// heartbeatWriteDue reports whether a heartbeat reported in cert changes the
// stored record enough to write it.
func heartbeatWriteDue(stored, cert persis.WorkerCertificate) bool {
	return cert.LastHeartbeatAt.Sub(stored.LastHeartbeatAt) > workerCertificateHeartbeatInterval ||
		cert.WorkerID != stored.WorkerID || cert.Host != stored.Host || cert.Environment != stored.Environment
}

func (s *WorkerCertificateStore) SetState(ctx context.Context, id string, state persis.WorkerCertificateState) (persis.WorkerCertificate, error) {
	rank, ok := workerCertificateStateRank[state]
	if !ok {
		return persis.WorkerCertificate{}, fmt.Errorf("worker certificate store: unknown state %q", state)
	}
	var result persis.WorkerCertificate
	err := retryConflict(ctx, func(ctx context.Context) error {
		current, err := s.col.Get(ctx, id)
		if errors.Is(err, persis.ErrNotFound) {
			return persis.ErrWorkerCertificateNotFound
		}
		if err != nil {
			return fmt.Errorf("worker certificate store: read certificate %q: %w", id, err)
		}
		cert, err := decodeWorkerCertificate(current)
		if err != nil {
			return err
		}
		switch currentRank := workerCertificateStateRank[cert.State]; {
		case rank < currentRank:
			return persis.ErrWorkerCertificateStateBackward
		case rank == currentRank:
			result = cert
			return nil
		}
		cert.State = state
		next, err := encodeWorkerCertificate(cert)
		if err != nil {
			return err
		}
		err = s.col.CompareAndSwap(ctx, id, current.Data, next.Data)
		if errors.Is(err, persis.ErrNotFound) {
			return persis.ErrWorkerCertificateNotFound
		}
		if err != nil {
			return err
		}
		result = cert
		return nil
	})
	if err != nil {
		return persis.WorkerCertificate{}, err
	}
	return result, nil
}

func (s *WorkerCertificateStore) Prune(ctx context.Context, now time.Time) (int, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return 0, fmt.Errorf("worker certificate store: list: %w", err)
	}
	removed := 0
	for _, rec := range recs {
		cert, err := decodeWorkerCertificate(rec)
		if err != nil || cert.State != persis.WorkerCertificateActive {
			continue
		}
		seen := cert.LastHeartbeatAt
		if seen.IsZero() {
			seen = cert.CreatedAt
		}
		if now.Sub(seen) <= workerCertificateRetention {
			continue
		}
		// Only the record as read is deleted, so a heartbeat or a state
		// change written meanwhile keeps it.
		err = s.col.CompareAndDelete(ctx, rec)
		if errors.Is(err, persis.ErrConflict) || errors.Is(err, persis.ErrNotFound) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("worker certificate store: delete certificate %q: %w", rec.ID, err)
		}
		removed++
	}
	return removed, nil
}

func encodeWorkerCertificate(cert persis.WorkerCertificate) (*persis.Record, error) {
	data, err := persis.Encode(workerCertificateStoredRecord{
		SchemaVersion:   workerCertificateSchemaVersion,
		ID:              cert.ID,
		InviteID:        cert.InviteID,
		KeyFingerprint:  cert.KeyFingerprint,
		State:           cert.State,
		WorkerID:        cert.WorkerID,
		Host:            cert.Host,
		Environment:     cert.Environment,
		CreatedAt:       cert.CreatedAt.UTC(),
		LastHeartbeatAt: cert.LastHeartbeatAt.UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("worker certificate store: encode certificate %q: %w", cert.ID, err)
	}
	return &persis.Record{ID: cert.ID, Data: data, CreatedAt: cert.CreatedAt, UpdatedAt: cert.CreatedAt}, nil
}

func decodeWorkerCertificate(rec *persis.Record) (persis.WorkerCertificate, error) {
	var payload workerCertificateStoredRecord
	if err := persis.Decode(rec, &payload); err != nil {
		return persis.WorkerCertificate{}, fmt.Errorf("worker certificate store: decode %q: %w", rec.ID, err)
	}
	if _, ok := workerCertificateStateRank[payload.State]; payload.ID == "" || !ok {
		return persis.WorkerCertificate{}, fmt.Errorf("worker certificate store: record %q is not a certificate", rec.ID)
	}
	return persis.WorkerCertificate{
		ID:              payload.ID,
		InviteID:        payload.InviteID,
		KeyFingerprint:  payload.KeyFingerprint,
		State:           payload.State,
		WorkerID:        payload.WorkerID,
		Host:            payload.Host,
		Environment:     payload.Environment,
		CreatedAt:       payload.CreatedAt,
		LastHeartbeatAt: payload.LastHeartbeatAt,
	}, nil
}
