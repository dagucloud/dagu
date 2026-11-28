// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestLoadOrCreatePeerToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator", "peer-token")

	first, err := LoadOrCreatePeerToken(path)
	require.NoError(t, err)
	require.NotEmpty(t, first)

	// A second coordinator on the same data directory reads the same token.
	second, err := LoadOrCreatePeerToken(path)
	require.NoError(t, err)
	assert.Equal(t, first, second)

	token, err := readPeerToken(path)
	require.NoError(t, err)
	assert.Equal(t, first, token)
}

// callContext returns the incoming context of a call made with the client's
// peer token credentials.
func callContext(t *testing.T, creds *peerTokenCredentials) context.Context {
	t.Helper()
	md, err := creds.GetRequestMetadata(context.Background())
	require.NoError(t, err)
	return metadata.NewIncomingContext(context.Background(), metadata.New(md))
}

func TestPeerTokenAuth(t *testing.T) {
	const (
		method              = "/coordinator.v1.CoordinatorService/Poll"
		joinReadinessMethod = "/coordinator.v1.CoordinatorService/GetJoinReadiness"
	)
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "coordinator", "peer-token")
	token, err := LoadOrCreatePeerToken(tokenFile)
	require.NoError(t, err)
	otherFile := filepath.Join(dir, "other", "peer-token")
	_, err = LoadOrCreatePeerToken(otherFile)
	require.NoError(t, err)
	missingFile := filepath.Join(dir, "missing", "peer-token")

	tests := []struct {
		name       string
		auth       *PeerTokenAuth
		client     *peerTokenCredentials
		method     string
		wantDenied bool
	}{
		{name: "client reads the generated token", auth: NewPeerTokenAuth(token, false, true), client: newPeerTokenCredentials("", tokenFile)},
		{name: "configured token", auth: NewPeerTokenAuth("shared", true, true), client: newPeerTokenCredentials("shared", "")},
		{name: "no token is accepted when not required", auth: NewPeerTokenAuth(token, false, false), client: newPeerTokenCredentials("", missingFile)},
		{name: "no token is rejected when required", auth: NewPeerTokenAuth(token, false, true), client: newPeerTokenCredentials("", missingFile), wantDenied: true},
		{name: "another coordinator's generated token is accepted when not required", auth: NewPeerTokenAuth(token, false, false), client: newPeerTokenCredentials("", otherFile)},
		{name: "another generated token is rejected when required", auth: NewPeerTokenAuth(token, false, true), client: newPeerTokenCredentials("", otherFile), wantDenied: true},
		{name: "a token other than the configured one is rejected", auth: NewPeerTokenAuth("shared", true, false), client: newPeerTokenCredentials("other", ""), wantDenied: true},
		{name: "health checks need no token", auth: NewPeerTokenAuth(token, false, true), client: newPeerTokenCredentials("", missingFile), method: "/grpc.health.v1.Health/Check"},
		{name: "join readiness with the token", auth: NewPeerTokenAuth(token, false, false), client: newPeerTokenCredentials("", tokenFile), method: joinReadinessMethod},
		{name: "join readiness needs a token when none is required", auth: NewPeerTokenAuth(token, false, false), client: newPeerTokenCredentials("", missingFile), method: joinReadinessMethod, wantDenied: true},
		{name: "join readiness rejects another generated token", auth: NewPeerTokenAuth(token, false, false), client: newPeerTokenCredentials("", otherFile), method: joinReadinessMethod, wantDenied: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			callMethod := method
			if test.method != "" {
				callMethod = test.method
			}
			err := test.auth.authorize(callContext(t, test.client), callMethod)
			if test.wantDenied {
				assert.Equal(t, codes.Unauthenticated, status.Code(err))
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// A coordinator that regenerates its token, after its token file is removed,
// is followed by clients that read the file.
func TestPeerTokenCredentialsRereadFile(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "peer-token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("first\n"), 0o600))
	creds := newPeerTokenCredentials("", tokenFile)
	assert.Equal(t, "first", creds.current())

	require.NoError(t, os.WriteFile(tokenFile, []byte("second\n"), 0o600))
	creds.readAt = creds.readAt.Add(-peerTokenRereadInterval)
	assert.Equal(t, "second", creds.current())

	// A failed read keeps the last token.
	require.NoError(t, os.Remove(tokenFile))
	creds.readAt = creds.readAt.Add(-peerTokenRereadInterval)
	assert.Equal(t, "second", creds.current())
}

// A client that dialed before the coordinator wrote its token file picks the
// token up within a second, not after the full re-read interval.
func TestPeerTokenCredentialsRetryMissingFile(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "peer-token")
	creds := newPeerTokenCredentials("", tokenFile)
	assert.Empty(t, creds.current())

	require.NoError(t, os.WriteFile(tokenFile, []byte("generated\n"), 0o600))
	creds.readAt = creds.readAt.Add(-peerTokenRetryInterval)
	assert.Equal(t, "generated", creds.current())
}

func TestReadPeerTokenRejectsNonPrintable(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "peer-token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("first\x00second\n"), 0o600))

	_, err := readPeerToken(tokenFile)
	require.Error(t, err)
}

// Only a client certificate that chains to the CA given to
// AcceptWorkerCertificates counts as the peer token, so a client certificate
// from peer.client_ca_file on a coordinator with own certificates does not.
func TestPeerTokenAuthWorkerCertificate(t *testing.T) {
	const method = "/coordinator.v1.CoordinatorService/Poll"
	dir := t.TempDir()
	peerCA, err := LoadOrCreatePeerCA(filepath.Join(dir, "peer", "ca.pem"))
	require.NoError(t, err)
	otherCA, err := LoadOrCreatePeerCA(filepath.Join(dir, "other", "ca.pem"))
	require.NoError(t, err)
	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: "certificate-id"}}
	ctx := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf, peerCA.cert}}},
	}})

	for _, tc := range []struct {
		name     string
		accept   *PeerCA
		wantCode codes.Code
	}{
		{name: "peer CA", accept: peerCA, wantCode: codes.OK},
		{name: "another CA", accept: otherCA, wantCode: codes.Unauthenticated},
		{name: "none accepted", wantCode: codes.Unauthenticated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := NewPeerTokenAuth("token", true, true)
			if tc.accept != nil {
				auth.AcceptWorkerCertificates(tc.accept)
			}
			assert.Equal(t, tc.wantCode, status.Code(auth.authorize(ctx, method)))
		})
	}
}
