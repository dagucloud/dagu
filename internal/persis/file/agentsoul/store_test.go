// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentsoul

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_CreateAndGetByID(t *testing.T) {
	dir := t.TempDir()
	store, err := New(context.Background(), dir)
	require.NoError(t, err)

	soul := &agent.Soul{
		ID:          "test-soul",
		Name:        "Test Soul",
		Description: "A test soul",
		Content:     "# Identity\n\nYou are a test soul.",
	}

	err = store.Create(context.Background(), soul)
	require.NoError(t, err)

	got, err := store.GetByID(context.Background(), "test-soul")
	require.NoError(t, err)
	assert.Equal(t, soul.ID, got.ID)
	assert.Equal(t, soul.Name, got.Name)
	assert.Equal(t, soul.Description, got.Description)
	assert.Equal(t, soul.Content, got.Content)
}

func TestStore_CreateDuplicate(t *testing.T) {
	dir := t.TempDir()
	store, err := New(context.Background(), dir)
	require.NoError(t, err)

	soul := &agent.Soul{
		ID:      "test-soul",
		Name:    "Test Soul",
		Content: "content",
	}
	err = store.Create(context.Background(), soul)
	require.NoError(t, err)

	err = store.Create(context.Background(), soul)
	assert.ErrorIs(t, err, agent.ErrSoulAlreadyExists)
}

func TestStore_List(t *testing.T) {
	dir := t.TempDir()
	store, err := New(context.Background(), dir)
	require.NoError(t, err)

	for _, s := range []*agent.Soul{
		{ID: "b-soul", Name: "Beta", Content: "b"},
		{ID: "a-soul", Name: "Alpha", Content: "a"},
	} {
		require.NoError(t, store.Create(context.Background(), s))
	}

	souls, err := store.List(context.Background())
	require.NoError(t, err)
	require.Len(t, souls, 2)
	assert.Equal(t, "Alpha", souls[0].Name)
	assert.Equal(t, "Beta", souls[1].Name)
}

func TestStore_Update(t *testing.T) {
	dir := t.TempDir()
	store, err := New(context.Background(), dir)
	require.NoError(t, err)

	soul := &agent.Soul{
		ID:      "test-soul",
		Name:    "Original",
		Content: "original content",
	}
	require.NoError(t, store.Create(context.Background(), soul))

	soul.Name = "Updated"
	soul.Content = "updated content"
	require.NoError(t, store.Update(context.Background(), soul))

	got, err := store.GetByID(context.Background(), "test-soul")
	require.NoError(t, err)
	assert.Equal(t, "Updated", got.Name)
	assert.Equal(t, "updated content", got.Content)
}

func TestStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := New(context.Background(), dir)
	require.NoError(t, err)

	soul := &agent.Soul{
		ID:      "test-soul",
		Name:    "Test",
		Content: "content",
	}
	require.NoError(t, store.Create(context.Background(), soul))

	err = store.Delete(context.Background(), "test-soul")
	require.NoError(t, err)

	_, err = store.GetByID(context.Background(), "test-soul")
	assert.ErrorIs(t, err, agent.ErrSoulNotFound)
}

func TestStore_Search(t *testing.T) {
	dir := t.TempDir()
	store, err := New(context.Background(), dir)
	require.NoError(t, err)

	for _, s := range []*agent.Soul{
		{ID: "ops-soul", Name: "Ops Assistant", Content: "ops"},
		{ID: "dev-soul", Name: "Dev Assistant", Content: "dev"},
	} {
		require.NoError(t, store.Create(context.Background(), s))
	}

	result, err := store.Search(context.Background(), agent.SearchSoulsOptions{
		Query: "ops",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.TotalCount)
	assert.Equal(t, "ops-soul", result.Items[0].ID)
}

func TestStore_GetNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := New(context.Background(), dir)
	require.NoError(t, err)

	_, err = store.GetByID(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, agent.ErrSoulNotFound)
}

func TestStore_InvalidID(t *testing.T) {
	dir := t.TempDir()
	store, err := New(context.Background(), dir)
	require.NoError(t, err)

	soul := &agent.Soul{
		ID:      "INVALID",
		Name:    "Test",
		Content: "content",
	}
	err = store.Create(context.Background(), soul)
	assert.ErrorIs(t, err, agent.ErrInvalidSoulID)
}

func TestSeedExampleSouls(t *testing.T) {
	dir := t.TempDir()
	soulsDir := filepath.Join(dir, "souls")

	created, err := SeedExampleSouls(context.Background(), soulsDir)
	require.NoError(t, err)
	assert.True(t, created)

	// Verify marker file exists
	_, statErr := os.Stat(filepath.Join(soulsDir, examplesMarkerFile))
	assert.NoError(t, statErr)

	// Verify default soul exists
	_, statErr = os.Stat(filepath.Join(soulsDir, "default.md"))
	assert.NoError(t, statErr)

	// Second call should not re-seed
	created, err = SeedExampleSouls(context.Background(), soulsDir)
	require.NoError(t, err)
	assert.False(t, created)
}

func TestParseSoulFile(t *testing.T) {
	data := []byte(`---
name: Test Soul
description: A test
---
# Identity

You are a test soul.
`)

	soul, err := parseSoulFile(data, "test")
	require.NoError(t, err)
	assert.Equal(t, "test", soul.ID)
	assert.Equal(t, "Test Soul", soul.Name)
	assert.Equal(t, "A test", soul.Description)
	assert.Equal(t, "# Identity\n\nYou are a test soul.", soul.Content)
}

func TestParseSoulFile_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantErr     bool
		errContains string
		wantName    string
		wantContent string
	}{
		{
			name:        "missing opening delimiter",
			input:       "name: Test\n---\nBody here",
			wantErr:     true,
			errContains: "missing opening frontmatter delimiter",
		},
		{
			name:        "missing closing delimiter",
			input:       "---\nname: Test\nBody without closing",
			wantErr:     true,
			errContains: "missing closing frontmatter delimiter",
		},
		{
			name:        "empty name in frontmatter",
			input:       "---\nname: \"\"\ndescription: test\n---\nBody here",
			wantErr:     true,
			errContains: "name",
		},
		{
			name:        "CRLF line endings",
			input:       "---\r\nname: CRLF Soul\r\n---\r\nBody with CRLF\r\n",
			wantName:    "CRLF Soul",
			wantContent: "Body with CRLF",
		},
		{
			name:        "template-like syntax in body",
			input:       "---\nname: Template Soul\n---\nYou are {{.Something}} and use {{template}} things",
			wantName:    "Template Soul",
			wantContent: "You are {{.Something}} and use {{template}} things",
		},
		{
			name:        "no body after closing delimiter",
			input:       "---\nname: No Body\n---\n",
			wantName:    "No Body",
			wantContent: "",
		},
		{
			name:        "closing delimiter at EOF without trailing newline",
			input:       "---\nname: EOF Soul\n---",
			wantName:    "EOF Soul",
			wantContent: "",
		},
		{
			name:        "triple dash in body that is NOT a delimiter",
			input:       "---\nname: Dash Soul\n---\nSome text\n--- not a delimiter\nMore text",
			wantName:    "Dash Soul",
			wantContent: "Some text\n--- not a delimiter\nMore text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			soul, err := parseSoulFile([]byte(tt.input), "test-id")
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, soul.Name)
			assert.Equal(t, tt.wantContent, soul.Content)
		})
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	store, err := New(context.Background(), dir)
	require.NoError(t, err)

	ctx := context.Background()

	// Pre-populate 10 souls.
	for i := range 10 {
		require.NoError(t, store.Create(ctx, &agent.Soul{
			ID:      fmt.Sprintf("soul-%d", i),
			Name:    fmt.Sprintf("Soul %d", i),
			Content: fmt.Sprintf("Content for soul %d", i),
		}))
	}

	var wg sync.WaitGroup

	// 10 goroutines doing reads.
	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for range 20 {
				id := fmt.Sprintf("soul-%d", idx)
				_, _ = store.GetByID(ctx, id)
				_, _ = store.List(ctx)
				_, _ = store.Search(ctx, agent.SearchSoulsOptions{Query: "Soul"})
			}
		}(i)
	}

	// 10 goroutines doing writes (create + delete).
	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := range 5 {
				id := fmt.Sprintf("new-soul-%d-%d", idx, j)
				soul := &agent.Soul{
					ID:      id,
					Name:    fmt.Sprintf("New Soul %d-%d", idx, j),
					Content: "concurrent content",
				}
				_ = store.Create(ctx, soul)
				_ = store.Delete(ctx, id)
			}
		}(i)
	}

	wg.Wait()
}
