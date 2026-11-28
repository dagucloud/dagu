// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	coordinatorA = "coordinator-a:50055"
	coordinatorB = "coordinator-b:50055"
)

// testCA signs worker certificates as a coordinator's peer CA does.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return testCA{cert: cert, key: key}
}

// invite returns a new invite for the CA: its pin is the base64url SHA-256
// of the CA certificate.
func (ca testCA) invite(t *testing.T) string {
	t.Helper()
	secret := make([]byte, 32)
	_, err := rand.Read(secret)
	require.NoError(t, err)
	pin := sha256.Sum256(ca.cert.Raw)
	return "dagu1." + base64.RawURLEncoding.EncodeToString(pin[:]) + "." + base64.RawURLEncoding.EncodeToString(secret)
}

// fakeEnroll answers Enroll for each address as results says: a CA that
// signs the request's key, or an error. It records the requests.
type fakeEnroll struct {
	mu       sync.Mutex
	results  map[string][]any
	requests []*coordinatorv1.EnrollRequest
}

func (f *fakeEnroll) enroll(_ context.Context, _ *coordinator.Config, address string, req *coordinatorv1.EnrollRequest) (*coordinatorv1.EnrollResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	results := f.results[address]
	if len(results) == 0 {
		return nil, status.Error(codes.Unavailable, "connection refused")
	}
	result := results[0]
	if len(results) > 1 {
		f.results[address] = results[1:]
	}
	if err, ok := result.(error); ok {
		return nil, err
	}
	ca := result.(testCA)
	csr, err := x509.ParseCertificateRequest(req.Csr)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "cert-" + req.WorkerId},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, csr.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	return &coordinatorv1.EnrollResponse{Certificate: der, CaCertificate: ca.cert.Raw}, nil
}

func (f *fakeEnroll) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// enrolling returns a fake whose first coordinator enrolls with ca.
func enrolling(ca testCA) *fakeEnroll {
	return &fakeEnroll{results: map[string][]any{coordinatorA: {ca}}}
}

// workerConfig returns the config of a worker on dataDir with invite and
// peer.insecure unset.
func workerConfig(dataDir, invite string) *config.Config {
	cfg := &config.Config{}
	cfg.Paths.DataDir = dataDir
	cfg.Worker.Coordinators = []string{coordinatorA, coordinatorB}
	cfg.Worker.Invite = invite
	cfg.Core.Peer.Insecure = true
	cfg.Core.Peer.InsecureUnset = true
	return cfg
}

// testLockTiming waits for a held lock briefly, so tests need not wait 30
// seconds.
var testLockTiming = lockTiming{wait: 300 * time.Millisecond, retry: 10 * time.Millisecond}

// open opens the membership of cfg, enrolling through fake.
func open(t *testing.T, cfg *config.Config, fake *fakeEnroll) (*Membership, error) {
	t.Helper()
	if fake == nil {
		fake = &fakeEnroll{}
	}
	m, err := openMembership(t.Context(), cfg, nil, joinOptions{
		lock:        testLockTiming,
		enroll:      fake.enroll,
		retryPolicy: backoff.NewConstantBackoffPolicy(0),
	})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = m.Close() })
	return m, nil
}

// connect connects with m, enrolling when it needs to.
func connect(ctx context.Context, t *testing.T, m *Membership) error {
	t.Helper()
	client, err := m.Connect(ctx, "worker-1")
	if err == nil {
		t.Cleanup(func() { _ = client.Cleanup(context.Background()) })
	}
	return err
}

// join opens the membership of cfg, connects with it, and closes it.
func join(t *testing.T, cfg *config.Config, fake *fakeEnroll) *Membership {
	t.Helper()
	m, err := open(t, cfg, fake)
	require.NoError(t, err)
	require.NoError(t, connect(t.Context(), t, m))
	require.NoError(t, m.Close())
	return m
}

func startLine(t *testing.T, m *Membership) string {
	t.Helper()
	line, err := m.StartTransport()
	require.NoError(t, err)
	return line
}

// credentialPath is where a worker on dataDir keeps its credential.
func credentialPath(dataDir string) string {
	return filepath.Join(dataDir, credentialDirName, credentialFileName)
}

// skipWithoutPermissions skips a test that needs this user to be denied
// access, which root and Windows mode bits do not give.
func skipWithoutPermissions(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file this user cannot access")
	}
}

// A worker with an invite enrolls with a new key, keeps its credential in
// its data directory, readable by its user only and without the invite, and
// then presents its certificate. A restart with the same invite, or with
// none, keeps the certificate and does not enroll (Spec 108 §Worker Start,
// §Worker Files).
func TestMembershipEnrolls(t *testing.T) {
	ca := newTestCA(t)
	invite := ca.invite(t)
	fake := enrolling(ca)
	dataDir := t.TempDir()

	m, err := open(t, workerConfig(dataDir, invite), fake)
	require.NoError(t, err)
	assert.Equal(t, "built-in TLS (from worker.invite)", startLine(t, m))
	require.NoError(t, connect(t.Context(), t, m))
	require.NoError(t, m.Close())

	require.Len(t, fake.requests, 1)
	assert.Equal(t, "worker-1", fake.requests[0].WorkerId)
	assert.True(t, fake.requests[0].KeepsCertificate)
	assert.Equal(t, "built-in TLS (from worker certificate)", startLine(t, m))
	assert.True(t, ca.cert.Equal(m.cred.ca))

	data, err := fileutil.ReadFile(credentialPath(dataDir))
	require.NoError(t, err)
	assert.NotContains(t, string(data), invite[strings.LastIndex(invite, ".")+1:])
	if runtime.GOOS != "windows" {
		for _, path := range []string{credentialPath(dataDir), filepath.Dir(credentialPath(dataDir))} {
			info, err := os.Stat(path)
			require.NoError(t, err)
			assert.Zero(t, info.Mode().Perm()&0o077, path)
		}
	}

	for name, restart := range map[string]string{"SameInvite": invite, "NoInvite": ""} {
		t.Run(name, func(t *testing.T) {
			restarted := join(t, workerConfig(dataDir, restart), fake)
			assert.Equal(t, "built-in TLS (from worker certificate)", startLine(t, restarted))
			assert.Equal(t, 1, fake.calls(), "a restart does not enroll")
			assert.Equal(t, m.cred.cert.Certificate, restarted.cred.cert.Certificate)
		})
	}
}

// An invite from another CA than the one that signed the kept certificate
// enrolls with a new key and replaces the credential, as when a coordinator
// was set up again (Spec 108 §Worker Start).
func TestMembershipInviteFromNewCA(t *testing.T) {
	oldCA, newCA := newTestCA(t), newTestCA(t)
	fake := &fakeEnroll{results: map[string][]any{coordinatorA: {oldCA, newCA}}}
	dataDir := t.TempDir()
	first := join(t, workerConfig(dataDir, oldCA.invite(t)), fake)
	second := join(t, workerConfig(dataDir, newCA.invite(t)), fake)
	assert.Equal(t, 2, fake.calls())
	assert.True(t, newCA.cert.Equal(second.cred.ca))
	assert.NotEqual(t, first.cred.cert.Leaf.PublicKey, second.cred.cert.Leaf.PublicKey, "a new key")

	stored, err := newCredentialStore(dataDir).load()
	require.NoError(t, err)
	assert.True(t, newCA.cert.Equal(stored.ca))
}

// A reply that Enroll refuses stops the worker and leaves the kept
// credential as it was, so a restart without the invite still uses it.
func TestMembershipInvalidReplyKeepsCredential(t *testing.T) {
	oldCA, newCA := newTestCA(t), newTestCA(t)
	dataDir := t.TempDir()
	kept := join(t, workerConfig(dataDir, oldCA.invite(t)), enrolling(oldCA))

	refused := &fakeEnroll{results: map[string][]any{coordinatorA: {fmt.Errorf("%w: the certificate is for another key", coordinator.ErrInvalidEnrollReply)}}}
	m, err := open(t, workerConfig(dataDir, newCA.invite(t)), refused)
	require.NoError(t, err)
	require.EqualError(t, connect(t.Context(), t, m),
		"Coordinator at coordinator-a:50055 returned a certificate that does not match this worker's request\n  Check the coordinator address.")
	require.NoError(t, m.Close())

	restarted := join(t, workerConfig(dataDir, ""), nil)
	assert.Equal(t, kept.cred.cert.Certificate, restarted.cred.cert.Certificate)
}

// Enrollment tries each coordinator, retries while none is reachable or has
// built-in TLS, and stops with the message of Spec 108 §Messages when a
// coordinator refuses the invite or none has its CA.
func TestMembershipEnrollFailures(t *testing.T) {
	ca := newTestCA(t)
	refused := status.Error(codes.PermissionDenied, "the invite is invalid, expired, or already used")
	tests := []struct {
		name    string
		results map[string][]any
		wantErr string
		wantLog string
	}{
		{name: "SecondCoordinator", results: map[string][]any{coordinatorB: {ca}}},
		{
			name:    "UnreachableThenEnrolled",
			results: map[string][]any{coordinatorA: {status.Error(codes.Unavailable, "refused"), ca}},
			wantLog: "Cannot reach coordinator at coordinator-b:50055. Check that the coordinator accepts workers from other hosts, that port 50055 is open on the way, and that the address is right.",
		},
		{
			name:    "NoBuiltinTLSIsUnavailable",
			results: map[string][]any{coordinatorA: {coordinator.ErrNoBuiltinTLS, ca}, coordinatorB: {coordinator.ErrNoBuiltinTLS}},
			wantLog: "Coordinator at coordinator-b:50055 does not serve built-in TLS",
		},
		{
			name:    "OwnCertificatesAreUnavailable",
			results: map[string][]any{coordinatorA: {coordinator.ErrOwnCertificates, ca}, coordinatorB: {coordinator.ErrOwnCertificates}},
			wantLog: "Remove peer.cert_file and peer.key_file from the coordinator's config, so it serves built-in TLS.",
		},
		{
			// A coordinator's answer says more than a later address that
			// cannot be reached.
			name:    "AnswerBeforeUnreachable",
			results: map[string][]any{coordinatorA: {status.Error(codes.Internal, "failed to record the worker certificate"), ca}},
			wantLog: "Coordinator at coordinator-a:50055 could not enroll this worker: failed to record the worker certificate. Check the coordinator's log for the cause.",
		},
		{name: "Refused", results: map[string][]any{coordinatorA: {refused}}, wantErr: errInviteRefused.Error()},
		{
			name:    "OtherDagu",
			results: map[string][]any{coordinatorA: {coordinator.ErrInviteCAMismatch}},
			wantErr: "coordinator-a:50055 belongs to a different Dagu (its CA does not match the invite)\n  Check the coordinator address.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			ctx := logger.WithLogger(t.Context(), logger.NewLogger(logger.WithQuiet(), logger.WithWriter(&logs)))
			m, err := open(t, workerConfig(t.TempDir(), ca.invite(t)), &fakeEnroll{results: tt.results})
			require.NoError(t, err)
			err = connect(ctx, t, m)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, m.cred)
			assert.Contains(t, logs.String(), tt.wantLog)
		})
	}
}

// A worker whose settings fail at start does so before it enrolls, so a
// misconfigured worker never uses up a single-use invite.
func TestMembershipFailsBeforeEnrolling(t *testing.T) {
	ca := newTestCA(t)
	fake := enrolling(ca)
	cfg := workerConfig(t.TempDir(), ca.invite(t))
	cfg.Worker.Labels = map[string]string{"team": "data eng"}
	_, err := open(t, cfg, fake)
	require.ErrorContains(t, err, "team=data eng")
	assert.Zero(t, fake.calls())
}

// A worker that cannot write a data directory without a kept certificate
// enrolls with a key in memory and takes no lock, since nothing is kept to
// share. A single-use invite then fails with the change to make (Spec 108
// §Worker Files, §Configuration Conflicts).
func TestMembershipUnwritableDataDir(t *testing.T) {
	skipWithoutPermissions(t)
	ca := newTestCA(t)
	dataDir := t.TempDir()
	require.NoError(t, os.Chmod(dataDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0o700) })

	fake := enrolling(ca)
	m, err := open(t, workerConfig(dataDir, ca.invite(t)), fake)
	require.NoError(t, err)
	assert.Nil(t, m.lock)
	require.NoError(t, connect(t.Context(), t, m))
	assert.False(t, fake.requests[0].KeepsCertificate)
	assert.NotNil(t, m.cred)
	assert.NoDirExists(t, filepath.Join(dataDir, credentialDirName))

	singleUse := &fakeEnroll{results: map[string][]any{coordinatorA: {status.Error(codes.FailedPrecondition, "single use")}}}
	m, err = open(t, workerConfig(dataDir, ca.invite(t)), singleUse)
	require.NoError(t, err)
	require.EqualError(t, connect(t.Context(), t, m),
		"a single-use invite needs a writable data directory: use a reusable invite, or make "+dataDir+" writable")
}

// A kept certificate is used only under its lock. When the lock cannot be
// taken, as on a read-only volume, the worker stops, with or without its
// invite, instead of running beside another worker or enrolling again.
func TestMembershipKeptCertificateNeedsLock(t *testing.T) {
	skipWithoutPermissions(t)
	ca := newTestCA(t)
	invite := ca.invite(t)
	dataDir := t.TempDir()
	join(t, workerConfig(dataDir, invite), enrolling(ca))
	lockFile := filepath.Join(dataDir, credentialDirName, lockFileName)
	require.NoError(t, os.Chmod(lockFile, 0o400))

	for name, restart := range map[string]string{"SameInvite": invite, "NoInvite": ""} {
		t.Run(name, func(t *testing.T) {
			fake := enrolling(ca)
			_, err := open(t, workerConfig(dataDir, restart), fake)
			require.ErrorContains(t, err, "This worker cannot lock its certificate in its data directory "+dataDir)
			require.ErrorContains(t, err, "Make the data directory writable, so that one worker at a time uses the certificate.")
			assert.NotContains(t, err.Error(), lockFile)
			assert.Zero(t, fake.calls())
		})
	}
}

// A kept credential that cannot be read stops the worker, also when the
// worker could start on the peer token instead (Spec 108 §Worker Start).
func TestMembershipUnreadableCredential(t *testing.T) {
	skipWithoutPermissions(t)
	ca := newTestCA(t)
	invite := ca.invite(t)
	for name, denied := range map[string]func(dataDir string) string{
		"File":      credentialPath,
		"Directory": func(dataDir string) string { return filepath.Dir(credentialPath(dataDir)) },
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			join(t, workerConfig(dataDir, invite), enrolling(ca))
			path := denied(dataDir)
			require.NoError(t, os.Chmod(path, 0o000))
			t.Cleanup(func() { _ = os.Chmod(path, 0o700) })

			for _, restart := range []string{invite, ""} {
				fake := enrolling(ca)
				_, err := open(t, workerConfig(dataDir, restart), fake)
				require.ErrorContains(t, err, "This worker cannot read the certificate it keeps in its data directory "+dataDir)
				assert.NotContains(t, err.Error(), credentialPath(dataDir))
				assert.Zero(t, fake.calls())
			}
		})
	}
}

// A save that a stop interrupted leaves its partial file, which the next
// start removes, so no stray copy of the key stays behind.
func TestMembershipRemovesInterruptedSave(t *testing.T) {
	ca := newTestCA(t)
	dataDir := t.TempDir()
	join(t, workerConfig(dataDir, ca.invite(t)), enrolling(ca))
	leftover := credentialPath(dataDir) + ".tmp.123"
	require.NoError(t, os.WriteFile(leftover, []byte("partial"), 0o600))

	join(t, workerConfig(dataDir, ""), nil)
	assert.NoFileExists(t, leftover)
	assert.FileExists(t, credentialPath(dataDir))
}

// Settings that contradict worker.invite stop the worker at start (Spec 108
// §Configuration Conflicts).
func TestMembershipConflicts(t *testing.T) {
	ca := newTestCA(t)
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw}), 0o600))

	cfg := workerConfig(t.TempDir(), newTestCA(t).invite(t))
	cfg.Core.Peer.CACertFile = caFile
	_, err := open(t, cfg, nil)
	require.EqualError(t, err, "peer.ca_cert_file is not the CA that worker.invite names: remove peer.ca_cert_file or use an invite from this CA")

	cfg.Worker.Invite = ca.invite(t)
	_, err = open(t, cfg, nil)
	require.NoError(t, err)

	cfg.Worker.Invite = "dagu1.not-an-invite"
	_, err = open(t, cfg, nil)
	require.EqualError(t, err, errInviteRefused.Error())
}

// Workers on the peer token take no lock and behave as before Spec 108. A
// kept certificate under peer.insecure: true stays unused (Spec 108 §Worker
// Lock, §Worker Start).
func TestMembershipPeerToken(t *testing.T) {
	dataDir := t.TempDir()
	m, err := open(t, workerConfig(dataDir, ""), nil)
	require.NoError(t, err)
	assert.Nil(t, m.lock)
	assert.Equal(t, "h2c (from no TLS source)", startLine(t, m))
	require.NoError(t, m.Close())
	assert.NoDirExists(t, filepath.Join(dataDir, credentialDirName))

	ca := newTestCA(t)
	join(t, workerConfig(dataDir, ca.invite(t)), enrolling(ca))
	cfg := workerConfig(dataDir, "")
	cfg.Core.Peer.InsecureUnset = false
	m, err = open(t, cfg, nil)
	require.NoError(t, err)
	assert.Nil(t, m.lock)
	assert.Nil(t, m.cred)
	assert.Equal(t, "h2c (from peer.insecure: true)", startLine(t, m))
}

// A second worker on one data directory waits for the lock and fails while
// the first holds it, however long the first goes without doing anything,
// as when it is paused. It takes the lock once the first releases it (Spec
// 108 §Worker Lock).
func TestCertificateLock(t *testing.T) {
	dir := t.TempDir()
	cannotLock := func(err error) error { return err }
	first, err := lockCertificate(t.Context(), dir, testLockTiming, cannotLock)
	require.NoError(t, err)

	for range 2 {
		_, err = lockCertificate(t.Context(), dir, testLockTiming, cannotLock)
		require.EqualError(t, err, "Another worker uses this data directory\n  Give each worker its own data directory or volume.")
	}

	require.NoError(t, first.Unlock())
	second, err := lockCertificate(t.Context(), dir, testLockTiming, cannotLock)
	require.NoError(t, err)
	require.NoError(t, second.Unlock())
}
