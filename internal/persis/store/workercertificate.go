// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ persis.WorkerCertificateStore = (*WorkerCertificateStore)(nil)

// WorkerCertificateStore implements [persis.WorkerCertificateStore] over a
// [persis.Collection]. Each certificate has a record under "certs/". A
// certificate in state removing or revoked also has a removal record under
// "removals/", which holds its state, so reading the removals reads only
// those records. A certificate without a removal record is active.
type WorkerCertificateStore struct {
	col persis.Collection
}

type workerCertificateStoredRecord struct {
	// SchemaVersion is the record's format.
	SchemaVersion   int                      `json:"schema_version"`
	ID              string                   `json:"id"`
	InviteID        string                   `json:"invite_id"`
	KeyFingerprint  []byte                   `json:"key_fingerprint"`
	WorkerID        string                   `json:"worker_id,omitempty"`
	Host            string                   `json:"host,omitempty"`
	Environment     persis.WorkerEnvironment `json:"environment,omitempty"`
	CreatedAt       time.Time                `json:"created_at"`
	LastHeartbeatAt time.Time                `json:"last_heartbeat_at,omitzero"`
}

type workerCertificateRemovalStoredRecord struct {
	// SchemaVersion is the record's format.
	SchemaVersion  int                           `json:"schema_version"`
	ID             string                        `json:"id"`
	KeyFingerprint []byte                        `json:"key_fingerprint"`
	State          persis.WorkerCertificateState `json:"state"`
}

const (
	// workerCertificateSchemaVersion is the format this version writes for
	// both kinds of record.
	workerCertificateSchemaVersion = 1

	workerCertificatePrefix        = "certs/"
	workerCertificateRemovalPrefix = "removals/"

	// workerCertificateHeartbeatInterval bounds heartbeat writes to one a
	// day per certificate.
	workerCertificateHeartbeatInterval = 24 * time.Hour

	// workerCertificateChangeInterval bounds the writes of a changed worker
	// ID, host, or environment to one a minute per certificate, so workers
	// that share one certificate do not write at every heartbeat.
	workerCertificateChangeInterval = time.Minute

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
	// A removal record can outlive a pruned certificate record, and the ID
	// stays taken so the certificate never reads as active again.
	_, removal, err := s.getRemoval(ctx, cert.ID)
	if err != nil {
		return err
	}
	if removal != nil {
		return persis.ErrWorkerCertificateExists
	}
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
	cert, _, err := s.load(ctx, id)
	return cert, err
}

// load returns the certificate with id in its state, and its removal
// record or nil. A prune that read the removals before a removal began may
// delete the certificate record, so a removal record alone still names the
// certificate and lets its removal finish.
func (s *WorkerCertificateStore) load(ctx context.Context, id string) (persis.WorkerCertificate, *persis.Record, error) {
	removalRec, removal, err := s.getRemoval(ctx, id)
	if err != nil {
		return persis.WorkerCertificate{}, nil, err
	}
	_, cert, err := s.getCertificate(ctx, id)
	switch {
	case err == nil:
	case errors.Is(err, persis.ErrWorkerCertificateNotFound) && removal != nil:
		cert = persis.WorkerCertificate{ID: id, KeyFingerprint: removal.KeyFingerprint}
	default:
		return persis.WorkerCertificate{}, nil, err
	}
	if removal != nil {
		cert.State = removal.State
	}
	return cert, removalRec, nil
}

func (s *WorkerCertificateStore) List(ctx context.Context) ([]persis.WorkerCertificate, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{Prefix: workerCertificatePrefix})
	if err != nil {
		return nil, fmt.Errorf("worker certificate store: list: %w", err)
	}
	states, err := s.removalStates(ctx)
	if err != nil {
		return nil, err
	}
	certs := make([]persis.WorkerCertificate, 0, len(recs))
	for _, rec := range recs {
		// A record that cannot be read is left out, as the worker's next
		// heartbeat writes it again.
		cert, err := decodeWorkerCertificate(rec)
		if err != nil {
			continue
		}
		cert.State = cmp.Or(states[cert.ID], persis.WorkerCertificateActive)
		certs = append(certs, cert)
	}
	slices.SortFunc(certs, func(a, b persis.WorkerCertificate) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	return certs, nil
}

func (s *WorkerCertificateStore) ListRemovals(ctx context.Context) ([]persis.WorkerCertificateRemoval, error) {
	recs, err := listAllStrict(ctx, s.col, persis.ListQuery{Prefix: workerCertificateRemovalPrefix})
	if err != nil {
		return nil, fmt.Errorf("worker certificate store: list removals: %w", err)
	}
	removals := make([]persis.WorkerCertificateRemoval, 0, len(recs))
	for _, rec := range recs {
		removal, err := decodeWorkerCertificateRemoval(rec)
		if err != nil {
			return nil, err
		}
		removals = append(removals, removal)
	}
	return removals, nil
}

// removalStates maps the ID of each certificate in state removing or
// revoked to its state.
func (s *WorkerCertificateStore) removalStates(ctx context.Context) (map[string]persis.WorkerCertificateState, error) {
	removals, err := s.ListRemovals(ctx)
	if err != nil {
		return nil, err
	}
	states := make(map[string]persis.WorkerCertificateState, len(removals))
	for _, removal := range removals {
		states[removal.ID] = removal.State
	}
	return states, nil
}

func (s *WorkerCertificateStore) RecordHeartbeat(ctx context.Context, cert persis.WorkerCertificate) error {
	err := retryConflict(ctx, func(ctx context.Context) error {
		current, stored, err := s.getCertificate(ctx, cert.ID)
		next := cert
		switch {
		case err == nil:
			if !heartbeatWriteDue(stored, cert) {
				return nil
			}
			next.InviteID = stored.InviteID
			next.KeyFingerprint = stored.KeyFingerprint
			next.CreatedAt = stored.CreatedAt
		case errors.Is(err, persis.ErrWorkerCertificateNotFound):
		case current != nil:
			// The stored payload cannot be read, so the report replaces it.
		case errors.Is(err, persis.ErrCorrupt):
			// The stored file cannot be read at all, so no compare-and-swap
			// can match it. It is removed only while still unreadable, and
			// the report is then created, so a repair that read the file
			// before a newer report replaced it does not overwrite that
			// report.
			_, retryErr := removeCorruptRecordForRetry(ctx, s.col, workerCertificatePrefix+cert.ID, err)
			return retryErr
		default:
			return err
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

// heartbeatWriteDue reports whether the heartbeat reported in cert is to be
// written over stored. Both conditions need a newer report, so a delayed
// report never replaces newer fields.
func heartbeatWriteDue(stored, cert persis.WorkerCertificate) bool {
	age := cert.LastHeartbeatAt.Sub(stored.LastHeartbeatAt)
	changed := cert.WorkerID != stored.WorkerID || cert.Host != stored.Host || cert.Environment != stored.Environment
	return age > workerCertificateHeartbeatInterval || (changed && age >= workerCertificateChangeInterval)
}

func (s *WorkerCertificateStore) SetState(ctx context.Context, id string, state persis.WorkerCertificateState) (persis.WorkerCertificate, error) {
	rank, ok := workerCertificateStateRank[state]
	if !ok {
		return persis.WorkerCertificate{}, fmt.Errorf("worker certificate store: unknown state %q", state)
	}
	var result persis.WorkerCertificate
	err := retryConflict(ctx, func(ctx context.Context) error {
		cert, current, err := s.load(ctx, id)
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
		next, err := encodeWorkerCertificateRemoval(cert)
		if err != nil {
			return err
		}
		if err := createOrSwap(ctx, s.col, current, next); err != nil {
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
	recs, err := listAll(ctx, s.col, persis.ListQuery{Prefix: workerCertificatePrefix})
	if err != nil {
		return 0, fmt.Errorf("worker certificate store: list: %w", err)
	}
	// A removal written after this read keeps its own record, so the
	// certificate stays refused even if its record is pruned.
	states, err := s.removalStates(ctx)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, rec := range recs {
		cert, err := decodeWorkerCertificate(rec)
		if err != nil {
			continue
		}
		if _, ok := states[cert.ID]; ok {
			continue
		}
		seen := cert.LastHeartbeatAt
		if seen.IsZero() {
			seen = cert.CreatedAt
		}
		if now.Sub(seen) <= workerCertificateRetention {
			continue
		}
		// Only the record as read is deleted, so a heartbeat written
		// meanwhile keeps it.
		err = s.col.CompareAndDelete(ctx, rec)
		if errors.Is(err, persis.ErrConflict) || errors.Is(err, persis.ErrNotFound) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("worker certificate store: delete certificate %q: %w", cert.ID, err)
		}
		removed++
	}
	return removed, nil
}

// getCertificate returns the certificate record with id, in state active,
// or [persis.ErrWorkerCertificateNotFound]. When the record exists but its
// payload cannot be read, it returns the record with the decoding error.
func (s *WorkerCertificateStore) getCertificate(ctx context.Context, id string) (*persis.Record, persis.WorkerCertificate, error) {
	rec, err := s.col.Get(ctx, workerCertificatePrefix+id)
	if errors.Is(err, persis.ErrNotFound) {
		return nil, persis.WorkerCertificate{}, persis.ErrWorkerCertificateNotFound
	}
	if err != nil {
		return nil, persis.WorkerCertificate{}, fmt.Errorf("worker certificate store: read certificate %q: %w", id, err)
	}
	cert, err := decodeWorkerCertificate(rec)
	return rec, cert, err
}

// getRemoval returns the removal record of the certificate with id, or nil
// when the certificate is active.
func (s *WorkerCertificateStore) getRemoval(ctx context.Context, id string) (*persis.Record, *persis.WorkerCertificateRemoval, error) {
	rec, err := s.col.Get(ctx, workerCertificateRemovalPrefix+id)
	if errors.Is(err, persis.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("worker certificate store: read removal of %q: %w", id, err)
	}
	removal, err := decodeWorkerCertificateRemoval(rec)
	if err != nil {
		return nil, nil, err
	}
	return rec, &removal, nil
}

func encodeWorkerCertificate(cert persis.WorkerCertificate) (*persis.Record, error) {
	data, err := persis.Encode(workerCertificateStoredRecord{
		SchemaVersion:   workerCertificateSchemaVersion,
		ID:              cert.ID,
		InviteID:        cert.InviteID,
		KeyFingerprint:  cert.KeyFingerprint,
		WorkerID:        cert.WorkerID,
		Host:            cert.Host,
		Environment:     cert.Environment,
		CreatedAt:       cert.CreatedAt.UTC(),
		LastHeartbeatAt: cert.LastHeartbeatAt.UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("worker certificate store: encode certificate %q: %w", cert.ID, err)
	}
	return &persis.Record{ID: workerCertificatePrefix + cert.ID, Data: data, CreatedAt: cert.CreatedAt, UpdatedAt: cert.CreatedAt}, nil
}

func decodeWorkerCertificate(rec *persis.Record) (persis.WorkerCertificate, error) {
	var payload workerCertificateStoredRecord
	if err := persis.Decode(rec, &payload); err != nil {
		return persis.WorkerCertificate{}, fmt.Errorf("worker certificate store: decode %q: %w", rec.ID, err)
	}
	if payload.ID == "" {
		return persis.WorkerCertificate{}, fmt.Errorf("worker certificate store: record %q is not a certificate", rec.ID)
	}
	return persis.WorkerCertificate{
		ID:              payload.ID,
		InviteID:        payload.InviteID,
		KeyFingerprint:  payload.KeyFingerprint,
		State:           persis.WorkerCertificateActive,
		WorkerID:        payload.WorkerID,
		Host:            payload.Host,
		Environment:     payload.Environment,
		CreatedAt:       payload.CreatedAt,
		LastHeartbeatAt: payload.LastHeartbeatAt,
	}, nil
}

func encodeWorkerCertificateRemoval(cert persis.WorkerCertificate) (*persis.Record, error) {
	data, err := persis.Encode(workerCertificateRemovalStoredRecord{
		SchemaVersion:  workerCertificateSchemaVersion,
		ID:             cert.ID,
		KeyFingerprint: cert.KeyFingerprint,
		State:          cert.State,
	})
	if err != nil {
		return nil, fmt.Errorf("worker certificate store: encode removal of %q: %w", cert.ID, err)
	}
	now := time.Now().UTC()
	return &persis.Record{ID: workerCertificateRemovalPrefix + cert.ID, Data: data, CreatedAt: now, UpdatedAt: now}, nil
}

func decodeWorkerCertificateRemoval(rec *persis.Record) (persis.WorkerCertificateRemoval, error) {
	var payload workerCertificateRemovalStoredRecord
	if err := persis.Decode(rec, &payload); err != nil {
		return persis.WorkerCertificateRemoval{}, fmt.Errorf("worker certificate store: decode %q: %w", rec.ID, err)
	}
	if rank := workerCertificateStateRank[payload.State]; payload.ID == "" || rank == 0 {
		return persis.WorkerCertificateRemoval{}, fmt.Errorf("worker certificate store: record %q is not a removal", rec.ID)
	}
	return persis.WorkerCertificateRemoval{
		ID:             payload.ID,
		KeyFingerprint: payload.KeyFingerprint,
		State:          payload.State,
	}, nil
}
