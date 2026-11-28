// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/persis"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	// removalRereadInterval is how often a coordinator rereads the
	// certificates in removal, so it applies another coordinator's removal
	// within this interval.
	removalRereadInterval = 30 * time.Second
	// recordPruneInterval is how often a coordinator prunes the certificate
	// and invite records.
	recordPruneInterval = 24 * time.Hour
	// certificateHeartbeatPass bounds how often a coordinator passes a
	// worker's heartbeat to its certificate record. The store writes only
	// what is due by the record, so the record orders worker IDs by when
	// any coordinator last saw them.
	certificateHeartbeatPass = time.Minute
	// removedWorkerMessage refuses a call with a revoked worker certificate.
	removedWorkerMessage = "this worker was removed from the coordinator"
	// peerTokenWorkerMessage refuses the removal of a worker on the peer
	// token.
	peerTokenWorkerMessage = "this worker uses the peer token, so it would connect again: stop it on its host, or change the peer token"
	// connectedWorkerMessage refuses the removal of a connected worker.
	connectedWorkerMessage = "this worker is connected: stop it on its host, then remove it"
)

// removalStateRank orders the states of a certificate in removal.
var removalStateRank = map[persis.WorkerCertificateState]int{
	persis.WorkerCertificateRemoving: 1,
	persis.WorkerCertificateRevoked:  2,
}

// workerRemovals is a coordinator's copy of the certificates in removal.
// Calls read it, so a call reads no store to refuse a revoked certificate.
type workerRemovals struct {
	mu sync.RWMutex
	// loaded is set once the certificates in removal were read.
	loaded bool
	// byID holds the certificates in removal by ID, one entry per removed
	// certificate, as the store keeps one record for each.
	byID map[string]persis.WorkerCertificateRemoval
}

// load adds removals, a complete read of the certificates in removal, to
// the copy and marks it read.
func (r *workerRemovals) load(removals []persis.WorkerCertificateRemoval) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addLocked(removals)
	r.loaded = true
}

// apply adds removals that this coordinator made or read one by one. It
// leaves an unread copy unread, since other coordinators may have revoked
// certificates it lacks.
func (r *workerRemovals) apply(removals ...persis.WorkerCertificateRemoval) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addLocked(removals)
}

// addLocked adds removals to the copy. A certificate's state only moves
// forward, so a read that began before this coordinator changed a
// certificate does not undo the change.
func (r *workerRemovals) addLocked(removals []persis.WorkerCertificateRemoval) {
	if r.byID == nil {
		r.byID = make(map[string]persis.WorkerCertificateRemoval, len(removals))
	}
	for _, removal := range removals {
		if current, ok := r.byID[removal.ID]; ok && removalStateRank[removal.State] < removalStateRank[current.State] {
			continue
		}
		r.byID[removal.ID] = removal
	}
}

// refusal returns the error that refuses a call with the worker
// certificate id, or nil when the certificate is not revoked.
func (r *workerRemovals) refusal(id string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.loaded {
		return status.Error(codes.Unavailable, "the coordinator has not read its removed workers yet; try again")
	}
	if r.revokedLocked(id) {
		return status.Error(codes.Unauthenticated, removedWorkerMessage)
	}
	return nil
}

// revoked reports whether the certificate id is revoked.
func (r *workerRemovals) revoked(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.revokedLocked(id)
}

func (r *workerRemovals) revokedLocked(id string) bool {
	return r.byID[id].State == persis.WorkerCertificateRevoked
}

// RemoveWorker revokes every certificate of a worker that is not connected.
func (h *Handler) RemoveWorker(ctx context.Context, req *coordinatorv1.RemoveWorkerRequest) (*coordinatorv1.RemoveWorkerResponse, error) {
	if err := h.requirePeerCA(); err != nil {
		return nil, err
	}
	if req.WorkerId == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id is required")
	}
	now := h.clock()
	hb, err := h.workerPresence(ctx, req.WorkerId)
	if err != nil {
		logger.Error(ctx, "Failed to read a worker's heartbeat", tag.WorkerID(req.WorkerId), tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to read the worker's heartbeat")
	}
	if hb != nil && dispatch.WorkerHeartbeatFresh(*hb, now, h.staleHeartbeatThreshold) {
		// A coordinator that predates the certificate ID in heartbeats
		// writes a certificate connection without it.
		if hb.CertificateID == "" && hb.Connection != dispatch.WorkerConnectionCertificate {
			return nil, status.Error(codes.FailedPrecondition, peerTokenWorkerMessage)
		}
		return nil, status.Error(codes.FailedPrecondition, connectedWorkerMessage)
	}
	return h.revokeWorker(ctx, req.WorkerId, hb, now)
}

// revokeWorker revokes every certificate that names the worker workerID,
// which is not connected. hb is its stale heartbeat, or nil. A certificate
// that a worker under another ID may still connect with, as after a
// restart, keeps it connected, so the removal is refused. The removal
// counts itself on the worker ID's fence before its last listing, so an
// enrollment of the worker that runs alongside either records its
// certificate before that listing, which revokes it, or fails. An
// enrollment that started after the count is a new one, and keeps its
// certificate unless it carries a revoked key.
func (h *Handler) revokeWorker(ctx context.Context, workerID string, hb *dispatch.WorkerHeartbeatRecord, now time.Time) (*coordinatorv1.RemoveWorkerResponse, error) {
	certs, err := h.workerCertificates.List(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list worker certificates", tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to remove the worker")
	}
	var named []persis.WorkerCertificate
	for _, cert := range certs {
		if namesWorker(cert, workerID) || (hb != nil && cert.ID == hb.CertificateID) {
			named = append(named, cert)
		}
	}
	if len(named) == 0 {
		if hb != nil {
			return nil, status.Error(codes.FailedPrecondition, peerTokenWorkerMessage)
		}
		return nil, status.Error(codes.NotFound, "no worker has this ID")
	}
	presence, err := h.listPresence(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list worker heartbeats", tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to remove the worker")
	}
	for _, cert := range named {
		if mayHold(cert, presence, now, h.staleHeartbeatThreshold) {
			return nil, status.Error(codes.FailedPrecondition, connectedWorkerMessage)
		}
	}
	removals, err := h.workerCertificates.BeginWorkerRemoval(ctx, workerID)
	if err != nil {
		logger.Error(ctx, "Failed to begin a worker's removal", tag.WorkerID(workerID), tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to remove the worker")
	}
	ids, err := h.revokeAll(ctx, named)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to remove the worker")
	}
	// An enrollment confirms on the fence that no removal began since it
	// started, after it records its certificate. One that confirmed before
	// the removal counted itself recorded its certificate before this
	// listing, which finds it.
	certs, err = h.workerCertificates.List(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list worker certificates", tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to remove the worker")
	}
	var enrolled []persis.WorkerCertificate
	for _, cert := range certs {
		if cert.State != persis.WorkerCertificateRevoked && namesWorker(cert, workerID) &&
			(enrolledBefore(cert, workerID, removals) || sharesKey(cert, named)) {
			enrolled = append(enrolled, cert)
		}
	}
	more, err := h.revokeAll(ctx, enrolled)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to remove the worker")
	}
	h.warnReconnected(ctx, workerID, append(named, enrolled...))
	return &coordinatorv1.RemoveWorkerResponse{
		State:          coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVED,
		CertificateIds: append(ids, more...),
	}, nil
}

// enrolledBefore reports whether cert enrolled before the removal of
// workerID that brought its removals to removals began. A certificate that
// enrolled as another worker ID, or whose record predates the enrollment
// fields, counts as enrolled before.
func enrolledBefore(cert persis.WorkerCertificate, workerID string, removals int64) bool {
	return cert.EnrolledAs != workerID || cert.EnrolledRemovals < removals
}

// sharesKey reports whether cert has the key of one of certs.
func sharesKey(cert persis.WorkerCertificate, certs []persis.WorkerCertificate) bool {
	return slices.ContainsFunc(certs, func(other persis.WorkerCertificate) bool {
		return bytes.Equal(other.KeyFingerprint, cert.KeyFingerprint)
	})
}

// warnReconnected logs each of the revoked certs that a worker connected
// with while the removal ran. Such a worker is removed all the same: its
// next call is refused, here at once and elsewhere from the next reread.
func (h *Handler) warnReconnected(ctx context.Context, workerID string, revoked []persis.WorkerCertificate) {
	presence, err := h.listPresence(ctx)
	if err != nil {
		return
	}
	now := h.clock()
	for _, cert := range revoked {
		if mayHold(cert, presence, now, h.staleHeartbeatThreshold) {
			logger.Warn(ctx, "A worker connected while it was removed; its calls are refused from now on",
				tag.WorkerID(workerID), slog.String("certificate-id", cert.ID))
		}
	}
}

// revokeAll revokes certs and returns their IDs.
func (h *Handler) revokeAll(ctx context.Context, certs []persis.WorkerCertificate) ([]string, error) {
	ids := make([]string, 0, len(certs))
	for _, cert := range certs {
		if err := h.revoke(ctx, cert.ID); err != nil {
			return nil, err
		}
		ids = append(ids, cert.ID)
	}
	return ids, nil
}

// mayHold reports whether a worker with a fresh heartbeat in presence may
// connect with cert: its heartbeat carries the certificate, or it is a
// certificate connection that a coordinator predating the certificate ID in
// heartbeats recorded without it, under a worker ID the certificate's
// record names or on the certificate's host.
func mayHold(cert persis.WorkerCertificate, presence []dispatch.WorkerHeartbeatRecord, now time.Time, stale time.Duration) bool {
	for _, hb := range presence {
		if !dispatch.WorkerHeartbeatFresh(hb, now, stale) {
			continue
		}
		if hb.CertificateID == cert.ID {
			return true
		}
		if hb.CertificateID == "" && hb.Connection == dispatch.WorkerConnectionCertificate &&
			(cert.Host == "" || hb.Host == cert.Host || namesWorker(cert, hb.WorkerID)) {
			return true
		}
	}
	return false
}

// revoke revokes the certificate id, here at once and on other
// coordinators at their next reread.
func (h *Handler) revoke(ctx context.Context, id string) error {
	cert, err := h.workerCertificates.SetState(ctx, id, persis.WorkerCertificateRevoked)
	if err != nil {
		logger.Error(ctx, "Failed to revoke a worker certificate", slog.String("certificate-id", id), tag.Error(err))
		return err
	}
	h.removals.apply(removalOf(cert))
	logger.Info(ctx, "Worker certificate revoked", slog.String("certificate-id", id), tag.WorkerID(cert.WorkerID))
	return nil
}

// startPeerMaintenance reads the certificates in removal before the
// coordinator serves, then rereads them every 30 seconds and prunes the
// certificate and invite records once a day, until ctx ends. A coordinator
// without a peer CA starts nothing.
func (h *Handler) startPeerMaintenance(ctx context.Context) {
	if h.peerCA == nil {
		return
	}
	h.rereadRemovals(ctx)
	// The prune runs apart from the reread, so a slow prune never holds up
	// a reread.
	h.peerMaintenance.Go(func() {
		h.pruneRecords(ctx)
		prune := time.NewTicker(recordPruneInterval)
		defer prune.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-prune.C:
				h.pruneRecords(ctx)
			}
		}
	})
	h.peerMaintenance.Go(func() {
		reread := time.NewTicker(h.removalReread)
		defer reread.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-reread.C:
				h.rereadRemovals(ctx)
			}
		}
	})
}

// waitPeerMaintenance waits for the work startPeerMaintenance started to end
// after its context ended.
func (h *Handler) waitPeerMaintenance() {
	h.peerMaintenance.Wait()
}

// rereadRemovals reads the certificates in removal. A failed read keeps the
// last copy.
func (h *Handler) rereadRemovals(ctx context.Context) {
	removals, err := h.workerCertificates.ListRemovals(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to read the removed workers", tag.Error(err))
		return
	}
	h.removals.load(removals)
}

// pruneRecords deletes the certificate and invite records that the stores'
// retention rules let go.
func (h *Handler) pruneRecords(ctx context.Context) {
	now := h.clock()
	if _, err := h.workerCertificates.Prune(ctx, now); err != nil {
		logger.Warn(ctx, "Failed to prune worker certificate records", tag.Error(err))
	}
	if _, err := h.workerInvites.Prune(ctx, now); err != nil {
		logger.Warn(ctx, "Failed to prune worker invite records", tag.Error(err))
	}
}

// observeCertificateHeartbeat records a heartbeat that arrived at at with
// the worker certificate cert in the certificate's record when pass is set.
func (h *Handler) observeCertificateHeartbeat(ctx context.Context, cert *x509.Certificate, req *coordinatorv1.HeartbeatRequest, at time.Time, pass *certificatePass) {
	if pass == nil {
		return
	}
	if _, err := h.workerCertificates.RecordHeartbeat(ctx, certificateRecord(cert, req, at)); err != nil {
		logger.Warn(ctx, "Failed to record a worker certificate's heartbeat", slog.String("certificate-id", cert.Subject.CommonName), tag.Error(err))
	}
}

// certificateRecord is the record of the worker certificate cert as the
// heartbeat req reports it at at. It holds what a pruned record needs to be
// written again.
func certificateRecord(cert *x509.Certificate, req *coordinatorv1.HeartbeatRequest, at time.Time) persis.WorkerCertificate {
	fingerprint := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	record := persis.WorkerCertificate{
		ID:              cert.Subject.CommonName,
		KeyFingerprint:  fingerprint[:],
		State:           persis.WorkerCertificateActive,
		WorkerID:        req.WorkerId,
		Host:            req.Host,
		CreatedAt:       cert.NotBefore.Add(certBackdate).UTC(),
		LastHeartbeatAt: at,
	}
	for _, uri := range cert.URIs {
		if uri.Scheme == "urn" && strings.HasPrefix(uri.Opaque, workerCertInviteURNPrefix) {
			record.InviteID = strings.TrimPrefix(uri.Opaque, workerCertInviteURNPrefix)
		}
	}
	return record
}

// removalOf returns the removal of the certificate cert.
func removalOf(cert persis.WorkerCertificate) persis.WorkerCertificateRemoval {
	return persis.WorkerCertificateRemoval{
		ID:             cert.ID,
		KeyFingerprint: cert.KeyFingerprint,
		State:          cert.State,
		WorkerID:       cert.WorkerID,
	}
}

// keyRevoked reports whether fingerprint is the key fingerprint of a
// certificate that removals hold revoked.
func keyRevoked(removals []persis.WorkerCertificateRemoval, fingerprint []byte) bool {
	for _, removal := range removals {
		if removal.State == persis.WorkerCertificateRevoked && string(removal.KeyFingerprint) == string(fingerprint) {
			return true
		}
	}
	return false
}

// namesWorker reports whether the record of cert names the worker workerID.
func namesWorker(cert persis.WorkerCertificate, workerID string) bool {
	return cert.WorkerID == workerID || slices.ContainsFunc(cert.WorkerIDs, func(seen persis.WorkerIDSeen) bool { return seen.ID == workerID })
}

// listPresence returns the shared heartbeat record of every worker.
func (h *Handler) listPresence(ctx context.Context) ([]dispatch.WorkerHeartbeatRecord, error) {
	if h.workerHeartbeatStore == nil {
		return nil, nil
	}
	return h.workerHeartbeatStore.List(ctx)
}

// workerPresence returns the shared heartbeat record of workerID, or nil
// when it has none.
func (h *Handler) workerPresence(ctx context.Context, workerID string) (*dispatch.WorkerHeartbeatRecord, error) {
	if h.workerHeartbeatStore == nil {
		return nil, nil
	}
	hb, err := h.workerHeartbeatStore.Get(ctx, workerID)
	if errors.Is(err, dispatch.ErrWorkerHeartbeatNotFound) {
		return nil, nil
	}
	return hb, err
}

// callerCertificate returns the worker certificate the call carries, or
// nil.
func (h *Handler) callerCertificate(ctx context.Context) *x509.Certificate {
	if h.peerCA == nil {
		return nil
	}
	return workerCertificate(ctx, h.peerCA.cert)
}
