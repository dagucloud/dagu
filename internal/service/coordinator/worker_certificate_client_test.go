// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// workerCertConfig is the configuration of a worker that holds cert and key
// from the peer CA of server.
func workerCertConfig(server peerTLSServer, key *ecdsa.PrivateKey, cert *x509.Certificate) *coordinator.Config {
	config := coordinator.DefaultConfig()
	config.DialTimeout = 5 * time.Second
	return config.WithWorkerCertificate(tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}, server.caCert)
}

// workerClient is a worker client and the refusals as revoked it reported.
type workerClient struct {
	client    coordinator.Client
	otherDagu string
	err       error
	refusals  *atomic.Int32
}

// newWorkerClient returns the client of the worker with config for the
// coordinators at addresses.
func newWorkerClient(t *testing.T, config *coordinator.Config, addresses ...string) workerClient {
	t.Helper()
	refusals := &atomic.Int32{}
	client, otherDagu, err := coordinator.NewWorkerClient(t.Context(), addresses, config, func() { refusals.Add(1) })
	if client != nil {
		t.Cleanup(func() { _ = client.Cleanup(context.Background()) })
	}
	return workerClient{client: client, otherDagu: otherDagu, err: err, refusals: refusals}
}

// startAnsweringServer starts a coordinator on built-in TLS of the peer CA
// of server whose worker list answers every call with code.
func startAnsweringServer(t *testing.T, server peerTLSServer, code codes.Code) string {
	t.Helper()
	handler := answeringHandler{Handler: coordinator.NewHandler(coordinator.HandlerConfig{}), code: code}
	answering, addr := startMockServerWithHealth(t, handler, health.NewServer(), grpc.Creds(server.shared.creds))
	t.Cleanup(answering.Stop)
	return addr
}

// answeringHandler answers the worker list with its code.
type answeringHandler struct {
	*coordinator.Handler
	code codes.Code
}

func (h answeringHandler) GetWorkers(context.Context, *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
	return nil, status.Error(h.code, "the worker list failed")
}

// startOwnCertServer starts a coordinator that serves TLS with its own
// certificate for coordinatorCertName, signed by the peer CA of server but
// not one that a coordinator issues itself for built-in TLS.
func startOwnCertServer(t *testing.T, server peerTLSServer) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "own"},
		DNSNames:     []string{coordinatorCertName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, server.caCert, key.Public(), server.caKey)
	require.NoError(t, err)
	own, addr := startMockServerWithHealth(t, coordinator.NewHandler(coordinator.HandlerConfig{}), health.NewServer(),
		grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{{Certificate: [][]byte{der, server.caCert.Raw}, PrivateKey: key}},
			ClientAuth:   tls.RequestClientCert,
			MinVersion:   tls.VersionTLS13,
		})))
	t.Cleanup(own.Stop)
	return addr
}

// A worker that holds a certificate gets its client once a coordinator
// accepts the certificate, or once one of this Dagu answers at all, even
// with an error. A refusal as revoked fails it and is reported. A worker
// whose answering coordinators all belong to another Dagu is told the first
// of them (Spec 108 §Worker Start, §Messages).
func TestNewWorkerClient(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	other := startPeerTLSServer(t, certHosts)
	unreachable := fmt.Sprintf("127.0.0.1:%d", closedPort(t))
	key, cert, _ := server.enrolled(t)
	config := workerCertConfig(server, key, cert)

	accepted := newWorkerClient(t, config, unreachable, server.addr)
	require.NoError(t, accepted.err)
	assert.NotNil(t, accepted.client)

	otherDagu := newWorkerClient(t, config, unreachable, other.addr)
	assert.ErrorIs(t, otherDagu.err, coordinator.ErrInviteCAMismatch)
	assert.Equal(t, other.addr, otherDagu.otherDagu)

	// An answer after the handshake with the certificate is of this Dagu,
	// whatever its code.
	for _, code := range []codes.Code{codes.Unavailable, codes.Internal, codes.Unimplemented, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			answered := newWorkerClient(t, config, startAnsweringServer(t, server, code), other.addr)
			require.NoError(t, answered.err)
			assert.NotNil(t, answered.client)
		})
	}

	none := newWorkerClient(t, config, unreachable)
	require.NoError(t, none.err, "while none answers, the client retries")
	assert.NotNil(t, none.client)

	server.remove(t, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
	refused := newWorkerClient(t, config, server.addr)
	require.Error(t, refused.err)
	assert.Nil(t, refused.client)
	assert.Equal(t, int32(1), refused.refusals.Load())

	// The client got before the removal reports it on its next call.
	_, err := accepted.client.Heartbeat(t.Context(), &coordinatorv1.HeartbeatRequest{WorkerId: "worker-1"})
	require.Error(t, err)
	assert.Equal(t, int32(1), accepted.refusals.Load())
}

// Every call of the client reports a refusal as revoked, not only the
// heartbeat, so a coordinator that has not reread the revoked set yet and
// still accepts heartbeats cannot keep a removed worker running (Spec 108
// §Removed Worker).
func TestClientReportsRemoval(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, _ := server.enrolled(t)
	worker := newWorkerClient(t, workerCertConfig(server, key, cert), server.addr)
	require.NoError(t, worker.err)
	assert.Zero(t, worker.refusals.Load())

	server.remove(t, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
	_, err := worker.client.Poll(t.Context(), backoff.NewConstantBackoffPolicy(time.Second), &coordinatorv1.PollRequest{WorkerId: "worker-1", PollerId: "poller-1"})
	require.Error(t, err)
	assert.Equal(t, int32(1), worker.refusals.Load())
}

// A log stream that the coordinator refuses reports the refusal, also when
// the log writers drop the stream after a failed send without a receive
// (Spec 108 §Removed Worker).
func TestStreamRefusalReportsRemoval(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, _ := server.enrolled(t)
	worker := newWorkerClient(t, workerCertConfig(server, key, cert), server.addr)
	require.NoError(t, worker.err)
	server.remove(t, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stream, err := worker.client.StreamLogs(ctx)
	require.NoError(t, err)
	// Sends succeed until the refusal arrives, as they are buffered.
	var sendErr error
	for sendErr == nil && ctx.Err() == nil {
		sendErr = stream.Send(&coordinatorv1.LogChunk{DagRunId: "run-1"})
	}
	require.ErrorIs(t, sendErr, io.EOF)
	assert.Equal(t, int32(1), worker.refusals.Load(), "the refusal is reported without a receive")
	_, err = stream.CloseAndRecv()
	require.Error(t, err)
	assert.Equal(t, int32(1), worker.refusals.Load())
}

// The client's connections and the check refuse a coordinator that serves its
// own certificate signed by the worker's CA, since it is no coordinator
// certificate of built-in TLS (Spec 108 §Worker Side).
func TestOwnCertificateOnWorkerCA(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, _ := server.enrolled(t)
	config := workerCertConfig(server, key, cert)
	addr := startOwnCertServer(t, server)

	worker := newWorkerClient(t, config, addr)
	require.NoError(t, worker.err, "no coordinator answered")
	_, err := worker.client.GetWorkers(t.Context())
	assert.Error(t, err)

	result := coordinator.Check(t.Context(), config, checkMember(t, addr))
	assert.False(t, result.Passed())
	require.NotNil(t, result.TLS)
	assert.EqualError(t, result.TLS.Err, "the coordinator serves TLS with its own certificates, not built-in TLS")
}

// The check of a worker with a certificate names the certificate and its
// invite, and says whether the coordinator accepts it (Spec 108
// §dagu worker --check).
func TestCheckWorkerCertificate(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	key, cert, invite := server.enrolled(t)
	config := workerCertConfig(server, key, cert)

	result := coordinator.Check(t.Context(), config, checkMember(t, server.addr))
	require.True(t, result.Passed(), "%+v", result)
	assert.Equal(t, "built-in TLS (from worker certificate)", result.Transport)
	assert.True(t, result.TLS.Verified)
	assert.True(t, result.Credential.CertSent)
	credential, answer := result.Describe()
	assert.Equal(t, "worker certificate "+cert.Subject.CommonName+", from invite "+invite.Record.Id+"; no peer token", credential)
	assert.Equal(t, "serving, and it accepted the worker certificate", answer)
	assert.False(t, result.Removed())

	// A coordinator of another Dagu is named as such, not as an untrusted CA.
	other := startPeerTLSServer(t, certHosts)
	result = coordinator.Check(t.Context(), config, checkMember(t, other.addr))
	require.NotNil(t, result.TLS)
	assert.ErrorIs(t, result.TLS.Err, coordinator.ErrInviteCAMismatch)

	server.remove(t, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH)
	result = coordinator.Check(t.Context(), config, checkMember(t, server.addr))
	assert.False(t, result.Passed())
	assert.True(t, result.Removed())
	_, answer = result.Describe()
	assert.True(t, strings.HasPrefix(answer, "This worker was removed from the coordinator\n"), answer)
}

// The check of a worker that enrolls verifies the coordinator against the
// invite's pin, and makes no call that needs a credential (Spec 108
// §dagu worker --check).
func TestCheckInvite(t *testing.T) {
	t.Parallel()
	server := startPeerTLSServer(t, certHosts)
	other := startPeerTLSServer(t, certHosts)
	created := server.createInvite(t, &coordinatorv1.CreateInviteRequest{})
	config := enrollConfig(t, created.Invite)

	result := coordinator.Check(t.Context(), config, checkMember(t, server.addr))
	require.True(t, result.Passed(), "%+v", result)
	assert.Equal(t, "built-in TLS (from worker.invite)", result.Transport)
	assert.True(t, result.TLS.Verified)
	credential, answer := result.Describe()
	assert.Equal(t, "the invite of worker.invite, which only enrollment uses; no peer token", credential)
	assert.Equal(t, "serving; the worker enrolls with its invite when it starts", answer)
	assert.Equal(t, 0, server.inviteUses(t, created.Record.Id), "the check never enrolls")

	result = coordinator.Check(t.Context(), config, checkMember(t, other.addr))
	assert.False(t, result.Passed())
	require.NotNil(t, result.TLS)
	assert.ErrorIs(t, result.TLS.Err, coordinator.ErrInviteCAMismatch)
	assert.Empty(t, result.TLS.Fallback, "an invite worker never falls back to h2c")
}
