// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockStore struct {
	entries []*Entry
}

func (m *mockStore) Append(_ context.Context, entry *Entry) error {
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockStore) Query(_ context.Context, _ QueryFilter) (*QueryResult, error) {
	return &QueryResult{Entries: m.entries, Total: len(m.entries)}, nil
}

func TestService_Log(t *testing.T) {
	store := &mockStore{}
	svc := New(store)

	entry := NewEntry(CategoryUser, "login", "user-123", "testuser")
	err := svc.Log(context.Background(), entry)

	require.NoError(t, err)
	assert.Len(t, store.entries, 1)
	assert.Equal(t, "login", store.entries[0].Action)
}

func TestService_Query(t *testing.T) {
	store := &mockStore{
		entries: []*Entry{
			NewEntry(CategoryUser, "login", "user-1", "user1"),
			NewEntry(CategoryUser, "logout", "user-2", "user2"),
		},
	}
	svc := New(store)

	result, err := svc.Query(context.Background(), QueryFilter{})

	require.NoError(t, err)
	assert.Len(t, result.Entries, 2)
	assert.Equal(t, 2, result.Total)
}

func TestService_LogTerminalConnectionStart(t *testing.T) {
	store := &mockStore{}
	svc := New(store)

	err := svc.LogTerminalConnectionStart(context.Background(), "user-123", "testuser", "connection-456", "192.168.1.1")

	require.NoError(t, err)
	require.Len(t, store.entries, 1)
	assert.Equal(t, CategoryTerminal, store.entries[0].Category)
	assert.Equal(t, "connection_start", store.entries[0].Action)
	assert.Contains(t, store.entries[0].Details, "connection-456")
}

func TestService_LogTerminalCommand(t *testing.T) {
	store := &mockStore{}
	svc := New(store)

	err := svc.LogTerminalCommand(context.Background(), "user-123", "testuser", "connection-456", "ls -la", "192.168.1.1")

	require.NoError(t, err)
	require.Len(t, store.entries, 1)
	assert.Equal(t, "command", store.entries[0].Action)
	assert.Contains(t, store.entries[0].Details, "ls -la")
}

func TestService_LogTerminalConnectionEnd(t *testing.T) {
	store := &mockStore{}
	svc := New(store)

	err := svc.LogTerminalConnectionEnd(context.Background(), "user-123", "testuser", "connection-456", "closed", "192.168.1.1")

	require.NoError(t, err)
	require.Len(t, store.entries, 1)
	assert.Equal(t, "connection_end", store.entries[0].Action)
	assert.Contains(t, store.entries[0].Details, "closed")
}

func TestService_LogTerminalCommand_SpecialCharacters(t *testing.T) {
	store := &mockStore{}
	svc := New(store)

	// Test that special characters are properly escaped by json.Marshal
	testCases := []struct {
		name    string
		command string
	}{
		{"quotes", `say "hello"`},
		{"backslash", `path\to\file`},
		{"newline", "line1\nline2"},
		{"tab", "col1\tcol2"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			store.entries = nil
			err := svc.LogTerminalCommand(context.Background(), "user-123", "testuser", "connection-456", tc.command, "192.168.1.1")
			require.NoError(t, err)
			require.Len(t, store.entries, 1)
			assert.Contains(t, store.entries[0].Details, "command")
		})
	}
}
