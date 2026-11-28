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
// "removals/", which holds the whole certificate and its state, so reading
// the removals reads only those records. A certificate without a removal
// record is active. A removal record is never deleted, and once it exists
// the certificate record only carries newer heartbeats, so pruning it loses
// nothing.
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
	workerCertificateStoredRecord
	State persis.WorkerCertificateState `json:"state"`
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
// record or nil. It reads the certificate record before the removal
// record. A removal record is never deleted, so one missing at the second
// read was missing at the first, and the result is a state the certificate
// held during the call.
func (s *WorkerCertificateStore) load(ctx context.Context, id string) (persis.WorkerCertificate, *persis.Record, error) {
	certRec, cert, certErr := s.getCertificate(ctx, id)
	// An unreadable certificate record still lets the removal record, which
	// holds the whole certificate, answer.
	unreadable := certErr != nil && (certRec != nil || errors.Is(certErr, persis.ErrCorrupt))
	if certErr != nil && !errors.Is(certErr, persis.ErrWorkerCertificateNotFound) && !unreadable {
		return persis.WorkerCertificate{}, nil, certErr
	}
	removalRec, removed, err := s.getRemoval(ctx, id)
	if err != nil {
		return persis.WorkerCertificate{}, nil, err
	}
	switch {
	case removed != nil && certErr == nil:
		return withHeartbeat(*removed, cert), removalRec, nil
	case removed != nil:
		return *removed, removalRec, nil
	case certErr == nil:
		return cert, nil, nil
	default:
		return persis.WorkerCertificate{}, nil, certErr
	}
}

func (s *WorkerCertificateStore) List(ctx context.Context) ([]persis.WorkerCertificate, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{Prefix: workerCertificatePrefix})
	if err != nil {
		return nil, fmt.Errorf("worker certificate store: list: %w", err)
	}
	removals, err := s.listRemoved(ctx)
	if err != nil {
		return nil, err
	}
	pending := make(map[string]persis.WorkerCertificate, len(removals))
	for _, removed := range removals {
		pending[removed.ID] = removed
	}
	certs := make([]persis.WorkerCertificate, 0, len(recs)+len(removals))
	for _, rec := range recs {
		// A record that cannot be read is left out, as the worker's next
		// heartbeat writes it again.
		cert, err := decodeWorkerCertificate(rec)
		if err != nil {
			continue
		}
		if removed, ok := pending[cert.ID]; ok {
			cert = withHeartbeat(removed, cert)
			delete(pending, cert.ID)
		}
		certs = append(certs, cert)
	}
	// A removed certificate whose certificate record was pruned is listed
	// from its removal record alone.
	for _, removed := range pending {
		certs = append(certs, removed)
	}
	slices.SortFunc(certs, func(a, b persis.WorkerCertificate) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	return certs, nil
}

func (s *WorkerCertificateStore) ListRemovals(ctx context.Context) ([]persis.WorkerCertificateRemoval, error) {
	removed, err := s.listRemoved(ctx)
	if err != nil {
		return nil, err
	}
	removals := make([]persis.WorkerCertificateRemoval, 0, len(removed))
	for _, cert := range removed {
		removals = append(removals, persis.WorkerCertificateRemoval{ID: cert.ID, KeyFingerprint: cert.KeyFingerprint, State: cert.State})
	}
	return removals, nil
}

// listRemoved returns the certificates that have a removal record, as
// their removal records hold them. It fails when one cannot be read.
func (s *WorkerCertificateStore) listRemoved(ctx context.Context) ([]persis.WorkerCertificate, error) {
	recs, err := listAllStrict(ctx, s.col, persis.ListQuery{Prefix: workerCertificateRemovalPrefix})
	if err != nil {
		return nil, fmt.Errorf("worker certificate store: list removals: %w", err)
	}
	removed := make([]persis.WorkerCertificate, 0, len(recs))
	for _, rec := range recs {
		cert, err := decodeWorkerCertificateRemoval(rec)
		if err != nil {
			return nil, err
		}
		removed = append(removed, cert)
	}
	return removed, nil
}

// withHeartbeat returns the removed certificate with the heartbeat fields of
// its certificate record, when those are newer.
func withHeartbeat(removed, cert persis.WorkerCertificate) persis.WorkerCertificate {
	if cert.LastHeartbeatAt.After(removed.LastHeartbeatAt) {
		removed.WorkerID = cert.WorkerID
		removed.Host = cert.Host
		removed.Environment = cert.Environment
		removed.LastHeartbeatAt = cert.LastHeartbeatAt
	}
	return removed
}

func (s *WorkerCertificateStore) RecordHeartbeat(ctx context.Context, cert persis.WorkerCertificate) (persis.WorkerCertificate, error) {
	var result persis.WorkerCertificate
	err := retryConflict(ctx, func(ctx context.Context) error {
		current, stored, err := s.getCertificate(ctx, cert.ID)
		next := cert
		switch {
		case err == nil:
			if !heartbeatWriteDue(stored, cert) {
				result = stored
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
		if err := createOrSwap(ctx, s.col, current, rec); err != nil {
			return err
		}
		result = next
		return nil
	})
	if err != nil {
		return persis.WorkerCertificate{}, fmt.Errorf("worker certificate store: record heartbeat of %q: %w", cert.ID, err)
	}
	return result, nil
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
	// A removal written after this read holds the whole certificate, so a
	// certificate record pruned under it loses nothing.
	removals, err := s.listRemoved(ctx)
	if err != nil {
		return 0, err
	}
	removedIDs := make(map[string]bool, len(removals))
	for _, cert := range removals {
		removedIDs[cert.ID] = true
	}
	removed := 0
	for _, rec := range recs {
		cert, err := decodeWorkerCertificate(rec)
		if err != nil {
			continue
		}
		if removedIDs[cert.ID] {
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

// getRemoval returns the removal record of the certificate with id and the
// certificate it holds, or nils when the certificate is active.
func (s *WorkerCertificateStore) getRemoval(ctx context.Context, id string) (*persis.Record, *persis.WorkerCertificate, error) {
	rec, err := s.col.Get(ctx, workerCertificateRemovalPrefix+id)
	if errors.Is(err, persis.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("worker certificate store: read removal of %q: %w", id, err)
	}
	removed, err := decodeWorkerCertificateRemoval(rec)
	if err != nil {
		return nil, nil, err
	}
	return rec, &removed, nil
}

func encodeWorkerCertificate(cert persis.WorkerCertificate) (*persis.Record, error) {
	data, err := persis.Encode(storedWorkerCertificate(cert))
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
	return payload.certificate(persis.WorkerCertificateActive), nil
}

func storedWorkerCertificate(cert persis.WorkerCertificate) workerCertificateStoredRecord {
	return workerCertificateStoredRecord{
		SchemaVersion:   workerCertificateSchemaVersion,
		ID:              cert.ID,
		InviteID:        cert.InviteID,
		KeyFingerprint:  cert.KeyFingerprint,
		WorkerID:        cert.WorkerID,
		Host:            cert.Host,
		Environment:     cert.Environment,
		CreatedAt:       cert.CreatedAt.UTC(),
		LastHeartbeatAt: cert.LastHeartbeatAt.UTC(),
	}
}

func (r workerCertificateStoredRecord) certificate(state persis.WorkerCertificateState) persis.WorkerCertificate {
	return persis.WorkerCertificate{
		ID:              r.ID,
		InviteID:        r.InviteID,
		KeyFingerprint:  r.KeyFingerprint,
		State:           state,
		WorkerID:        r.WorkerID,
		Host:            r.Host,
		Environment:     r.Environment,
		CreatedAt:       r.CreatedAt,
		LastHeartbeatAt: r.LastHeartbeatAt,
	}
}

func encodeWorkerCertificateRemoval(cert persis.WorkerCertificate) (*persis.Record, error) {
	data, err := persis.Encode(workerCertificateRemovalStoredRecord{
		workerCertificateStoredRecord: storedWorkerCertificate(cert),
		State:                         cert.State,
	})
	if err != nil {
		return nil, fmt.Errorf("worker certificate store: encode removal of %q: %w", cert.ID, err)
	}
	now := time.Now().UTC()
	return &persis.Record{ID: workerCertificateRemovalPrefix + cert.ID, Data: data, CreatedAt: now, UpdatedAt: now}, nil
}

func decodeWorkerCertificateRemoval(rec *persis.Record) (persis.WorkerCertificate, error) {
	var payload workerCertificateRemovalStoredRecord
	if err := persis.Decode(rec, &payload); err != nil {
		return persis.WorkerCertificate{}, fmt.Errorf("worker certificate store: decode %q: %w", rec.ID, err)
	}
	if rank := workerCertificateStateRank[payload.State]; payload.ID == "" || rank == 0 {
		return persis.WorkerCertificate{}, fmt.Errorf("worker certificate store: record %q is not a removal", rec.ID)
	}
	return payload.certificate(payload.State), nil
}
