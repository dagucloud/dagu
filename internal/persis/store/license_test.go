// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func sampleActivation() *license.ActivationData {
	return &license.ActivationData{
		Token:           "tok-abc123",
		HeartbeatSecret: "hb-secret-xyz",
		LicenseKey:      "LK-0000-1111-2222-3333",
		ServerID:        "srv-deadbeef",
	}
}

func newMemoryLicenseStore() *store.LicenseStore {
	col := testutil.NewMemoryBackend().Collection("license")
	return store.NewLicenseStore(col)
}

// newFileLicenseStore constructs the store the same way production wiring does.
func newFileLicenseStore(t *testing.T, dir string) *store.LicenseStore {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	return store.NewLicenseStore(file.NewCollection(dir, file.WithIndentedJSON()))
}

// ─── Interface-level tests (memory-backed) ────────────────────────────────────

func TestLicenseStore_SaveLoad_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newMemoryLicenseStore()

	original := sampleActivation()
	require.NoError(t, s.Save(original))

	loaded, err := s.Load()
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, *original, *loaded)
}

func TestLicenseStore_Save_OverwriteReturnsUpdatedData(t *testing.T) {
	t.Parallel()
	s := newMemoryLicenseStore()

	require.NoError(t, s.Save(sampleActivation()))

	updated := &license.ActivationData{
		Token:           "updated-token",
		HeartbeatSecret: "updated-secret",
		LicenseKey:      "LK-UPDATED",
		ServerID:        "srv-updated",
	}
	require.NoError(t, s.Save(updated))

	loaded, err := s.Load()
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, *updated, *loaded)
}

func TestLicenseStore_Load_NoRecord(t *testing.T) {
	t.Parallel()
	s := newMemoryLicenseStore()

	got, err := s.Load()
	assert.NoError(t, err)
	assert.Nil(t, got)
}

func TestLicenseStore_Remove_Idempotent(t *testing.T) {
	t.Parallel()
	s := newMemoryLicenseStore()

	// Remove without a prior Save must not error.
	assert.NoError(t, s.Remove())

	// Remove after Save must delete the record so subsequent Load returns nil.
	require.NoError(t, s.Save(sampleActivation()))
	require.NoError(t, s.Remove())

	got, err := s.Load()
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ─── File-layout compatibility tests ──────────────────────────────────────────

// On-disk bytes equal json.MarshalIndent(v, "", "  ") at {dir}/activation.json.
func TestLicenseStore_File_OnDiskFormatMatchesReleasedJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileLicenseStore(t, dir)

	ad := sampleActivation()
	require.NoError(t, s.Save(ad))

	got, err := os.ReadFile(filepath.Join(dir, "activation.json"))
	require.NoError(t, err)

	expected, err := json.MarshalIndent(ad, "", "  ")
	require.NoError(t, err)

	assert.True(t, bytes.Equal(got, expected),
		"on-disk bytes must equal json.MarshalIndent output\n  got:  %q\n  want: %q",
		string(got), string(expected))
}

// Files written by older binaries (json.MarshalIndent, 0o600) decode through Load.
func TestLicenseStore_File_ReadsReleasedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(dir, 0o700))

	ad := sampleActivation()
	raw, err := json.MarshalIndent(ad, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "activation.json"), raw, 0o600))

	s := store.NewLicenseStore(file.NewCollection(dir, file.WithIndentedJSON()))
	loaded, err := s.Load()
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, *ad, *loaded)
}

func TestLicenseStore_File_FilePermissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileLicenseStore(t, dir)

	require.NoError(t, s.Save(sampleActivation()))

	info, err := os.Stat(filepath.Join(dir, "activation.json"))
	require.NoError(t, err)

	if testutil.SupportsPOSIXPermissionBits() {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
			"activation file must be 0600")
	}
}

func TestLicenseStore_File_DirPermissions(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dir := filepath.Join(base, "license")
	s := newFileLicenseStore(t, dir)

	require.NoError(t, s.Save(sampleActivation()))

	info, err := os.Stat(dir)
	require.NoError(t, err)

	if testutil.SupportsPOSIXPermissionBits() {
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(),
			"license directory must be 0700")
	}
}

func TestLicenseStore_File_Save_PrettyPrinted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileLicenseStore(t, dir)

	require.NoError(t, s.Save(sampleActivation()))

	raw, err := os.ReadFile(filepath.Join(dir, "activation.json"))
	require.NoError(t, err)
	content := string(raw)
	assert.True(t, strings.Contains(content, "\n"), "on-disk JSON must be newline-separated")
	assert.True(t, strings.Contains(content, "  "), "on-disk JSON must be 2-space indented")
}

func TestLicenseStore_File_Load_InvalidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "activation.json"), []byte("{not valid json!!"), 0o600))

	s := store.NewLicenseStore(file.NewCollection(dir, file.WithIndentedJSON()))
	got, err := s.Load()
	require.Error(t, err)
	assert.Nil(t, got)
}

func TestLicenseStore_File_Remove_DeletesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileLicenseStore(t, dir)

	require.NoError(t, s.Save(sampleActivation()))
	require.NoError(t, s.Remove())

	_, err := os.Stat(filepath.Join(dir, "activation.json"))
	assert.True(t, os.IsNotExist(err), "activation file must not exist after Remove")
}

func TestLicenseStore_File_Concurrent_SaveLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileLicenseStore(t, dir)

	require.NoError(t, s.Save(sampleActivation()))

	const numWorkers = 10
	var wg sync.WaitGroup
	errCh := make(chan error, numWorkers*2)

	for range numWorkers {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := s.Save(sampleActivation()); err != nil {
				errCh <- err
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := s.Load(); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	assert.Empty(t, errs)
}
