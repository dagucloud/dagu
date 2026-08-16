// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

func TestFileCollection(t *testing.T) {
	t.Parallel()

	factory := func(t *testing.T) (persis.Collection, persis.Collection) {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "test")
		return file.NewCollection(dir), file.NewCollection(dir)
	}
	testutil.RunCollectionContract(t, factory)
}

func TestFileBackendPreservesCollectionLayout(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	paths := config.PathsConfig{
		DataDir:        dataDir,
		DAGStateDir:    filepath.Join(root, "custom-dag-state"),
		QueueDir:       filepath.Join(root, "custom-queue"),
		UsersDir:       filepath.Join(root, "custom-users"),
		APIKeysDir:     filepath.Join(root, "custom-api-keys"),
		WebhooksDir:    filepath.Join(root, "custom-webhooks"),
		RemoteNodesDir: filepath.Join(root, "custom-remote-nodes"),
		WorkspacesDir:  filepath.Join(root, "custom-workspaces"),
		ViewsDir:       filepath.Join(root, "custom-views"),
	}
	backend := file.NewBackend(paths)
	distributedDir := filepath.Join(dataDir, "distributed")

	tests := []struct {
		name     string
		dir      string
		indented bool
	}{
		{persis.CollectionAPIKeys, paths.APIKeysDir, true},
		{persis.CollectionActiveDistributedRuns, filepath.Join(distributedDir, "active-runs"), false},
		{persis.CollectionAgentSessionCleanups, filepath.Join(dataDir, "agent-session-cleanups"), false},
		{persis.CollectionDAGRunLeases, filepath.Join(distributedDir, "leases"), false},
		{persis.CollectionDAGSettings, filepath.Join(dataDir, "dag-settings"), true},
		{persis.CollectionDAGState, paths.DAGStateDir, false},
		{persis.CollectionDispatchTasks, distributedDir, false},
		{persis.CollectionIncidents, filepath.Join(dataDir, "incidents"), true},
		{persis.CollectionLicense, filepath.Join(dataDir, "license"), true},
		{persis.CollectionNotifications, filepath.Join(dataDir, "notifications"), true},
		{persis.CollectionProfiles, filepath.Join(dataDir, "profiles"), true},
		{persis.CollectionQueue, paths.QueueDir, false},
		{persis.CollectionRemoteNodes, paths.RemoteNodesDir, true},
		{persis.CollectionSchedulerState, filepath.Join(dataDir, "scheduler"), true},
		{persis.CollectionSecrets, filepath.Join(dataDir, "secrets"), true},
		{persis.CollectionUpgradeCheck, filepath.Join(dataDir, "upgrade"), true},
		{persis.CollectionUsers, paths.UsersDir, true},
		{persis.CollectionViews, paths.ViewsDir, true},
		{persis.CollectionWebhooks, paths.WebhooksDir, true},
		{persis.CollectionWorkerHeartbeats, filepath.Join(distributedDir, "workers"), false},
		{persis.CollectionWorkspaces, paths.WorkspacesDir, true},
		{"custom", filepath.Join(dataDir, "custom"), false},
	}

	compact := []byte(`{"value":1}`)
	var indented bytes.Buffer
	require.NoError(t, json.Indent(&indented, compact, "", "  "))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			col := backend.Collection(tt.name)
			id := "probe"
			if tt.name == persis.CollectionDispatchTasks {
				id = "pending/probe"
			}
			require.NoError(t, col.Put(t.Context(), &persis.Record{ID: id, Data: compact}))

			raw, err := os.ReadFile(filepath.Join(tt.dir, filepath.FromSlash(id)+".json"))
			require.NoError(t, err)
			if tt.indented {
				assert.Equal(t, indented.Bytes(), raw)
			} else {
				assert.Equal(t, compact, raw)
			}

			got, err := backend.Collection(tt.name).Get(t.Context(), id)
			require.NoError(t, err)
			assert.Equal(t, compact, got.Data)
		})
	}
}

func TestFileBackendCollectionsAreIsolated(t *testing.T) {
	t.Parallel()

	backend := file.NewBackend(config.PathsConfig{DataDir: t.TempDir()})
	activeRuns := backend.Collection(persis.CollectionActiveDistributedRuns)
	dispatchTasks := backend.Collection(persis.CollectionDispatchTasks)
	require.NoError(t, activeRuns.Put(t.Context(), &persis.Record{ID: "run-1", Data: []byte(`{}`)}))
	for _, id := range []string{"pending/task-1", "claims/task-1", "admissions/attempts/task-1"} {
		require.NoError(t, dispatchTasks.Put(t.Context(), &persis.Record{ID: id, Data: []byte(`{}`)}))
	}

	page, err := dispatchTasks.List(t.Context(), persis.ListQuery{})
	require.NoError(t, err)
	require.Len(t, page.Records, 3)
	assert.ElementsMatch(t, []string{
		"pending/task-1",
		"claims/task-1",
		"admissions/attempts/task-1",
	}, []string{page.Records[0].ID, page.Records[1].ID, page.Records[2].ID})

	_, err = dispatchTasks.Get(t.Context(), "active-runs/run-1")
	assert.ErrorIs(t, err, persis.ErrNotFound)
}

func TestFileBackendRejectsInvalidCollectionNames(t *testing.T) {
	t.Parallel()

	backend := file.NewBackend(config.PathsConfig{DataDir: t.TempDir()})
	for _, name := range []string{"", ".", "..", "../escape", "nested/name", `nested\name`} {
		t.Run(name, func(t *testing.T) {
			assert.Panics(t, func() { backend.Collection(name) })
		})
	}
}

func TestFileCollectionRejectsEscapingListPrefix(t *testing.T) {
	t.Parallel()

	col := file.NewCollection(filepath.Join(t.TempDir(), "collection"))
	_, err := col.List(t.Context(), persis.ListQuery{Prefix: "../"})
	assert.Error(t, err)
	_, err = col.RecordIDs(t.Context(), "../")
	assert.Error(t, err)
}

func TestFileCollectionWritesRawJSONBody(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	col := file.NewCollection(root)
	raw := []byte(`{"id":"user-1","name":"admin"}`)
	rec := &persis.Record{
		ID:        "users/user-1",
		Data:      raw,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	require.NoError(t, col.Put(ctx, rec))

	path := filepath.Join(root, "users", "user-1.json")
	gotRaw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, raw, gotRaw)

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(gotRaw, &body))
	assert.NotContains(t, body, "encoding")
	assert.NotContains(t, body, "data")

	got, err := col.Get(ctx, "users/user-1")
	require.NoError(t, err)
	assert.Equal(t, raw, got.Data)
}

// TestFileCollectionIndentedMatchesReleasedFormat pins the on-disk bytes of an
// indented collection to json.MarshalIndent(v, "", "  ") — the exact format the
// pre-refactor (<= v2.7.4) file stores wrote — so upgrades need no migration.
func TestFileCollectionIndentedMatchesReleasedFormat(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	col := file.NewCollection(root, file.WithIndentedJSON())

	type sample struct {
		ID   string   `json:"id"`
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	v := sample{ID: "u1", Name: "admin", Tags: []string{"a", "b"}}

	compact, err := json.Marshal(v)
	require.NoError(t, err)
	rec := &persis.Record{
		ID:        "users/u1",
		Data:      compact,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, col.Put(ctx, rec))

	// On-disk bytes must equal the released json.MarshalIndent output.
	onDisk, err := os.ReadFile(filepath.Join(root, "users", "u1.json"))
	require.NoError(t, err)
	wantIndented, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	assert.Equal(t, wantIndented, onDisk)

	// Get normalizes back to canonical compact Data.
	got, err := col.Get(ctx, "users/u1")
	require.NoError(t, err)
	assert.Equal(t, compact, got.Data)
}

// TestFileCollectionIndentedReadsLegacyIndentedFile verifies a file written by
// an older release (indented, no envelope) is read back as canonical compact
// Data without any migration step.
func TestFileCollectionIndentedReadsLegacyIndentedFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	col := file.NewCollection(root, file.WithIndentedJSON())

	compact := []byte(`{"id":"k1","name":"ci"}`)
	legacy, err := json.MarshalIndent(json.RawMessage(compact), "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "api_keys"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "api_keys", "k1.json"), legacy, 0o600))

	got, err := col.Get(ctx, "api_keys/k1")
	require.NoError(t, err)
	assert.Equal(t, compact, got.Data)
}

// TestFileCollectionIndentedContract runs the full Collection contract against
// an indented collection, proving CompareAndSwap, CompareAndDelete, List, and
// Claim all stay correct when records are indented on disk.
func TestFileCollectionIndentedContract(t *testing.T) {
	t.Parallel()

	factory := func(t *testing.T) (persis.Collection, persis.Collection) {
		t.Helper()
		dir := t.TempDir()
		return file.NewCollection(dir, file.WithIndentedJSON()), file.NewCollection(dir, file.WithIndentedJSON())
	}
	testutil.RunCollectionContract(t, factory)
}

func TestFileCollectionNilRecordReturnsError(t *testing.T) {
	t.Parallel()

	col := file.NewCollection(t.TempDir())
	for name, operation := range map[string]func() error{
		"put":                func() error { return col.Put(t.Context(), nil) },
		"create":             func() error { return col.Create(t.Context(), nil) },
		"compare_and_delete": func() error { return col.CompareAndDelete(t.Context(), nil) },
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t, operation(), "nil record")
		})
	}
}

func TestFileCollectionGetReportsTypedCorruption(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "broken.json"), nil, 0o600))

	_, err := file.NewCollection(root).Get(context.Background(), "broken")
	assert.ErrorIs(t, err, persis.ErrCorrupt)
}

func TestFileCollectionListingIgnoresLockMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	col := file.NewCollection(root)
	record := &persis.Record{
		ID:        "queue/item",
		Data:      []byte(`{"value":1}`),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, col.Put(ctx, record))

	for _, name := range []string{
		dirlock.LockDirectoryName,
		dirlock.LockDirectoryName + ".releasing.1234.test",
	} {
		lockDir := filepath.Join(root, "queue", name)
		require.NoError(t, os.MkdirAll(lockDir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(lockDir, "metadata.json"), []byte(`{}`), 0o600))
	}

	ids, err := col.RecordIDs(ctx, "queue/")
	require.NoError(t, err)
	assert.Equal(t, []string{"queue/item"}, ids)

	page, err := col.List(ctx, persis.ListQuery{Prefix: "queue/"})
	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	assert.Equal(t, record.ID, page.Records[0].ID)
}
