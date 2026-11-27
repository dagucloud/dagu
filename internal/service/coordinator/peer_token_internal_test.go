// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
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
	const method = "/coordinator.v1.CoordinatorService/Poll"
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

// logCapture returns a context whose logs go to the returned buffer.
func logCapture(ctx context.Context) (context.Context, *bytes.Buffer) {
	var logs bytes.Buffer
	return logger.WithLogger(ctx, logger.NewLogger(logger.WithWriter(&logs), logger.WithQuiet())), &logs
}

// A refused call is logged with its method and the fixes, at most once per
// warn interval, and is remembered so a dispatch failure can name it.
func TestPeerTokenAuthLogsRefusal(t *testing.T) {
	const method = "/coordinator.v1.CoordinatorService/Poll"
	auth := NewPeerTokenAuth("coordinator-token", false, true)
	assert.False(t, auth.refusedRecently())

	ctx, logs := logCapture(callContext(t, newPeerTokenCredentials("", "")))
	require.Error(t, auth.authorize(ctx, method))
	assert.Contains(t, logs.String(), "Coordinator refused a call without a peer token")
	assert.Contains(t, logs.String(), method)
	assert.Contains(t, logs.String(), "coordinator.require_token: false")
	assert.True(t, auth.refusedRecently())

	logs.Reset()
	require.Error(t, auth.authorize(ctx, method))
	assert.Empty(t, logs.String())

	auth.lastRefusal = auth.lastRefusal.Add(-peerTokenRefusalWindow)
	assert.False(t, auth.refusedRecently())
}

// With coordinator.require_token false, a call without a token passes and the
// coordinator warns.
func TestPeerTokenAuthOptOutWarns(t *testing.T) {
	auth := NewPeerTokenAuth("coordinator-token", false, false)
	ctx, logs := logCapture(callContext(t, newPeerTokenCredentials("", "")))

	require.NoError(t, auth.authorize(ctx, "/coordinator.v1.CoordinatorService/Poll"))
	assert.Contains(t, logs.String(), "Coordinator accepted a call without a peer token")
	assert.False(t, auth.refusedRecently())
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
