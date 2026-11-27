// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

var enrolledAt = time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)

const retention = 90 * 24 * time.Hour

func newWorkerCertificateStore(t *testing.T) (*store.WorkerCertificateStore, persis.Collection) {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection(persis.CollectionWorkerCertificates)
	s, err := store.NewWorkerCertificateStore(col)
	require.NoError(t, err)
	return s, col
}

func workerCertificate(id string) persis.WorkerCertificate {
	return persis.WorkerCertificate{
		ID:              id,
		InviteID:        "invite-1",
		KeyFingerprint:  secretHash("key of " + id),
		State:           persis.WorkerCertificateActive,
		WorkerID:        "worker-" + id,
		Host:            "host-" + id,
		Environment:     persis.WorkerEnvironmentContainer,
		CreatedAt:       enrolledAt,
		LastHeartbeatAt: enrolledAt,
	}
}

func createCertificate(t *testing.T, s *store.WorkerCertificateStore, cert persis.WorkerCertificate) {
	t.Helper()
	require.NoError(t, s.Create(t.Context(), cert))
}

func getCertificate(t *testing.T, s *store.WorkerCertificateStore, id string) persis.WorkerCertificate {
	t.Helper()
	cert, err := s.Get(t.Context(), id)
	require.NoError(t, err)
	return cert
}

func TestWorkerCertificateStore_CreateAndList(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	first := workerCertificate("cert-b")
	second := workerCertificate("cert-a")
	second.CreatedAt = enrolledAt.Add(time.Minute)
	createCertificate(t, s, first)
	createCertificate(t, s, second)

	assert.ErrorIs(t, s.Create(ctx, first), persis.ErrWorkerCertificateExists)
	assert.Equal(t, first, getCertificate(t, s, "cert-b"))
	certs, err := s.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []persis.WorkerCertificate{first, second}, certs)

	_, err = s.Get(ctx, "missing")
	assert.ErrorIs(t, err, persis.ErrWorkerCertificateNotFound)
}

// A record that cannot be read fails the listing, so a revoked certificate
// is never taken for an unknown one.
func TestWorkerCertificateStore_ListFailsOnUnreadableRecord(t *testing.T) {
	t.Parallel()
	s, col := newWorkerCertificateStore(t)
	createCertificate(t, s, workerCertificate("cert-1"))
	require.NoError(t, col.Put(t.Context(), &persis.Record{ID: "cert-2", Data: []byte(`{"state":"unknown"}`)}))

	_, err := s.List(t.Context())
	assert.Error(t, err)
}

func TestWorkerCertificateStore_SetState(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	createCertificate(t, s, workerCertificate("cert-1"))

	removing, err := s.SetState(ctx, "cert-1", persis.WorkerCertificateRemoving)
	require.NoError(t, err)
	assert.Equal(t, persis.WorkerCertificateRemoving, removing.State)
	_, err = s.SetState(ctx, "cert-1", persis.WorkerCertificateActive)
	assert.ErrorIs(t, err, persis.ErrWorkerCertificateStateBackward)

	revoked, err := s.SetState(ctx, "cert-1", persis.WorkerCertificateRevoked)
	require.NoError(t, err)
	assert.Equal(t, persis.WorkerCertificateRevoked, revoked.State)
	again, err := s.SetState(ctx, "cert-1", persis.WorkerCertificateRevoked)
	require.NoError(t, err)
	assert.Equal(t, revoked, again)
	_, err = s.SetState(ctx, "cert-1", persis.WorkerCertificateRemoving)
	assert.ErrorIs(t, err, persis.ErrWorkerCertificateStateBackward)
	assert.Equal(t, revoked, getCertificate(t, s, "cert-1"))

	_, err = s.SetState(ctx, "missing", persis.WorkerCertificateRevoked)
	assert.ErrorIs(t, err, persis.ErrWorkerCertificateNotFound)
}

func TestWorkerCertificateStore_RecordHeartbeat(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	cert := workerCertificate("cert-1")
	createCertificate(t, s, cert)

	// Within a day of the stored heartbeat, the same report writes nothing.
	beat := cert
	beat.LastHeartbeatAt = enrolledAt.Add(24 * time.Hour)
	require.NoError(t, s.RecordHeartbeat(ctx, beat))
	assert.Equal(t, cert, getCertificate(t, s, "cert-1"))

	beat.LastHeartbeatAt = enrolledAt.Add(24*time.Hour + time.Second)
	require.NoError(t, s.RecordHeartbeat(ctx, beat))
	assert.Equal(t, beat, getCertificate(t, s, "cert-1"))

	// A changed worker ID is written at once.
	renamed := beat
	renamed.WorkerID = "worker-renamed"
	renamed.LastHeartbeatAt = beat.LastHeartbeatAt.Add(time.Minute)
	require.NoError(t, s.RecordHeartbeat(ctx, renamed))
	assert.Equal(t, renamed, getCertificate(t, s, "cert-1"))
}

func TestWorkerCertificateStore_RecordHeartbeatKeepsState(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	cert := workerCertificate("cert-1")
	createCertificate(t, s, cert)
	_, err := s.SetState(ctx, "cert-1", persis.WorkerCertificateRevoked)
	require.NoError(t, err)

	beat := cert
	beat.LastHeartbeatAt = enrolledAt.Add(48 * time.Hour)
	require.NoError(t, s.RecordHeartbeat(ctx, beat))

	got := getCertificate(t, s, "cert-1")
	assert.Equal(t, persis.WorkerCertificateRevoked, got.State)
	assert.True(t, got.LastHeartbeatAt.Equal(beat.LastHeartbeatAt))
}

// A heartbeat with a certificate whose record was pruned records it again.
func TestWorkerCertificateStore_RecordHeartbeatAfterPrune(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	cert := workerCertificate("cert-1")
	createCertificate(t, s, cert)
	pruned, err := s.Prune(ctx, enrolledAt.Add(retention+time.Second))
	require.NoError(t, err)
	require.Equal(t, 1, pruned)

	beat := cert
	beat.LastHeartbeatAt = enrolledAt.Add(retention + time.Minute)
	require.NoError(t, s.RecordHeartbeat(ctx, beat))
	assert.Equal(t, beat, getCertificate(t, s, "cert-1"))
}

func TestWorkerCertificateStore_Prune(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	now := enrolledAt.Add(200 * 24 * time.Hour)
	// The age counts from the last heartbeat, or from enrollment before the
	// first.
	certs := map[string]struct {
		state         persis.WorkerCertificateState
		createdAt     time.Time
		lastHeartbeat time.Time
		kept          bool
	}{
		"at-limit":          {persis.WorkerCertificateActive, enrolledAt, now.Add(-retention), true},
		"past-limit":        {persis.WorkerCertificateActive, enrolledAt, now.Add(-retention - time.Nanosecond), false},
		"recent-heartbeat":  {persis.WorkerCertificateActive, enrolledAt, now.Add(-time.Hour), true},
		"never-heartbeat":   {persis.WorkerCertificateActive, enrolledAt, time.Time{}, false},
		"enrolled-recently": {persis.WorkerCertificateActive, now.Add(-time.Hour), time.Time{}, true},
		"removing-past":     {persis.WorkerCertificateRemoving, enrolledAt, now.Add(-retention - time.Hour), true},
		"revoked-past":      {persis.WorkerCertificateRevoked, enrolledAt, now.Add(-retention - time.Hour), true},
	}
	for id, c := range certs {
		cert := workerCertificate(id)
		cert.CreatedAt = c.createdAt
		cert.LastHeartbeatAt = c.lastHeartbeat
		createCertificate(t, s, cert)
		if c.state != persis.WorkerCertificateActive {
			_, err := s.SetState(ctx, id, c.state)
			require.NoError(t, err)
		}
	}

	removed, err := s.Prune(ctx, now)
	require.NoError(t, err)

	assert.Equal(t, 2, removed)
	for id, c := range certs {
		_, err := s.Get(ctx, id)
		if c.kept {
			assert.NoError(t, err, id)
		} else {
			assert.ErrorIs(t, err, persis.ErrWorkerCertificateNotFound, id)
		}
	}
}
