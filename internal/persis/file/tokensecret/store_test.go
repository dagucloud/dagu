// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tokensecret

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore(t *testing.T) {
	t.Run("auto-generates when file missing", func(t *testing.T) {
		dir := t.TempDir()
		authDir := filepath.Join(dir, "auth")

		s := New(authDir)
		ts, err := s.Resolve(context.Background())
		require.NoError(t, err)
		assert.True(t, ts.IsValid())

		// Secret file should exist with correct permissions.
		path := filepath.Join(authDir, "token_secret")
		info, err := os.Stat(path)
		require.NoError(t, err)
		if testutil.SupportsPOSIXPermissionBits() {
			assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
		}

		// Generated secret should be 43 chars (32 bytes base64url, no padding).
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Len(t, string(data), 43)
	})

	t.Run("reads existing file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "token_secret")
		require.NoError(t, os.WriteFile(path, []byte("existing-secret"), 0600))

		s := New(dir)
		ts, err := s.Resolve(context.Background())
		require.NoError(t, err)
		assert.Equal(t, []byte("existing-secret"), ts.SigningKey())
	})

	t.Run("regenerates on empty file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "token_secret")
		require.NoError(t, os.WriteFile(path, []byte(""), 0600))

		s := New(dir)
		ts, err := s.Resolve(context.Background())
		require.NoError(t, err)
		assert.True(t, ts.IsValid())

		// File should now contain a generated secret.
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Len(t, string(data), 43)
	})

	t.Run("regenerates on whitespace-only file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "token_secret")
		require.NoError(t, os.WriteFile(path, []byte("  \n\t  "), 0600))

		s := New(dir)
		ts, err := s.Resolve(context.Background())
		require.NoError(t, err)
		assert.True(t, ts.IsValid())
	})

	t.Run("stable across calls", func(t *testing.T) {
		dir := t.TempDir()
		authDir := filepath.Join(dir, "auth")

		s := New(authDir)

		ts1, err := s.Resolve(context.Background())
		require.NoError(t, err)

		ts2, err := s.Resolve(context.Background())
		require.NoError(t, err)

		assert.Equal(t, ts1.SigningKey(), ts2.SigningKey())
	})

	t.Run("directory permissions", func(t *testing.T) {
		dir := t.TempDir()
		authDir := filepath.Join(dir, "auth")

		s := New(authDir)
		_, err := s.Resolve(context.Background())
		require.NoError(t, err)

		info, err := os.Stat(authDir)
		require.NoError(t, err)
		if testutil.SupportsPOSIXPermissionBits() {
			assert.Equal(t, os.FileMode(0700), info.Mode().Perm())
		}
	})

	t.Run("concurrent resolve converges to same secret", func(t *testing.T) {
		dir := t.TempDir()
		authDir := filepath.Join(dir, "auth")

		const goroutines = 10
		secrets := make([][]byte, goroutines)
		errs := make([]error, goroutines)

		var wg sync.WaitGroup
		wg.Add(goroutines)
		for i := range goroutines {
			go func(idx int) {
				defer wg.Done()
				s := New(authDir)
				ts, err := s.Resolve(context.Background())
				errs[idx] = err
				if err == nil {
					secrets[idx] = ts.SigningKey()
				}
			}(i)
		}
		wg.Wait()

		// All goroutines should succeed.
		for i, err := range errs {
			require.NoError(t, err, "goroutine %d", i)
		}

		// All goroutines should converge to the same secret.
		for i := 1; i < goroutines; i++ {
			assert.Equal(t, secrets[0], secrets[i], "goroutine %d has different secret", i)
		}
	})
}
