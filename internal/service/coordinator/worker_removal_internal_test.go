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
			assert.Equal(t, connectedWorkerMessage, status.Convert(err).Message())
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

func (s *countingRecords) RecordHeartbeat(ctx context.Context, cert persis.WorkerCertificate) (persis.WorkerCertificate, error) {
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
