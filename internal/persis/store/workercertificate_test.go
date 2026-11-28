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
		WorkerIDs:       []persis.WorkerIDSeen{{ID: "worker-" + id, SeenAt: enrolledAt}},
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

func recordHeartbeat(t *testing.T, s *store.WorkerCertificateStore, cert persis.WorkerCertificate) {
	t.Helper()
	require.NoError(t, s.RecordHeartbeat(t.Context(), cert))
}

// clockAt returns a clock stopped at at.
func clockAt(at time.Time) func() time.Time {
	return func() time.Time { return at }
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
	removingAt := enrolledAt.Add(time.Hour)
	_, err := s.MarkRemoving(ctx, "removing", "worker-removing", clockAt(removingAt))
	require.NoError(t, err)
	setState(t, s, "revoked", persis.WorkerCertificateRevoked)

	removals, err := s.ListRemovals(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []persis.WorkerCertificateRemoval{
		{
			ID: "removing", KeyFingerprint: workerCertificate("removing").KeyFingerprint, State: persis.WorkerCertificateRemoving,
			WorkerID: "worker-removing", RemovingAt: removingAt,
		},
		{
			ID: "revoked", KeyFingerprint: workerCertificate("revoked").KeyFingerprint, State: persis.WorkerCertificateRevoked,
			WorkerID: "worker-revoked",
		},
	}, removals)
}

// A removal keeps the time the certificate became removing, names the
// worker it was given, and leaves a revoked certificate as it is.
func TestWorkerCertificateStore_MarkRemoving(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	createCertificate(t, s, workerCertificate("cert-1"))
	first := enrolledAt.Add(time.Hour)
	later := first.Add(time.Minute)

	finish, err := s.MarkRemoving(ctx, "cert-1", "worker-restarted", clockAt(first))
	require.NoError(t, err)
	assert.Equal(t, persis.WorkerCertificateRemoving, finish.State)
	assert.Equal(t, "worker-restarted", finish.HolderID)
	assert.True(t, finish.RemovingAt.Equal(first))

	again, err := s.MarkRemoving(ctx, "cert-1", "worker-restarted", clockAt(later))
	require.NoError(t, err)
	assert.True(t, again.RemovingAt.Equal(first), "a later removal keeps the first time")
	assert.Equal(t, again, getCertificate(t, s, "cert-1"))

	revoked := setState(t, s, "cert-1", persis.WorkerCertificateRevoked)
	assert.True(t, revoked.RemovingAt.Equal(first), "revocation keeps the removal times")
	got, err := s.MarkRemoving(ctx, "cert-1", "worker-other", clockAt(later.Add(time.Hour)))
	require.NoError(t, err)
	assert.Equal(t, revoked, got)

	_, err = s.MarkRemoving(ctx, "missing", "worker-restarted", clockAt(later))
	assert.ErrorIs(t, err, persis.ErrWorkerCertificateNotFound)
}

// A removal that names a worker the newer heartbeat already carries still
// writes it to the removal record, which ListRemovals reads. The worker IDs
// that heartbeats report during the removal reach the certificate record,
// and the certificate names them beside the removal record's own, also
// once revoked.
func TestWorkerCertificateStore_MarkRemovingNamesHeartbeatWorker(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	createCertificate(t, s, workerCertificate("cert-1"))
	removingAt := enrolledAt.Add(time.Hour)
	_, err := s.MarkRemoving(ctx, "cert-1", "worker-a", clockAt(removingAt))
	require.NoError(t, err)
	beat := workerCertificate("cert-1")
	beat.WorkerID = "worker-b"
	beat.LastHeartbeatAt = removingAt.Add(25 * time.Hour)
	recordHeartbeat(t, s, beat)

	named := func() string {
		removals, err := s.ListRemovals(ctx)
		require.NoError(t, err)
		require.Len(t, removals, 1)
		return removals[0].WorkerID
	}
	got, err := s.MarkRemoving(ctx, "cert-1", "worker-b", clockAt(removingAt.Add(26*time.Hour)))
	require.NoError(t, err)
	assert.Equal(t, "worker-b", got.HolderID)
	assert.Equal(t, "worker-b", named())

	// The removal named worker-b. A later removal for worker-b keeps
	// worker-b while the newer heartbeat names worker-c.
	beat.WorkerID = "worker-c"
	beat.LastHeartbeatAt = removingAt.Add(77 * time.Hour)
	recordHeartbeat(t, s, beat)
	got, err = s.MarkRemoving(ctx, "cert-1", "worker-b", clockAt(removingAt.Add(78*time.Hour)))
	require.NoError(t, err)
	assert.Equal(t, "worker-b", got.HolderID, "a call that writes nothing returns the named worker")
	assert.Equal(t, "worker-b", named())

	for i := range 10 {
		beat.WorkerID = fmt.Sprintf("worker-%d", i)
		beat.LastHeartbeatAt = removingAt.Add(time.Duration(80+i) * time.Hour)
		recordHeartbeat(t, s, beat)
	}
	want := []string{
		"worker-cert-1", "worker-a", "worker-b",
		"worker-2", "worker-3", "worker-4", "worker-5", "worker-6", "worker-7", "worker-8", "worker-9",
	}
	assert.Equal(t, want, seenIDs(getCertificate(t, s, "cert-1")))
	revoked := setState(t, s, "cert-1", persis.WorkerCertificateRevoked)
	assert.Equal(t, want, seenIDs(revoked))
	assert.Equal(t, "worker-b", named(), "the revoked record keeps the named worker")
}

// A certificate in removal names the worker IDs of both its records: the
// one its removal named, which its certificate record never saw, and the
// one a newer heartbeat reported.
func TestWorkerCertificateStore_GetDuringRemovalKeepsBothWorkerIDs(t *testing.T) {
	t.Parallel()
	s, _ := newWorkerCertificateStore(t)
	createCertificate(t, s, workerCertificate("cert-1"))
	removingAt := enrolledAt.Add(time.Hour)
	_, err := s.MarkRemoving(t.Context(), "cert-1", "worker-a", clockAt(removingAt))
	require.NoError(t, err)
	beat := workerCertificate("cert-1")
	beat.WorkerID = "worker-b"
	beat.LastHeartbeatAt = removingAt.Add(time.Hour)
	recordHeartbeat(t, s, beat)

	got := getCertificate(t, s, "cert-1")
	assert.Equal(t, []string{"worker-cert-1", "worker-a", "worker-b"}, seenIDs(got))
	assert.Equal(t, "worker-b", got.WorkerID)
}

// A heartbeat writes a new worker ID to the certificate record while a
// revocation checks that the certificate is idle. The certificate names
// that ID once revoked, and the revoked record keeps that heartbeat, also
// once the certificate record is gone (Spec 108 §Removal).
func TestWorkerCertificateStore_RevokeIfIdleKeepsHeartbeatDuringCheck(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, col := newWorkerCertificateStore(t)
	createCertificate(t, s, workerCertificate("cert-1"))
	removingAt := enrolledAt.Add(time.Hour)
	_, err := s.MarkRemoving(ctx, "cert-1", "worker-a", clockAt(removingAt))
	require.NoError(t, err)

	_, revoked, err := s.RevokeIfIdle(ctx, "cert-1", func(context.Context, persis.WorkerCertificate) (bool, error) {
		beat := workerCertificate("cert-1")
		beat.WorkerID = "worker-b"
		beat.LastHeartbeatAt = removingAt.Add(time.Minute)
		recordHeartbeat(t, s, beat)
		return true, nil
	})
	require.NoError(t, err)
	require.True(t, revoked)
	assert.Equal(t, []string{"worker-cert-1", "worker-a", "worker-b"}, seenIDs(getCertificate(t, s, "cert-1")))
	require.NoError(t, col.Delete(ctx, "certs/cert-1"))
	got := getCertificate(t, s, "cert-1")
	assert.Equal(t, persis.WorkerCertificateRevoked, got.State)
	assert.Equal(t, "worker-b", got.WorkerID)
}

// A certificate in removal ran under eight IDs, and its worker restarted
// under eight new ones, which push the earlier IDs out of the certificate
// record. The certificate names all sixteen, before and after its
// revocation, as the removal record keeps the earlier eight (Spec 108
// §Removal).
func TestWorkerCertificateStore_RemovalKeepsIDsAtHistoryLimit(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	createCertificate(t, s, workerCertificate("cert-1"))
	beat := workerCertificate("cert-1")
	var want []string
	sight := func(id string, at time.Time) {
		beat.WorkerID = id
		beat.LastHeartbeatAt = at
		recordHeartbeat(t, s, beat)
		want = append(want, id)
	}
	for i := range 7 {
		sight(fmt.Sprintf("before-%d", i), enrolledAt.Add(time.Duration(i+1)*time.Minute))
	}
	want = append([]string{"worker-cert-1"}, want...)
	removingAt := enrolledAt.Add(time.Hour)
	_, err := s.MarkRemoving(ctx, "cert-1", "before-6", clockAt(removingAt))
	require.NoError(t, err)
	for i := range 8 {
		sight(fmt.Sprintf("restarted-%d", i), removingAt.Add(time.Duration(i+1)*time.Minute))
	}

	assert.Equal(t, want, seenIDs(getCertificate(t, s, "cert-1")))
	assert.Equal(t, want, seenIDs(setState(t, s, "cert-1", persis.WorkerCertificateRevoked)))
	assert.Equal(t, want, seenIDs(getCertificate(t, s, "cert-1")))
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
	newer.WorkerIDs = []persis.WorkerIDSeen{{ID: newer.WorkerID, SeenAt: newer.LastHeartbeatAt}}
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

	// A changed host is written a minute after the last write.
	moved := beat
	moved.Host = "host-moved"
	moved.LastHeartbeatAt = beat.LastHeartbeatAt.Add(time.Second)
	recordHeartbeat(t, s, moved)
	assert.Equal(t, beat, getCertificate(t, s, "cert-1"))
	moved.LastHeartbeatAt = beat.LastHeartbeatAt.Add(time.Minute)
	recordHeartbeat(t, s, moved)
	assert.Equal(t, moved, getCertificate(t, s, "cert-1"))

	// A worker ID the record does not name joins its worker IDs at once, so
	// removal finds the worker by any ID it reported.
	renamed := moved
	renamed.WorkerID = "worker-renamed"
	renamed.LastHeartbeatAt = moved.LastHeartbeatAt.Add(time.Second)
	recordHeartbeat(t, s, renamed)
	renamed.WorkerIDs = []persis.WorkerIDSeen{{ID: "worker-cert-1", SeenAt: enrolledAt}, {ID: "worker-renamed", SeenAt: renamed.LastHeartbeatAt}}
	assert.Equal(t, renamed, getCertificate(t, s, "cert-1"))

	// A return to an ID last seen more than a minute before writes, and
	// makes it the most recently seen.
	back := renamed
	back.WorkerID = "worker-cert-1"
	back.LastHeartbeatAt = renamed.LastHeartbeatAt.Add(time.Second)
	recordHeartbeat(t, s, back)
	back.WorkerIDs = []persis.WorkerIDSeen{{ID: "worker-renamed", SeenAt: renamed.LastHeartbeatAt}, {ID: "worker-cert-1", SeenAt: back.LastHeartbeatAt}}
	assert.Equal(t, back, getCertificate(t, s, "cert-1"))

	// A return within a minute of the ID's last sighting writes nothing, so
	// two workers that share the certificate do not write at every
	// heartbeat.
	again := back
	again.WorkerID = "worker-renamed"
	again.LastHeartbeatAt = back.LastHeartbeatAt.Add(time.Second)
	recordHeartbeat(t, s, again)
	assert.Equal(t, back, getCertificate(t, s, "cert-1"))
	renamed = back

	// A report older than the stored heartbeat writes nothing.
	late := renamed
	late.WorkerID = "worker-late"
	late.LastHeartbeatAt = renamed.LastHeartbeatAt.Add(-time.Hour)
	recordHeartbeat(t, s, late)
	assert.Equal(t, renamed, getCertificate(t, s, "cert-1"))
}

// Each removal of a worker ID returns the removals counted so far, and a
// heartbeat that writes the record keeps how the certificate enrolled,
// which removals compare with (Spec 108 §Removal).
func TestWorkerCertificateStore_EnrollmentOrder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := newWorkerCertificateStore(t)
	for want := range int64(2) {
		removals, err := s.BeginWorkerRemoval(ctx, "worker-cert-1")
		require.NoError(t, err)
		assert.Equal(t, want+1, removals)
	}
	removals, err := s.WorkerRemovals(ctx, "worker-cert-1")
	require.NoError(t, err)
	cert := workerCertificate("cert-1")
	cert.EnrolledAs, cert.EnrolledRemovals = cert.WorkerID, removals
	createCertificate(t, s, cert)

	beat := workerCertificate("cert-1")
	beat.WorkerID = "worker-renamed"
	beat.LastHeartbeatAt = enrolledAt.Add(time.Minute)
	recordHeartbeat(t, s, beat)
	stored := getCertificate(t, s, "cert-1")
	assert.Equal(t, "worker-renamed", stored.WorkerID)
	assert.Equal(t, "worker-cert-1", stored.EnrolledAs)
	assert.Equal(t, int64(2), stored.EnrolledRemovals)
}

// Two workers that run on one copied certificate report different hosts at
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

// seenIDs returns the worker IDs of cert, the least recently seen first.
func seenIDs(cert persis.WorkerCertificate) []string {
	ids := make([]string, 0, len(cert.WorkerIDs))
	for _, seen := range cert.WorkerIDs {
		ids = append(ids, seen.ID)
	}
	return ids
}

// A record keeps the eight most recently seen worker IDs, so workers that
// share one certificate under many IDs cannot grow it. An ID that returns
// stays, and a newer one evicts the least recently seen.
func TestWorkerCertificateStore_RecordHeartbeatBoundsWorkerIDs(t *testing.T) {
	t.Parallel()
	s, _ := newWorkerCertificateStore(t)
	cert := workerCertificate("cert-1")
	createCertificate(t, s, cert)
	report := func(id string, at time.Time) {
		beat := cert
		beat.WorkerID = id
		beat.LastHeartbeatAt = at
		recordHeartbeat(t, s, beat)
	}
	for i := range 8 {
		report(fmt.Sprintf("worker-%d", i), enrolledAt.Add(time.Duration(i+1)*time.Second))
	}
	assert.Equal(t, []string{"worker-0", "worker-1", "worker-2", "worker-3", "worker-4", "worker-5", "worker-6", "worker-7"},
		seenIDs(getCertificate(t, s, "cert-1")))
	report("worker-0", enrolledAt.Add(2*time.Minute))
	report("worker-8", enrolledAt.Add(2*time.Minute+time.Second))
	assert.Equal(t, []string{"worker-2", "worker-3", "worker-4", "worker-5", "worker-6", "worker-7", "worker-0", "worker-8"},
		seenIDs(getCertificate(t, s, "cert-1")))
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
	beat.WorkerIDs = []persis.WorkerIDSeen{{ID: beat.WorkerID, SeenAt: beat.LastHeartbeatAt}}
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
