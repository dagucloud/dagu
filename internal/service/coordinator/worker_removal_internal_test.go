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
	"path/filepath"
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
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

var removalStart = time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)

// removalFixture is a coordinator over the shared stores, with a clock the
// test moves.
type removalFixture struct {
	h          *Handler
	now        time.Time
	ca         *PeerCA
	certs      persis.WorkerCertificateStore
	invites    persis.WorkerInviteStore
	heartbeats dispatch.WorkerHeartbeatStore
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
		certs:      certs,
		invites:    invites,
		heartbeats: newTestWorkerHeartbeatStore(baseDir),
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
		DispatchTaskStore:      f.dispatches,
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

// heartbeat sends the worker's heartbeat on h.
func (f *removalFixture) heartbeat(t *testing.T, h *Handler, w removalWorker) {
	t.Helper()
	_, err := h.Heartbeat(w.ctx, &coordinatorv1.HeartbeatRequest{WorkerId: w.id, Host: "host-" + w.id})
	require.NoError(t, err)
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

// A worker that is not connected is revoked at once, and its long poll
// ends with the refusal (Spec 108 §Revocation).
func TestRemovedWorkerPoll(t *testing.T) {
	f := newRemovalFixture(t)
	w := f.enroll(t, "worker-a")
	resp := f.remove(t, w)
	assert.Equal(t, coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVED, resp.State)
	assert.Equal(t, persis.WorkerCertificateRevoked, f.state(t, w))

	_, err := f.poll(t, f.h, w)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
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
// worker connects under a new ID, is refused, so a certificate in use is
// not revoked (Spec 108 §Removal).
func TestRemoveStaleIDOfConnectedCertificate(t *testing.T) {
	f := newRemovalFixture(t)
	first := f.enroll(t, "worker-a")
	f.heartbeat(t, f.h, first)
	restarted := first
	restarted.id = "worker-b"
	f.now = removalStart.Add(time.Minute)
	f.heartbeat(t, f.h, restarted)

	_, err := f.h.RemoveWorker(t.Context(), &coordinatorv1.RemoveWorkerRequest{WorkerId: first.id})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Equal(t, persis.WorkerCertificateActive, f.state(t, first))
}

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
		WorkerHeartbeatStore:   f.heartbeats,
		PeerCA:                 f.ca,
		WorkerInviteStore:      f.invites,
		WorkerCertificateStore: failingRemovals{f.certs},
	})
	h.clock = func() time.Time { return f.now }
	h.rereadRemovals(t.Context())
	assert.Equal(t, codes.Unavailable, status.Code(h.removals.refusal(revoked.certID)))

	other := f.enroll(t, "worker-b")
	_, err = h.RemoveWorker(t.Context(), &coordinatorv1.RemoveWorkerRequest{WorkerId: other.id})
	require.NoError(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(h.removals.refusal(revoked.certID)))
}

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
		WorkerID: w.id, Host: "host-" + w.id, CreatedAt: got.CreatedAt, LastHeartbeatAt: f.now,
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
