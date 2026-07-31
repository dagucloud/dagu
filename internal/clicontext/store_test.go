// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package clicontext

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_CRUDAndCurrent(t *testing.T) {
	t.Parallel()

	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)

	store, err := NewStore(t.TempDir(), enc)
	require.NoError(t, err)

	current, err := store.Current(context.Background())
	require.NoError(t, err)
	assert.Equal(t, LocalContextName, current)

	err = store.Create(context.Background(), &Context{
		Name:           "prod",
		ServerURL:      "https://example.com",
		APIKey:         "dagu_test_123",
		Description:    "production",
		SkipTLSVerify:  true,
		TimeoutSeconds: 15,
	})
	require.NoError(t, err)

	item, err := store.Get(context.Background(), "prod")
	require.NoError(t, err)
	assert.Equal(t, "https://example.com", item.ServerURL)
	assert.Equal(t, "dagu_test_123", item.APIKey)
	assert.True(t, item.SkipTLSVerify)

	items, err := store.List(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "prod", items[0].Name)

	require.NoError(t, store.Use(context.Background(), "prod"))
	current, err = store.Current(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "prod", current)

	require.NoError(t, store.Delete(context.Background(), "prod"))
	current, err = store.Current(context.Background())
	require.NoError(t, err)
	assert.Equal(t, LocalContextName, current)
}

func TestStore_ValidateContext(t *testing.T) {
	t.Parallel()

	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)

	store, err := NewStore(t.TempDir(), enc)
	require.NoError(t, err)

	tests := []struct {
		name    string
		ctx     *Context
		wantErr string
	}{
		{
			name: "missing name",
			ctx: &Context{
				ServerURL: "https://example.com",
				APIKey:    "dagu_test",
			},
			wantErr: "context name is required",
		},
		{
			name: "reserved local",
			ctx: &Context{
				Name:      LocalContextName,
				ServerURL: "https://example.com",
				APIKey:    "dagu_test",
			},
			wantErr: "\"local\"",
		},
		{
			name: "reserved current",
			ctx: &Context{
				Name:      "current",
				ServerURL: "https://example.com",
				APIKey:    "dagu_test",
			},
			wantErr: "\"current\"",
		},
		{
			name: "invalid url",
			ctx: &Context{
				Name:      "prod",
				ServerURL: "://bad",
				APIKey:    "dagu_test",
			},
			wantErr: "invalid server URL",
		},
		{
			name: "invalid api key",
			ctx: &Context{
				Name:      "prod",
				ServerURL: "https://example.com",
				APIKey:    "token",
			},
			wantErr: "api key must use the dagu_ prefix",
		},
		{
			name: "path separator",
			ctx: &Context{
				Name:      "prod/east",
				ServerURL: "https://example.com",
				APIKey:    "dagu_test",
			},
			wantErr: "path separators",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.ValidateContext(tt.ctx)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestStore_CreateNormalizesValues(t *testing.T) {
	t.Parallel()

	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)

	store, err := NewStore(t.TempDir(), enc)
	require.NoError(t, err)

	item := &Context{
		Name:      " prod ",
		ServerURL: " https://example.com ",
		APIKey:    " dagu_test ",
	}
	require.NoError(t, store.Create(context.Background(), item))
	assert.Equal(t, "prod", item.Name)
	assert.Equal(t, "https://example.com", item.ServerURL)
	assert.Equal(t, "dagu_test", item.APIKey)

	stored, err := store.Get(context.Background(), "prod")
	require.NoError(t, err)
	assert.Equal(t, "prod", stored.Name)
	assert.Equal(t, "https://example.com", stored.ServerURL)
	assert.Equal(t, "dagu_test", stored.APIKey)
}

func TestStore_ListReturnsPartialResultsAndErrorOnCorruptEntry(t *testing.T) {
	t.Parallel()

	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)

	baseDir := t.TempDir()
	store, err := NewStore(baseDir, enc)
	require.NoError(t, err)

	require.NoError(t, store.Create(context.Background(), &Context{
		Name:      "prod",
		ServerURL: "https://example.com",
		APIKey:    "dagu_test",
	}))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "broken.json"), []byte("not-json"), 0o600))

	items, err := store.List(context.Background())
	require.Error(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "prod", items[0].Name)
	assert.Contains(t, err.Error(), "broken.json")
}
