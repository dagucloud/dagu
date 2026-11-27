// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
)

var enrolledAt = time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)

const retention = 90 * 24 * time.Hour

// newWorkerCertificateStore opens the store on the file backend, whose
// listing order and write times differ from the memory backend's.
func newWorkerCertificateStore(t *testing.T) (*store.WorkerCertificateStore, persis.Collection) {
	t.Helper()
	col := file.NewBackend(config.PathsConfig{DataDir: t.TempDir()}).Collection(persis.CollectionWorkerCertificates)
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

func setState(t *testing.T, s *store.WorkerCertificateStore, id string, state persis.WorkerCertificateState) persis.WorkerCertificate {
	t.Helper()
	cert, err := s.SetState(t.Context(), id, state)
	require.NoError(t, err)
	return cert
}

func TestWorkerCertificateStore_CreateAndGet(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	cert := workerCertificate("cert-1")
	createCertificate(t, s, cert)

	assert.Equal(t, cert, getCertificate(t, s, "cert-1"))
	assert.ErrorIs(t, s.Create(ctx, cert), persis.ErrWorkerCertificateExists)
	_, err := s.Get(ctx, "missing")
	assert.ErrorIs(t, err, persis.ErrWorkerCertificateNotFound)
}

// List keeps enrollment order after a record is written again.
func TestWorkerCertificateStore_ListOldestFirst(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	first := workerCertificate("cert-b")
	second := workerCertificate("cert-a")
	second.CreatedAt = enrolledAt.Add(time.Minute)
	createCertificate(t, s, first)
	createCertificate(t, s, second)

	first.LastHeartbeatAt = enrolledAt.Add(48 * time.Hour)
	require.NoError(t, s.RecordHeartbeat(ctx, first))
	first = setState(t, s, "cert-b", persis.WorkerCertificateRemoving)

	certs, err := s.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []persis.WorkerCertificate{first, second}, certs)
}

func TestWorkerCertificateStore_ListRemovals(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	for _, id := range []string{"active", "removing", "revoked"} {
		createCertificate(t, s, workerCertificate(id))
	}
	setState(t, s, "removing", persis.WorkerCertificateRemoving)
	setState(t, s, "revoked", persis.WorkerCertificateRevoked)

	removals, err := s.ListRemovals(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []persis.WorkerCertificateRemoval{
		{ID: "removing", KeyFingerprint: workerCertificate("removing").KeyFingerprint, State: persis.WorkerCertificateRemoving},
		{ID: "revoked", KeyFingerprint: workerCertificate("revoked").KeyFingerprint, State: persis.WorkerCertificateRevoked},
	}, removals)
}

// A removal that cannot be read fails the read, so a revoked certificate is
// never taken for an active one.
func TestWorkerCertificateStore_ListRemovalsFailsOnUnreadableRecord(t *testing.T) {
	t.Parallel()
	s, col := newWorkerCertificateStore(t)
	createCertificate(t, s, workerCertificate("cert-1"))
	require.NoError(t, col.Put(t.Context(), &persis.Record{ID: "removals/cert-1", Data: []byte(`{"state":"unknown"}`)}))

	_, err := s.ListRemovals(t.Context())
	assert.Error(t, err)
	_, err = s.List(t.Context())
	assert.Error(t, err)
}

func TestWorkerCertificateStore_SetState(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	createCertificate(t, s, workerCertificate("cert-1"))

	removing := setState(t, s, "cert-1", persis.WorkerCertificateRemoving)
	assert.Equal(t, persis.WorkerCertificateRemoving, removing.State)
	_, err := s.SetState(ctx, "cert-1", persis.WorkerCertificateActive)
	assert.ErrorIs(t, err, persis.ErrWorkerCertificateStateBackward)

	revoked := setState(t, s, "cert-1", persis.WorkerCertificateRevoked)
	assert.Equal(t, persis.WorkerCertificateRevoked, revoked.State)
	assert.Equal(t, revoked, setState(t, s, "cert-1", persis.WorkerCertificateRevoked))
	_, err = s.SetState(ctx, "cert-1", persis.WorkerCertificateRemoving)
	assert.ErrorIs(t, err, persis.ErrWorkerCertificateStateBackward)
	assert.Equal(t, revoked, getCertificate(t, s, "cert-1"))

	_, err = s.SetState(ctx, "missing", persis.WorkerCertificateRevoked)
	assert.ErrorIs(t, err, persis.ErrWorkerCertificateNotFound)
}

// A prune that read the removals before a removal began may delete the
// certificate record. The ID stays taken, and the removal still reaches
// revoked.
func TestWorkerCertificateStore_SetStateAfterPrunedRecord(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, col := newWorkerCertificateStore(t)
	cert := workerCertificate("cert-1")
	createCertificate(t, s, cert)
	setState(t, s, "cert-1", persis.WorkerCertificateRemoving)
	require.NoError(t, col.Delete(ctx, "certs/cert-1"))

	assert.ErrorIs(t, s.Create(ctx, cert), persis.ErrWorkerCertificateExists)
	revoked := setState(t, s, "cert-1", persis.WorkerCertificateRevoked)

	want := persis.WorkerCertificate{ID: "cert-1", KeyFingerprint: cert.KeyFingerprint, State: persis.WorkerCertificateRevoked}
	assert.Equal(t, want, revoked)
	assert.Equal(t, want, getCertificate(t, s, "cert-1"))
}

// A heartbeat replaces a certificate record that cannot be read, whether
// its file is not JSON or its payload is not a certificate.
func TestWorkerCertificateStore_RecordHeartbeatRepairsRecord(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{"not JSON": "{", "not a certificate": "{}"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dataDir := t.TempDir()
			col := file.NewBackend(config.PathsConfig{DataDir: dataDir}).Collection(persis.CollectionWorkerCertificates)
			s, err := store.NewWorkerCertificateStore(col)
			require.NoError(t, err)
			path := filepath.Join(dataDir, "worker-certificates", "certs", "cert-1.json")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
			require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))

			beat := workerCertificate("cert-1")
			require.NoError(t, s.RecordHeartbeat(t.Context(), beat))
			assert.Equal(t, beat, getCertificate(t, s, "cert-1"))
		})
	}
}

// Two coordinators repair one unreadable record. The one with the older
// report read the file before the newer report replaced it, and the newer
// report stays.
func TestWorkerCertificateStore_RecordHeartbeatRepairKeepsNewerReport(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "worker-certificates", "certs", "cert-1.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	openCollection := func() *file.Collection {
		return file.NewBackend(config.PathsConfig{DataDir: dataDir}).Collection(persis.CollectionWorkerCertificates).(*file.Collection)
	}
	first, err := store.NewWorkerCertificateStore(openCollection())
	require.NoError(t, err)
	second, err := store.NewWorkerCertificateStore(&staleCorruptRead{Collection: openCollection()})
	require.NoError(t, err)

	newer := workerCertificate("cert-1")
	newer.LastHeartbeatAt = enrolledAt.Add(time.Hour)
	require.NoError(t, first.RecordHeartbeat(ctx, newer))
	older := workerCertificate("cert-1")
	older.WorkerID = "worker-older"
	require.NoError(t, second.RecordHeartbeat(ctx, older))

	assert.Equal(t, newer, getCertificate(t, first, "cert-1"))
}

// staleCorruptRead answers its first Get with the unreadable file it saw
// before another client repaired it.
type staleCorruptRead struct {
	*file.Collection
	served bool
}

func (c *staleCorruptRead) Get(ctx context.Context, id string) (*persis.Record, error) {
	if !c.served {
		c.served = true
		return nil, fmt.Errorf("file backend: %w at %q: invalid JSON", persis.ErrCorrupt, id)
	}
	return c.Collection.Get(ctx, id)
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

	// A changed worker ID is written a minute after the last write.
	renamed := beat
	renamed.WorkerID = "worker-renamed"
	renamed.LastHeartbeatAt = beat.LastHeartbeatAt.Add(time.Minute)
	require.NoError(t, s.RecordHeartbeat(ctx, renamed))
	assert.Equal(t, renamed, getCertificate(t, s, "cert-1"))

	// A report older than the stored heartbeat writes nothing.
	late := renamed
	late.WorkerID = "worker-late"
	late.LastHeartbeatAt = renamed.LastHeartbeatAt.Add(-time.Hour)
	require.NoError(t, s.RecordHeartbeat(ctx, late))
	assert.Equal(t, renamed, getCertificate(t, s, "cert-1"))
}

// Two workers that run on one copied certificate report different fields at
// every heartbeat. They write the record at most once a minute.
func TestWorkerCertificateStore_RecordHeartbeatBoundsChanges(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	cert := workerCertificate("cert-1")
	createCertificate(t, s, cert)
	clone := cert
	clone.WorkerID = "worker-clone"
	clone.Host = "host-clone"

	const reports = 180
	writes := 0
	last := cert.LastHeartbeatAt
	for i := 1; i <= reports; i++ {
		beat := cert
		if i%2 == 1 {
			beat = clone
		}
		beat.LastHeartbeatAt = enrolledAt.Add(time.Duration(i) * time.Second)
		require.NoError(t, s.RecordHeartbeat(ctx, beat))
		if stored := getCertificate(t, s, "cert-1").LastHeartbeatAt; !stored.Equal(last) {
			writes++
			last = stored
		}
	}

	assert.Positive(t, writes)
	assert.LessOrEqual(t, writes, reports/60)
}

func TestWorkerCertificateStore_RecordHeartbeatKeepsState(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	cert := workerCertificate("cert-1")
	createCertificate(t, s, cert)
	setState(t, s, "cert-1", persis.WorkerCertificateRevoked)

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
			setState(t, s, id, c.state)
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
