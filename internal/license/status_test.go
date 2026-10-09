// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusFor(t *testing.T) {
	t.Parallel()

	t.Run("nil checker reports community status", func(t *testing.T) {
		t.Parallel()

		status := StatusFor(nil)

		assert.True(t, status.Community)
		assert.False(t, status.Valid)
		assert.Empty(t, status.Features)
	})

	t.Run("active license includes its public status", func(t *testing.T) {
		t.Parallel()

		manager := NewTestManager(FeatureAudit, FeatureRBAC)
		status := StatusFor(manager.Checker())

		assert.False(t, status.Community)
		assert.True(t, status.Valid)
		assert.Equal(t, "pro", status.Plan)
		assert.Equal(t, []string{FeatureAudit, FeatureRBAC}, status.Features)
		assert.WithinDuration(t, status.Expiry.Add(gracePeriod), status.GraceEndsAt, time.Second)
	})
}

func TestManagerStatusIdentity(t *testing.T) {
	t.Parallel()

	t.Run("key activation reports the server and its console link", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := validClaims()
		claims.ID = "lic-123"
		claims.Workspace = "acme"
		token := signToken(t, priv, claims)
		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler:  activateHandlerFn(token, "hb"),
			heartbeatHandler: heartbeatHandlerFn(token),
		})
		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
			ServerName: "build-01",
		}, pub, nil, nil)
		t.Cleanup(func() { stopWithTimeout(t, m, 5*time.Second) })

		_, err := m.ActivateWithKey(context.Background(), "key")
		require.NoError(t, err)
		status := m.Status()

		assert.Equal(t, "build-01", status.ServerName)
		assert.Equal(t, "lic-123", status.LicenseID)
		assert.Equal(t, "acme", status.Workspace)
		assert.Equal(t, ConnectedViaKey, status.ConnectedVia)
		require.NotEmpty(t, status.ServerID)
		assert.Equal(t, srv.URL+"/servers?server="+status.ServerID, status.ConsoleURL)
		assert.WithinDuration(t, time.Now(), status.LastCheckIn, time.Minute)
	})

	t.Run("successful heartbeat updates the last check-in", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		token := signToken(t, priv, validClaims())
		srv := newMockCloudServer(t, mockCloudServerConfig{heartbeatHandler: heartbeatHandlerFn(token)})
		store := &mockActivationStore{}
		m := NewManager(ManagerConfig{LicenseDir: t.TempDir(), CloudURL: srv.URL}, pub, store, nil)
		m.state.Update(validClaims(), token)
		m.setSource(SourceActivationFile)

		ad := makeAD("srv-1")
		ad.CheckedInAt = time.Now().Add(-48 * time.Hour)
		m.doHeartbeat(context.Background(), ad)

		assert.WithinDuration(t, time.Now(), m.Status().LastCheckIn, time.Minute)
		assert.WithinDuration(t, time.Now(), store.data.CheckedInAt, time.Minute)
	})

	t.Run("offline licenses have no server identity", func(t *testing.T) {
		t.Parallel()

		m := NewTestManager(FeatureAudit)
		m.setSource(SourceFileJWT)
		status := m.Status()

		assert.Equal(t, ConnectedViaFile, status.ConnectedVia)
		assert.Empty(t, status.ServerID)
		assert.Empty(t, status.ConsoleURL)
		assert.NotEmpty(t, status.ServerName)
	})

	t.Run("community reports only the server name", func(t *testing.T) {
		t.Parallel()

		pub, _ := testKeyPair(t)
		m := NewManager(ManagerConfig{ServerName: "build-01"}, pub, nil, nil)
		status := m.Status()

		assert.Equal(t, "build-01", status.ServerName)
		assert.Empty(t, status.ConnectedVia)
		assert.Empty(t, status.ServerID)
	})
}

// Uses t.Setenv, so it must not run in parallel.
func TestManagerStatusIdentity_ConsoleActivationFile(t *testing.T) {
	pub, priv := testKeyPair(t)
	token := signToken(t, priv, validClaims())
	checkedIn := time.Now().Add(-2 * time.Hour).UTC()
	store := &mockActivationStore{data: &ActivationData{
		Token:           token,
		HeartbeatSecret: "hb",
		ServerID:        "srv-1",
		Via:             ConnectedViaConsole,
		CheckedInAt:     checkedIn,
	}}
	t.Setenv("DAGU_LICENSE", "")
	t.Setenv("DAGU_LICENSE_KEY", "")
	t.Setenv("DAGU_LICENSE_FILE", "")

	// The unreachable console makes the startup check-in fail.
	m := NewManager(ManagerConfig{LicenseDir: t.TempDir(), CloudURL: "http://127.0.0.1:0"}, pub, store, nil)
	t.Cleanup(func() { stopWithTimeout(t, m, 5*time.Second) })

	require.NoError(t, m.Start(context.Background()))
	status := m.Status()

	assert.Equal(t, ConnectedViaConsole, status.ConnectedVia)
	assert.Equal(t, "srv-1", status.ServerID)
	assert.True(t, checkedIn.Equal(status.LastCheckIn), "a failed check-in keeps the last successful time")
}

func TestManagerStatusIncludesFailureAndSource(t *testing.T) {
	t.Parallel()

	manager := NewExpiredTestManager(FeatureAudit)
	manager.setSource(SourceFileJWT)

	status := manager.Status()

	require.False(t, status.Community)
	assert.False(t, status.Valid)
	assert.False(t, status.GracePeriod)
	assert.Equal(t, SourceFileJWT, status.Source)
	assert.Equal(t, licenseExpiredFailure, status.Failure)
}
