// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bytes"
	"cmp"
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
	// idleRevokeAttempts bounds the write attempts of one idle revocation.
	idleRevokeAttempts = 3
	// recordPruneInterval is how often a coordinator prunes the certificate
	// and invite records.
	recordPruneInterval = 24 * time.Hour
	// removingMinimum is how long a certificate stays removing at least
	// before it is revoked. A coordinator reads the certificate's state from
	// the store before it lets the worker accept a task, so the minimum only
	// has to outlast the moment from that read to the task's lease.
	removingMinimum = 30 * time.Second
	// stopRunsWait bounds how long a removal that stops the worker's runs
	// waits for them to end.
	stopRunsWait = 2 * time.Minute
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
	// heartbeatSettleInterval bounds how often heartbeats of a removing
	// worker that lists no running task check whether its removal is due.
	heartbeatSettleInterval = 5 * time.Second
	// legacyHeartbeatMessage refuses the removal of a worker whose
	// heartbeats reach a coordinator predating the certificate ID in them.
	legacyHeartbeatMessage = "this worker connects through a coordinator of an older version, which does not report its certificate: update every coordinator, then remove it"
	// removingAcceptanceMessage refuses a task to a worker in removal.
	removingAcceptanceMessage = "this worker is being removed and takes no new task"
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
	// settledAt holds when a heartbeat last settled each removing
	// certificate. An entry goes when its certificate is revoked.
	settledAt map[string]time.Time
	// removingSeen holds when this coordinator first knew each certificate
	// to be removing in the store, from which it counts the minimum wait.
	// An entry goes when its certificate is revoked.
	removingSeen map[string]time.Time
	// now reads the time for removingSeen, after the store read or write
	// that brought the state returned.
	now func() time.Time
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
		current, ok := r.byID[removal.ID]
		if ok && removalStateRank[removal.State] < removalStateRank[current.State] {
			continue
		}
		// A read that began before a stop request keeps the request and
		// its time.
		if ok && current.StopRequested {
			removal.StopRequested = true
		}
		if ok && !current.StopRunsAt.IsZero() && (removal.StopRunsAt.IsZero() || current.StopRunsAt.Before(removal.StopRunsAt)) {
			removal.StopRunsAt = current.StopRunsAt
		}
		r.byID[removal.ID] = removal
		switch removal.State {
		case persis.WorkerCertificateRevoked:
			delete(r.settledAt, removal.ID)
			delete(r.removingSeen, removal.ID)
		case persis.WorkerCertificateRemoving:
			if _, seen := r.removingSeen[removal.ID]; !seen {
				if r.removingSeen == nil {
					r.removingSeen = make(map[string]time.Time)
				}
				now := time.Now
				if r.now != nil {
					now = r.now
				}
				r.removingSeen[removal.ID] = now()
			}
		case persis.WorkerCertificateActive:
			// The copy holds only certificates in removal.
		}
	}
}

// removingFor reports whether this coordinator has known the certificate id
// to be removing for at least d at now. The count starts when the state
// reached this copy, from a clock read after the store read or write that
// brought it, so it never starts before the removal is visible.
func (r *workerRemovals) removingFor(id string, d time.Duration, now time.Time) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen, ok := r.removingSeen[id]
	return ok && now.Sub(seen) >= d
}

// settleDue reports whether a heartbeat may settle the removal of the
// certificate id at now, at most once per heartbeatSettleInterval, and
// records the check.
func (r *workerRemovals) settleDue(id string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if last, ok := r.settledAt[id]; ok && now.Sub(last) < heartbeatSettleInterval {
		return false
	}
	if r.settledAt == nil {
		r.settledAt = make(map[string]time.Time)
	}
	r.settledAt[id] = now
	return true
}

// refusal returns the error that refuses a call with the worker
// certificate id, or nil when the certificate is not revoked.
func (r *workerRemovals) refusal(id string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.loaded {
		return status.Error(codes.Unavailable, "the coordinator has not read its removed workers yet; try again")
	}
	if r.byID[id].State == persis.WorkerCertificateRevoked {
		return status.Error(codes.Unauthenticated, removedWorkerMessage)
	}
	return nil
}

// get returns the certificate id in removal, or false when it is active.
func (r *workerRemovals) get(id string) (persis.WorkerCertificateRemoval, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	removal, ok := r.byID[id]
	return removal, ok
}

// stopDeadlines returns when the wait for stopped runs of each removing
// certificate ends, also when it has already passed.
func (r *workerRemovals) stopDeadlines() map[string]time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	deadlines := make(map[string]time.Time)
	for _, removal := range r.byID {
		if removal.State == persis.WorkerCertificateRemoving && !removal.StopRunsAt.IsZero() {
			deadlines[removal.ID] = removal.StopRunsAt.Add(stopRunsWait)
		}
	}
	return deadlines
}

// removing returns the certificates in state removing.
func (r *workerRemovals) removing() []persis.WorkerCertificateRemoval {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var removing []persis.WorkerCertificateRemoval
	for _, removal := range r.byID {
		if removal.State == persis.WorkerCertificateRemoving {
			removing = append(removing, removal)
		}
	}
	return removing
}

// RemoveWorker revokes the certificate of a worker that is not connected,
// and marks the certificate of a connected worker removing, so it is
// revoked once the runs the worker holds end. With stop, it also cancels
// those runs.
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
	connected := hb != nil && dispatch.WorkerHeartbeatFresh(*hb, now, h.staleHeartbeatThreshold)
	if connected && hb.CertificateID == "" {
		// A coordinator that predates the certificate ID in heartbeats
		// writes a certificate connection without it.
		if hb.Connection == dispatch.WorkerConnectionCertificate {
			return nil, status.Error(codes.FailedPrecondition, legacyHeartbeatMessage)
		}
		return nil, status.Error(codes.FailedPrecondition, peerTokenWorkerMessage)
	}
	stop := req.Runs == coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_STOP
	if !connected {
		return h.revokeWorker(ctx, req.WorkerId, hb, now, stop)
	}
	removals, err := h.workerCertificates.BeginWorkerRemoval(ctx, req.WorkerId)
	if err != nil {
		logger.Error(ctx, "Failed to begin a worker's removal", tag.WorkerID(req.WorkerId), tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to remove the worker")
	}
	resp, err := h.startRemoval(ctx, hb.CertificateID, req.WorkerId, stop)
	if err != nil {
		return nil, err
	}
	more, err := h.revokeEnrolledAlongside(ctx, req.WorkerId, removals, map[string]bool{hb.CertificateID: true})
	if err != nil {
		return nil, err
	}
	resp.CertificateIds = append(resp.CertificateIds, more...)
	return resp, nil
}

// startRemoval marks the certificate certID removing for the connected
// worker workerID, and with stop cancels the runs its holders hold. It
// removes that certificate only, so it leaves the enrollments of workerID
// alone.
func (h *Handler) startRemoval(ctx context.Context, certID, workerID string, stop bool) (*coordinatorv1.RemoveWorkerResponse, error) {
	// The removal names the worker as it connects now: a worker ID that
	// changed at a restart may not have reached the record yet.
	cert, err := h.workerCertificates.MarkRemoving(ctx, certID, workerID, h.clock, stop)
	if errors.Is(err, persis.ErrWorkerCertificateNotFound) {
		// A worker back after its record was pruned connects before its
		// heartbeat writes the record again, within a minute.
		return nil, status.Error(codes.Unavailable, "the worker's certificate record is being written again; try again in a minute")
	}
	if err != nil {
		logger.Error(ctx, "Failed to mark a worker certificate removing",
			tag.WorkerID(workerID), slog.String("certificate-id", certID), tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to remove the worker")
	}
	h.removals.apply(removalOf(cert))
	resp := &coordinatorv1.RemoveWorkerResponse{
		State:          coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING,
		CertificateIds: []string{cert.ID},
	}
	if cert.State == persis.WorkerCertificateRevoked {
		resp.State = coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVED
		return resp, nil
	}
	logger.Info(ctx, "Worker removal started",
		tag.WorkerID(workerID), slog.String("certificate-id", cert.ID), slog.Bool("stop-runs", stop))
	if stop {
		// Every coordinator counts the 2-minute wait from the time the
		// stop request's write recorded.
		h.wakeStopDeadlines()
		h.cancelHeldRuns(ctx, removalOf(cert))
	}
	return resp, nil
}

// revokeWorker revokes every certificate that names the worker workerID,
// which is not connected. hb is its stale heartbeat, or nil. A certificate
// that a worker under another ID still connects with, as after a restart,
// goes through removing for that worker instead, and one that such a
// worker may connect with through a coordinator predating the certificate
// ID in heartbeats refuses the removal. A certificate that an enrollment
// running alongside records for the worker is revoked too, as
// revokeEnrolledAlongside states.
func (h *Handler) revokeWorker(ctx context.Context, workerID string, hb *dispatch.WorkerHeartbeatRecord, now time.Time, stop bool) (*coordinatorv1.RemoveWorkerResponse, error) {
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
	var unheld []persis.WorkerCertificate
	holders := make(map[string]string)
	for _, cert := range named {
		if holder := freshHolder(cert.ID, presence, now, h.staleHeartbeatThreshold); holder != "" {
			holders[cert.ID] = holder
			continue
		}
		if legacyHolder(cert, presence, now, h.staleHeartbeatThreshold) {
			return nil, status.Error(codes.FailedPrecondition, legacyHeartbeatMessage)
		}
		unheld = append(unheld, cert)
	}
	resp := &coordinatorv1.RemoveWorkerResponse{State: coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVED}
	for _, cert := range named {
		holder, ok := holders[cert.ID]
		if !ok {
			continue
		}
		removing, err := h.startRemoval(ctx, cert.ID, holder, stop)
		if err != nil {
			return nil, err
		}
		if removing.State == coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING {
			resp.State = removing.State
		}
		resp.CertificateIds = append(resp.CertificateIds, cert.ID)
	}
	removals, err := h.workerCertificates.BeginWorkerRemoval(ctx, workerID)
	if err != nil {
		logger.Error(ctx, "Failed to begin a worker's removal", tag.WorkerID(workerID), tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to remove the worker")
	}
	ids, err := h.revokeAll(ctx, unheld)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to remove the worker")
	}
	resp.CertificateIds = append(resp.CertificateIds, ids...)
	known := make(map[string]bool, len(named))
	for _, cert := range named {
		known[cert.ID] = true
	}
	more, err := h.revokeEnrolledAlongside(ctx, workerID, removals, known)
	if err != nil {
		return nil, err
	}
	resp.CertificateIds = append(resp.CertificateIds, more...)
	h.warnReconnected(ctx, workerID, unheld)
	return resp, nil
}

// revokeEnrolledAlongside revokes each active certificate of the worker
// workerID that known lacks, and returns their IDs. The removal counted
// itself on the worker ID's fence before, bringing its removals to
// removals, and an enrollment confirms there after it records its
// certificate, so an enrollment that ran alongside either recorded its
// certificate before this listing, which finds it, or fails. It spares an
// enrollment that started after the count, unless it carries the key of a
// known certificate, and a certificate that another worker ID connects
// with, also through a coordinator predating the certificate ID in
// heartbeats.
func (h *Handler) revokeEnrolledAlongside(ctx context.Context, workerID string, removals int64, known map[string]bool) ([]string, error) {
	certs, err := h.workerCertificates.List(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list worker certificates", tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to remove the worker")
	}
	presence, err := h.listPresence(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list worker heartbeats", tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to remove the worker")
	}
	now := h.clock()
	var knownCerts []persis.WorkerCertificate
	for _, cert := range certs {
		if known[cert.ID] {
			knownCerts = append(knownCerts, cert)
		}
	}
	var enrolled []persis.WorkerCertificate
	for _, cert := range certs {
		if known[cert.ID] || cert.State != persis.WorkerCertificateActive || !namesWorker(cert, workerID) {
			continue
		}
		if holder := freshHolder(cert.ID, presence, now, h.staleHeartbeatThreshold); holder != "" && holder != workerID {
			continue
		}
		// A worker may connect with it through a coordinator predating the
		// certificate ID in heartbeats.
		if legacyHolder(cert, presence, now, h.staleHeartbeatThreshold) {
			continue
		}
		if enrolledBefore(cert, workerID, removals) || sharesKey(cert, knownCerts) {
			enrolled = append(enrolled, cert)
		}
	}
	ids, err := h.revokeAll(ctx, enrolled)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to remove the worker")
	}
	return ids, nil
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
		if freshHolder(cert.ID, presence, now, h.staleHeartbeatThreshold) != "" || legacyHolder(cert, presence, now, h.staleHeartbeatThreshold) {
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

// freshHolder returns the ID of a worker whose fresh heartbeat in presence
// carries the certificate certID, or empty when none does.
func freshHolder(certID string, presence []dispatch.WorkerHeartbeatRecord, now time.Time, stale time.Duration) string {
	for _, hb := range presence {
		if hb.CertificateID == certID && dispatch.WorkerHeartbeatFresh(hb, now, stale) {
			return hb.WorkerID
		}
	}
	return ""
}

// legacyHolder reports whether a worker may connect with cert through a
// coordinator predating the certificate ID in heartbeats: a fresh
// certificate connection recorded without it, under a worker ID the
// certificate's record names or on the certificate's host.
func legacyHolder(cert persis.WorkerCertificate, presence []dispatch.WorkerHeartbeatRecord, now time.Time, stale time.Duration) bool {
	for _, hb := range presence {
		if hb.CertificateID == "" && hb.Connection == dispatch.WorkerConnectionCertificate &&
			(cert.Host == "" || hb.Host == cert.Host || namesWorker(cert, hb.WorkerID)) && dispatch.WorkerHeartbeatFresh(hb, now, stale) {
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

// cancelHeldRuns sends the cancel request for each run whose lease names
// the certificate of removal. A pass that cannot list the leases leaves the
// runs to the next reread, which sends the requests again.
func (h *Handler) cancelHeldRuns(ctx context.Context, removal persis.WorkerCertificateRemoval) {
	leases, err := h.listLeases(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to list run leases to stop a removed worker's runs", tag.WorkerID(removal.WorkerID), tag.Error(err))
		return
	}
	h.stopLeasedRuns(ctx, removal.ID, leases)
}

// heldRun is a run a worker holds: the DAG run its cancel request names,
// and the attempt the worker runs, or empty when the worker names none.
type heldRun struct {
	cancel     *coordinatorv1.RequestCancelRequest
	attemptKey string
}

// stopLeasedRuns sends the cancel request for each attempt whose lease in
// leases names the certificate certID, also a lease gone stale.
func (h *Handler) stopLeasedRuns(ctx context.Context, certID string, leases []dispatch.DAGRunLease) {
	for _, lease := range leases {
		if lease.CertificateID != certID {
			continue
		}
		h.stopAttempt(ctx, certID, lease.AttemptKey)
	}
}

// stopAttempt records on the lease of the attempt attemptKey that the
// removal of the certificate certID asks it to stop, when, as the request is
// written, the lease names that certificate; a lease that a later write
// left naming another certificate or none gets no request. The worker that
// holds the lease learns of the request from its heartbeat and ends the
// run, and a run whose worker no longer reports ends through the stale-lease
// check. The coordinator never ends the attempt itself, so an acceptance of
// the attempt that runs alongside never races its end.
func (h *Handler) stopAttempt(ctx context.Context, certID, attemptKey string) {
	if h.dagRunLeaseStore == nil || attemptKey == "" {
		return
	}
	if _, err := h.dagRunLeaseStore.RequestStop(ctx, attemptKey, certID); err != nil {
		logger.Warn(ctx, "Failed to stop a removed worker's run", tag.AttemptKey(attemptKey), tag.Error(err))
	}
}

// stoppedRuns returns the attempt keys of tasks whose lease records a stop
// request of the removal of the certificate certID. It reads the leases
// only while this coordinator's copy shows that certificate in a removal
// with stop, so a heartbeat of any other worker reads none. A stop that
// another coordinator requested reaches the worker here from the next
// reread of the removals, within 30 seconds.
func (h *Handler) stoppedRuns(ctx context.Context, certID string, tasks []*coordinatorv1.RunningTask) []string {
	if h.dagRunLeaseStore == nil {
		return nil
	}
	if removal, ok := h.removals.get(certID); !ok || !removal.StopRequested {
		return nil
	}
	var stopped []string
	for _, task := range tasks {
		if task.GetAttemptKey() == "" {
			continue
		}
		lease, err := h.dagRunLeaseStore.Get(ctx, task.AttemptKey)
		if err == nil && lease.StopRequested && lease.CertificateID == certID {
			stopped = append(stopped, task.AttemptKey)
		}
	}
	return stopped
}

// heldRuns returns the runs that the workers of holders hold: those their
// heartbeats list, and those with a fresh lease that counts says is theirs.
func heldRuns(holders []dispatch.WorkerHeartbeatRecord, leases []dispatch.DAGRunLease, counts func(dispatch.DAGRunLease) bool, now time.Time, staleLease time.Duration) []heldRun {
	byKey := make(map[string]heldRun)
	for _, hb := range holders {
		if hb.Stats == nil {
			continue
		}
		for _, task := range hb.Stats.RunningTasks {
			byKey[runKey(task.AttemptKey, task.DAGRunID)] = heldRun{attemptKey: task.AttemptKey, cancel: &coordinatorv1.RequestCancelRequest{
				DagName: task.DAGName, DagRunId: task.DAGRunID, RootDagRunName: task.RootDAGRunName, RootDagRunId: task.RootDAGRunID,
			}}
		}
	}
	for _, lease := range leases {
		if !lease.IsFresh(now, staleLease) || !counts(lease) {
			continue
		}
		byKey[runKey(lease.AttemptKey, lease.DAGRun.ID)] = heldRun{attemptKey: lease.AttemptKey, cancel: &coordinatorv1.RequestCancelRequest{
			DagName: lease.DAGRun.Name, DagRunId: lease.DAGRun.ID, RootDagRunName: lease.Root.Name, RootDagRunId: lease.Root.ID,
		}}
	}
	runs := make([]heldRun, 0, len(byKey))
	for _, run := range byKey {
		runs = append(runs, run)
	}
	return runs
}

// runKey returns attemptKey, or dagRunID for a task that names no attempt.
func runKey(attemptKey, dagRunID string) string {
	if attemptKey != "" {
		return attemptKey
	}
	return dagRunID
}

// startPeerMaintenance reads the certificates in removal before the
// coordinator serves, then rereads them every 30 seconds and prunes the
// certificate and invite records once a day, until ctx ends. A coordinator
// without a peer CA starts nothing.
func (h *Handler) startPeerMaintenance(ctx context.Context) {
	if h.peerCA == nil {
		return
	}
	// The first read arms the stop deadlines before any listing, so a
	// slow listing at startup never holds one up.
	h.loadRemovals(ctx)
	h.peerMaintenance.Go(func() { h.runStopDeadlines(ctx) })
	// The prune runs apart from the removal loop, so a slow prune never
	// holds up a reread or a stop deadline.
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
		h.settleLoaded(ctx)
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

// runStopDeadlines revokes each stop removal when its 2-minute wait ends,
// until ctx ends. It reads only this coordinator's copy of the removals, and
// each revocation runs on its own, so no listing, cancel request, reread,
// or other certificate's revocation holds one up. A revocation that fails is
// tried again heartbeatSettleInterval later, and only that certificate
// waits. The revocation's write still shares the store's collection lock,
// so a slow write or scan of that collection, such as a prune, can delay
// it.
func (h *Handler) runStopDeadlines(ctx context.Context) {
	timer := time.NewTimer(recordPruneInterval)
	defer timer.Stop()
	for {
		now := h.clock()
		if next := h.startDueStops(ctx, now); !next.IsZero() {
			timer.Reset(next.Sub(now))
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-h.removalStarted:
		}
	}
}

// stopDeadlineState is the progress of each stop removal's deadline: the
// revocations under way and, for each whose write failed, when it is tried
// again. Every deadline write goes through it, so no certificate has two
// at once.
type stopDeadlineState struct {
	mu       sync.Mutex
	inFlight map[string]bool
	retryAt  map[string]time.Time
}

// claim marks a revocation of the certificate id under way and reports
// whether none was.
func (st *stopDeadlineState) claim(id string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.inFlight[id] {
		return false
	}
	if st.inFlight == nil {
		st.inFlight = make(map[string]bool)
	}
	st.inFlight[id] = true
	return true
}

// release ends the revocation of the certificate id that claim marked.
func (st *stopDeadlineState) release(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.inFlight, id)
}

// startDueStops starts the revocation of each stop removal whose deadline,
// or retry after a failed write, has come at now, each in its own
// goroutine, and returns the next such time, or zero when none waits.
func (h *Handler) startDueStops(ctx context.Context, now time.Time) time.Time {
	deadlines := h.removals.stopDeadlines()
	st := &h.stops
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.inFlight == nil {
		st.inFlight = make(map[string]bool)
	}
	if st.retryAt == nil {
		st.retryAt = make(map[string]time.Time)
	}
	for id := range st.retryAt {
		if _, ok := deadlines[id]; !ok {
			delete(st.retryAt, id)
		}
	}
	var next time.Time
	for id, at := range deadlines {
		if st.inFlight[id] {
			continue
		}
		if retry, ok := st.retryAt[id]; ok {
			at = retry
		}
		if at.After(now) {
			if next.IsZero() || at.Before(next) {
				next = at
			}
			continue
		}
		st.inFlight[id] = true
		h.peerMaintenance.Go(func() {
			err := h.revoke(ctx, id)
			st.mu.Lock()
			delete(st.inFlight, id)
			if err != nil {
				st.retryAt[id] = h.clock().Add(heartbeatSettleInterval)
			} else {
				delete(st.retryAt, id)
			}
			st.mu.Unlock()
			h.wakeStopDeadlines()
		})
	}
	return next
}

// wakeStopDeadlines makes runStopDeadlines read the removals again, as after
// a stop removal began or a revocation ended.
func (h *Handler) wakeStopDeadlines() {
	select {
	case h.removalStarted <- struct{}{}:
	default:
	}
}

// waitPeerMaintenance waits for the work startPeerMaintenance started to end
// after its context ended.
func (h *Handler) waitPeerMaintenance() {
	h.peerMaintenance.Wait()
}

// rereadRemovals reads the certificates in removal and revokes each
// removing one that is due. A failed read keeps the last copy.
func (h *Handler) rereadRemovals(ctx context.Context) {
	if h.loadRemovals(ctx) {
		h.settleLoaded(ctx)
	}
}

// loadRemovals reads the certificates in removal into the copy and wakes
// the stop deadlines. A failed read keeps the last copy, whose deadlines
// still revoke, and reports false.
func (h *Handler) loadRemovals(ctx context.Context) bool {
	removals, err := h.workerCertificates.ListRemovals(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to read the removed workers", tag.Error(err))
		return false
	}
	h.removals.load(removals)
	h.wakeStopDeadlines()
	return true
}

// settleLoaded settles each removing certificate of the copy.
func (h *Handler) settleLoaded(ctx context.Context) {
	if removing := h.removals.removing(); len(removing) > 0 {
		h.settleRemovals(ctx, removing, h.clock())
	}
}

// settleRemovals revokes each certificate of removing that is due.
func (h *Handler) settleRemovals(ctx context.Context, removing []persis.WorkerCertificateRemoval, now time.Time) {
	presence, err := h.listPresence(ctx)
	var leases []dispatch.DAGRunLease
	if err == nil {
		leases, err = h.listLeases(ctx)
	}
	if err != nil {
		// A stop removal past its wait is revoked by its deadline, which
		// needs no list.
		logger.Warn(ctx, "Failed to list worker heartbeats or run leases for removing workers", tag.Error(err))
		return
	}
	for _, removal := range removing {
		// A stop removal past its wait is its deadline's to revoke.
		if stopRunsDue(removal, now) {
			continue
		}
		// The cancel requests go out again while a stop removal waits, so
		// a run that a failed pass missed still gets one. They change
		// nothing for a run that has one.
		if removal.StopRequested {
			h.stopLeasedRuns(ctx, removal.ID, leases)
		}
		if h.removalDue(removal, presence, leases, now) {
			h.revokeIdle(ctx, removal, now)
		}
	}
}

// revokeIdle revokes the removing certificate of removal once it is due
// from listings taken after its removal record was read, in one write
// from that version: an acceptance confirmed in between changes the
// record, so the listings are taken again and see its lease. The holder
// it waits for is the one that version names. Right before its write, after
// the listings and reads, it takes the same per-certificate state as the
// stop deadline, so the two never write at once and no listing or read
// holds up the deadline, and it leaves a stop removal whose wait ended
// meanwhile to the deadline.
func (h *Handler) revokeIdle(ctx context.Context, removal persis.WorkerCertificateRemoval, now time.Time) {
	claimed := false
	idle := func(ctx context.Context, read persis.WorkerCertificate) (bool, error) {
		presence, err := h.listPresence(ctx)
		if err != nil {
			return false, err
		}
		leases, err := h.listLeases(ctx)
		if err != nil {
			return false, err
		}
		return h.removalDue(removalOf(read), presence, leases, now), nil
	}
	commit := func(read persis.WorkerCertificate) bool {
		if !h.stops.claim(removal.ID) {
			return false
		}
		claimed = true
		return !stopRunsDue(removalOf(read), h.clock())
	}
	var (
		cert    persis.WorkerCertificate
		revoked bool
		err     error
	)
	// A write that lost to an acceptance reads and lists again. Each
	// attempt lets the state go once it ends, and a later settle takes up
	// one that keeps losing.
	for range idleRevokeAttempts {
		cert, revoked, err = h.workerCertificates.RevokeIfIdle(ctx, removal.ID, idle, commit)
		if claimed {
			h.stops.release(removal.ID)
			claimed = false
			h.wakeStopDeadlines()
		}
		if !errors.Is(err, persis.ErrConflict) {
			break
		}
	}
	if err != nil {
		logger.Warn(ctx, "Failed to revoke a removed worker's certificate", slog.String("certificate-id", removal.ID), tag.Error(err))
		return
	}
	h.removals.apply(removalOf(cert))
	if revoked {
		logger.Info(ctx, "Worker certificate revoked", slog.String("certificate-id", cert.ID), tag.WorkerID(removal.WorkerID))
	}
}

// removalDue reports whether the removing certificate removal is to be
// revoked: no worker that holds it has a fresh heartbeat, 2 minutes passed
// since its runs were asked to stop, or it was removing for 30 seconds and
// no worker that holds it holds a run. A worker holds it when its fresh
// heartbeat carries it, so a worker ID that changes during the removal is
// still waited for, and a replacement that took the worker ID with another
// certificate is not. A heartbeat without a certificate ID, from a
// coordinator predating the field, holds it when it comes from the worker
// the removal names.
func (h *Handler) removalDue(removal persis.WorkerCertificateRemoval, presence []dispatch.WorkerHeartbeatRecord, leases []dispatch.DAGRunLease, now time.Time) bool {
	return len(h.removalHolders(removal, presence, now)) == 0 ||
		(h.removals.removingFor(removal.ID, removingMinimum, now) && len(h.heldRunsOf(removal, presence, leases, now)) == 0)
}

// removalHolders returns the fresh heartbeats of the workers that hold the
// certificate of removal.
func (h *Handler) removalHolders(removal persis.WorkerCertificateRemoval, presence []dispatch.WorkerHeartbeatRecord, now time.Time) []dispatch.WorkerHeartbeatRecord {
	var holders []dispatch.WorkerHeartbeatRecord
	for _, hb := range presence {
		if (hb.CertificateID == removal.ID || (hb.CertificateID == "" && hb.WorkerID == removal.WorkerID)) && dispatch.WorkerHeartbeatFresh(hb, now, h.staleHeartbeatThreshold) {
			holders = append(holders, hb)
		}
	}
	return holders
}

// heldRunsOf returns the runs that the workers holding the certificate of
// removal hold.
func (h *Handler) heldRunsOf(removal persis.WorkerCertificateRemoval, presence []dispatch.WorkerHeartbeatRecord, leases []dispatch.DAGRunLease, now time.Time) []heldRun {
	holders := h.removalHolders(removal, presence, now)
	workerIDs := map[string]bool{removal.WorkerID: true}
	for _, hb := range holders {
		workerIDs[hb.WorkerID] = true
	}
	// A lease that names the certificate counts. One without a certificate
	// ID, written before the field, counts under a holder's worker ID, which
	// errs on the safe side.
	counts := func(lease dispatch.DAGRunLease) bool {
		return lease.CertificateID == removal.ID || (lease.CertificateID == "" && workerIDs[lease.WorkerID])
	}
	return heldRuns(holders, leases, counts, now, h.staleLeaseThreshold)
}

// stopRunsDue reports whether 2 minutes passed since the removal asked the
// worker's runs to stop.
func stopRunsDue(removal persis.WorkerCertificateRemoval, now time.Time) bool {
	return !removal.StopRunsAt.IsZero() && now.Sub(removal.StopRunsAt) >= stopRunsWait
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
// the worker certificate cert in the certificate's record when pass is set,
// and settles the certificate's removal when the heartbeat may make it due:
// it lists no running task after the minimum wait, or the wait for stopped
// runs has passed.
func (h *Handler) observeCertificateHeartbeat(ctx context.Context, cert *x509.Certificate, req *coordinatorv1.HeartbeatRequest, at time.Time, pass *certificatePass) {
	certID := cert.Subject.CommonName
	if pass != nil {
		if err := h.workerCertificates.RecordHeartbeat(ctx, certificateRecord(cert, req, at)); err != nil {
			logger.Warn(ctx, "Failed to record a worker certificate's heartbeat", slog.String("certificate-id", certID), tag.Error(err))
		}
	}
	removal, ok := h.removals.get(certID)
	if !ok || removal.State != persis.WorkerCertificateRemoving {
		return
	}
	// A stop removal past its wait is the deadline loop's to revoke, so
	// this heartbeat only wakes it.
	if stopRunsDue(removal, at) {
		h.wakeStopDeadlines()
		return
	}
	// The bound keeps a revocation that keeps failing from being retried
	// at every heartbeat.
	idle := len(req.GetStats().GetRunningTasks()) == 0 && h.removals.removingFor(certID, removingMinimum, h.clock())
	if idle && h.removals.settleDue(certID, at) {
		h.settleRemovals(ctx, []persis.WorkerCertificateRemoval{removal}, at)
	}
}

// refuseRemovingAcceptance refuses the acceptance of the task claimed with
// claimToken when the call's worker certificate is in removal. It reads the
// certificate's state from the store, so a coordinator that has not reread
// the removals yet refuses too. It leaves the claim, so an acceptance still
// in flight on another coordinator cannot race a release, and the task
// returns to the queue when the claim expires. It returns nil when the
// acceptance may go on.
func (h *Handler) refuseRemovingAcceptance(ctx context.Context, workerID, claimToken string) (*coordinatorv1.AckTaskClaimResponse, error) {
	cert := h.callerCertificate(ctx)
	if cert == nil {
		return nil, nil
	}
	record, err := h.workerCertificates.Get(ctx, cert.Subject.CommonName)
	switch {
	case errors.Is(err, persis.ErrWorkerCertificateNotFound):
		// A certificate whose record was pruned still connects.
		return nil, nil
	case err != nil:
		logger.Error(ctx, "Failed to read a worker certificate", slog.String("certificate-id", cert.Subject.CommonName), tag.Error(err))
		// The worker retries an unavailable acceptance.
		return nil, status.Error(codes.Unavailable, "failed to read the worker's certificate; try again")
	case record.State == persis.WorkerCertificateActive:
		return nil, nil
	}
	h.removals.apply(removalOf(record))
	if record.State == persis.WorkerCertificateRevoked {
		return nil, status.Error(codes.Unauthenticated, removedWorkerMessage)
	}
	// An acceptance retried after its lease was created goes the usual
	// path, so the run it holds is waited for. So does an acceptance of a
	// claim that is gone or another worker's, which that path answers.
	claimed, err := h.dispatchTaskStore.GetClaim(ctx, claimToken)
	switch {
	case errors.Is(err, dispatch.ErrDispatchTaskNotFound):
		return nil, nil
	case err != nil:
		return nil, status.Error(codes.Internal, "failed to load claim: "+err.Error())
	case claimed.Task == nil:
		return nil, nil
	}
	owner := cmp.Or(workerID, claimed.WorkerID)
	if claimed.WorkerID != "" && owner != claimed.WorkerID {
		return nil, nil
	}
	lease, err := h.dagRunLeaseStore.Get(ctx, claimed.Task.AttemptKey)
	switch {
	case err == nil && lease.MatchesClaim(claimed.Task.AttemptKey, owner) && lease.ClaimToken == claimToken:
		return nil, nil
	case err != nil && !errors.Is(err, dispatch.ErrDAGRunLeaseNotFound):
		// Whether this acceptance already holds the run is unknown, so
		// the worker asks again.
		return nil, status.Error(codes.Unavailable, "failed to read the task's lease; try again")
	}
	return &coordinatorv1.AckTaskClaimResponse{Accepted: false, Error: removingAcceptanceMessage}, nil
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
	record.InviteID = certificateInviteID(cert)
	return record
}

// certificateInviteID returns the invite the worker certificate cert names,
// or empty when it names none.
func certificateInviteID(cert *x509.Certificate) string {
	for _, uri := range cert.URIs {
		if uri.Scheme == "urn" && strings.HasPrefix(uri.Opaque, workerCertInviteURNPrefix) {
			return strings.TrimPrefix(uri.Opaque, workerCertInviteURNPrefix)
		}
	}
	return ""
}

// removalOf returns the removal of the certificate cert.
func removalOf(cert persis.WorkerCertificate) persis.WorkerCertificateRemoval {
	return persis.WorkerCertificateRemoval{
		ID:             cert.ID,
		KeyFingerprint: cert.KeyFingerprint,
		State:          cert.State,
		WorkerID:       cmp.Or(cert.HolderID, cert.WorkerID),
		RemovingAt:     cert.RemovingAt,
		StopRequested:  cert.StopRequested,
		StopRunsAt:     cert.StopRunsAt,
	}
}

// keyRemoved reports whether fingerprint is the key fingerprint of a
// certificate in removals, removing or revoked. A removing one is revoked
// once its runs end, so its key is refused before then too.
func keyRemoved(removals []persis.WorkerCertificateRemoval, fingerprint []byte) bool {
	for _, removal := range removals {
		if string(removal.KeyFingerprint) == string(fingerprint) {
			return true
		}
	}
	return false
}

// namesWorker reports whether the record of cert names the worker workerID.
func namesWorker(cert persis.WorkerCertificate, workerID string) bool {
	return cert.WorkerID == workerID || slices.ContainsFunc(cert.WorkerIDs, func(seen persis.WorkerIDSeen) bool { return seen.ID == workerID })
}

// workerInRemoval reports whether the worker workerID is connected with a
// certificate in removal, as this coordinator's copy or the store holds it.
// A run bound to such a worker's node is refused. It fails when it cannot
// read the worker's heartbeat or certificate, and a pruned record counts as
// active.
func (h *Handler) workerInRemoval(ctx context.Context, workerID string) (bool, error) {
	hb, err := h.workerPresence(ctx, workerID)
	if err != nil {
		return false, err
	}
	if hb == nil || hb.CertificateID == "" || !dispatch.WorkerHeartbeatFresh(*hb, h.clock().UTC(), h.staleHeartbeatThreshold) {
		return false, nil
	}
	if _, removed := h.removals.get(hb.CertificateID); removed {
		return true, nil
	}
	if h.workerCertificates == nil {
		return false, nil
	}
	record, err := h.workerCertificates.Get(ctx, hb.CertificateID)
	switch {
	case errors.Is(err, persis.ErrWorkerCertificateNotFound):
		return false, nil
	case err != nil:
		logger.Warn(ctx, "Failed to read a worker certificate", slog.String("certificate-id", hb.CertificateID), tag.Error(err))
		return false, err
	case record.State == persis.WorkerCertificateActive:
		return false, nil
	}
	h.removals.apply(removalOf(record))
	return true, nil
}

// dropClaim deletes the claim claimToken, whose acceptance was refused after
// its lease was written.
func (h *Handler) dropClaim(ctx context.Context, claimToken string) {
	if err := h.dispatchTaskStore.DeleteClaim(context.WithoutCancel(ctx), claimToken); err != nil && !errors.Is(err, dispatch.ErrDispatchTaskNotFound) {
		logger.Warn(ctx, "Failed to drop a refused task claim", tag.Error(err))
	}
}

// listPresence returns the shared heartbeat record of every worker.
func (h *Handler) listPresence(ctx context.Context) ([]dispatch.WorkerHeartbeatRecord, error) {
	if h.workerHeartbeatStore == nil {
		return nil, nil
	}
	return h.workerHeartbeatStore.List(ctx)
}

// settleAcceptedRun reads the call's worker certificate again after the
// lease of task was created, and refuses the acceptance when the
// certificate was revoked meanwhile. A refusal leaves the lease, which the
// stale-lease check ends, since a retry of the same claim may own it. A
// removal with stop that began after the acceptance was checked may have
// run its own pass before the lease existed, so the run gets its cancel
// request here; the acceptance stands, and the worker ends the run once it
// learns of the request. A removal that starts later sees the lease in its
// own pass.
func (h *Handler) settleAcceptedRun(ctx context.Context, task *coordinatorv1.Task) error {
	cert := h.callerCertificate(ctx)
	if cert == nil {
		return nil
	}
	// The confirmation is a write on the removal record, so a revocation
	// that read the record before it fails and looks again, finding this
	// call's lease.
	record, err := h.workerCertificates.ConfirmAcceptance(ctx, cert.Subject.CommonName)
	switch {
	case errors.Is(err, persis.ErrWorkerCertificateNotFound):
		return nil
	case err != nil:
		// The claim and the lease stay, and the worker's retry reads the
		// certificate again.
		logger.Warn(ctx, "Failed to read a worker certificate", slog.String("certificate-id", cert.Subject.CommonName), tag.Error(err))
		return status.Error(codes.Unavailable, "failed to read the worker's certificate; try again")
	}
	if record.State == persis.WorkerCertificateRevoked {
		h.removals.apply(removalOf(record))
		return status.Error(codes.Unauthenticated, removedWorkerMessage)
	}
	if record.StopRequested {
		h.stopAttempt(ctx, record.ID, task.AttemptKey)
	}
	return nil
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

// listLeases returns the run leases of every worker.
func (h *Handler) listLeases(ctx context.Context) ([]dispatch.DAGRunLease, error) {
	if h.dagRunLeaseStore == nil {
		return nil, nil
	}
	return h.dagRunLeaseStore.ListAll(ctx)
}

// callerCertificate returns the worker certificate the call carries, or
// nil.
func (h *Handler) callerCertificate(ctx context.Context) *x509.Certificate {
	if h.peerCA == nil {
		return nil
	}
	return workerCertificate(ctx, h.peerCA.cert)
}
