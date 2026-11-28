// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

var removalStart = time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)

// attemptKeyOf returns the attempt key of attempt-1 of the root run runID
// of dag, as its status gives it.
func attemptKeyOf(runID string) string {
	return ir.GenerateAttemptKey("dag", runID, "dag", runID, "attempt-1")
}

// removalFixture is a coordinator over the shared stores, with a clock the
// test moves.
type removalFixture struct {
	h          *Handler
	now        time.Time
	ca         *PeerCA
	runs       *mockDAGRunStore
	certs      persis.WorkerCertificateStore
	invites    persis.WorkerInviteStore
	heartbeats dispatch.WorkerHeartbeatStore
	leases     dispatch.DAGRunLeaseStore
	dispatches dispatch.DispatchTaskStore
}

// removalWorker is a worker on a worker certificate.
type removalWorker struct {
	id     string
	certID string
	cert   *x509.Certificate
	// ctx is the context of a call that carries the certificate.
	ctx context.Context
}

func newRemovalFixture(t *testing.T) *removalFixture {
	t.Helper()
	ca, err := LoadOrCreatePeerCA(filepath.Join(t.TempDir(), "peer", "ca.pem"))
	require.NoError(t, err)
	backend := testutil.NewMemoryBackend()
	certs, err := store.NewWorkerCertificateStore(backend.Collection(persis.CollectionWorkerCertificates))
	require.NoError(t, err)
	invites, err := store.NewWorkerInviteStore(backend.Collection(persis.CollectionWorkerInvites))
	require.NoError(t, err)
	baseDir := t.TempDir()
	f := &removalFixture{
		now:        removalStart,
		ca:         ca,
		runs:       newMockDAGRunStore(),
		certs:      certs,
		invites:    invites,
		heartbeats: newTestWorkerHeartbeatStore(baseDir),
		leases:     newTestDAGRunLeaseStore(baseDir),
		dispatches: newTestDispatchTaskStore(baseDir),
	}
	f.h = f.coordinator(t)
	return f
}

// coordinator returns a coordinator on the fixture's stores and clock, as
// one starts: with the removed workers read.
func (f *removalFixture) coordinator(t *testing.T) *Handler {
	t.Helper()
	h := NewHandler(HandlerConfig{
		DAGRunRepository:       f.runs.repository,
		DispatchTaskStore:      f.dispatches,
		DAGRunLeaseStore:       f.leases,
		WorkerHeartbeatStore:   f.heartbeats,
		PeerCA:                 f.ca,
		WorkerInviteStore:      f.invites,
		WorkerCertificateStore: f.certs,
	})
	h.clock = func() time.Time { return f.now }
	h.rereadRemovals(t.Context())
	t.Cleanup(func() { h.Close(context.Background()) })
	return h
}

// enroll records a certificate for the worker id, as Enroll does.
func (f *removalFixture) enroll(t *testing.T, id string) removalWorker {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	certID := uuid.NewString()
	der, err := f.ca.issueWorkerCertificate(&key.PublicKey, certID, "invite-1", f.now)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	fingerprint := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	require.NoError(t, f.certs.Create(t.Context(), persis.WorkerCertificate{
		ID: certID, InviteID: "invite-1", KeyFingerprint: fingerprint[:], State: persis.WorkerCertificateActive,
		WorkerID: id, Host: "host-" + id, CreatedAt: f.now,
	}))
	ctx := peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{cert, f.ca.cert}}},
	}})
	return removalWorker{id: id, certID: certID, cert: cert, ctx: ctx}
}

// heartbeat sends the worker's heartbeat on h, listing the runs runIDs.
func (f *removalFixture) heartbeat(t *testing.T, h *Handler, w removalWorker, runIDs ...string) {
	t.Helper()
	stats := &coordinatorv1.WorkerStats{}
	for _, runID := range runIDs {
		stats.RunningTasks = append(stats.RunningTasks, &coordinatorv1.RunningTask{DagName: "dag", DagRunId: runID, AttemptKey: attemptKeyOf(runID)})
	}
	_, err := h.Heartbeat(w.ctx, &coordinatorv1.HeartbeatRequest{WorkerId: w.id, Host: "host-" + w.id, Stats: stats})
	require.NoError(t, err)
}

// lease gives the worker a fresh lease for the run runID.
func (f *removalFixture) lease(t *testing.T, w removalWorker, runID string) {
	t.Helper()
	require.NoError(t, f.leases.Upsert(t.Context(), dispatch.DAGRunLease{
		AttemptKey: attemptKeyOf(runID), DAGRun: ir.DAGRunRef{Name: "dag", ID: runID}, AttemptID: "attempt-1",
		WorkerID: w.id, CertificateID: w.certID, ClaimedAt: f.now.UnixMilli(), LastHeartbeatAt: f.now.UnixMilli(),
	}))
}

func (f *removalFixture) remove(t *testing.T, w removalWorker) *coordinatorv1.RemoveWorkerResponse {
	t.Helper()
	resp, err := f.h.RemoveWorker(t.Context(), &coordinatorv1.RemoveWorkerRequest{WorkerId: w.id})
	require.NoError(t, err)
	return resp
}

func (f *removalFixture) state(t *testing.T, w removalWorker) persis.WorkerCertificateState {
	t.Helper()
	cert, err := f.certs.Get(t.Context(), w.certID)
	require.NoError(t, err)
	return cert.State
}

// poll polls h for the worker and returns the task, or nil when none came
// before a short wait ended.
func (f *removalFixture) poll(t *testing.T, h *Handler, w removalWorker) (*coordinatorv1.Task, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(w.ctx, 300*time.Millisecond)
	defer cancel()
	resp, err := h.Poll(ctx, &coordinatorv1.PollRequest{WorkerId: w.id, PollerId: uuid.NewString()})
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return resp.Task, nil
}

// A removal with finish gives the worker no new task, and revokes its
// certificate once it holds no run, by its heartbeat and by the leases, and
// once 30 seconds passed (Spec 108 §Removal).
func TestRemoveWorkerFinish(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w, "run-1")
	f.lease(t, w, "run-2")
	require.NoError(t, f.dispatches.Enqueue(t.Context(), &dispatch.DispatchTask{
		DAGRunID: "run-3", Target: "dag", AttemptID: "attempt-1", AttemptKey: attemptKeyOf("run-3"),
	}))

	resp := f.remove(t, w)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING, resp.State)
	assert.Equal(t, []string{w.certID}, resp.CertificateIds)
	task, err := f.poll(t, f.h, w)
	require.NoError(t, err)
	assert.Nil(t, task, "a removing worker gets no new task")

	// Its runs end before 30 seconds pass, which keeps it removing.
	f.now = removalStart.Add(10 * time.Second)
	require.NoError(t, f.leases.Delete(t.Context(), attemptKeyOf("run-2")))
	f.heartbeat(t, f.h, w)
	f.h.rereadRemovals(t.Context())
	assert.Equal(t, persis.WorkerCertificateRemoving, f.state(t, w))

	// A run another coordinator handed out before its reread is waited for.
	f.now = removalStart.Add(removingMinimum)
	f.lease(t, w, "run-2")
	f.heartbeat(t, f.h, w)
	f.h.rereadRemovals(t.Context())
	assert.Equal(t, persis.WorkerCertificateRemoving, f.state(t, w))

	// Heartbeats check the removal at most once per interval, so the one
	// right after the run ends waits for the next check.
	require.NoError(t, f.leases.Delete(t.Context(), attemptKeyOf("run-2")))
	f.heartbeat(t, f.h, w)
	assert.Equal(t, persis.WorkerCertificateRemoving, f.state(t, w))
	f.now = f.now.Add(heartbeatSettleInterval)
	f.heartbeat(t, f.h, w)
	assert.Equal(t, persis.WorkerCertificateRevoked, f.state(t, w))
	_, err = f.poll(t, f.h, w)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// The pending task is still there for another worker.
	other := f.enroll(t, "worker-b")
	f.heartbeat(t, f.h, other)
	task, err = f.poll(t, f.h, other)
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, "run-3", task.DagRunId)
}

// An acceptance retried after it created the task's lease, whose claim a
// failed write left behind, is accepted while the certificate is removing,
// so the run it holds is waited for, and refused once it is revoked.
func TestAcceptanceRetryDuringRemoval(t *testing.T) {
	for _, tc := range []struct {
		name     string
		revoke   bool
		wantCode codes.Code
	}{
		{name: "removing", wantCode: codes.OK},
		{name: "revoked", revoke: true, wantCode: codes.Unauthenticated},
	} {
		t.Run(tc.name, func(t *testing.T) { testAcceptanceRetry(t, tc.revoke, tc.wantCode) })
	}
}

func testAcceptanceRetry(t *testing.T, revoke bool, wantCode codes.Code) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	require.NoError(t, f.dispatches.Enqueue(t.Context(), &dispatch.DispatchTask{
		DAGRunID: "run-3", Target: "dag", AttemptID: "attempt-1", AttemptKey: attemptKeyOf("run-3"),
	}))
	task, err := f.poll(t, f.h, w)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.NoError(t, f.leases.Upsert(t.Context(), dispatch.DAGRunLease{
		AttemptKey: task.AttemptKey, DAGRun: ir.DAGRunRef{Name: "dag", ID: "run-3"}, AttemptID: "attempt-1",
		WorkerID: w.id, ClaimToken: task.ClaimToken, ClaimedAt: f.now.UnixMilli(), LastHeartbeatAt: f.now.UnixMilli(),
	}))

	f.remove(t, w)
	if revoke {
		_, err := f.certs.SetState(t.Context(), w.certID, persis.WorkerCertificateRevoked)
		require.NoError(t, err)
	}
	ack, err := f.h.AckTaskClaim(w.ctx, &coordinatorv1.AckTaskClaimRequest{
		WorkerId: w.id, ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey,
	})
	require.Equal(t, wantCode, status.Code(err))
	if err == nil {
		assert.True(t, ack.Accepted)
	}
}

// Removing a connected worker whose certificate record was pruned, before
// its heartbeat writes the record again, asks the caller to try again.
func TestRemoveWorkerPrunedRecord(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	require.NoError(t, f.heartbeats.Upsert(t.Context(), dispatch.WorkerHeartbeatRecord{
		WorkerID: w.id, LastHeartbeatAt: f.now.UnixMilli(), CertificateID: "pruned-cert",
	}))
	_, err := f.h.RemoveWorker(t.Context(), &coordinatorv1.RemoveWorkerRequest{WorkerId: w.id})
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

// failingClaims fails the first claim read of a dispatch store.
type failingClaims struct {
	dispatch.DispatchTaskStore
	err error
}

func (s *failingClaims) GetClaim(ctx context.Context, token string) (*dispatch.ClaimedDispatchTask, error) {
	if err := s.err; err != nil {
		s.err = nil
		return nil, err
	}
	return s.DispatchTaskStore.GetClaim(ctx, token)
}

// failingLeaseRead fails every lease read.
type failingLeaseRead struct {
	dispatch.DAGRunLeaseStore
}

func (failingLeaseRead) Get(context.Context, string) (*dispatch.DAGRunLease, error) {
	return nil, errors.New("unreadable lease")
}

// An acceptance during removal that cannot read the claim, or the lease
// that tells whether it already holds the run, fails without a lease:
// Internal for the claim, and Unavailable, which the worker retries, for
// the lease.
func TestAcceptanceDuringRemovalStoreFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fail     func(f *removalFixture)
		wantCode codes.Code
	}{
		{name: "claim unreadable", wantCode: codes.Internal, fail: func(f *removalFixture) {
			f.h.dispatchTaskStore = &failingClaims{DispatchTaskStore: f.dispatches, err: errors.New("unreadable claim")}
		}},
		{name: "lease unreadable", wantCode: codes.Unavailable, fail: func(f *removalFixture) {
			f.h.dagRunLeaseStore = failingLeaseRead{f.leases}
		}},
		{name: "certificate unreadable", wantCode: codes.Unavailable, fail: func(f *removalFixture) {
			f.h.workerCertificates = &failNthGet{WorkerCertificateStore: f.certs, n: 1}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRemovalFixture(t)
			w := f.enroll(t, "worker-a")
			f.heartbeat(t, f.h, w)
			require.NoError(t, f.dispatches.Enqueue(t.Context(), &dispatch.DispatchTask{
				DAGRunID: "run-3", Target: "dag", AttemptID: "attempt-1", AttemptKey: attemptKeyOf("run-3"),
			}))
			task, err := f.poll(t, f.h, w)
			require.NoError(t, err)
			require.NotNil(t, task)
			f.remove(t, w)

			tc.fail(f)
			_, err = f.h.AckTaskClaim(w.ctx, &coordinatorv1.AckTaskClaimRequest{
				WorkerId: w.id, ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey,
			})
			assert.Equal(t, tc.wantCode, status.Code(err))
			_, err = f.leases.Get(t.Context(), task.AttemptKey)
			assert.ErrorIs(t, err, dispatch.ErrDAGRunLeaseNotFound)
		})
	}
}

// removeOnClaim applies a removal as ClaimNext hands out a task, as one
// that lands while Poll claims does.
type removeOnClaim struct {
	dispatch.DispatchTaskStore
	remove func()
}

func (s removeOnClaim) ClaimNext(ctx context.Context, claim dispatch.DispatchTaskClaim) (*dispatch.ClaimedDispatchTask, error) {
	claimed, err := s.DispatchTaskStore.ClaimNext(ctx, claim)
	if claimed != nil {
		s.remove()
	}
	return claimed, err
}

// A removal that this coordinator applies while Poll claims a task takes
// the task back before the worker sees it, so another worker gets it.
func TestPollRemovalDuringClaim(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	require.NoError(t, f.dispatches.Enqueue(t.Context(), &dispatch.DispatchTask{
		DAGRunID: "run-3", Target: "dag", AttemptID: "attempt-1", AttemptKey: attemptKeyOf("run-3"),
	}))
	f.h.dispatchTaskStore = removeOnClaim{DispatchTaskStore: f.dispatches, remove: func() {
		f.remove(t, w)
	}}
	task, err := f.poll(t, f.h, w)
	require.NoError(t, err)
	assert.Nil(t, task, "a removing worker gets no new task")

	f.h.dispatchTaskStore = f.dispatches
	taker := f.enroll(t, "worker-b")
	f.heartbeat(t, f.h, taker)
	retaken, err := f.poll(t, f.h, taker)
	require.NoError(t, err)
	require.NotNil(t, retaken)
	assert.Equal(t, "run-3", retaken.DagRunId)
}

// failOnceUpsert fails the next presence write, after running during, as
// a heartbeat that overlaps the failing one does.
type failOnceUpsert struct {
	dispatch.WorkerHeartbeatStore
	fail   bool
	during func()
}

func (s *failOnceUpsert) Upsert(ctx context.Context, record dispatch.WorkerHeartbeatRecord) error {
	if s.fail {
		s.fail = false
		if s.during != nil {
			s.during()
		}
		return errors.New("disk full")
	}
	return s.WorkerHeartbeatStore.Upsert(ctx, record)
}

// A heartbeat under a new worker ID whose presence write fails does not
// hold back the next one: the next heartbeat passes the new ID to the
// certificate record.
func TestCertificateHeartbeatAfterFailedWrite(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	presence := &failOnceUpsert{WorkerHeartbeatStore: f.heartbeats}
	f.h.workerHeartbeatStore = presence

	renamed := w
	renamed.id = "worker-b"
	f.now = removalStart.Add(2 * time.Minute)
	presence.fail = true
	_, err := f.h.Heartbeat(renamed.ctx, &coordinatorv1.HeartbeatRequest{WorkerId: renamed.id, Host: "host-" + renamed.id})
	require.Error(t, err)
	f.now = f.now.Add(time.Second)
	f.heartbeat(t, f.h, renamed)
	cert, err := f.certs.Get(t.Context(), w.certID)
	require.NoError(t, err)
	assert.Equal(t, renamed.id, cert.WorkerID)
}

// A heartbeat that overlaps a failing one shares its pass to the record and
// skips it. The failure still lets the next heartbeat make the pass.
func TestCertificateHeartbeatOverlappingFailedWrite(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	renamed := w
	renamed.id = "worker-b"
	presence := &failOnceUpsert{WorkerHeartbeatStore: f.heartbeats, fail: true, during: func() {
		f.now = f.now.Add(time.Second)
		f.heartbeat(t, f.h, renamed)
	}}
	f.h.workerHeartbeatStore = presence

	f.now = removalStart.Add(2 * time.Minute)
	_, err := f.h.Heartbeat(renamed.ctx, &coordinatorv1.HeartbeatRequest{WorkerId: renamed.id, Host: "host-" + renamed.id})
	require.Error(t, err)
	f.now = f.now.Add(time.Second)
	f.heartbeat(t, f.h, renamed)
	cert, err := f.certs.Get(t.Context(), w.certID)
	require.NoError(t, err)
	assert.Equal(t, renamed.id, cert.WorkerID)
}

// Removing the worker ID a certificate held before a restart, while the
// worker connects under a new ID, starts a removal for the new ID instead
// of revoking a certificate in use (Spec 108 §Removal).
func TestRemoveStaleIDOfConnectedCertificate(t *testing.T) {
	f := newRemovalFixture(t)
	first := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, first)
	restarted := first
	restarted.id = "worker-b"
	f.now = removalStart.Add(time.Minute)
	f.heartbeat(t, f.h, restarted, "run-1")

	resp := f.remove(t, first)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING, resp.State)
	assert.Equal(t, persis.WorkerCertificateRemoving, f.state(t, first))
	removals, err := f.certs.ListRemovals(t.Context())
	require.NoError(t, err)
	require.Len(t, removals, 1)
	assert.Equal(t, restarted.id, removals[0].WorkerID)
}

// failNthGet fails the n-th certificate read.
type failNthGet struct {
	persis.WorkerCertificateStore
	n, reads int
}

func (s *failNthGet) Get(ctx context.Context, id string) (persis.WorkerCertificate, error) {
	s.reads++
	if s.reads == s.n {
		return persis.WorkerCertificate{}, errors.New("unreadable certificate")
	}
	return s.WorkerCertificateStore.Get(ctx, id)
}

// failingConfirm fails every confirmation of an acceptance, the read an
// acceptance makes after it wrote the lease.
type failingConfirm struct {
	persis.WorkerCertificateStore
}

func (failingConfirm) ConfirmAcceptance(context.Context, string) (persis.WorkerCertificate, error) {
	return persis.WorkerCertificate{}, errors.New("unreadable certificate")
}

// An acceptance whose second certificate read fails, after it created the
// lease, fails as unavailable and keeps the claim and the lease, which a
// concurrent retry may own, so the worker's retry reads the certificate
// again.
func TestAcceptanceSecondReadFailure(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	require.NoError(t, f.dispatches.Enqueue(t.Context(), &dispatch.DispatchTask{
		DAGRunID: "run-3", Target: "dag", AttemptID: "attempt-1", AttemptKey: attemptKeyOf("run-3"),
	}))
	task, err := f.poll(t, f.h, w)
	require.NoError(t, err)
	require.NotNil(t, task)
	f.h.workerCertificates = failingConfirm{f.certs}

	req := &coordinatorv1.AckTaskClaimRequest{WorkerId: w.id, ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey}
	_, err = f.h.AckTaskClaim(w.ctx, req)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	_, err = f.dispatches.GetClaim(t.Context(), task.ClaimToken)
	assert.NoError(t, err, "the claim stays for the retry")
	_, err = f.leases.Get(t.Context(), task.AttemptKey)
	assert.NoError(t, err, "the lease stays")
	f.h.workerCertificates = f.certs
	ack, err := f.h.AckTaskClaim(w.ctx, req)
	require.NoError(t, err)
	assert.True(t, ack.Accepted)
}

// A run bound to the worker node of a worker in removal fails at once, as
// one bound to an offline node does, and says that the worker is being
// removed, since it is still connected.
func TestDispatchToRemovingWorkerNode(t *testing.T) {
	f := newHoldFixture(t, time.Hour)
	require.NoError(t, f.heartbeats.Upsert(t.Context(), dispatch.WorkerHeartbeatRecord{
		WorkerID: "tokyo-gw", LastHeartbeatAt: time.Now().UnixMilli(), CertificateID: "cert-1",
	}))
	f.h.removals.load([]persis.WorkerCertificateRemoval{{ID: "cert-1", State: persis.WorkerCertificateRemoving, WorkerID: "tokyo-gw"}})
	task := cpuTask("run-bound")
	task.WorkerSelector = nil
	task.TargetWorkerId, task.BoundNode = "tokyo-gw", "tokyo-gw"

	_, err := f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: task})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Equal(t, `worker node "tokyo-gw" is being removed`, status.Convert(err).Message())
	assert.Nil(t, f.runs.attempts["run-bound"], "nothing is created when refused")
}

// Another coordinator stored the removal of a bound node's worker, and
// this coordinator has not reread the removals yet. The dispatch reads the
// stored state and is refused (Spec 108 §Removal).
func TestDispatchToNodeRemovedElsewhere(t *testing.T) {
	f := newHoldFixture(t, time.Hour)
	certs, err := store.NewWorkerCertificateStore(testutil.NewMemoryBackend().Collection(persis.CollectionWorkerCertificates))
	require.NoError(t, err)
	require.NoError(t, certs.Create(t.Context(), persis.WorkerCertificate{
		ID: "cert-1", KeyFingerprint: make([]byte, 32), State: persis.WorkerCertificateActive, WorkerID: "tokyo-gw", CreatedAt: time.Now(),
	}))
	_, err = certs.MarkRemoving(t.Context(), "cert-1", "tokyo-gw", time.Now)
	require.NoError(t, err)
	f.h.workerCertificates = certs
	require.NoError(t, f.heartbeats.Upsert(t.Context(), dispatch.WorkerHeartbeatRecord{
		WorkerID: "tokyo-gw", LastHeartbeatAt: time.Now().UnixMilli(), CertificateID: "cert-1",
	}))
	task := cpuTask("run-bound")
	task.WorkerSelector = nil
	task.TargetWorkerId, task.BoundNode = "tokyo-gw", "tokyo-gw"

	_, err = f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: task})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Equal(t, `worker node "tokyo-gw" is being removed`, status.Convert(err).Message())
	assert.Nil(t, f.runs.attempts["run-bound"], "nothing is created when refused")
}

// A worker in removal does not count as a worker a dispatch can go to.
func TestRemovingWorkerTakesNoDispatch(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	healthy, err := f.h.listHealthyWorkers(t.Context())
	require.NoError(t, err)
	require.Len(t, healthy, 1)

	f.remove(t, w)
	healthy, err = f.h.listHealthyWorkers(t.Context())
	require.NoError(t, err)
	assert.Empty(t, healthy)
}

// deleteClaimHook runs hook once, before the first claim delete, as an
// acceptance that pauses between its checks and finishing its claim does.
type deleteClaimHook struct {
	dispatch.DispatchTaskStore
	hook func()
	done bool
}

func (s *deleteClaimHook) DeleteClaim(ctx context.Context, token string) error {
	if !s.done {
		s.done = true
		s.hook()
	}
	return s.DispatchTaskStore.DeleteClaim(ctx, token)
}

// An acceptance passes its checks and pauses before it finishes the claim.
// The certificate is revoked, and a retry of the same claim is refused.
// The retry leaves the lease, which the first acceptance owns and returns
// as accepted (Spec 108 §Removal).
func TestOverlappingAcceptanceKeepsLease(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	require.NoError(t, f.dispatches.Enqueue(t.Context(), &dispatch.DispatchTask{
		DAGRunID: "run-3", Target: "dag", AttemptID: "attempt-1", AttemptKey: attemptKeyOf("run-3"),
	}))
	task, err := f.poll(t, f.h, w)
	require.NoError(t, err)
	require.NotNil(t, task)
	req := &coordinatorv1.AckTaskClaimRequest{WorkerId: w.id, ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey}
	f.h.dispatchTaskStore = &deleteClaimHook{DispatchTaskStore: f.dispatches, hook: func() {
		_, err := f.certs.SetState(t.Context(), w.certID, persis.WorkerCertificateRevoked)
		require.NoError(t, err)
		_, err = f.h.AckTaskClaim(w.ctx, req)
		require.Equal(t, codes.Unauthenticated, status.Code(err))
	}}

	ack, err := f.h.AckTaskClaim(w.ctx, req)
	require.NoError(t, err)
	assert.True(t, ack.Accepted)
	_, err = f.leases.Get(t.Context(), task.AttemptKey)
	assert.NoError(t, err, "the accepted run keeps its lease")
}

// The lease that an acceptance with a worker certificate creates names
// the certificate, so a stop removal finds the run by it.
func TestAcceptanceLeaseNamesCertificate(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	require.NoError(t, f.dispatches.Enqueue(t.Context(), &dispatch.DispatchTask{
		DAGRunID: "run-3", Target: "dag", AttemptID: "attempt-1", AttemptKey: attemptKeyOf("run-3"),
	}))
	task, err := f.poll(t, f.h, w)
	require.NoError(t, err)
	require.NotNil(t, task)
	ack, err := f.h.AckTaskClaim(w.ctx, &coordinatorv1.AckTaskClaimRequest{
		WorkerId: w.id, ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey,
	})
	require.NoError(t, err)
	require.True(t, ack.Accepted)
	lease, err := f.leases.Get(t.Context(), task.AttemptKey)
	require.NoError(t, err)
	assert.Equal(t, w.certID, lease.CertificateID)
}

// getHook runs hook once, after the first Get, as a removal that lands
// between the acceptance check and the lease does.
type getHook struct {
	persis.WorkerCertificateStore
	hook func()
	done bool
}

func (s *getHook) Get(ctx context.Context, id string) (persis.WorkerCertificate, error) {
	cert, err := s.WorkerCertificateStore.Get(ctx, id)
	if !s.done {
		s.done = true
		s.hook()
	}
	return cert, err
}

// A revocation lands after the acceptance read the certificate as active,
// as when the acceptance stalls past the removal's minimum wait. The call
// is refused and the lease stays, for the stale-lease check to end, since
// a retry of the same claim may own it. The claim goes, so its expiry
// never queues the attempt again (Spec 108 §Removal).
func TestRevocationDuringAcceptance(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	require.NoError(t, f.dispatches.Enqueue(t.Context(), &dispatch.DispatchTask{
		DAGRunID: "run-3", Target: "dag", AttemptID: "attempt-1", AttemptKey: attemptKeyOf("run-3"),
	}))
	task, err := f.poll(t, f.h, w)
	require.NoError(t, err)
	require.NotNil(t, task)
	f.h.workerCertificates = &getHook{WorkerCertificateStore: f.certs, hook: func() {
		_, err := f.certs.SetState(t.Context(), w.certID, persis.WorkerCertificateRevoked)
		require.NoError(t, err)
	}}

	_, err = f.h.AckTaskClaim(w.ctx, &coordinatorv1.AckTaskClaimRequest{
		WorkerId: w.id, ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey,
	})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = f.leases.Get(t.Context(), task.AttemptKey)
	assert.NoError(t, err, "the lease stays for the stale-lease check")
	_, err = f.dispatches.GetClaim(t.Context(), task.ClaimToken)
	assert.ErrorIs(t, err, dispatch.ErrDispatchTaskNotFound, "the claim goes")
}

// A coordinator that starts while a worker is removing, as after a restart,
// or that shares the stores with the one that took the removal, gives the
// worker no task after its reread, and revokes the certificate once the
// worker's heartbeat goes stale (Spec 108 §Removal).
func TestRemovalOnAnotherCoordinator(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w, "run-1")
	other := f.coordinator(t)
	require.NoError(t, f.dispatches.Enqueue(t.Context(), &dispatch.DispatchTask{
		DAGRunID: "run-2", Target: "dag", AttemptID: "attempt-1", AttemptKey: attemptKeyOf("run-2"),
	}))

	f.remove(t, w)
	other.rereadRemovals(t.Context())
	task, err := f.poll(t, other, w)
	require.NoError(t, err)
	assert.Nil(t, task)

	restarted := f.coordinator(t)
	f.now = removalStart.Add(time.Hour)
	restarted.rereadRemovals(t.Context())
	assert.Equal(t, persis.WorkerCertificateRevoked, f.state(t, w))
	assert.Equal(t, codes.Unauthenticated, status.Code(restarted.removals.refusal(w.certID)))
	assert.NoError(t, other.removals.refusal(w.certID), "another coordinator applies it at its next reread")
	other.rereadRemovals(t.Context())
	assert.Equal(t, codes.Unauthenticated, status.Code(other.removals.refusal(w.certID)))
}

// A worker that restarted with a new worker ID on the same certificate,
// before the record took the new ID, holds a run when it is removed with
// finish. Its certificate stays removing while it holds the run, also when
// its worker ID changes again during the removal.
func TestRemoveRestartedWorker(t *testing.T) {
	f := newRemovalFixture(t)
	first := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, first)
	restarted := first
	restarted.id = "worker-b"
	f.now = removalStart.Add(30 * time.Second)
	f.heartbeat(t, f.h, restarted, "run-1")
	f.lease(t, restarted, "run-1")

	f.remove(t, restarted)
	f.now = f.now.Add(removingMinimum)
	f.lease(t, restarted, "run-1")
	f.heartbeat(t, f.h, restarted)
	f.h.rereadRemovals(t.Context())
	assert.Equal(t, persis.WorkerCertificateRemoving, f.state(t, restarted), "worker-b still holds run-1")

	again := first
	again.id = "worker-c"
	f.now = f.now.Add(time.Second)
	f.heartbeat(t, f.h, again, "run-2")
	f.h.rereadRemovals(t.Context())
	assert.Equal(t, persis.WorkerCertificateRemoving, f.state(t, again), "worker-c holds run-2 on the certificate")

	require.NoError(t, f.leases.Delete(t.Context(), attemptKeyOf("run-1")))
	f.now = f.now.Add(heartbeatSettleInterval)
	f.heartbeat(t, f.h, again)
	assert.Equal(t, persis.WorkerCertificateRevoked, f.state(t, again))
}

// failingRemovals fails ListRemovals, as a store with an unreadable removal
// record does.
type failingRemovals struct {
	persis.WorkerCertificateStore
}

func (failingRemovals) ListRemovals(context.Context) ([]persis.WorkerCertificateRemoval, error) {
	return nil, errors.New("unreadable removal record")
}

// A coordinator whose read of the removed workers failed keeps refusing
// every worker certificate after it removes a worker itself, since the
// removals it lacks may hold revoked certificates.
func TestRemovalsUnreadAfterLocalRemoval(t *testing.T) {
	f := newRemovalFixture(t)
	revoked := f.enroll(t, "worker-a")
	_, err := f.certs.SetState(t.Context(), revoked.certID, persis.WorkerCertificateRevoked)
	require.NoError(t, err)
	h := NewHandler(HandlerConfig{
		DispatchTaskStore:      f.dispatches,
		DAGRunLeaseStore:       f.leases,
		WorkerHeartbeatStore:   f.heartbeats,
		PeerCA:                 f.ca,
		WorkerInviteStore:      f.invites,
		WorkerCertificateStore: failingRemovals{f.certs},
	})
	h.clock = func() time.Time { return f.now }
	h.rereadRemovals(t.Context())
	assert.Equal(t, codes.Unavailable, status.Code(h.removals.refusal(revoked.certID)))

	other := f.enroll(t, "worker-b")
	f.heartbeat(t, h, other)
	_, err = h.RemoveWorker(t.Context(), &coordinatorv1.RemoveWorkerRequest{WorkerId: other.id})
	require.NoError(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(h.removals.refusal(revoked.certID)))
}

// A coordinator that has not reread the removals hands a task to a
// removing worker. The worker's acceptance is refused from the
// certificate's stored state, whether it is removing or already revoked,
// no lease is created, and the claim stays until it expires, so no
// acceptance still in flight can race a release (Spec 108 §Removal).
func TestAcceptanceDuringRemoval(t *testing.T) {
	for _, tc := range []struct {
		name   string
		revoke bool
		// anonymous sends the acceptance without a worker ID, which the
		// claim's worker stands in for.
		anonymous bool
		wantCode  codes.Code
	}{
		{name: "removing", wantCode: codes.OK},
		{name: "removing, no worker ID", anonymous: true, wantCode: codes.OK},
		{name: "revoked", revoke: true, wantCode: codes.Unauthenticated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRemovalFixture(t)
			w := f.enroll(t, "worker-a")
			f.heartbeat(t, f.h, w)
			other := f.coordinator(t)
			require.NoError(t, f.dispatches.Enqueue(t.Context(), &dispatch.DispatchTask{
				DAGRunID: "run-3", Target: "dag", AttemptID: "attempt-1", AttemptKey: attemptKeyOf("run-3"),
			}))

			f.remove(t, w)
			f.now = removalStart.Add(removingMinimum - time.Second)
			task, err := f.poll(t, other, w)
			require.NoError(t, err)
			require.NotNil(t, task, "the other coordinator has not reread yet")
			if tc.revoke {
				f.now = removalStart.Add(removingMinimum)
				f.heartbeat(t, f.h, w)
				require.Equal(t, persis.WorkerCertificateRevoked, f.state(t, w))
			}

			req := &coordinatorv1.AckTaskClaimRequest{WorkerId: w.id, ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey}
			if tc.anonymous {
				req.WorkerId = ""
			}
			ack, err := other.AckTaskClaim(w.ctx, req)
			require.Equal(t, tc.wantCode, status.Code(err))
			if err == nil {
				assert.False(t, ack.Accepted)
			}
			_, err = f.leases.Get(t.Context(), task.AttemptKey)
			assert.ErrorIs(t, err, dispatch.ErrDAGRunLeaseNotFound, "the worker holds no lease")
			_, err = f.dispatches.GetClaim(t.Context(), task.ClaimToken)
			assert.NoError(t, err, "the claim stays until it expires")
		})
	}
}

// revokeOnCreate revokes the certificate revoke as Create records a new
// one, as a removal that lands while a certificate is issued does.
type revokeOnCreate struct {
	persis.WorkerCertificateStore
	revoke string
}

func (s revokeOnCreate) Create(ctx context.Context, cert persis.WorkerCertificate) error {
	if _, err := s.SetState(ctx, s.revoke, persis.WorkerCertificateRevoked); err != nil {
		return err
	}
	return s.WorkerCertificateStore.Create(ctx, cert)
}

// A key whose certificate is revoked while Enroll issues a new one for it
// is refused, and the new certificate is revoked (Spec 108 §Coordinator
// Side).
func TestEnrollDuringRemoval(t *testing.T) {
	f := newRemovalFixture(t)
	invite, err := f.h.CreateInvite(t.Context(), &coordinatorv1.CreateInviteRequest{Reusable: true})
	require.NoError(t, err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	require.NoError(t, err)
	req := &coordinatorv1.EnrollRequest{Invite: invite.Invite, Csr: csr, WorkerId: "worker-a", KeepsCertificate: true}
	tlsCtx := peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{}})
	first, err := f.h.Enroll(tlsCtx, req)
	require.NoError(t, err)
	firstCert, err := x509.ParseCertificate(first.Certificate)
	require.NoError(t, err)

	f.h.workerCertificates = revokeOnCreate{WorkerCertificateStore: f.certs, revoke: firstCert.Subject.CommonName}
	_, err = f.h.Enroll(tlsCtx, req)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	certs, err := f.certs.List(t.Context())
	require.NoError(t, err)
	require.Len(t, certs, 2)
	for _, cert := range certs {
		assert.Equal(t, persis.WorkerCertificateRevoked, cert.State, cert.ID)
	}
}

// Enroll issues the certificate and records it at the handler's time, so a
// record that a heartbeat writes again after a prune keeps that time.
func TestEnrollUsesHandlerClock(t *testing.T) {
	f := newRemovalFixture(t)
	invite, err := f.h.CreateInvite(t.Context(), &coordinatorv1.CreateInviteRequest{Reusable: true})
	require.NoError(t, err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	require.NoError(t, err)
	tlsCtx := peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{}})
	resp, err := f.h.Enroll(tlsCtx, &coordinatorv1.EnrollRequest{Invite: invite.Invite, Csr: csr, WorkerId: "worker-a", KeepsCertificate: true})
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(resp.Certificate)
	require.NoError(t, err)
	assert.True(t, cert.NotBefore.Add(certBackdate).Equal(removalStart), "issued at %v", cert.NotBefore)
	record, err := f.certs.Get(t.Context(), cert.Subject.CommonName)
	require.NoError(t, err)
	assert.True(t, record.CreatedAt.Equal(removalStart))
}

// secondRemovalsFail fails the second read of the removals.
type secondRemovalsFail struct {
	persis.WorkerCertificateStore
	reads int
}

func (s *secondRemovalsFail) ListRemovals(ctx context.Context) ([]persis.WorkerCertificateRemoval, error) {
	s.reads++
	if s.reads == 2 {
		return nil, errors.New("unreadable removal record")
	}
	return s.WorkerCertificateStore.ListRemovals(ctx)
}

// Enroll refuses when it cannot check the key again after recording the
// certificate, and does not mark the key removed, so a retry with the same
// key enrolls.
func TestEnrollRecheckFailure(t *testing.T) {
	f := newRemovalFixture(t)
	invite, err := f.h.CreateInvite(t.Context(), &coordinatorv1.CreateInviteRequest{Reusable: true})
	require.NoError(t, err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	require.NoError(t, err)
	req := &coordinatorv1.EnrollRequest{Invite: invite.Invite, Csr: csr, WorkerId: "worker-a", KeepsCertificate: true}
	tlsCtx := peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{}})

	f.h.workerCertificates = &secondRemovalsFail{WorkerCertificateStore: f.certs}
	_, err = f.h.Enroll(tlsCtx, req)
	assert.Equal(t, codes.Internal, status.Code(err))
	_, err = f.h.Enroll(tlsCtx, req)
	assert.NoError(t, err)
}

// A heartbeat writes its certificate's last heartbeat at most once a day,
// and writes a pruned record again from the certificate it carries (Spec
// 108 §Certificate Records).
func TestCertificateHeartbeat(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	lastHeartbeat := func() time.Time {
		cert, err := f.certs.Get(t.Context(), w.certID)
		require.NoError(t, err)
		return cert.LastHeartbeatAt
	}

	f.heartbeat(t, f.h, w)
	assert.True(t, lastHeartbeat().Equal(removalStart))
	f.now = removalStart.Add(2 * time.Hour)
	f.heartbeat(t, f.h, w)
	assert.True(t, lastHeartbeat().Equal(removalStart), "written at most once a day")
	f.now = removalStart.Add(25 * time.Hour)
	f.heartbeat(t, f.h, w)
	assert.True(t, lastHeartbeat().Equal(f.now))

	// A new worker ID within a minute of the last write reaches the record
	// a minute later, not an hour later.
	written := f.now
	renamed := w
	renamed.id = "worker-b"
	f.now = written.Add(30 * time.Second)
	f.heartbeat(t, f.h, renamed)
	f.now = written.Add(2 * time.Minute)
	f.heartbeat(t, f.h, renamed)
	cert, err := f.certs.Get(t.Context(), w.certID)
	require.NoError(t, err)
	assert.Equal(t, renamed.id, cert.WorkerID)
	w = renamed

	f.now = f.now.Add(91 * 24 * time.Hour)
	f.h.pruneRecords(t.Context())
	_, err = f.certs.Get(t.Context(), w.certID)
	require.ErrorIs(t, err, persis.ErrWorkerCertificateNotFound)
	f.heartbeat(t, f.h, w)
	got, err := f.certs.Get(t.Context(), w.certID)
	require.NoError(t, err)
	fingerprint := sha256.Sum256(w.cert.RawSubjectPublicKeyInfo)
	assert.Equal(t, persis.WorkerCertificate{
		ID: w.certID, InviteID: "invite-1", KeyFingerprint: fingerprint[:], State: persis.WorkerCertificateActive,
		WorkerID: w.id, WorkerIDs: []persis.WorkerIDSeen{{ID: w.id, SeenAt: f.now}}, Host: "host-" + w.id, CreatedAt: got.CreatedAt, LastHeartbeatAt: f.now,
	}, got)
	assert.True(t, got.CreatedAt.Equal(removalStart), "the enrollment time, from the certificate: %v", got.CreatedAt)
}

// The daily prune deletes old certificate records and ended invites, and
// keeps a reusable invite without an expiry (Spec 108 §Invite Records).
func TestPruneRecords(t *testing.T) {
	f := newRemovalFixture(t)
	ctx := t.Context()
	w := f.enroll(t, "worker-a")
	ended, err := f.invites.Create(ctx, persis.WorkerInvite{
		SecretHash: make([]byte, sha256.Size), Kind: persis.WorkerInviteSingleUse, CreatedAt: removalStart, ExpiresAt: removalStart.Add(time.Hour),
	})
	require.NoError(t, err)
	forever, err := f.invites.Create(ctx, persis.WorkerInvite{
		SecretHash: make([]byte, sha256.Size), Kind: persis.WorkerInviteReusable, CreatedAt: removalStart,
	})
	require.NoError(t, err)

	f.now = removalStart.Add(8 * 24 * time.Hour)
	f.h.pruneRecords(ctx)
	_, err = f.invites.Get(ctx, ended.ID)
	assert.ErrorIs(t, err, persis.ErrWorkerInviteNotFound)
	_, err = f.invites.Get(ctx, forever.ID)
	assert.NoError(t, err)
	assert.Equal(t, persis.WorkerCertificateActive, f.state(t, w))

	f.now = removalStart.Add(91 * 24 * time.Hour)
	f.h.pruneRecords(ctx)
	_, err = f.certs.Get(ctx, w.certID)
	assert.ErrorIs(t, err, persis.ErrWorkerCertificateNotFound)
	_, err = f.invites.Get(ctx, forever.ID)
	assert.NoError(t, err)
}

// enrollCall returns an enrollment of the worker id with a new key through a
// reusable invite, and the context of a call over TLS.
func (f *removalFixture) enrollCall(t *testing.T, id string) (*coordinatorv1.EnrollRequest, context.Context) {
	t.Helper()
	invite, err := f.h.CreateInvite(t.Context(), &coordinatorv1.CreateInviteRequest{Reusable: true})
	require.NoError(t, err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	require.NoError(t, err)
	req := &coordinatorv1.EnrollRequest{Invite: invite.Invite, Csr: csr, WorkerId: id, KeepsCertificate: true}
	return req, peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{}})
}

// beforeRevoke runs hook once, before the first revocation.
type beforeRevoke struct {
	persis.WorkerCertificateStore
	hook func()
}

func (s *beforeRevoke) SetState(ctx context.Context, id string, state persis.WorkerCertificateState) (persis.WorkerCertificate, error) {
	if hook := s.hook; hook != nil {
		s.hook = nil
		hook()
	}
	return s.WorkerCertificateStore.SetState(ctx, id, state)
}

// An enrollment of the removed worker's key finishes, its key checks
// included, after the removal listed the certificates and before it
// revoked them. The removal revokes that certificate too, so no
// certificate of the key stays active (Spec 108 §Revocation).
func TestRemovalDuringEnrollment(t *testing.T) {
	f := newRemovalFixture(t)
	req, tlsCtx := f.enrollCall(t, "worker-a")
	_, err := f.h.Enroll(tlsCtx, req)
	require.NoError(t, err)
	f.h.workerCertificates = &beforeRevoke{WorkerCertificateStore: f.certs, hook: func() {
		_, err := f.h.Enroll(tlsCtx, req)
		require.NoError(t, err)
	}}

	resp, err := f.h.RemoveWorker(t.Context(), &coordinatorv1.RemoveWorkerRequest{WorkerId: "worker-a"})
	require.NoError(t, err)
	assert.Len(t, resp.CertificateIds, 2)
	certs, err := f.certs.List(t.Context())
	require.NoError(t, err)
	require.Len(t, certs, 2)
	for _, cert := range certs {
		assert.Equal(t, persis.WorkerCertificateRevoked, cert.State, cert.ID)
	}
}

// A removal of A counts itself on A's fence, and another coordinator then
// enrolls A with a new key before the removal's last listing. The
// enrollment started after the removal began, so both succeed and the new
// certificate stays active (Spec 108 §Removal).
func TestEnrollmentAfterRemovalBegan(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	other := f.coordinator(t)
	var enrolled *coordinatorv1.EnrollResponse
	f.h.workerCertificates = &beforeRevoke{WorkerCertificateStore: f.certs, hook: func() {
		req, tlsCtx := f.enrollCall(t, w.id)
		var err error
		enrolled, err = other.Enroll(tlsCtx, req)
		require.NoError(t, err)
	}}

	resp := f.remove(t, w)
	assert.Equal(t, []string{w.certID}, resp.CertificateIds)
	require.NotNil(t, enrolled)
	issued, err := x509.ParseCertificate(enrolled.Certificate)
	require.NoError(t, err)
	assert.Equal(t, persis.WorkerCertificateActive, f.state(t, removalWorker{certID: issued.Subject.CommonName}))
}

// A worker's certificate is removing while its runs end. An enrollment with
// the same key is refused, so no certificate of the key outlasts the
// removal, and an enrollment with a new key succeeds (Spec 108 §Removal).
func TestEnrollRefusesKeyOfRemovingCertificate(t *testing.T) {
	f := newRemovalFixture(t)
	req, tlsCtx := f.enrollCall(t, "worker-a")
	resp, err := f.h.Enroll(tlsCtx, req)
	require.NoError(t, err)
	issued, err := x509.ParseCertificate(resp.Certificate)
	require.NoError(t, err)
	_, err = f.certs.MarkRemoving(t.Context(), issued.Subject.CommonName, "worker-a", time.Now)
	require.NoError(t, err)

	_, err = f.h.Enroll(tlsCtx, req)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Equal(t, removedKeyMessage, status.Convert(err).Message())
	fresh, freshCtx := f.enrollCall(t, "worker-a")
	_, err = f.h.Enroll(freshCtx, fresh)
	assert.NoError(t, err)
}

// A coordinator that predates the certificate ID in heartbeats records a
// certificate connection without it. Such a fresh heartbeat counts as
// connected, under the removed worker's ID or under another ID on the
// certificate's host, so the removal is refused (Spec 108 §Removal).
func TestRemoveWorkerLegacyHeartbeat(t *testing.T) {
	for _, tc := range []struct {
		name     string
		workerID string
	}{
		{name: "same worker ID", workerID: "worker-a"},
		{name: "restarted under another ID", workerID: "worker-b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRemovalFixture(t)
			w := f.enroll(t, "worker-a")
			require.NoError(t, f.heartbeats.Upsert(t.Context(), dispatch.WorkerHeartbeatRecord{
				WorkerID: tc.workerID, Host: "host-worker-a", LastHeartbeatAt: f.now.UnixMilli(),
				Connection: dispatch.WorkerConnectionCertificate,
			}))

			_, err := f.h.RemoveWorker(t.Context(), &coordinatorv1.RemoveWorkerRequest{WorkerId: w.id})
			assert.Equal(t, codes.FailedPrecondition, status.Code(err))
			assert.Equal(t, legacyHeartbeatMessage, status.Convert(err).Message())
			assert.Equal(t, persis.WorkerCertificateActive, f.state(t, w))
		})
	}
}

// A worker restarts under a new ID within a minute of its certificate's
// last record write and stops before its next heartbeat. Its presence
// record goes, and a removal by the new ID still finds the certificate
// (Spec 108 §Removal).
func TestRemoveRenamedWorkerAfterRestart(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	renamed := w
	renamed.id = "worker-b"
	f.now = removalStart.Add(30 * time.Second)
	f.heartbeat(t, f.h, renamed)
	f.now = removalStart.Add(time.Hour)
	_, err := f.heartbeats.DeleteStale(t.Context(), f.now)
	require.NoError(t, err)

	resp := f.remove(t, renamed)
	assert.Equal(t, []string{w.certID}, resp.CertificateIds)
	assert.Equal(t, persis.WorkerCertificateRevoked, f.state(t, w))
}

// Enroll records the time it issued the certificate at, at the
// certificate's precision, so a record written again from the certificate
// keeps its creation time.
func TestEnrollRecordsIssuanceTime(t *testing.T) {
	f := newRemovalFixture(t)
	req, tlsCtx := f.enrollCall(t, "worker-a")
	tick := removalStart.Add(400 * time.Millisecond)
	f.h.clock = func() time.Time {
		tick = tick.Add(700 * time.Millisecond)
		return tick
	}

	resp, err := f.h.Enroll(tlsCtx, req)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(resp.Certificate)
	require.NoError(t, err)
	record, err := f.certs.Get(t.Context(), cert.Subject.CommonName)
	require.NoError(t, err)
	assert.True(t, record.CreatedAt.Equal(cert.NotBefore.Add(certBackdate)), "recorded %v, issued %v", record.CreatedAt, cert.NotBefore)
}

// blockedPrune holds every prune until release closes or the call ends.
type blockedPrune struct {
	persis.WorkerCertificateStore
	release chan struct{}
}

func (s blockedPrune) Prune(ctx context.Context, _ time.Time) (int, error) {
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	return 0, nil
}

// The maintenance loop rereads the removals on its own schedule while the
// startup prune is still running, so a revocation made by another
// coordinator applies here (Spec 108 §Revocation).
func TestScheduledRereadDuringPrune(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	release := make(chan struct{})
	f.h.workerCertificates = blockedPrune{WorkerCertificateStore: f.certs, release: release}
	f.h.removalReread = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	f.h.startPeerMaintenance(ctx)
	t.Cleanup(func() {
		cancel()
		close(release)
		f.h.waitPeerMaintenance()
	})
	require.NoError(t, f.h.removals.refusal(w.certID))

	_, err := f.certs.SetState(t.Context(), w.certID, persis.WorkerCertificateRevoked)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return status.Code(f.h.removals.refusal(w.certID)) == codes.Unauthenticated
	}, 5*time.Second, 10*time.Millisecond)
}

// beforeCreate runs hook once, before the first certificate record is
// created.
type beforeCreate struct {
	persis.WorkerCertificateStore
	hook func()
}

func (s *beforeCreate) Create(ctx context.Context, cert persis.WorkerCertificate) error {
	if hook := s.hook; hook != nil {
		s.hook = nil
		hook()
	}
	return s.WorkerCertificateStore.Create(ctx, cert)
}

// An enrollment of the worker ID with a new key on another coordinator
// reads the removals, and the whole removal runs before it records its
// certificate. The enrollment sees the removal afterwards, revokes its
// certificate, and fails, so the removed worker keeps no active
// certificate. A later enrollment succeeds (Spec 108 §Removal).
func TestEnrollmentAcrossRemoval(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	other := f.coordinator(t)
	other.workerCertificates = &beforeCreate{WorkerCertificateStore: f.certs, hook: func() {
		f.remove(t, w)
	}}
	req, tlsCtx := f.enrollCall(t, "worker-a")

	_, err := other.Enroll(tlsCtx, req)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Equal(t, removedDuringEnrollMessage, status.Convert(err).Message())
	certs, err := f.certs.List(t.Context())
	require.NoError(t, err)
	require.Len(t, certs, 2)
	for _, cert := range certs {
		assert.Equal(t, persis.WorkerCertificateRevoked, cert.State, cert.ID)
	}
	again, tlsCtx := f.enrollCall(t, "worker-a")
	_, err = other.Enroll(tlsCtx, again)
	assert.NoError(t, err)
}

// Two certificates share a key under different worker IDs. Removing the
// worker that is not connected leaves the certificate of the connected one
// active (Spec 108 §Removal).
func TestRemovalSparesSharedKey(t *testing.T) {
	f := newRemovalFixture(t)
	offline := f.enroll(t, "worker-a")
	stored, err := f.certs.Get(t.Context(), offline.certID)
	require.NoError(t, err)
	require.NoError(t, f.certs.Create(t.Context(), persis.WorkerCertificate{
		ID: "cert-shared", InviteID: "invite-1", KeyFingerprint: stored.KeyFingerprint, State: persis.WorkerCertificateActive,
		WorkerID: "worker-b", Host: "host-worker-b", CreatedAt: f.now,
	}))
	require.NoError(t, f.heartbeats.Upsert(t.Context(), dispatch.WorkerHeartbeatRecord{
		WorkerID: "worker-b", Host: "host-worker-b", LastHeartbeatAt: f.now.UnixMilli(),
		Connection: dispatch.WorkerConnectionCertificate, CertificateID: "cert-shared",
	}))

	resp := f.remove(t, offline)
	assert.Equal(t, []string{offline.certID}, resp.CertificateIds)
	shared, err := f.certs.Get(t.Context(), "cert-shared")
	require.NoError(t, err)
	assert.Equal(t, persis.WorkerCertificateActive, shared.State)
}

// A worker's ID goes from A to B and back to A within seconds, and the
// worker then stops. The record names A, so a removal by A finds the
// certificate after the heartbeats are cleaned up (Spec 108 §Removal).
func TestRemoveWorkerBackToEarlierID(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	renamed := w
	renamed.id = "worker-b"
	f.now = removalStart.Add(time.Second)
	f.heartbeat(t, f.h, renamed)
	f.now = removalStart.Add(2 * time.Second)
	f.heartbeat(t, f.h, w)
	f.now = removalStart.Add(time.Hour)
	_, err := f.heartbeats.DeleteStale(t.Context(), f.now)
	require.NoError(t, err)

	resp := f.remove(t, w)
	assert.Equal(t, []string{w.certID}, resp.CertificateIds)
}

// revokeOnClaim revokes the worker's certificate right after a task is
// claimed, as a removal that lands while a poll claims does.
type revokeOnClaim struct {
	dispatch.DispatchTaskStore
	revoke func()
}

func (s revokeOnClaim) ClaimNext(ctx context.Context, claim dispatch.DispatchTaskClaim) (*dispatch.ClaimedDispatchTask, error) {
	claimed, err := s.DispatchTaskStore.ClaimNext(ctx, claim)
	if claimed != nil {
		s.revoke()
	}
	return claimed, err
}

// A removal revokes the worker while its poll claims a task. The poll is
// refused and the task goes back to the queue at once, so another worker
// takes it (Spec 108 §Revocation).
func TestPollRevokedDuringClaim(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	require.NoError(t, f.dispatches.Enqueue(t.Context(), &dispatch.DispatchTask{
		DAGRunID: "run-3", Target: "dag", AttemptID: "attempt-1",
	}))
	f.h.dispatchTaskStore = revokeOnClaim{DispatchTaskStore: f.dispatches, revoke: func() { f.remove(t, w) }}

	_, err := f.poll(t, f.h, w)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	f.h.dispatchTaskStore = f.dispatches
	task, err := f.poll(t, f.h, f.enroll(t, "worker-b"))
	require.NoError(t, err)
	require.NotNil(t, task, "the task is back in the queue")
	assert.Equal(t, "run-3", task.DagRunId)
}

// A worker that enrolled as A restarted as B, so its record names B, and
// it stopped. An enrollment of A with a new key on another coordinator
// reads the removals, and a removal of A runs before it records its
// certificate. The removal finds the certificate by A, and the enrollment
// sees A among the revoked certificate's worker IDs and fails (Spec 108
// §Removal).
func TestEnrollmentAcrossRemovalOfEarlierID(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	renamed := w
	renamed.id = "worker-b"
	f.now = removalStart.Add(time.Second)
	f.heartbeat(t, f.h, renamed)
	f.now = removalStart.Add(time.Hour)
	other := f.coordinator(t)
	other.workerCertificates = &beforeCreate{WorkerCertificateStore: f.certs, hook: func() {
		f.remove(t, w)
	}}
	req, tlsCtx := f.enrollCall(t, "worker-a")

	_, err := other.Enroll(tlsCtx, req)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	certs, err := f.certs.List(t.Context())
	require.NoError(t, err)
	for _, cert := range certs {
		assert.Equal(t, persis.WorkerCertificateRevoked, cert.State, cert.ID)
	}
}

// A worker's ID goes A, B, A within seconds, with A's heartbeats on one
// coordinator and B's on another, and the worker then stops. A removal by
// A still finds the certificate after the heartbeats are cleaned up.
func TestRemoveWorkerBackToEarlierIDAcrossCoordinators(t *testing.T) {
	f := newRemovalFixture(t)
	other := f.coordinator(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	renamed := w
	renamed.id = "worker-b"
	f.now = removalStart.Add(time.Second)
	f.heartbeat(t, other, renamed)
	f.now = removalStart.Add(2 * time.Second)
	f.heartbeat(t, f.h, w)
	f.now = removalStart.Add(time.Hour)
	_, err := f.heartbeats.DeleteStale(t.Context(), f.now)
	require.NoError(t, err)

	resp := f.remove(t, w)
	assert.Equal(t, []string{w.certID}, resp.CertificateIds)
}

// countingRecords counts the heartbeats passed to certificate records.
type countingRecords struct {
	persis.WorkerCertificateStore
	calls int
}

func (s *countingRecords) RecordHeartbeat(ctx context.Context, cert persis.WorkerCertificate) error {
	s.calls++
	return s.WorkerCertificateStore.RecordHeartbeat(ctx, cert)
}

// Two workers share one certificate under two IDs and send heartbeats in
// turn every second. Once the record names both, their heartbeats pass to
// it no more than the hourly bound allows.
func TestSharedCertificateHeartbeatsBounded(t *testing.T) {
	f := newRemovalFixture(t)
	first := f.enroll(t, "worker-a")
	second := first
	second.id = "worker-b"
	records := &countingRecords{WorkerCertificateStore: f.certs}
	f.h.workerCertificates = records

	for i := range 10 {
		f.now = removalStart.Add(time.Duration(i) * time.Second)
		if i%2 == 0 {
			f.heartbeat(t, f.h, first)
		} else {
			f.heartbeat(t, f.h, second)
		}
	}
	assert.LessOrEqual(t, records.calls, 2)
}

// A worker's heartbeats stop, and the cleanup pass drops this
// coordinator's entry for it along with its stale presence. Its next
// heartbeat passes to the certificate record at once, as a new worker's
// does, so the entries stay bounded by the workers still reporting.
func TestCleanupDropsStaleHeartbeatEntries(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	records := &countingRecords{WorkerCertificateStore: f.certs}
	f.h.workerCertificates = records
	f.heartbeat(t, f.h, w)
	f.heartbeat(t, f.h, w)
	require.Equal(t, 1, records.calls)

	// The cleanup pass reads the wall clock, which is past the fixture's.
	f.h.detectAndCleanupZombies(t.Context())
	f.heartbeat(t, f.h, w)
	assert.Equal(t, 2, records.calls)
}

// heartbeatAs sends a heartbeat for w under the worker ID id at the time at.
func (f *removalFixture) heartbeatAs(t *testing.T, w removalWorker, id string, at time.Time) {
	t.Helper()
	f.heartbeatOn(t, f.h, w, id, at)
}

// heartbeatOn sends a heartbeat for w on h under the worker ID id at the
// time at.
func (f *removalFixture) heartbeatOn(t *testing.T, h *Handler, w removalWorker, id string, at time.Time) {
	t.Helper()
	f.now = at
	renamed := w
	renamed.id = id
	f.heartbeat(t, h, renamed)
}

// A worker enrolled as A ran under eight more IDs, so the record no longer
// lists A, and stopped. A removal of A finds the certificate by A's stale
// heartbeat while an enrollment of A runs on another coordinator, and the
// enrollment fails (Spec 108 §Removal).
func TestEnrollmentAcrossRemovalAfterIDEviction(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	for i := range 8 {
		f.heartbeatAs(t, w, fmt.Sprintf("worker-%d", i), removalStart.Add(time.Duration(i+1)*time.Second))
	}
	record, err := f.certs.Get(t.Context(), w.certID)
	require.NoError(t, err)
	require.False(t, namesWorker(record, w.id), "the record no longer names A")
	f.now = removalStart.Add(time.Hour)
	other := f.coordinator(t)
	other.workerCertificates = &beforeCreate{WorkerCertificateStore: f.certs, hook: func() {
		f.remove(t, w)
	}}
	req, tlsCtx := f.enrollCall(t, w.id)

	_, err = other.Enroll(tlsCtx, req)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	certs, err := f.certs.List(t.Context())
	require.NoError(t, err)
	for _, cert := range certs {
		assert.Equal(t, persis.WorkerCertificateRevoked, cert.State, cert.ID)
	}
}

// An enrollment of A starts, and a removal of A runs before it records its
// certificate. A second enrollment of A starts after the removal returned,
// and the first one fails and revokes its own certificate before the
// second confirms. The second enrollment succeeds (Spec 108 §Removal).
func TestEnrollmentAfterRemovalBesideRefusedOne(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	firstCoord, secondCoord := f.coordinator(t), f.coordinator(t)
	removed, resume, firstDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	firstCoord.workerCertificates = &beforeCreate{WorkerCertificateStore: f.certs, hook: func() {
		f.remove(t, w)
		close(removed)
		<-resume
	}}
	firstReq, firstCtx := f.enrollCall(t, w.id)
	go func() {
		_, err := firstCoord.Enroll(firstCtx, firstReq)
		firstDone <- err
	}()
	<-removed
	secondCoord.workerCertificates = &beforeCreate{WorkerCertificateStore: f.certs, hook: func() {
		close(resume)
		assert.Equal(t, codes.PermissionDenied, status.Code(<-firstDone))
	}}
	secondReq, secondCtx := f.enrollCall(t, w.id)

	_, err := secondCoord.Enroll(secondCtx, secondReq)
	assert.NoError(t, err)
}

// A worker ran under eight IDs, A first, then came back as A more than a
// minute later, and a ninth ID joined. The other IDs reach the record
// through the same coordinator as A or through another one. The record
// keeps A, the most recently seen but one, so a removal by A finds the
// certificate after the heartbeats are cleaned up (Spec 108 §Removal).
func TestRemoveWorkerReturnedAtCapacity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		others func(f *removalFixture, t *testing.T) *Handler
	}{
		{"one coordinator", func(f *removalFixture, _ *testing.T) *Handler { return f.h }},
		{"two coordinators", func(f *removalFixture, t *testing.T) *Handler { return f.coordinator(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRemovalFixture(t)
			others := tc.others(f, t)
			w := f.enroll(t, "worker-a")
			f.heartbeat(t, f.h, w)
			for i := range 7 {
				f.heartbeatOn(t, others, w, fmt.Sprintf("worker-%d", i), removalStart.Add(time.Duration(i+1)*time.Second))
			}
			f.heartbeatOn(t, f.h, w, w.id, removalStart.Add(2*time.Minute))
			f.heartbeatOn(t, others, w, "worker-8", removalStart.Add(2*time.Minute+time.Second))
			f.now = removalStart.Add(time.Hour)
			_, err := f.heartbeats.DeleteStale(t.Context(), f.now)
			require.NoError(t, err)

			resp := f.remove(t, w)
			assert.Equal(t, []string{w.certID}, resp.CertificateIds)
		})
	}
}

// The record lists A and B. A coordinator that predates the certificate ID
// in heartbeats records a fresh certificate heartbeat for B on another
// host. A removal of A counts B as connected and is refused (Spec 108
// §Removal).
func TestRemoveWorkerLegacyHeartbeatOfKnownID(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	f.heartbeatAs(t, w, "worker-b", removalStart.Add(time.Second))
	f.now = removalStart.Add(time.Hour)
	require.NoError(t, f.heartbeats.Upsert(t.Context(), dispatch.WorkerHeartbeatRecord{
		WorkerID: "worker-b", Host: "host-elsewhere", LastHeartbeatAt: f.now.UnixMilli(),
		Connection: dispatch.WorkerConnectionCertificate,
	}))

	_, err := f.h.RemoveWorker(t.Context(), &coordinatorv1.RemoveWorkerRequest{WorkerId: w.id})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Equal(t, persis.WorkerCertificateActive, f.state(t, w))
}

// removalDispatchFixture is a coordinator that dispatches to the bound
// worker node-a on the certificate cert-1, with the certificate store the
// test changes as another coordinator would.
func removalDispatchFixture(t *testing.T) (*holdFixture, persis.WorkerCertificateStore) {
	t.Helper()
	f := newHoldFixture(t, 0)
	certs, err := store.NewWorkerCertificateStore(testutil.NewMemoryBackend().Collection(persis.CollectionWorkerCertificates))
	require.NoError(t, err)
	require.NoError(t, certs.Create(t.Context(), persis.WorkerCertificate{
		ID: "cert-1", KeyFingerprint: make([]byte, 32), State: persis.WorkerCertificateActive, WorkerID: "node-a", CreatedAt: time.Now(),
	}))
	f.h.workerCertificates = certs
	require.NoError(t, f.heartbeats.Upsert(t.Context(), dispatch.WorkerHeartbeatRecord{
		WorkerID: "node-a", LastHeartbeatAt: time.Now().UnixMilli(), CertificateID: "cert-1",
	}))
	return f, certs
}

func boundRepeatTask(runID string) *coordinatorv1.Task {
	task := repeatTask(runID)
	task.TargetWorkerId, task.BoundNode = "node-a", "node-a"
	return task
}

// A repeat of a bound dispatch admitted before another coordinator removed
// the worker succeeds on a coordinator that has not reread the removals,
// as on one that has; a new dispatch to that worker is refused (Spec 108
// §Removal).
func TestDispatchRepeatToNodeRemovedElsewhere(t *testing.T) {
	f, certs := removalDispatchFixture(t)
	_, err := f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: boundRepeatTask("run-bound")})
	require.NoError(t, err)
	admitted := f.attempt("run-bound")
	_, err = certs.MarkRemoving(t.Context(), "cert-1", "node-a", time.Now)
	require.NoError(t, err)

	_, err = f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: boundRepeatTask("run-bound")})
	require.NoError(t, err)
	assert.Same(t, admitted, f.attempt("run-bound"))
	_, err = f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: boundRepeatTask("run-other")})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Contains(t, status.Convert(err).Message(), "is being removed")
}

// The scheduler queues a run bound to node-a through one coordinator, and
// another coordinator removes the worker. A repeat of that queued dispatch
// reaches a third coordinator, whose own dispatch store looked up the
// attempt before it was queued. The repeat still succeeds (Spec 108
// §Removal).
func TestQueuedRepeatToNodeRemovedElsewhere(t *testing.T) {
	f, certs := removalDispatchFixture(t)
	queued := &ir.DAGRunStatus{Name: "routed", DAGRunID: "run-queued", AttemptID: "queued01", Status: ir.Queued}
	f.runs.addAttempt(ir.NewDAGRunRef("routed", "run-queued"), queued)
	previous, err := convert.DAGRunStatusToProto(queued)
	require.NoError(t, err)
	task := func() *coordinatorv1.Task {
		task := boundRepeatTask("run-queued")
		task.Operation = coordinatorv1.Operation_OPERATION_RETRY
		task.PreviousStatus = previous
		return task
	}
	dispatches := newTestDispatchTaskStore(f.baseDir)
	other := NewHandler(HandlerConfig{
		DAGRunRepository:     f.runs.repository,
		DispatchTaskStore:    dispatches,
		DAGRunLeaseStore:     f.leases,
		WorkerHeartbeatStore: f.heartbeats,
	})
	t.Cleanup(func() { other.Close(context.Background()) })
	other.workerCertificates = certs
	attemptKey := ir.GenerateAttemptKey("routed", "run-queued", "routed", "run-queued", "queued01")
	outstanding, err := dispatches.HasOutstandingAttempt(t.Context(), attemptKey, 0)
	require.NoError(t, err)
	require.False(t, outstanding)

	_, err = f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: task()})
	require.NoError(t, err)
	_, err = certs.MarkRemoving(t.Context(), "cert-1", "node-a", time.Now)
	require.NoError(t, err)

	_, err = other.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: task()})
	assert.NoError(t, err)
}

// strandFixture is a coordinator over shared file stores with a queue that
// runs one run at a time, and node-a, a worker on certificate cert-1.
type strandFixture struct {
	t       *testing.T
	baseDir string
	h       *Handler
	runs    *mockDAGRunStore
	certs   persis.WorkerCertificateStore
	beats   dispatch.WorkerHeartbeatStore
	leases  *store.DAGRunLeaseStore
	active  *store.ActiveDistributedRunStore
	// dispatches is h's dispatch store.
	dispatches *store.DispatchTaskStore
}

func newStrandFixture(t *testing.T) *strandFixture {
	t.Helper()
	registerCommandExecutorCapsForCoordinatorTest()
	f := &strandFixture{t: t, baseDir: t.TempDir(), runs: newMockDAGRunStore()}
	f.dispatches, f.leases, f.active = newTestDispatchAdmissionTaskStore(f.baseDir)
	f.beats = newTestWorkerHeartbeatStore(f.baseDir)
	certs, err := store.NewWorkerCertificateStore(testutil.NewMemoryBackend().Collection(persis.CollectionWorkerCertificates))
	require.NoError(t, err)
	f.certs = certs
	f.addCertificate("cert-1")
	f.h = f.coordinator(f.dispatches)
	return f
}

// coordinator returns a coordinator over the fixture's stores and the
// dispatch store dispatches.
func (f *strandFixture) coordinator(dispatches dispatch.DispatchTaskStore) *Handler {
	f.t.Helper()
	h := NewHandler(HandlerConfig{
		DAGRunRepository:          f.runs.repository,
		DispatchTaskStore:         dispatches,
		WorkerHeartbeatStore:      f.beats,
		DAGRunLeaseStore:          f.leases,
		ActiveDistributedRunStore: f.active,
	})
	h.workerCertificates = f.certs
	f.t.Cleanup(func() { h.Close(context.Background()) })
	return h
}

// addCertificate records the active certificate id for node-a, which
// connects with it now.
func (f *strandFixture) addCertificate(id string) {
	f.t.Helper()
	require.NoError(f.t, f.certs.Create(f.t.Context(), persis.WorkerCertificate{
		ID: id, KeyFingerprint: []byte(id), State: persis.WorkerCertificateActive, WorkerID: "node-a", CreatedAt: time.Now(),
	}))
	require.NoError(f.t, f.beats.Upsert(f.t.Context(), dispatch.WorkerHeartbeatRecord{
		WorkerID: "node-a", LastHeartbeatAt: time.Now().UnixMilli(), CertificateID: id,
	}))
}

// reserve asks the queue for a place for runID.
func (f *strandFixture) reserve(runID string) *dispatch.DispatchAdmissionDecision {
	f.t.Helper()
	decision, err := f.dispatches.ReserveAdmission(f.t.Context(), dispatch.DispatchAdmissionRequest{
		QueueName: "queue-1", MaxConcurrency: 1, StaleThreshold: time.Minute,
		AttemptKey: ir.GenerateAttemptKey("routed", runID, "routed", runID, repeatAttemptID),
		AttemptID:  repeatAttemptID, DAGRun: ir.NewDAGRunRef("routed", runID),
	})
	require.NoError(f.t, err)
	return decision
}

// admit dispatches runID, bound to node-a, through h in the queue's place.
func (f *strandFixture) admit(h *Handler, runID string) {
	f.t.Helper()
	admitted := f.reserve(runID)
	require.True(f.t, admitted.Reserved)
	task := boundRepeatTask(runID)
	task.QueueName = "queue-1"
	_, err := h.Dispatch(f.t.Context(), &coordinatorv1.DispatchRequest{Task: task, AdmissionReservationToken: admitted.ReservationToken})
	require.NoError(f.t, err)
	require.False(f.t, f.reserve("run-next").Reserved, "the queue is full")
}

// revoke revokes cert-1, whose worker no longer reports.
func (f *strandFixture) revoke() {
	f.t.Helper()
	_, err := f.certs.SetState(f.t.Context(), "cert-1", persis.WorkerCertificateRevoked)
	require.NoError(f.t, err)
	require.NoError(f.t, f.beats.Upsert(f.t.Context(), dispatch.WorkerHeartbeatRecord{
		WorkerID: "node-a", LastHeartbeatAt: time.Now().Add(-time.Hour).UnixMilli(), CertificateID: "cert-1",
	}))
}

func (f *strandFixture) status(runID string) *ir.DAGRunStatus {
	f.t.Helper()
	status, err := f.runs.attempts[runID].ReadStatus(f.t.Context())
	require.NoError(f.t, err)
	return status
}

// A queue that runs one run at a time admits a run bound to node-a, and
// node-a's certificate is revoked before it accepts the run. The next
// reread fails the run with the removal as its reason and frees the queue's
// place for its next run: also for a run the worker polled before the
// removal, one that another coordinator admitted after this one read its
// tasks, and one this coordinator finds only after a restart. A worker that
// enrolled again as node-a keeps the run (Spec 108 §Removal).
func TestRemovalFailsAdmittedBoundRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		// run admits run-1, revokes cert-1, and returns the coordinator
		// whose reread is checked.
		run func(f *strandFixture) *Handler
		// kept is set when the run is to stay with node-a.
		kept bool
	}{
		{name: "pending", run: func(f *strandFixture) *Handler {
			f.admit(f.h, "run-1")
			f.revoke()
			return f.h
		}},
		{name: "polled before the removal", run: func(f *strandFixture) *Handler {
			f.admit(f.h, "run-1")
			claimed, err := f.dispatches.ClaimNext(f.t.Context(), dispatch.DispatchTaskClaim{WorkerID: "node-a", PollerID: "poller-1"})
			require.NoError(f.t, err)
			require.NotNil(f.t, claimed)
			f.revoke()
			return f.h
		}},
		{name: "admitted by another coordinator", run: func(f *strandFixture) *Handler {
			_, err := f.dispatches.ListBoundTo(f.t.Context(), []string{"node-a"})
			require.NoError(f.t, err)
			peerStore, _, _ := newTestDispatchAdmissionTaskStore(f.baseDir)
			f.admit(f.coordinator(peerStore), "run-1")
			f.revoke()
			return f.h
		}},
		{name: "after a restart", run: func(f *strandFixture) *Handler {
			f.admit(f.h, "run-1")
			f.revoke()
			restartedStore, _, _ := newTestDispatchAdmissionTaskStore(f.baseDir)
			return f.coordinator(restartedStore)
		}},
		{name: "enrolled again", kept: true, run: func(f *strandFixture) *Handler {
			f.admit(f.h, "run-1")
			f.revoke()
			f.addCertificate("cert-2")
			return f.h
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStrandFixture(t)
			h := tc.run(f)

			h.rereadRemovals(t.Context())
			if tc.kept {
				assert.NotEqual(t, ir.Failed, f.status("run-1").Status)
				assert.False(t, f.reserve("run-next").Reserved, "the run keeps its place")
				claimed, err := f.dispatches.ClaimNext(t.Context(), dispatch.DispatchTaskClaim{WorkerID: "node-a", PollerID: "poller-2"})
				require.NoError(t, err)
				assert.NotNil(t, claimed, "node-a can still take the run")
				return
			}
			status := f.status("run-1")
			assert.Equal(t, ir.Failed, status.Status)
			assert.Contains(t, status.Error, "node-a was removed")
			assert.True(t, f.reserve("run-next").Reserved, "the queue's next run is admitted")
		})
	}
}

// failingCertificateRead fails every certificate read.
type failingCertificateRead struct {
	persis.WorkerCertificateStore
}

func (failingCertificateRead) Get(context.Context, string) (persis.WorkerCertificate, error) {
	return persis.WorkerCertificate{}, errors.New("unreadable certificate")
}

// A bound dispatch whose worker's certificate cannot be read is refused as
// unavailable, so the caller tries again, rather than admitted to a worker
// that may be in removal.
func TestDispatchToNodeWithUnreadableCertificate(t *testing.T) {
	f, certs := removalDispatchFixture(t)
	f.h.workerCertificates = failingCertificateRead{certs}

	_, err := f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: boundRepeatTask("run-bound")})
	assert.Equal(t, codes.Unavailable, status.Code(err))
	assert.Nil(t, f.runs.attempts["run-bound"], "nothing is created when refused")
}

// An acceptance writes the task's lease, and its second certificate read
// fails, so the claim stays. The certificate is revoked, the retry is
// refused, and the claim expires. Another worker's poll does not get the
// task, since the lease owns the attempt now (Spec 108 §Removal).
func TestClaimNotReissuedAfterLeasedRefusal(t *testing.T) {
	f := newRemovalFixture(t)
	baseDir := t.TempDir()
	f.dispatches = store.NewDispatchTaskStore(file.NewCollection(baseDir),
		store.WithDispatchAdmissionLiveness(f.leases, newTestActiveDistributedRunStore(baseDir)),
		store.WithDispatchReservationTTL(50*time.Millisecond))
	f.h.dispatchTaskStore = f.dispatches
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	require.NoError(t, f.dispatches.Enqueue(t.Context(), &dispatch.DispatchTask{
		DAGRunID: "run-3", Target: "dag", AttemptID: "attempt-1", AttemptKey: attemptKeyOf("run-3"),
	}))
	task, err := f.poll(t, f.h, w)
	require.NoError(t, err)
	require.NotNil(t, task)
	req := &coordinatorv1.AckTaskClaimRequest{WorkerId: w.id, ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey}
	f.h.workerCertificates = failingConfirm{f.certs}
	_, err = f.h.AckTaskClaim(w.ctx, req)
	require.Equal(t, codes.Unavailable, status.Code(err))
	_, err = f.certs.SetState(t.Context(), w.certID, persis.WorkerCertificateRevoked)
	require.NoError(t, err)
	_, err = f.h.AckTaskClaim(w.ctx, req)
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	time.Sleep(100 * time.Millisecond)
	other, err := f.poll(t, f.h, f.enroll(t, "worker-b"))
	require.NoError(t, err)
	assert.Nil(t, other, "the leased attempt does not go to another worker")
	_, err = f.leases.Get(t.Context(), task.AttemptKey)
	assert.NoError(t, err, "the lease stays for the stale-lease check")
}

// slowCommit lets the clock move on between the time a removal's write
// takes and the write's commit, as a slow store does.
type slowCommit struct {
	persis.WorkerCertificateStore
	delay func()
}

func (s slowCommit) MarkRemoving(ctx context.Context, id, workerID string, now func() time.Time) (persis.WorkerCertificate, error) {
	return s.WorkerCertificateStore.MarkRemoving(ctx, id, workerID, func() time.Time {
		at := now()
		s.delay()
		return at
	})
}

// The removal's write commits longer than the minimum wait after it took
// its time. The wait counts from when the write returned, so the worker's
// next idle heartbeat does not revoke it (Spec 108 §Removal).
func TestRemovingMinimumStartsAtCommit(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	f.h.workerCertificates = slowCommit{WorkerCertificateStore: f.certs, delay: func() {
		f.now = f.now.Add(removingMinimum + time.Second)
	}}

	f.remove(t, w)
	f.heartbeat(t, f.h, w)
	assert.Equal(t, persis.WorkerCertificateRemoving, f.state(t, w))
}

// A run that declares connections is bound to a removing worker that
// cannot take them. The dispatch says the worker is being removed, as it
// does for any run bound to it (Spec 108 §Removal).
func TestDispatchToRemovingNodeWithoutConnections(t *testing.T) {
	f, certs := removalDispatchFixture(t)
	_, err := certs.MarkRemoving(t.Context(), "cert-1", "node-a", time.Now)
	require.NoError(t, err)
	task := boundRepeatTask("run-bound")
	task.Definition = "steps:\n  - name: read\n    action: mcp.call\n    with: {connection: work, tool: search}\n"

	_, err = f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: task})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Equal(t, `worker node "node-a" is being removed`, status.Convert(err).Message())
}

// stallLeaseWrite pauses the first lease write until release closes, after
// telling reached, as an acceptance that stalls before its lease does.
type stallLeaseWrite struct {
	dispatch.DAGRunLeaseStore
	reached chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (s *stallLeaseWrite) Upsert(ctx context.Context, lease dispatch.DAGRunLease) error {
	s.once.Do(func() {
		close(s.reached)
		<-s.release
	})
	return s.DAGRunLeaseStore.Upsert(ctx, lease)
}

// afterIdleCheck runs hook once, right after the first idle check of a
// revocation, which lists the leases after it read the removal record.
type afterIdleCheck struct {
	persis.WorkerCertificateStore
	hook func()
	once sync.Once
}

func (s *afterIdleCheck) RevokeIfIdle(ctx context.Context, id string, idle func(context.Context, persis.WorkerCertificate) (bool, error)) (persis.WorkerCertificate, bool, error) {
	return s.WorkerCertificateStore.RevokeIfIdle(ctx, id, func(ctx context.Context, read persis.WorkerCertificate) (bool, error) {
		ok, err := idle(ctx, read)
		s.once.Do(s.hook)
		return ok, err
	})
}

// An acceptance that read the certificate active stalls before its lease.
// A removal with finish becomes due, and its revocation reads the removal
// record and lists the leases before the acceptance writes its lease and
// confirms. The confirmation changes the record, so the revocation's write
// fails, and it lists again, finds the lease and waits; the acceptance
// stands (Spec 108 §Removal).
func TestFinishRevocationAfterAcceptance(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	require.NoError(t, f.dispatches.Enqueue(t.Context(), &dispatch.DispatchTask{
		DAGRunID: "run-3", Target: "dag", AttemptID: "attempt-1", AttemptKey: attemptKeyOf("run-3"),
	}))
	task, err := f.poll(t, f.h, w)
	require.NoError(t, err)
	require.NotNil(t, task)
	acceptor := f.coordinator(t)
	reached, release := make(chan struct{}), make(chan struct{})
	acceptor.dagRunLeaseStore = &stallLeaseWrite{DAGRunLeaseStore: f.leases, reached: reached, release: release}
	acked := make(chan *coordinatorv1.AckTaskClaimResponse, 1)
	go func() {
		ack, err := acceptor.AckTaskClaim(w.ctx, &coordinatorv1.AckTaskClaimRequest{WorkerId: w.id, ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey})
		assert.NoError(t, err)
		acked <- ack
	}()
	<-reached
	f.remove(t, w)
	f.h.workerCertificates = &afterIdleCheck{WorkerCertificateStore: f.certs, hook: func() {
		close(release)
		ack := <-acked
		assert.True(t, ack.Accepted)
	}}

	// The idle heartbeat past the minimum wait settles the removal.
	f.now = removalStart.Add(removingMinimum + time.Second)
	f.heartbeat(t, f.h, w)
	assert.Equal(t, persis.WorkerCertificateRemoving, f.state(t, w), "the accepted run is waited for")
}

// beforeClaimMark runs hook once, before the first mark of a claim.
type beforeClaimMark struct {
	dispatch.DispatchTaskStore
	hook func()
	once sync.Once
}

func (s *beforeClaimMark) MarkClaimLeasing(ctx context.Context, claimToken string) error {
	s.once.Do(s.hook)
	return s.DispatchTaskStore.MarkClaimLeasing(ctx, claimToken)
}

// A retried acknowledgement reads the claim, and the first one writes the
// lease and drops the claim before the retry marks it. The retry finds the
// lease the first one wrote and is accepted too.
func TestAckRetryAfterClaimFinalized(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	require.NoError(t, f.dispatches.Enqueue(t.Context(), &dispatch.DispatchTask{
		DAGRunID: "run-4", Target: "dag", AttemptID: "attempt-1", AttemptKey: attemptKeyOf("run-4"),
	}))
	task, err := f.poll(t, f.h, w)
	require.NoError(t, err)
	require.NotNil(t, task)
	req := &coordinatorv1.AckTaskClaimRequest{WorkerId: w.id, ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey}
	retrier := f.coordinator(t)
	retrier.dispatchTaskStore = &beforeClaimMark{DispatchTaskStore: f.dispatches, hook: func() {
		ack, err := f.h.AckTaskClaim(w.ctx, req)
		require.NoError(t, err)
		require.True(t, ack.Accepted)
	}}

	ack, err := retrier.AckTaskClaim(w.ctx, req)
	require.NoError(t, err)
	assert.True(t, ack.Accepted, ack.Error)
}

// recordHook runs hook once, before the first record pass.
type recordHook struct {
	persis.WorkerCertificateStore
	hook func()
	once sync.Once
}

func (s *recordHook) RecordHeartbeat(ctx context.Context, cert persis.WorkerCertificate) error {
	s.once.Do(s.hook)
	return s.WorkerCertificateStore.RecordHeartbeat(ctx, cert)
}

// A heartbeat that arrived before a removal reaches another coordinator
// while that coordinator applies the removal. The wait counts from when the
// removal reached its copy, not from the heartbeat's arrival, so the next
// heartbeat a second after the removal does not revoke (Spec 108 §Removal).
func TestRemovingMinimumAfterDelayedHeartbeat(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, w)
	other := f.coordinator(t)
	other.workerCertificates = &recordHook{WorkerCertificateStore: f.certs, hook: func() {
		f.now = removalStart.Add(removingMinimum)
		f.remove(t, w)
		// The removal reaches this coordinator's copy, as a refused call
		// brings it.
		record, err := f.certs.Get(t.Context(), w.certID)
		require.NoError(t, err)
		other.removals.apply(removalOf(record))
	}}
	f.heartbeat(t, other, w)

	f.now = removalStart.Add(removingMinimum + time.Second)
	f.heartbeat(t, other, w)
	assert.Equal(t, persis.WorkerCertificateRemoving, f.state(t, w))
}

// beforeRemovalBegins runs hook once, before the first count of a removal on
// the fence.
type beforeRemovalBegins struct {
	persis.WorkerCertificateStore
	hook func()
	once sync.Once
}

func (s *beforeRemovalBegins) BeginWorkerRemoval(ctx context.Context, workerID string) (int64, error) {
	s.once.Do(s.hook)
	return s.WorkerCertificateStore.BeginWorkerRemoval(ctx, workerID)
}

// afterRemovalBegins runs hook once, after the first count of a removal on
// the fence.
type afterRemovalBegins struct {
	persis.WorkerCertificateStore
	hook func()
	once sync.Once
}

func (s *afterRemovalBegins) BeginWorkerRemoval(ctx context.Context, workerID string) (int64, error) {
	removals, err := s.WorkerCertificateStore.BeginWorkerRemoval(ctx, workerID)
	s.once.Do(s.hook)
	return removals, err
}

// An enrollment of a connected worker's ID meets its removal with finish on
// either side of the removal's start. One that confirms before the removal
// begins has its certificate revoked by the removal; one whose
// confirmation comes after fails. One that starts after the removal began
// is a new one and keeps its certificate (Spec 108 §Removal).
func TestEnrollmentAroundConnectedRemoval(t *testing.T) {
	t.Run("confirmed before", func(t *testing.T) {
		f := newRemovalFixture(t)
		w := f.enroll(t, "worker-a")
		f.heartbeat(t, f.h, w)
		other := f.coordinator(t)
		var enrolled string
		f.h.workerCertificates = &beforeRemovalBegins{WorkerCertificateStore: f.certs, hook: func() {
			req, tlsCtx := f.enrollCall(t, w.id)
			resp, err := other.Enroll(tlsCtx, req)
			require.NoError(t, err)
			cert, err := x509.ParseCertificate(resp.Certificate)
			require.NoError(t, err)
			enrolled = cert.Subject.CommonName
		}}

		resp := f.remove(t, w)
		assert.Contains(t, resp.CertificateIds, enrolled)
		record, err := f.certs.Get(t.Context(), enrolled)
		require.NoError(t, err)
		assert.Equal(t, persis.WorkerCertificateRevoked, record.State)
	})
	t.Run("confirming after", func(t *testing.T) {
		f := newRemovalFixture(t)
		w := f.enroll(t, "worker-a")
		f.heartbeat(t, f.h, w)
		other := f.coordinator(t)
		other.workerCertificates = &beforeCreate{WorkerCertificateStore: f.certs, hook: func() {
			f.remove(t, w)
		}}
		req, tlsCtx := f.enrollCall(t, w.id)

		_, err := other.Enroll(tlsCtx, req)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
		assert.Equal(t, persis.WorkerCertificateRemoving, f.state(t, w))
	})
	t.Run("started after", func(t *testing.T) {
		f := newRemovalFixture(t)
		w := f.enroll(t, "worker-a")
		f.heartbeat(t, f.h, w)
		other := f.coordinator(t)
		var enrolled string
		f.h.workerCertificates = &afterRemovalBegins{WorkerCertificateStore: f.certs, hook: func() {
			req, tlsCtx := f.enrollCall(t, w.id)
			resp, err := other.Enroll(tlsCtx, req)
			require.NoError(t, err)
			cert, err := x509.ParseCertificate(resp.Certificate)
			require.NoError(t, err)
			enrolled = cert.Subject.CommonName
		}}

		resp := f.remove(t, w)
		assert.Equal(t, []string{w.certID}, resp.CertificateIds)
		assert.Equal(t, persis.WorkerCertificateActive, f.state(t, removalWorker{certID: enrolled}))
	})
}

// A worker on certificate X ran as node-a and now runs as node-b. A removal
// of node-a makes X removing for node-b while another coordinator enrolls
// node-b. The removal names X only, so the enrollment of node-b succeeds
// (Spec 108 §Removal).
func TestEnrollmentDuringRemovalOfHeldCertificate(t *testing.T) {
	f := newRemovalFixture(t)
	moved := f.enroll(t, "node-a")
	f.heartbeat(t, f.h, moved)
	f.heartbeatAs(t, moved, "node-b", removalStart.Add(2*time.Minute))
	f.now = removalStart.Add(time.Hour)
	f.heartbeatAs(t, moved, "node-b", f.now)
	other := f.coordinator(t)
	other.workerCertificates = &beforeCreate{WorkerCertificateStore: f.certs, hook: func() {
		resp := f.remove(t, removalWorker{id: "node-a"})
		assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING, resp.State)
	}}
	req, tlsCtx := f.enrollCall(t, "node-b")

	_, err := other.Enroll(tlsCtx, req)
	require.NoError(t, err)
	assert.Equal(t, persis.WorkerCertificateRemoving, f.state(t, moved))
}

// A worker on certificate X ran as node-a and now runs as node-b, and
// another worker took node-a on certificate Y. A removal of node-a removes
// Y and leaves X, which node-b still connects with, active, also when
// node-b's heartbeat reaches a coordinator that predates the certificate ID
// in heartbeats (Spec 108 §Removal).
func TestRemoveConnectedIDOnceHeldElsewhere(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			f := newRemovalFixture(t)
			moved := f.enroll(t, "node-a")
			f.heartbeat(t, f.h, moved)
			f.heartbeatAs(t, moved, "node-b", removalStart.Add(2*time.Minute))
			if legacy {
				require.NoError(t, f.heartbeats.Upsert(t.Context(), dispatch.WorkerHeartbeatRecord{
					WorkerID: "node-b", Host: "host-node-b", LastHeartbeatAt: f.now.UnixMilli(),
					Connection: dispatch.WorkerConnectionCertificate,
				}))
			}
			replacement := f.enroll(t, "node-a")
			f.heartbeat(t, f.h, replacement)

			resp := f.remove(t, replacement)
			assert.Equal(t, []string{replacement.certID}, resp.CertificateIds)
			assert.Equal(t, persis.WorkerCertificateActive, f.state(t, moved))
		})
	}
}

// A repeat of a bound dispatch admitted before a removal elsewhere succeeds
// even when this coordinator cannot read the worker's certificate (Spec 108
// §Removal).
func TestDispatchRepeatWithUnreadableCertificate(t *testing.T) {
	f, certs := removalDispatchFixture(t)
	_, err := f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: boundRepeatTask("run-bound")})
	require.NoError(t, err)
	_, err = certs.MarkRemoving(t.Context(), "cert-1", "node-a", time.Now)
	require.NoError(t, err)
	f.h.workerCertificates = failingCertificateRead{certs}

	_, err = f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: boundRepeatTask("run-bound")})
	assert.NoError(t, err)
}
