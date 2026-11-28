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
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/test/peercert"
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
// signs the request's key, or an error. It records the requests. It answers
// the confirmation of a kept certificate as answers says, and accepts it at
// an address answers does not list.
type fakeEnroll struct {
	mu       sync.Mutex
	results  map[string][]any
	requests []*coordinatorv1.EnrollRequest
	answers  map[string]error
	confirms int
	// kubernetes runs the worker in Kubernetes, which reads the
	// termination message from terminationLog.
	kubernetes     bool
	terminationLog string
}

func (f *fakeEnroll) environment() coordinatorv1.HostEnvironment {
	if f.kubernetes {
		return coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES
	}
	return coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_HOST
}

func (f *fakeEnroll) confirm(_ context.Context, _ *coordinator.Config, address string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirms++
	return f.answers[address]
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
		lock:           testLockTiming,
		enroll:         fake.enroll,
		confirm:        fake.confirm,
		retryPolicy:    backoff.NewConstantBackoffPolicy(0),
		environment:    fake.environment,
		terminationLog: fake.terminationLog,
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
		{
			// An answer from one coordinator says the worker reached its
			// own Dagu, so another Dagu at a second address does not end
			// the attempt.
			name: "AnswerBeforeOtherDagu",
			results: map[string][]any{
				coordinatorA: {status.Error(codes.Internal, "failed to record the worker certificate"), ca},
				coordinatorB: {coordinator.ErrInviteCAMismatch},
			},
			wantLog: "Coordinator at coordinator-a:50055 could not enroll this worker: failed to record the worker certificate.",
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

// roundCoordinator is what one address gives in an enrollment round.
type roundCoordinator struct {
	kind string
	code codes.Code
}

var (
	roundEnrolled    = roundCoordinator{kind: "enrolled"}
	roundOtherDagu   = roundCoordinator{kind: "other Dagu"}
	roundUnreachable = roundCoordinator{kind: "unreachable"}
	roundH2COnly     = roundCoordinator{kind: "h2c only"}
	roundOwnTLS      = roundCoordinator{kind: "own TLS"}
	// roundSilent is a coordinator of the invite's CA that holds its reply
	// past the call's deadline.
	roundSilent = roundCoordinator{kind: "silent"}
)

// roundAnswer is a coordinator of the invite's CA that answers Enroll with
// code.
func roundAnswer(code codes.Code) roundCoordinator {
	return roundCoordinator{kind: "answer", code: code}
}

// answeringCoordinator answers Enroll with its code.
type answeringCoordinator struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer
	code codes.Code
	// release, when set, holds the reply until the test closes it, after
	// the round ended, so the caller's deadline always passes first.
	release <-chan struct{}
	// entered is closed when Enroll is first called.
	entered *closeOnce
}

// closeOnce is a channel that is closed at most once.
type closeOnce struct {
	once sync.Once
	ch   chan struct{}
}

func (c *closeOnce) close() { c.once.Do(func() { close(c.ch) }) }

func (c answeringCoordinator) Enroll(context.Context, *coordinatorv1.EnrollRequest) (*coordinatorv1.EnrollResponse, error) {
	if c.entered != nil {
		c.entered.close()
	}
	if c.release != nil {
		<-c.release
		return nil, status.Error(codes.Unavailable, "released")
	}
	return nil, status.Error(c.code, "try again")
}

// roundServers serve the coordinators of an enrollment round over real
// connections: ones of the invite's CA, of another CA, with own
// certificates, and on h2c only.
type roundServers struct {
	t      *testing.T
	invite string
	ours   credentials.TransportCredentials
	other  credentials.TransportCredentials
	ca     testCA
	// entered tells, by address, when a silent coordinator got its call.
	entered map[string]*closeOnce
}

func newRoundServers(t *testing.T) roundServers {
	t.Helper()
	ours, oursCert := peerCredentials(t)
	other, _ := peerCredentials(t)
	secret := make([]byte, 32)
	_, err := rand.Read(secret)
	require.NoError(t, err)
	pin := sha256.Sum256(oursCert.Raw)
	return roundServers{
		t:       t,
		invite:  "dagu1." + base64.RawURLEncoding.EncodeToString(pin[:]) + "." + base64.RawURLEncoding.EncodeToString(secret),
		ours:    ours,
		other:   other,
		ca:      newTestCA(t),
		entered: map[string]*closeOnce{},
	}
}

// peerCredentials returns the credentials of a coordinator with a new peer
// CA, and the CA certificate.
func peerCredentials(t *testing.T) (credentials.TransportCredentials, *x509.Certificate) {
	t.Helper()
	dir := t.TempDir()
	ca, err := coordinator.LoadOrCreatePeerCA(filepath.Join(dir, "ca.pem"))
	require.NoError(t, err)
	creds, err := coordinator.NewPeerTransportCredentials(t.Context(), ca)
	require.NoError(t, err)
	published := filepath.Join(dir, "ca.crt")
	_, err = ca.PublishCertificate(published)
	require.NoError(t, err)
	data, err := fileutil.ReadFile(published)
	require.NoError(t, err)
	block, _ := pem.Decode(data)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return creds, cert
}

// address serves c and returns its address.
func (r roundServers) address(c roundCoordinator) string {
	t := r.t
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	if c.kind == "unreachable" {
		require.NoError(t, listener.Close())
		return listener.Addr().String()
	}
	var opts []grpc.ServerOption
	switch c.kind {
	case "answer", "silent":
		opts = append(opts, grpc.Creds(r.ours))
	case "other Dagu":
		opts = append(opts, grpc.Creds(r.other))
	case "own TLS":
		certFile, keyFile := peercert.Write(t, t.TempDir(), peercert.Options{Name: "server", DNSNames: []string{"coordinator.test"}})
		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		require.NoError(t, err)
		opts = append(opts, grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13})))
	}
	server := grpc.NewServer(opts...)
	service := answeringCoordinator{code: c.code}
	var release chan struct{}
	if c.kind == "silent" {
		release = make(chan struct{})
		service.release = release
		service.entered = &closeOnce{ch: make(chan struct{})}
		r.entered[listener.Addr().String()] = service.entered
	}
	coordinatorv1.RegisterCoordinatorServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	if release != nil {
		// Cleanups run last first, so the reply is released before the
		// server stops.
		t.Cleanup(func() { close(release) })
	}
	return listener.Addr().String()
}

// One round of enrollment ends by the precedence that enrollRound states,
// whichever coordinator gives which reply. Each reply but an enrollment
// comes from a real coordinator over a real connection.
func TestEnrollRoundPrecedence(t *testing.T) {
	servers := newRoundServers(t)
	const (
		answerA     = "Coordinator at %[1]s could not enroll this worker: try again"
		answerB     = "Coordinator at %[2]s could not enroll this worker: try again"
		otherDaguA  = "%[1]s belongs to a different Dagu"
		otherDaguB  = "%[2]s belongs to a different Dagu"
		noTLSA      = "Coordinator at %[1]s does not serve built-in TLS"
		noTLSB      = "Coordinator at %[2]s does not serve built-in TLS"
		ownA        = "Coordinator at %[1]s serves TLS with its own certificates"
		unreachable = "Cannot reach coordinator at %[2]s"
	)
	for _, tc := range []struct {
		name      string
		a, b      roundCoordinator
		readOnly  bool
		wantStop  string
		wantRetry string
	}{
		{name: "EnrolledAfterOtherDagu", a: roundOtherDagu, b: roundEnrolled},
		{name: "EnrolledAfterUnreachable", a: roundUnreachable, b: roundEnrolled},
		{name: "RefusedEndsAtOnce", a: roundAnswer(codes.PermissionDenied), b: roundEnrolled, wantStop: "The invite is invalid, expired, or already used"},
		{name: "SingleUseWhileReadOnlyEndsAtOnce", a: roundAnswer(codes.FailedPrecondition), b: roundEnrolled, readOnly: true, wantStop: "a single-use invite needs a writable data directory"},
		{name: "FailedPreconditionWhileWritableIsAnswer", a: roundAnswer(codes.FailedPrecondition), b: roundOtherDagu, wantRetry: answerA},
		{name: "UnavailableAnswerBeforeOtherDagu", a: roundAnswer(codes.Unavailable), b: roundOtherDagu, wantRetry: answerA},
		{name: "OtherDaguBeforeUnavailableAnswer", a: roundOtherDagu, b: roundAnswer(codes.Unavailable), wantRetry: answerB},
		{name: "InternalAnswerBeforeOtherDagu", a: roundAnswer(codes.Internal), b: roundOtherDagu, wantRetry: answerA},
		{name: "AnswerBeforeUnreachable", a: roundAnswer(codes.Internal), b: roundUnreachable, wantRetry: answerA},
		{name: "AnswerBeforeH2COnly", a: roundAnswer(codes.Internal), b: roundH2COnly, wantRetry: answerA},
		{name: "OlderCoordinatorIsNoBuiltinTLS", a: roundAnswer(codes.Unimplemented), b: roundUnreachable, wantRetry: noTLSA},
		{name: "OtherDaguBeforeUnreachable", a: roundUnreachable, b: roundOtherDagu, wantStop: otherDaguB},
		// A reply the worker gave up waiting for is no answer, nor is a
		// coordinator's word that the deadline passed.
		{name: "OtherDaguBeforeSilent", a: roundSilent, b: roundOtherDagu, wantStop: otherDaguB},
		{name: "OtherDaguBeforeDeadlineAnswer", a: roundAnswer(codes.DeadlineExceeded), b: roundOtherDagu, wantStop: otherDaguB},
		{name: "SilentIsUnreachable", a: roundSilent, b: roundUnreachable, wantRetry: unreachable},
		{name: "OtherDaguBeforeH2COnly", a: roundOtherDagu, b: roundH2COnly, wantStop: otherDaguA},
		{name: "H2COnlyBeforeUnreachable", a: roundH2COnly, b: roundUnreachable, wantRetry: noTLSA},
		{name: "OwnTLSBeforeUnreachable", a: roundOwnTLS, b: roundUnreachable, wantRetry: ownA},
		{name: "LastH2COnly", a: roundH2COnly, b: roundH2COnly, wantRetry: noTLSB},
		{name: "LastUnreachable", a: roundUnreachable, b: roundUnreachable, wantRetry: unreachable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addrA, addrB := servers.address(tc.a), servers.address(tc.b)
			cfg := workerConfig(t.TempDir(), servers.invite)
			cfg.Worker.Coordinators = []string{addrA, addrB}
			m, err := open(t, cfg, nil)
			require.NoError(t, err)
			// A silent coordinator holds each call this long.
			m.config.DialTimeout = time.Second
			fake := &fakeEnroll{results: map[string][]any{addrA: {servers.ca}, addrB: {servers.ca}}}
			m.opts.enroll = func(ctx context.Context, config *coordinator.Config, address string, req *coordinatorv1.EnrollRequest) (*coordinatorv1.EnrollResponse, error) {
				if (address == addrA && tc.a == roundEnrolled) || (address == addrB && tc.b == roundEnrolled) {
					return fake.enroll(ctx, config, address, req)
				}
				return coordinator.Enroll(ctx, config, address, req)
			}

			addresses := strings.NewReplacer("%[1]s", addrA, "%[2]s", addrB)
			resp, retry, err := m.enrollRound(t.Context(), &coordinatorv1.EnrollRequest{Csr: testCSR(t), KeepsCertificate: !tc.readOnly})
			switch {
			case tc.wantStop != "":
				require.ErrorContains(t, err, addresses.Replace(tc.wantStop))
			case tc.wantRetry != "":
				require.NoError(t, err)
				assert.Nil(t, resp)
				assert.Contains(t, retry.logLine(), addresses.Replace(tc.wantRetry))
			default:
				require.NoError(t, err)
				assert.NotNil(t, resp)
			}
			// A silent coordinator's row says something only when the
			// call reached it.
			for _, entered := range []*closeOnce{servers.entered[addrA], servers.entered[addrB]} {
				if entered == nil {
					continue
				}
				select {
				case <-entered.ch:
				default:
					t.Fatal("the silent coordinator got no call")
				}
			}
		})
	}
}

// testCSR returns a certificate signing request for a new key.
func testCSR(t *testing.T) []byte {
	t.Helper()
	_, csr, err := newWorkerKey()
	require.NoError(t, err)
	return csr
}

// A malformed invite stops the worker before it dials anything.
func TestMembershipMalformedInvite(t *testing.T) {
	ca := newTestCA(t)
	valid := ca.invite(t)
	// The pin has its length but is no base64url digest.
	malformed := "dagu1." + strings.Repeat("!", 43) + valid[strings.LastIndex(valid, "."):]
	fake := enrolling(ca)
	_, err := open(t, workerConfig(t.TempDir(), malformed), fake)
	require.EqualError(t, err, errInviteRefused.Error())
	assert.Zero(t, fake.calls())
}

// A failure to restrict the credential directory names no path inside the
// data directory in what the worker prints.
func TestReasonHidesPaths(t *testing.T) {
	for _, err := range []error{
		&fs.PathError{Op: "restrict to owner", Path: "/data/worker", Err: errors.New("access is denied")},
		fmt.Errorf("keep it: %w", &os.LinkError{Op: "rename", Old: "/data/worker/a", New: "/data/worker/b", Err: errors.New("access is denied")}),
	} {
		assert.Equal(t, "access is denied", reason(err))
	}
}

// When the system refuses to restrict the credential directory, as an
// access list can on Windows, a restart says so with the system's cause,
// naming only the data directory as configured.
func TestMembershipRestrictFailure(t *testing.T) {
	dataDir := t.TempDir()
	m, err := open(t, workerConfig(dataDir, ""), nil)
	require.NoError(t, err)
	// The error MkdirPrivate returns when the system refuses the
	// restriction.
	refused := &fs.PathError{Op: "restrict to owner", Path: filepath.Join(dataDir, credentialDirName), Err: syscall.EACCES}

	require.EqualError(t, m.errCannotLock(refused), "This worker cannot lock its certificate: "+syscall.EACCES.Error()+
		"\n  Make "+dataDir+" writable, so that one worker at a time uses the certificate.")
}

// A worker that must replace a certificate from another CA, and could not
// keep the new one, stops before it enrolls, so a restart without the
// invite cannot fall back to the old one.
func TestMembershipReplacementNeedsWritable(t *testing.T) {
	oldCA, newCA := newTestCA(t), newTestCA(t)
	dataDir := t.TempDir()
	join(t, workerConfig(dataDir, oldCA.invite(t)), enrolling(oldCA))
	// A directory in place of the write probe fails it, while the lock
	// still works.
	require.NoError(t, os.MkdirAll(filepath.Join(dataDir, credentialDirName, writeProbeName, "taken"), 0o700))

	fake := enrolling(newCA)
	_, err := open(t, workerConfig(dataDir, newCA.invite(t)), fake)
	require.EqualError(t, err, "This worker cannot replace the certificate it enrolled for with one from the invite's CA\n  Make "+dataDir+" writable, then start the worker again.")
	assert.Zero(t, fake.calls())
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
			require.ErrorContains(t, err, "This worker cannot lock its certificate: ")
			require.ErrorContains(t, err, "Make "+dataDir+" writable, so that one worker at a time uses the certificate.")
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
				require.ErrorContains(t, err, "This worker cannot read the certificate it enrolled for: ")
				require.ErrorContains(t, err, "Let this user read "+dataDir+", or start the worker with a new data directory and a new invite.")
				assert.NotContains(t, err.Error(), credentialPath(dataDir))
				assert.Zero(t, fake.calls())
			}
		})
	}
}

// A worker that enrolls but cannot keep its certificate says so, naming
// the data directory as configured and no file inside it.
func TestMembershipSaveFailure(t *testing.T) {
	ca := newTestCA(t)
	dataDir := t.TempDir()
	m, err := open(t, workerConfig(dataDir, ca.invite(t)), enrolling(ca))
	require.NoError(t, err)
	// A directory in place of the credential fails the save's rename.
	require.NoError(t, os.MkdirAll(filepath.Join(credentialPath(dataDir), "taken"), 0o700))

	err = connect(t.Context(), t, m)
	require.ErrorContains(t, err, "This worker enrolled but cannot keep its certificate: ")
	require.ErrorContains(t, err, "Make "+dataDir+" writable.")
	assert.NotContains(t, err.Error(), filepath.Join(dataDir, credentialDirName))
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

const (
	// removedMessage is the failure of a removed worker outside Kubernetes.
	removedMessage = "This worker was removed from the coordinator\n  Get a new command from \"Add worker\" to add it again."
	// removedKubernetesLine is the termination message of a removed worker
	// in Kubernetes.
	removedKubernetesLine = `This worker was removed from the coordinator. Scale its Deployment down to stop it, or get a new command from "Add worker" to add it again.`
)

// removedAnswer is a coordinator's refusal of a revoked certificate.
var removedAnswer = status.Error(codes.Unauthenticated, "this worker was removed from the coordinator")

// A worker whose certificate a coordinator refused while it ran keeps the
// refusal with its certificate, so a restart with the same invite, or with
// none, exits with the same message without calling a coordinator. A
// different invite enrolls again with a new key (Spec 108 §Removed Worker,
// §Worker Start).
func TestMembershipRemoved(t *testing.T) {
	ca := newTestCA(t)
	invite := ca.invite(t)
	dataDir := t.TempDir()
	fake := &fakeEnroll{results: map[string][]any{coordinatorA: {ca, ca}}}
	m, err := open(t, workerConfig(dataDir, invite), fake)
	require.NoError(t, err)
	require.NoError(t, connect(t.Context(), t, m))
	assert.EqualError(t, m.Removed(t.Context()), removedMessage)
	removedKey := m.cred.cert.Leaf.PublicKey
	require.NoError(t, m.Close())

	for name, restart := range map[string]string{"SameInvite": invite, "NoInvite": ""} {
		t.Run(name, func(t *testing.T) {
			_, err := open(t, workerConfig(dataDir, restart), fake)
			assert.EqualError(t, err, removedMessage)
		})
	}
	assert.Equal(t, 1, fake.calls(), "a removed worker does not enroll with its invite")
	assert.Zero(t, fake.confirms, "a removed worker calls no coordinator")

	rejoined := join(t, workerConfig(dataDir, ca.invite(t)), fake)
	assert.Equal(t, 2, fake.calls())
	assert.NotEqual(t, removedKey, rejoined.cred.cert.Leaf.PublicKey, "a new key")
	restarted := join(t, workerConfig(dataDir, ""), fake)
	assert.Equal(t, rejoined.cred.cert.Certificate, restarted.cred.cert.Certificate)
}

// A kept certificate is confirmed before the worker starts. A refusal as
// revoked from any coordinator enrolls the worker again when it has another
// invite than the one it enrolled with, and otherwise stops it with the
// message, which later starts repeat without calling a coordinator (Spec 108
// §Worker Start).
func TestMembershipRemovedAtStart(t *testing.T) {
	ca := newTestCA(t)
	dataDir := t.TempDir()
	fake := &fakeEnroll{results: map[string][]any{coordinatorA: {ca, ca}}}
	enrolled := join(t, workerConfig(dataDir, ca.invite(t)), fake)
	fake.answers = map[string]error{
		coordinatorA: status.Error(codes.Unavailable, "connection refused"),
		coordinatorB: fmt.Errorf("heartbeat failed: %w", removedAnswer),
	}

	other := ca.invite(t)
	rejoined := join(t, workerConfig(dataDir, other), fake)
	assert.Equal(t, 2, fake.calls(), "another invite enrolls again")
	assert.NotEqual(t, enrolled.cred.cert.Leaf.PublicKey, rejoined.cred.cert.Leaf.PublicKey, "a new key")

	m, err := open(t, workerConfig(dataDir, other), fake)
	require.NoError(t, err)
	assert.EqualError(t, connect(t.Context(), t, m), removedMessage)
	require.NoError(t, m.Close())
	confirms := fake.confirms

	_, err = open(t, workerConfig(dataDir, other), fake)
	assert.EqualError(t, err, removedMessage)
	assert.Equal(t, confirms, fake.confirms, "the restart calls no coordinator")
	assert.Equal(t, 2, fake.calls())
}

// A worker with a certificate stops when every coordinator that answers
// belongs to another Dagu. It starts when one accepts its certificate, and
// while none answers, since its calls retry (Spec 108 §Errors).
func TestMembershipOtherDagu(t *testing.T) {
	unavailable := status.Error(codes.Unavailable, "connection refused")
	tests := []struct {
		name    string
		answers map[string]error
		// sameInvite restarts the worker with the invite it enrolled with.
		sameInvite bool
		wantErr    string
	}{
		{
			name:    "OtherDagu",
			answers: map[string]error{coordinatorA: unavailable, coordinatorB: coordinator.ErrOtherDagu},
			wantErr: "coordinator-b:50055 belongs to a different Dagu than the one this worker joined\n  Get a new command from \"Add worker\" to join it.",
		},
		{
			name:       "OtherDaguWithInvite",
			answers:    map[string]error{coordinatorA: unavailable, coordinatorB: coordinator.ErrOtherDagu},
			sameInvite: true,
			wantErr:    "coordinator-b:50055 belongs to a different Dagu (its CA does not match the invite)\n  Check the coordinator address.",
		},
		{name: "OneAccepts", answers: map[string]error{coordinatorA: coordinator.ErrOtherDagu}},
		{name: "NoneAnswers", answers: map[string]error{coordinatorA: unavailable, coordinatorB: unavailable}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ca := newTestCA(t)
			dataDir := t.TempDir()
			fake := enrolling(ca)
			invite := ca.invite(t)
			join(t, workerConfig(dataDir, invite), fake)
			fake.answers = tc.answers

			restart := ""
			if tc.sameInvite {
				restart = invite
			}
			m, err := open(t, workerConfig(dataDir, restart), fake)
			require.NoError(t, err)
			err = connect(t.Context(), t, m)
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tc.wantErr)
		})
	}
}

// In Kubernetes, a removed worker also writes its message to the
// termination log, at each start, with the change that stops the pod from
// restarting. Elsewhere it writes nothing (Spec 108 §Removed Worker).
func TestMembershipRemovedTerminationMessage(t *testing.T) {
	for name, kubernetes := range map[string]bool{"Kubernetes": true, "Host": false} {
		t.Run(name, func(t *testing.T) {
			ca := newTestCA(t)
			dataDir := t.TempDir()
			log := filepath.Join(t.TempDir(), "termination-log")
			require.NoError(t, os.WriteFile(log, nil, 0o600))
			fake := enrolling(ca)
			fake.kubernetes, fake.terminationLog = kubernetes, log
			m, err := open(t, workerConfig(dataDir, ca.invite(t)), fake)
			require.NoError(t, err)
			require.NoError(t, connect(t.Context(), t, m))
			err = m.Removed(t.Context())
			require.NoError(t, m.Close())

			data, readErr := os.ReadFile(log)
			require.NoError(t, readErr)
			if !kubernetes {
				assert.EqualError(t, err, removedMessage)
				assert.Empty(t, data)
				return
			}
			assert.EqualError(t, err, strings.Replace(removedKubernetesLine, ". ", "\n  ", 1))
			assert.Equal(t, removedKubernetesLine, string(data))

			require.NoError(t, os.WriteFile(log, nil, 0o600))
			_, err = open(t, workerConfig(dataDir, ""), fake)
			require.Error(t, err)
			data, readErr = os.ReadFile(log)
			require.NoError(t, readErr)
			assert.Equal(t, removedKubernetesLine, string(data), "a restart writes it again")
		})
	}

	// A missing termination log stays missing.
	missing := filepath.Join(t.TempDir(), "termination-log")
	writeTerminationMessage(t.Context(), missing, removedKubernetesLine)
	assert.NoFileExists(t, missing)
}

// dagu worker --check dials as the worker starts: with its kept
// certificate, also one a coordinator refused as revoked, or else with its
// invite. It takes no lock, so it runs beside the worker (Spec 108
// §dagu worker --check).
func TestCoordinatorClientConfig(t *testing.T) {
	ca := newTestCA(t)
	invite := ca.invite(t)
	dataDir := t.TempDir()
	checkLine := func(cfg *config.Config) string {
		t.Helper()
		clientConfig, err := CoordinatorClientConfig(cfg, nil)
		require.NoError(t, err)
		line, err := clientConfig.StartTransport()
		require.NoError(t, err)
		return line
	}
	assert.Equal(t, "built-in TLS (from worker.invite)", checkLine(workerConfig(dataDir, invite)))

	m, err := open(t, workerConfig(dataDir, invite), enrolling(ca))
	require.NoError(t, err)
	require.NoError(t, connect(t.Context(), t, m))
	for _, restart := range []string{invite, ""} {
		assert.Equal(t, "built-in TLS (from worker certificate)", checkLine(workerConfig(dataDir, restart)))
	}
	// peer.insecure: true leaves the kept certificate unused, as at start.
	insecure := workerConfig(dataDir, "")
	insecure.Core.Peer.InsecureUnset = false
	assert.Equal(t, "h2c (from peer.insecure: true)", checkLine(insecure))

	assert.NoError(t, RecordedRemoval(workerConfig(dataDir, invite)))

	require.Error(t, m.Removed(t.Context()))
	assert.Equal(t, "built-in TLS (from worker certificate)", checkLine(workerConfig(dataDir, invite)))
	assert.Equal(t, "built-in TLS (from worker.invite)", checkLine(workerConfig(dataDir, ca.invite(t))), "another invite enrolls again")
	// The check reports the removal that the start exits on, whatever a
	// coordinator answers.
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	for _, restart := range []string{invite, ""} {
		assert.EqualError(t, RecordedRemoval(workerConfig(dataDir, restart)), removedMessage)
	}
	assert.NoError(t, RecordedRemoval(workerConfig(dataDir, ca.invite(t))))
}

// The check refuses the settings and the kept credential that the start
// refuses, with the same messages, and names no file of the credential
// (Spec 108 §Configuration Conflicts, §Worker Files).
func TestCoordinatorClientConfigRefusals(t *testing.T) {
	ca := newTestCA(t)
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw}), 0o600))
	cfg := workerConfig(t.TempDir(), newTestCA(t).invite(t))
	cfg.Core.Peer.CACertFile = caFile
	_, err := CoordinatorClientConfig(cfg, nil)
	require.EqualError(t, err, "peer.ca_cert_file is not the CA that worker.invite names: remove peer.ca_cert_file or use an invite from this CA")

	dataDir := t.TempDir()
	join(t, workerConfig(dataDir, ca.invite(t)), enrolling(ca))
	require.NoError(t, os.WriteFile(credentialPath(dataDir), []byte("not a credential"), 0o600))
	for _, invite := range []string{ca.invite(t), ""} {
		_, err = CoordinatorClientConfig(workerConfig(dataDir, invite), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "This worker cannot read the certificate it keeps in its data directory "+dataDir+": ")
		assert.NotContains(t, err.Error(), filepath.Join(dataDir, credentialDirName))
	}
}

// A refusal as revoked on any call of the worker's client is recorded before
// the worker stops, so a worker that a signal or a kill ends right after it
// still exits at its next start (Spec 108 §Removed Worker).
func TestMembershipRevoked(t *testing.T) {
	ca := newTestCA(t)
	invite := ca.invite(t)
	dataDir := t.TempDir()
	fake := enrolling(ca)
	join(t, workerConfig(dataDir, invite), fake)

	m, err := open(t, workerConfig(dataDir, invite), fake)
	require.NoError(t, err)
	require.NoError(t, connect(t.Context(), t, m))
	select {
	case <-m.Revoked():
		t.Fatal("revoked before any refusal")
	default:
	}
	m.revoke(t.Context())
	<-m.Revoked()
	require.NoError(t, m.Close())

	_, err = open(t, workerConfig(dataDir, invite), fake)
	assert.EqualError(t, err, removedMessage)
	assert.EqualError(t, m.Removed(t.Context()), removedMessage, "Removed after the refusal gives the same failure")
}
