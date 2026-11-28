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

func recordHeartbeat(t *testing.T, s *store.WorkerCertificateStore, cert persis.WorkerCertificate) persis.WorkerCertificate {
	t.Helper()
	stored, err := s.RecordHeartbeat(t.Context(), cert)
	require.NoError(t, err)
	return stored
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
	recordHeartbeat(t, s, first)
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

// A removal lands after Prune reads the removals and before it deletes the
// certificate record. The removed certificate keeps every field, and its
// removal still moves forward.
func TestWorkerCertificateStore_RemovalDuringPrune(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, col := newWorkerCertificateStore(t)
	cert := workerCertificate("cert-1")
	createCertificate(t, s, cert)
	pruner, err := store.NewWorkerCertificateStore(&beforeFirstDelete{Collection: col, hook: func() {
		setState(t, s, "cert-1", persis.WorkerCertificateRemoving)
	}})
	require.NoError(t, err)

	pruned, err := pruner.Prune(ctx, enrolledAt.Add(retention+time.Second))
	require.NoError(t, err)
	require.Equal(t, 1, pruned)

	want := cert
	want.State = persis.WorkerCertificateRevoked
	assert.Equal(t, want, setState(t, s, "cert-1", persis.WorkerCertificateRevoked))
	assert.Equal(t, want, getCertificate(t, s, "cert-1"))
	certs, err := s.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []persis.WorkerCertificate{want}, certs)
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
			recordHeartbeat(t, s, beat)
			assert.Equal(t, beat, getCertificate(t, s, "cert-1"))
		})
	}
}

// A removed certificate whose certificate record became unreadable is read
// from its removal record, and its removal still moves forward.
func TestWorkerCertificateStore_RemovalWithUnreadableRecord(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	col := file.NewBackend(config.PathsConfig{DataDir: dataDir}).Collection(persis.CollectionWorkerCertificates)
	s, err := store.NewWorkerCertificateStore(col)
	require.NoError(t, err)
	cert := workerCertificate("cert-1")
	createCertificate(t, s, cert)
	setState(t, s, "cert-1", persis.WorkerCertificateRemoving)
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "worker-certificates", "certs", "cert-1.json"), []byte("{"), 0o600))

	want := cert
	want.State = persis.WorkerCertificateRemoving
	assert.Equal(t, want, getCertificate(t, s, "cert-1"))
	want.State = persis.WorkerCertificateRevoked
	assert.Equal(t, want, setState(t, s, "cert-1", persis.WorkerCertificateRevoked))
}

// Another coordinator read the certificate record before a prune deleted
// it. Between Get's two reads, that coordinator's removal lands and a
// heartbeat writes the certificate record again. The certificate was never
// active during the call, so Get does not report it active.
func TestWorkerCertificateStore_GetDuringRemoval(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, col := newWorkerCertificateStore(t)
	cert := workerCertificate("cert-1")
	createCertificate(t, s, cert)
	readBeforePrune, err := col.Get(ctx, "certs/cert-1")
	require.NoError(t, err)
	require.NoError(t, col.Delete(ctx, "certs/cert-1"))
	remover, err := store.NewWorkerCertificateStore(&staleFirstGet{Collection: col, rec: readBeforePrune})
	require.NoError(t, err)
	beat := cert
	beat.LastHeartbeatAt = enrolledAt.Add(48 * time.Hour)
	reader, err := store.NewWorkerCertificateStore(&afterFirstGet{Collection: col, hook: func() {
		setState(t, remover, "cert-1", persis.WorkerCertificateRevoked)
		recordHeartbeat(t, s, beat)
	}})
	require.NoError(t, err)

	got, err := reader.Get(ctx, "cert-1")

	require.NoError(t, err)
	assert.Equal(t, persis.WorkerCertificateRevoked, got.State)
	assert.Equal(t, cert.InviteID, got.InviteID)
	assert.True(t, got.CreatedAt.Equal(cert.CreatedAt))
}

// The collections below land another client's writes at a chosen point of
// a store call, so a test drives an interleaving without timing.

// beforeFirstDelete runs hook right before its first CompareAndDelete.
type beforeFirstDelete struct {
	persis.Collection
	hook func()
	done bool
}

func (c *beforeFirstDelete) CompareAndDelete(ctx context.Context, rec *persis.Record) error {
	if !c.done {
		c.done = true
		c.hook()
	}
	return c.Collection.CompareAndDelete(ctx, rec)
}

// afterFirstGet runs hook right after its first Get.
type afterFirstGet struct {
	persis.Collection
	hook func()
	done bool
}

func (c *afterFirstGet) Get(ctx context.Context, id string) (*persis.Record, error) {
	rec, err := c.Collection.Get(ctx, id)
	if !c.done {
		c.done = true
		c.hook()
	}
	return rec, err
}

// staleFirstGet answers its first Get of rec's ID with rec, as read before
// later writes.
type staleFirstGet struct {
	persis.Collection
	rec  *persis.Record
	done bool
}

func (c *staleFirstGet) Get(ctx context.Context, id string) (*persis.Record, error) {
	if !c.done && id == c.rec.ID {
		c.done = true
		return c.rec, nil
	}
	return c.Collection.Get(ctx, id)
}

// Two coordinators repair one unreadable record. The one with the older
// report read the file before the newer report replaced it, and the newer
// report stays.
func TestWorkerCertificateStore_RecordHeartbeatRepairKeepsNewerReport(t *testing.T) {
	t.Parallel()
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
	recordHeartbeat(t, first, newer)
	older := workerCertificate("cert-1")
	older.WorkerID = "worker-older"
	recordHeartbeat(t, second, older)

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
	s, _ := newWorkerCertificateStore(t)
	cert := workerCertificate("cert-1")
	createCertificate(t, s, cert)

	// Within a day of the stored heartbeat, the same report writes nothing.
	beat := cert
	beat.LastHeartbeatAt = enrolledAt.Add(24 * time.Hour)
	recordHeartbeat(t, s, beat)
	assert.Equal(t, cert, getCertificate(t, s, "cert-1"))

	beat.LastHeartbeatAt = enrolledAt.Add(24*time.Hour + time.Second)
	recordHeartbeat(t, s, beat)
	assert.Equal(t, beat, getCertificate(t, s, "cert-1"))

	// A changed worker ID is written a minute after the last write.
	renamed := beat
	renamed.WorkerID = "worker-renamed"
	renamed.LastHeartbeatAt = beat.LastHeartbeatAt.Add(time.Minute)
	recordHeartbeat(t, s, renamed)
	assert.Equal(t, renamed, getCertificate(t, s, "cert-1"))

	// A report older than the stored heartbeat writes nothing, and the
	// result says the stored worker ID differs from the reported one.
	late := renamed
	late.WorkerID = "worker-late"
	late.LastHeartbeatAt = renamed.LastHeartbeatAt.Add(-time.Hour)
	assert.Equal(t, renamed.WorkerID, recordHeartbeat(t, s, late).WorkerID)
	assert.Equal(t, renamed, getCertificate(t, s, "cert-1"))
}

// Two workers that run on one copied certificate report different fields at
// every heartbeat. They write the record at most once a minute.
func TestWorkerCertificateStore_RecordHeartbeatBoundsChanges(t *testing.T) {
	t.Parallel()
	_, base := newWorkerCertificateStore(t)
	col := &countingWrites{Collection: base}
	s, err := store.NewWorkerCertificateStore(col)
	require.NoError(t, err)
	cert := workerCertificate("cert-1")
	createCertificate(t, s, cert)
	clone := cert
	clone.WorkerID = "worker-clone"
	clone.Host = "host-clone"

	const reports = 180
	var writtenAt []time.Time
	for i := 1; i <= reports; i++ {
		beat := cert
		if i%2 == 1 {
			beat = clone
		}
		beat.LastHeartbeatAt = enrolledAt.Add(time.Duration(i) * time.Second)
		before := col.writes
		recordHeartbeat(t, s, beat)
		require.LessOrEqual(t, col.writes-before, 1)
		if col.writes > before {
			writtenAt = append(writtenAt, beat.LastHeartbeatAt)
		}
	}

	require.NotEmpty(t, writtenAt)
	last := cert.LastHeartbeatAt
	for _, at := range writtenAt {
		assert.GreaterOrEqual(t, at.Sub(last), time.Minute)
		last = at
	}
}

// countingWrites counts the writes that change a record.
type countingWrites struct {
	persis.Collection
	writes int
}

func (c *countingWrites) Put(ctx context.Context, rec *persis.Record) error {
	return c.count(c.Collection.Put(ctx, rec))
}

func (c *countingWrites) Create(ctx context.Context, rec *persis.Record) error {
	return c.count(c.Collection.Create(ctx, rec))
}

func (c *countingWrites) CompareAndSwap(ctx context.Context, id string, expected, next []byte) error {
	return c.count(c.Collection.CompareAndSwap(ctx, id, expected, next))
}

func (c *countingWrites) count(err error) error {
	if err == nil {
		c.writes++
	}
	return err
}

func TestWorkerCertificateStore_RecordHeartbeatKeepsState(t *testing.T) {
	t.Parallel()
	s, _ := newWorkerCertificateStore(t)
	cert := workerCertificate("cert-1")
	createCertificate(t, s, cert)
	setState(t, s, "cert-1", persis.WorkerCertificateRevoked)

	beat := cert
	beat.LastHeartbeatAt = enrolledAt.Add(48 * time.Hour)
	recordHeartbeat(t, s, beat)

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
	recordHeartbeat(t, s, beat)
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
