// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	SchemaVersion    int                      `json:"schema_version"`
	ID               string                   `json:"id"`
	InviteID         string                   `json:"invite_id"`
	KeyFingerprint   []byte                   `json:"key_fingerprint"`
	WorkerID         string                   `json:"worker_id,omitempty"`
	WorkerIDs        []workerIDSeenRecord     `json:"worker_ids,omitempty"`
	Host             string                   `json:"host,omitempty"`
	Environment      persis.WorkerEnvironment `json:"environment,omitempty"`
	CreatedAt        time.Time                `json:"created_at"`
	LastHeartbeatAt  time.Time                `json:"last_heartbeat_at,omitzero"`
	EnrolledAs       string                   `json:"enrolled_as,omitempty"`
	EnrolledRemovals int64                    `json:"enrolled_removals,omitempty"`
}

type workerIDSeenRecord struct {
	ID     string    `json:"id"`
	SeenAt time.Time `json:"seen_at"`
}

// workerFenceRecord orders the enrollments and removals of one worker ID:
// each removal counts itself before it looks for certificates, and each
// enrollment confirms the count it read at its start in one write, so one
// of the two always sees the other.
type workerFenceRecord struct {
	WorkerID    string `json:"worker_id"`
	Removals    int64  `json:"removals"`
	Enrollments int64  `json:"enrollments"`
}

type workerCertificateRemovalStoredRecord struct {
	workerCertificateStoredRecord
	State      persis.WorkerCertificateState `json:"state"`
	RemovingAt time.Time                     `json:"removing_at,omitzero"`
	// HolderID is the worker the removal named, apart from the worker IDs
	// the heartbeats report.
	HolderID string `json:"holder_id,omitempty"`
	// Acceptances counts the task acceptances recorded during the removal,
	// so a revocation decided before one fails its compare-and-swap.
	Acceptances int64 `json:"acceptances,omitempty"`
}

const (
	// workerCertificateSchemaVersion is the format this version writes for
	// both kinds of record.
	workerCertificateSchemaVersion = 1

	workerCertificatePrefix        = "certs/"
	workerCertificateRemovalPrefix = "removals/"
	workerFencePrefix              = "workers/"

	// workerCertificateHeartbeatInterval bounds heartbeat writes to one a
	// day per certificate.
	workerCertificateHeartbeatInterval = 24 * time.Hour

	// workerCertificateWorkerIDs bounds the worker IDs a record keeps, so
	// workers that share one certificate under many IDs cannot grow it.
	workerCertificateWorkerIDs = 8

	// workerCertificateChangeInterval bounds the writes of a changed host
	// or environment to one a minute per certificate, so workers that share
	// one certificate do not write at every heartbeat. A changed worker ID
	// is written at once, since removal finds a worker by it.
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
		removals = append(removals, persis.WorkerCertificateRemoval{
			ID:             cert.ID,
			KeyFingerprint: cert.KeyFingerprint,
			State:          cert.State,
			WorkerID:       cmp.Or(cert.HolderID, cert.WorkerID),
			RemovingAt:     cert.RemovingAt,
		})
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
		removed.WorkerIDs = cert.WorkerIDs
		removed.Host = cert.Host
		removed.Environment = cert.Environment
		removed.LastHeartbeatAt = cert.LastHeartbeatAt
	}
	return removed
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
			next.EnrolledAs = stored.EnrolledAs
			next.EnrolledRemovals = stored.EnrolledRemovals
			next.WorkerIDs = seeWorkerID(knownWorkerIDs(stored), cert.WorkerID, cert.LastHeartbeatAt)
		case errors.Is(err, persis.ErrWorkerCertificateNotFound):
			next.WorkerIDs = seeWorkerID(nil, cert.WorkerID, cert.LastHeartbeatAt)
		case current != nil:
			// The stored payload cannot be read, so the report replaces it.
			next.WorkerIDs = seeWorkerID(nil, cert.WorkerID, cert.LastHeartbeatAt)
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
// written over stored. Each condition needs a newer report, so a delayed
// report never replaces newer fields.
func heartbeatWriteDue(stored, cert persis.WorkerCertificate) bool {
	age := cert.LastHeartbeatAt.Sub(stored.LastHeartbeatAt)
	if age <= 0 {
		return false
	}
	changed := cert.Host != stored.Host || cert.Environment != stored.Environment
	return age > workerCertificateHeartbeatInterval || workerIDDue(knownWorkerIDs(stored), cert.WorkerID, cert.LastHeartbeatAt) ||
		(changed && age >= workerCertificateChangeInterval)
}

// workerIDDue reports whether a heartbeat at at from the worker ID id
// changes ids enough to write: id is new, or it returns after another ID
// and was last seen more than workerCertificateChangeInterval before. A
// worker that keeps its ID writes nothing for it.
func workerIDDue(ids []persis.WorkerIDSeen, id string, at time.Time) bool {
	if id == "" {
		return false
	}
	i := slices.IndexFunc(ids, func(seen persis.WorkerIDSeen) bool { return seen.ID == id })
	if i < 0 {
		return true
	}
	return i < len(ids)-1 && at.Sub(ids[i].SeenAt) > workerCertificateChangeInterval
}

// knownWorkerIDs returns the worker IDs that cert names: its WorkerIDs, or
// its WorkerID for a record written before them.
func knownWorkerIDs(cert persis.WorkerCertificate) []persis.WorkerIDSeen {
	if len(cert.WorkerIDs) > 0 {
		return cert.WorkerIDs
	}
	if cert.WorkerID == "" {
		return nil
	}
	return []persis.WorkerIDSeen{{ID: cert.WorkerID, SeenAt: cert.LastHeartbeatAt}}
}

// seeWorkerID returns ids with id seen at at, the most recently seen last,
// dropping the least recently seen beyond workerCertificateWorkerIDs.
func seeWorkerID(ids []persis.WorkerIDSeen, id string, at time.Time) []persis.WorkerIDSeen {
	// The most recently seen ID stays as it is, so a worker that keeps its
	// ID changes nothing here.
	if len(ids) > 0 && ids[len(ids)-1].ID == id {
		return slices.Clone(ids)
	}
	seen := slices.DeleteFunc(slices.Clone(ids), func(known persis.WorkerIDSeen) bool { return known.ID == id })
	if id != "" {
		seen = append(seen, persis.WorkerIDSeen{ID: id, SeenAt: at.UTC()})
	}
	if extra := len(seen) - workerCertificateWorkerIDs; extra > 0 {
		seen = seen[extra:]
	}
	return seen
}

func (s *WorkerCertificateStore) WorkerRemovals(ctx context.Context, workerID string) (int64, error) {
	_, fence, err := s.getFence(ctx, workerID)
	return fence.Removals, err
}

func (s *WorkerCertificateStore) BeginWorkerRemoval(ctx context.Context, workerID string) (int64, error) {
	var removals int64
	err := retryConflict(ctx, func(ctx context.Context) error {
		current, fence, err := s.getFence(ctx, workerID)
		if err != nil {
			return err
		}
		fence.Removals++
		removals = fence.Removals
		return s.putFence(ctx, current, fence)
	})
	if err != nil {
		return 0, fmt.Errorf("worker certificate store: begin the removal of %q: %w", workerID, err)
	}
	return removals, nil
}

func (s *WorkerCertificateStore) ConfirmEnrollment(ctx context.Context, workerID string, removals int64) (bool, error) {
	confirmed := false
	err := retryConflict(ctx, func(ctx context.Context) error {
		current, fence, err := s.getFence(ctx, workerID)
		if err != nil {
			return err
		}
		if fence.Removals != removals {
			confirmed = false
			return nil
		}
		fence.Enrollments++
		if err := s.putFence(ctx, current, fence); err != nil {
			return err
		}
		confirmed = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("worker certificate store: confirm the enrollment of %q: %w", workerID, err)
	}
	return confirmed, nil
}

// getFence returns the fence record of workerID and its stored record, or a
// new fence and nil when none is stored.
func (s *WorkerCertificateStore) getFence(ctx context.Context, workerID string) (*persis.Record, workerFenceRecord, error) {
	rec, err := s.col.Get(ctx, workerFenceID(workerID))
	if errors.Is(err, persis.ErrNotFound) {
		return nil, workerFenceRecord{WorkerID: workerID}, nil
	}
	if err != nil {
		return nil, workerFenceRecord{}, err
	}
	var fence workerFenceRecord
	if err := persis.Decode(rec, &fence); err != nil {
		return nil, workerFenceRecord{}, fmt.Errorf("decode the fence of %q: %w", workerID, err)
	}
	return rec, fence, nil
}

// putFence writes fence over current, the record it was read from, or
// creates it when current is nil; either fails with a conflict when another
// write came first.
func (s *WorkerCertificateStore) putFence(ctx context.Context, current *persis.Record, fence workerFenceRecord) error {
	data, err := persis.Encode(fence)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return createOrSwap(ctx, s.col, current, &persis.Record{ID: workerFenceID(fence.WorkerID), Data: data, CreatedAt: now, UpdatedAt: now})
}

// workerFenceID is the record ID of the fence of workerID. The worker ID is
// hashed, so any ID makes a valid record ID.
func workerFenceID(workerID string) string {
	sum := sha256.Sum256([]byte(workerID))
	return workerFencePrefix + hex.EncodeToString(sum[:])
}

func (s *WorkerCertificateStore) MarkRemoving(ctx context.Context, id, workerID string, now func() time.Time) (persis.WorkerCertificate, error) {
	var result persis.WorkerCertificate
	err := retryConflict(ctx, func(ctx context.Context) error {
		cert, current, err := s.load(ctx, id)
		if err != nil {
			return err
		}
		result = cert
		if cert.State == persis.WorkerCertificateRevoked {
			return nil
		}
		changed := false
		if cert.State == persis.WorkerCertificateActive {
			cert.State = persis.WorkerCertificateRemoving
			// Taken for each write attempt, so a slow read or a retried
			// swap does not count toward the time in removing.
			cert.RemovingAt = now()
			changed = true
		}
		// The removal record, not the newer heartbeat that load merges in,
		// is what ListRemovals reads, so the stored ID decides.
		if workerID != cert.HolderID {
			changed = true
		}
		cert.HolderID = workerID
		if known := knownWorkerIDs(cert); !slices.ContainsFunc(known, func(seen persis.WorkerIDSeen) bool { return seen.ID == workerID }) {
			cert.WorkerIDs = seeWorkerID(known, workerID, cert.RemovingAt)
			changed = true
		}
		// Unchanged, cert is the record as stored, with the worker ID of
		// the removal record rather than of a newer heartbeat.
		result = cert
		if !changed {
			return nil
		}
		rec, err := encodeWorkerCertificateRemoval(cert, acceptancesOf(current))
		if err != nil {
			return err
		}
		return createOrSwap(ctx, s.col, current, rec)
	})
	if err != nil {
		return persis.WorkerCertificate{}, err
	}
	return result, nil
}

func (s *WorkerCertificateStore) ConfirmAcceptance(ctx context.Context, id string) (persis.WorkerCertificate, error) {
	var result persis.WorkerCertificate
	err := retryConflict(ctx, func(ctx context.Context) error {
		cert, current, err := s.load(ctx, id)
		if err != nil {
			return err
		}
		result = cert
		if current == nil || cert.State == persis.WorkerCertificateRevoked {
			return nil
		}
		removal, err := decodeWorkerCertificateRemovalRecord(current)
		if err != nil {
			return err
		}
		removal.Acceptances++
		next, err := encodeRemovalRecord(cert.ID, removal)
		if err != nil {
			return err
		}
		return createOrSwap(ctx, s.col, current, next)
	})
	if err != nil {
		return persis.WorkerCertificate{}, err
	}
	return result, nil
}

func (s *WorkerCertificateStore) RevokeIfIdle(ctx context.Context, id string, idle func(context.Context) (bool, error)) (persis.WorkerCertificate, bool, error) {
	var result persis.WorkerCertificate
	revoked := false
	err := retryConflict(ctx, func(ctx context.Context) error {
		revoked = false
		cert, current, err := s.load(ctx, id)
		if err != nil {
			return err
		}
		result = cert
		if current == nil || cert.State != persis.WorkerCertificateRemoving {
			return nil
		}
		ok, err := idle(ctx)
		if err != nil || !ok {
			return err
		}
		removal, err := decodeWorkerCertificateRemovalRecord(current)
		if err != nil {
			return err
		}
		cert.State = persis.WorkerCertificateRevoked
		removal.workerCertificateStoredRecord = storedWorkerCertificate(cert)
		removal.State = cert.State
		next, err := encodeRemovalRecord(cert.ID, removal)
		if err != nil {
			return err
		}
		if err := createOrSwap(ctx, s.col, current, next); err != nil {
			return err
		}
		result = cert
		revoked = true
		return nil
	})
	if err != nil {
		return persis.WorkerCertificate{}, false, err
	}
	return result, revoked, nil
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
		next, err := encodeWorkerCertificateRemoval(cert, acceptancesOf(current))
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
		SchemaVersion:    workerCertificateSchemaVersion,
		ID:               cert.ID,
		InviteID:         cert.InviteID,
		KeyFingerprint:   cert.KeyFingerprint,
		WorkerID:         cert.WorkerID,
		WorkerIDs:        storedWorkerIDs(cert.WorkerIDs),
		Host:             cert.Host,
		Environment:      cert.Environment,
		CreatedAt:        cert.CreatedAt.UTC(),
		LastHeartbeatAt:  cert.LastHeartbeatAt.UTC(),
		EnrolledAs:       cert.EnrolledAs,
		EnrolledRemovals: cert.EnrolledRemovals,
	}
}

func (r workerCertificateStoredRecord) certificate(state persis.WorkerCertificateState) persis.WorkerCertificate {
	return persis.WorkerCertificate{
		ID:               r.ID,
		InviteID:         r.InviteID,
		KeyFingerprint:   r.KeyFingerprint,
		State:            state,
		WorkerID:         r.WorkerID,
		WorkerIDs:        r.workerIDs(),
		Host:             r.Host,
		Environment:      r.Environment,
		CreatedAt:        r.CreatedAt,
		LastHeartbeatAt:  r.LastHeartbeatAt,
		EnrolledAs:       r.EnrolledAs,
		EnrolledRemovals: r.EnrolledRemovals,
	}
}

func storedWorkerIDs(ids []persis.WorkerIDSeen) []workerIDSeenRecord {
	stored := make([]workerIDSeenRecord, 0, len(ids))
	for _, seen := range ids {
		stored = append(stored, workerIDSeenRecord{ID: seen.ID, SeenAt: seen.SeenAt.UTC()})
	}
	return stored
}

func (r workerCertificateStoredRecord) workerIDs() []persis.WorkerIDSeen {
	if len(r.WorkerIDs) == 0 {
		return nil
	}
	ids := make([]persis.WorkerIDSeen, 0, len(r.WorkerIDs))
	for _, seen := range r.WorkerIDs {
		ids = append(ids, persis.WorkerIDSeen{ID: seen.ID, SeenAt: seen.SeenAt})
	}
	return ids
}

// encodeWorkerCertificateRemoval encodes the removal record of cert, keeping
// the count of acceptances the record it replaces holds.
func encodeWorkerCertificateRemoval(cert persis.WorkerCertificate, acceptances int64) (*persis.Record, error) {
	return encodeRemovalRecord(cert.ID, workerCertificateRemovalStoredRecord{
		workerCertificateStoredRecord: storedWorkerCertificate(cert),
		State:                         cert.State,
		RemovingAt:                    cert.RemovingAt.UTC(),
		HolderID:                      cert.HolderID,
		Acceptances:                   acceptances,
	})
}

func encodeRemovalRecord(id string, removal workerCertificateRemovalStoredRecord) (*persis.Record, error) {
	data, err := persis.Encode(removal)
	if err != nil {
		return nil, fmt.Errorf("worker certificate store: encode removal of %q: %w", id, err)
	}
	now := time.Now().UTC()
	return &persis.Record{ID: workerCertificateRemovalPrefix + id, Data: data, CreatedAt: now, UpdatedAt: now}, nil
}

// decodeWorkerCertificateRemovalRecord returns the stored removal record
// rec.
func decodeWorkerCertificateRemovalRecord(rec *persis.Record) (workerCertificateRemovalStoredRecord, error) {
	var payload workerCertificateRemovalStoredRecord
	if err := persis.Decode(rec, &payload); err != nil {
		return workerCertificateRemovalStoredRecord{}, fmt.Errorf("worker certificate store: decode %q: %w", rec.ID, err)
	}
	return payload, nil
}

// acceptancesOf returns the count of acceptances that the removal record rec
// holds, or zero for none.
func acceptancesOf(rec *persis.Record) int64 {
	if rec == nil {
		return 0
	}
	removal, err := decodeWorkerCertificateRemovalRecord(rec)
	if err != nil {
		return 0
	}
	return removal.Acceptances
}

func decodeWorkerCertificateRemoval(rec *persis.Record) (persis.WorkerCertificate, error) {
	var payload workerCertificateRemovalStoredRecord
	if err := persis.Decode(rec, &payload); err != nil {
		return persis.WorkerCertificate{}, fmt.Errorf("worker certificate store: decode %q: %w", rec.ID, err)
	}
	if rank := workerCertificateStateRank[payload.State]; payload.ID == "" || rank == 0 {
		return persis.WorkerCertificate{}, fmt.Errorf("worker certificate store: record %q is not a removal", rec.ID)
	}
	cert := payload.certificate(payload.State)
	cert.RemovingAt = payload.RemovingAt
	cert.HolderID = payload.HolderID
	return cert, nil
}
