// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
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
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
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

// invite returns a new invite for the CA.
func (ca testCA) invite(t *testing.T) string {
	t.Helper()
	secret := make([]byte, 32)
	_, err := rand.Read(secret)
	require.NoError(t, err)
	return "dagu1." + coordinator.CAPin(ca.cert) + "." + base64.RawURLEncoding.EncodeToString(secret)
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

// openMembership opens the membership of cfg, enrolling through fake.
func openMembership(t *testing.T, cfg *config.Config, fake *fakeEnroll) (*Membership, error) {
	t.Helper()
	m, err := OpenMembership(t.Context(), cfg, nil)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = m.Close() })
	m.enroll = fake.enroll
	m.retryPolicy = backoff.NewConstantBackoffPolicy(0)
	return m, nil
}

// join opens the membership of cfg and enrolls when it needs to.
func join(t *testing.T, cfg *config.Config, fake *fakeEnroll) *Membership {
	t.Helper()
	m, err := openMembership(t, cfg, fake)
	require.NoError(t, err)
	require.NoError(t, m.Enroll(t.Context(), "worker-1"))
	return m
}

func startLine(t *testing.T, m *Membership) string {
	t.Helper()
	line, err := m.ClientConfig().StartTransport()
	require.NoError(t, err)
	return line
}

// A worker with an invite enrolls with a new key, keeps its credential in
// its data directory, readable by its user only and without the invite, and
// then presents its certificate without the peer token. A restart with the
// same invite, or with none, keeps the certificate and does not enroll
// (Spec 108 §Worker Start, §Worker Files).
func TestMembershipEnrolls(t *testing.T) {
	ca := newTestCA(t)
	invite := ca.invite(t)
	fake := &fakeEnroll{results: map[string][]any{coordinatorA: {ca}}}
	dataDir := t.TempDir()
	cfg := workerConfig(dataDir, invite)
	cfg.Core.Peer.Token = "peer-token"

	m, err := openMembership(t, cfg, fake)
	require.NoError(t, err)
	assert.Equal(t, "built-in TLS (from worker.invite)", startLine(t, m))
	require.NoError(t, m.Enroll(t.Context(), "worker-1"))
	require.NoError(t, m.Close())

	require.Len(t, fake.requests, 1)
	req := fake.requests[0]
	assert.Equal(t, invite, req.Invite)
	assert.Equal(t, "worker-1", req.WorkerId)
	assert.True(t, req.KeepsCertificate)
	assert.Equal(t, "built-in TLS (from worker certificate)", startLine(t, m))
	assert.NotNil(t, m.ClientConfig().WorkerCertificate)
	assert.True(t, ca.cert.Equal(m.ClientConfig().WorkerCA))

	file := filepath.Join(dataDir, credentialDirName, credentialFileName)
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.NotContains(t, string(data), invite[strings.LastIndex(invite, ".")+1:])
	if runtime.GOOS != "windows" {
		info, err := os.Stat(file)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(credentialFileMode), info.Mode().Perm())
		info, err = os.Stat(filepath.Dir(file))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(credentialDirMode), info.Mode().Perm())
	}

	for name, restart := range map[string]string{"SameInvite": invite, "NoInvite": ""} {
		t.Run(name, func(t *testing.T) {
			restarted := join(t, workerConfig(dataDir, restart), fake)
			assert.Equal(t, "built-in TLS (from worker certificate)", startLine(t, restarted))
			assert.Equal(t, 1, fake.calls(), "a restart does not enroll")
			assert.Equal(t, m.ClientConfig().WorkerCertificate.Certificate, restarted.ClientConfig().WorkerCertificate.Certificate)
			require.NoError(t, restarted.Close())
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
	require.NoError(t, first.Close())

	second := join(t, workerConfig(dataDir, newCA.invite(t)), fake)
	require.NoError(t, second.Close())
	assert.Equal(t, 2, fake.calls())
	assert.True(t, newCA.cert.Equal(second.ClientConfig().WorkerCA))
	assert.NotEqual(t, first.cred.cert.Leaf.PublicKey, second.cred.cert.Leaf.PublicKey, "a new key")

	stored, err := newCredentialStore(dataDir).load()
	require.NoError(t, err)
	assert.True(t, newCA.cert.Equal(stored.ca))
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
			m, err := openMembership(t, workerConfig(t.TempDir(), ca.invite(t)), &fakeEnroll{results: tt.results})
			require.NoError(t, err)
			err = m.Enroll(ctx, "worker-1")
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, m.ClientConfig().WorkerCertificate)
			assert.Contains(t, logs.String(), tt.wantLog)
		})
	}
}

// A worker that cannot write its data directory holds its key in memory,
// takes no lock, and says that it is not a single-use invite's place when
// the coordinator refuses to enroll it (Spec 108 §Worker Files,
// §Configuration Conflicts).
func TestMembershipUnwritableDataDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory this user cannot write")
	}
	ca := newTestCA(t)
	dataDir := t.TempDir()
	require.NoError(t, os.Chmod(dataDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0o700) })

	fake := &fakeEnroll{results: map[string][]any{coordinatorA: {ca}}}
	m := join(t, workerConfig(dataDir, ca.invite(t)), fake)
	assert.False(t, fake.requests[0].KeepsCertificate)
	assert.NotNil(t, m.ClientConfig().WorkerCertificate)
	assert.Nil(t, m.Lost(), "no lock")
	assert.NoDirExists(t, filepath.Join(dataDir, credentialDirName))

	singleUse := &fakeEnroll{results: map[string][]any{coordinatorA: {status.Error(codes.FailedPrecondition, "single use")}}}
	m, err := openMembership(t, workerConfig(dataDir, ca.invite(t)), singleUse)
	require.NoError(t, err)
	require.EqualError(t, m.Enroll(t.Context(), "worker-1"),
		"a single-use invite needs a writable data directory: use a reusable invite, or make "+dataDir+" writable")
}

// Settings that contradict worker.invite stop the worker at start (Spec 108
// §Configuration Conflicts).
func TestMembershipConflicts(t *testing.T) {
	ca := newTestCA(t)
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw}), 0o600))

	cfg := workerConfig(t.TempDir(), newTestCA(t).invite(t))
	cfg.Core.Peer.CACertFile = caFile
	_, err := OpenMembership(t.Context(), cfg, nil)
	require.EqualError(t, err, "peer.ca_cert_file is not the CA that worker.invite names: remove peer.ca_cert_file or use an invite from this CA")

	cfg.Worker.Invite = ca.invite(t)
	m, err := OpenMembership(t.Context(), cfg, nil)
	require.NoError(t, err)
	require.NoError(t, m.Close())

	cfg.Worker.Invite = "dagu1.not-an-invite"
	_, err = OpenMembership(t.Context(), cfg, nil)
	require.EqualError(t, err, errInviteRefused.Error())
}

// Workers on the peer token take no lock and behave as before Spec 108. A
// kept certificate under peer.insecure: true stays unused (Spec 108 §Worker
// Lock, §Worker Start).
func TestMembershipPeerToken(t *testing.T) {
	dataDir := t.TempDir()
	m, err := OpenMembership(t.Context(), workerConfig(dataDir, ""), nil)
	require.NoError(t, err)
	assert.Nil(t, m.Lost())
	assert.Equal(t, "h2c (from no TLS source)", startLine(t, m))
	require.NoError(t, m.Close())
	assert.NoDirExists(t, filepath.Join(dataDir, credentialDirName))

	ca := newTestCA(t)
	require.NoError(t, join(t, workerConfig(dataDir, ca.invite(t)), &fakeEnroll{results: map[string][]any{coordinatorA: {ca}}}).Close())
	cfg := workerConfig(dataDir, "")
	cfg.Core.Peer.InsecureUnset = false
	m, err = OpenMembership(t.Context(), cfg, nil)
	require.NoError(t, err)
	assert.Nil(t, m.Lost())
	assert.Nil(t, m.ClientConfig().WorkerCertificate)
	assert.Equal(t, "h2c (from peer.insecure: true)", startLine(t, m))
}

// testLockTiming waits for a held lock briefly, so tests need not wait 30
// seconds.
var testLockTiming = lockTiming{staleAfter: time.Second, wait: 300 * time.Millisecond, refresh: 50 * time.Millisecond, retry: 10 * time.Millisecond}

// A second worker on one data directory waits for the lock and fails while a
// live worker keeps it, and takes it once it is released or its holder is
// gone (Spec 108 §Worker Lock).
func TestCertificateLock(t *testing.T) {
	dir := t.TempDir()
	first, err := lockCertificate(t.Context(), dir, testLockTiming)
	require.NoError(t, err)

	_, err = lockCertificate(t.Context(), dir, testLockTiming)
	require.EqualError(t, err, "Another worker uses this data directory\n  Give each worker its own data directory or volume.")

	require.NoError(t, first.release())
	second, err := lockCertificate(t.Context(), dir, testLockTiming)
	require.NoError(t, err)

	// A holder that stopped without releasing leaves a lock that goes
	// stale within the wait.
	second.stop()
	<-second.done
	gone := testLockTiming
	gone.wait = 2 * gone.staleAfter
	third, err := lockCertificate(t.Context(), dir, gone)
	require.NoError(t, err)
	require.NoError(t, third.release())
}

// A worker whose lock another worker took over learns it at its next
// refresh.
func TestCertificateLockLost(t *testing.T) {
	dir := t.TempDir()
	first, err := lockCertificate(t.Context(), dir, testLockTiming)
	require.NoError(t, err)
	require.NoError(t, dirlock.ForceUnlock(dir))
	second, err := lockCertificate(t.Context(), dir, testLockTiming)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.release() })

	select {
	case <-first.lost:
	case <-time.After(10 * time.Second):
		t.Fatal("the first worker did not learn that it lost the lock")
	}
	assert.NoError(t, first.release())
}
