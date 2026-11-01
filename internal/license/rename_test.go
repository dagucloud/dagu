// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renamingManager is a manager whose license, signed with priv, checks in
// with a console that answers renames with rename, and the store its license
// is saved to.
func renamingManager(t *testing.T, pub ed25519.PublicKey, priv ed25519.PrivateKey, rename http.HandlerFunc) (*Manager, *mockActivationStore) {
	t.Helper()
	claims := validClaims()
	claims.ID = "lic-123"
	token := signToken(t, priv, claims)
	srv := newMockCloudServer(t, mockCloudServerConfig{renameHandler: rename})
	store := &mockActivationStore{}
	m := NewManager(ManagerConfig{LicenseDir: t.TempDir(), CloudURL: srv.URL, ServerName: "build-01"}, pub, store, nil)
	m.state.Update(claims, token)
	m.setSource(SourceActivationFile)
	ad := makeAD("srv-1")
	ad.Token = token
	ad.CheckedInAt = time.Now().Add(-time.Hour).UTC()
	m.setActivation(ad)
	return m, store
}

// consoleRename answers a rename as Dagu Console does: with a token that
// names the server as asked, signed with priv.
func consoleRename(t *testing.T, priv ed25519.PrivateKey, got *RenameRequest) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var req RenameRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		*got = req
		claims := validClaims()
		claims.ID = req.LicenseID
		claims.ServerName = req.Name
		if claims.ServerName == "" {
			claims.ServerName = "build-01"
		}
		heartbeatHandlerFn(signToken(t, priv, claims))(w, r)
	}
}

func TestManager_RenameServer(t *testing.T) {
	t.Parallel()

	t.Run("shows the name Dagu Console gives the server", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		var got RenameRequest
		m, store := renamingManager(t, pub, priv, consoleRename(t, priv, &got))
		checkedIn := m.Status().LastCheckIn

		require.NoError(t, m.RenameServer(context.Background(), "  Build box  "))

		assert.Equal(t, RenameRequest{LicenseID: "lic-123", ServerID: "srv-1", HeartbeatSecret: "hb-secret", Name: "Build box"}, got)
		status := m.Status()
		assert.Equal(t, "Build box", status.ServerName)
		assert.Equal(t, "build-01", status.ReportedServerName)
		assert.Equal(t, checkedIn, status.LastCheckIn, "a rename is not a check-in")
		saved, err := store.Load()
		require.NoError(t, err)
		require.NotNil(t, saved)
		claims, err := VerifyToken(pub, saved.Token)
		require.NoError(t, err)
		assert.Equal(t, "Build box", claims.ServerName, "the renamed license is saved for the next start")

		require.NoError(t, m.RenameServer(context.Background(), " "))
		assert.Empty(t, got.Name, "an empty name clears it")
		assert.Equal(t, "build-01", m.Status().ServerName)
	})

	t.Run("refuses a name longer than Dagu Console shows", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		var renames atomic.Int32
		m, _ := renamingManager(t, pub, priv, func(w http.ResponseWriter, _ *http.Request) {
			renames.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		})

		err := m.RenameServer(context.Background(), strings.Repeat("a", MaxServerNameLength+1))

		require.ErrorIs(t, err, ErrServerNameTooLong)
		assert.Zero(t, renames.Load(), "Dagu Console is not asked")
	})

	t.Run("counts characters, not bytes", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		var got RenameRequest
		m, _ := renamingManager(t, pub, priv, consoleRename(t, priv, &got))

		require.NoError(t, m.RenameServer(context.Background(), strings.Repeat("界", MaxServerNameLength)))
		assert.Equal(t, strings.Repeat("界", MaxServerNameLength), got.Name)
	})

	t.Run("keeps the license when Dagu Console refuses", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		m, store := renamingManager(t, pub, priv, errorHandlerFn(http.StatusBadRequest, "license has expired"))
		before := m.Status()

		err := m.RenameServer(context.Background(), "Build box")

		cloudErr, ok := errors.AsType[*CloudError](err)
		require.True(t, ok, "got %v", err)
		assert.Equal(t, "license has expired", cloudErr.Message)
		assert.Equal(t, before, m.Status())
		assert.Zero(t, store.saveCalls)
	})

	t.Run("an older Dagu Console", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		m, _ := renamingManager(t, pub, priv, http.NotFound)

		assert.ErrorIs(t, m.RenameServer(context.Background(), "Build box"), ErrRenameUnsupported)
	})

	t.Run("licenses that never check in", func(t *testing.T) {
		t.Parallel()

		m := NewTestManager(FeatureAudit)
		m.setSource(SourceFileJWT)

		assert.ErrorIs(t, m.RenameServer(context.Background(), "Build box"), ErrNoCheckIn)
	})
}
