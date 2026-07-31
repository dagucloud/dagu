// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package remotenode

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStore is a test double for the Store interface.
type mockStore struct {
	nodes   []*RemoteNode
	listErr error
}

func (m *mockStore) Create(_ context.Context, _ *RemoteNode) error {
	return nil
}

func (m *mockStore) GetByID(_ context.Context, id string) (*RemoteNode, error) {
	for _, n := range m.nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return nil, ErrRemoteNodeNotFound
}

func (m *mockStore) GetByName(_ context.Context, name string) (*RemoteNode, error) {
	for _, n := range m.nodes {
		if n.Name == name {
			return n, nil
		}
	}
	return nil, ErrRemoteNodeNotFound
}

func (m *mockStore) List(_ context.Context) ([]*RemoteNode, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.nodes, nil
}

func (m *mockStore) Update(_ context.Context, _ *RemoteNode) error {
	return nil
}

func (m *mockStore) Delete(_ context.Context, _ string) error {
	return nil
}

// errMockStore always returns an error for GetByName.
type errMockStore struct {
	mockStore
	getByNameErr error
}

func (m *errMockStore) GetByName(_ context.Context, _ string) (*RemoteNode, error) {
	return nil, m.getByNameErr
}

func TestToConfigNode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *RemoteNode
		expected config.RemoteNode
	}{
		{
			name: "basic auth with description",
			input: &RemoteNode{
				Name:              "node1",
				Description:       "Test node",
				APIBaseURL:        "http://example.com/api",
				AuthType:          AuthTypeBasic,
				BasicAuthUsername: "user",
				BasicAuthPassword: "pass",
				SkipTLSVerify:     true,
			},
			expected: config.RemoteNode{
				Name:              "node1",
				Description:       "Test node",
				APIBaseURL:        "http://example.com/api",
				AuthType:          "basic",
				BasicAuthUsername: "user",
				BasicAuthPassword: "pass",
				SkipTLSVerify:     true,
			},
		},
		{
			name: "token auth without description",
			input: &RemoteNode{
				Name:       "node2",
				APIBaseURL: "http://example.com/api",
				AuthType:   AuthTypeToken,
				AuthToken:  "tok-123",
			},
			expected: config.RemoteNode{
				Name:       "node2",
				APIBaseURL: "http://example.com/api",
				AuthType:   "token",
				AuthToken:  "tok-123",
			},
		},
		{
			name: "no auth",
			input: &RemoteNode{
				Name:       "node3",
				APIBaseURL: "http://example.com/api",
				AuthType:   AuthTypeNone,
			},
			expected: config.RemoteNode{
				Name:       "node3",
				APIBaseURL: "http://example.com/api",
				AuthType:   "none",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ToConfigNode(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFromConfigNode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    config.RemoteNode
		expected *RemoteNode
	}{
		{
			name: "basic auth with description",
			input: config.RemoteNode{
				Name:              "node1",
				Description:       "Test node",
				APIBaseURL:        "http://example.com/api",
				AuthType:          "basic",
				BasicAuthUsername: "user",
				BasicAuthPassword: "pass",
				SkipTLSVerify:     true,
			},
			expected: &RemoteNode{
				ID:                "cfg:node1",
				Name:              "node1",
				Description:       "Test node",
				APIBaseURL:        "http://example.com/api",
				AuthType:          AuthTypeBasic,
				BasicAuthUsername: "user",
				BasicAuthPassword: "pass",
				SkipTLSVerify:     true,
			},
		},
		{
			name: "token auth without description",
			input: config.RemoteNode{
				Name:       "node2",
				APIBaseURL: "http://example.com/api",
				AuthType:   "token",
				AuthToken:  "tok-123",
			},
			expected: &RemoteNode{
				ID:         "cfg:node2",
				Name:       "node2",
				APIBaseURL: "http://example.com/api",
				AuthType:   AuthTypeToken,
				AuthToken:  "tok-123",
			},
		},
		{
			name: "no auth",
			input: config.RemoteNode{
				Name:       "node3",
				APIBaseURL: "http://example.com/api",
			},
			expected: &RemoteNode{
				ID:         "cfg:node3",
				Name:       "node3",
				APIBaseURL: "http://example.com/api",
				AuthType:   AuthTypeNone,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := FromConfigNode(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRoundTripConfigNode(t *testing.T) {
	t.Parallel()

	original := &RemoteNode{
		Name:              "roundtrip",
		Description:       "Round-trip test",
		APIBaseURL:        "http://example.com/api",
		AuthType:          AuthTypeBasic,
		BasicAuthUsername: "user",
		BasicAuthPassword: "pass",
		SkipTLSVerify:     true,
	}

	cn := ToConfigNode(original)
	result := FromConfigNode(cn)

	assert.Equal(t, original.Name, result.Name)
	assert.Equal(t, original.Description, result.Description)
	assert.Equal(t, original.APIBaseURL, result.APIBaseURL)
	assert.Equal(t, original.AuthType, result.AuthType)
	assert.Equal(t, original.BasicAuthUsername, result.BasicAuthUsername)
	assert.Equal(t, original.BasicAuthPassword, result.BasicAuthPassword)
	assert.Equal(t, original.SkipTLSVerify, result.SkipTLSVerify)
}

func TestResolver_GetByName(t *testing.T) {
	t.Parallel()

	t.Run("StoreNodeFound", func(t *testing.T) {
		t.Parallel()
		storeNode := &RemoteNode{ID: "uuid-1", Name: "node1", APIBaseURL: "http://store.example.com"}
		store := &mockStore{nodes: []*RemoteNode{storeNode}}
		resolver := NewResolver([]config.RemoteNode{
			{Name: "node1", APIBaseURL: "http://config.example.com"},
		}, store)

		result, err := resolver.GetByName(context.Background(), "node1")
		require.NoError(t, err)
		assert.Equal(t, "uuid-1", result.ID)
		assert.Equal(t, "http://store.example.com", result.APIBaseURL)
	})

	t.Run("StoreMissConfigFallback", func(t *testing.T) {
		t.Parallel()
		store := &mockStore{nodes: nil}
		resolver := NewResolver([]config.RemoteNode{
			{Name: "node1", APIBaseURL: "http://config.example.com"},
		}, store)

		result, err := resolver.GetByName(context.Background(), "node1")
		require.NoError(t, err)
		assert.Equal(t, "cfg:node1", result.ID)
		assert.Equal(t, "http://config.example.com", result.APIBaseURL)
	})

	t.Run("BothMissNotFound", func(t *testing.T) {
		t.Parallel()
		store := &mockStore{nodes: nil}
		resolver := NewResolver(nil, store)

		_, err := resolver.GetByName(context.Background(), "missing")
		require.ErrorIs(t, err, ErrRemoteNodeNotFound)
	})

	t.Run("StoreErrorPropagated", func(t *testing.T) {
		t.Parallel()
		storeErr := errors.New("database connection failed")
		store := &errMockStore{getByNameErr: storeErr}
		resolver := NewResolver([]config.RemoteNode{
			{Name: "node1", APIBaseURL: "http://config.example.com"},
		}, store)

		_, err := resolver.GetByName(context.Background(), "node1")
		require.Error(t, err)
		assert.Equal(t, storeErr, err)
	})

	t.Run("NilStoreConfigHit", func(t *testing.T) {
		t.Parallel()
		resolver := NewResolver([]config.RemoteNode{
			{Name: "node1", APIBaseURL: "http://config.example.com"},
		}, nil)

		result, err := resolver.GetByName(context.Background(), "node1")
		require.NoError(t, err)
		assert.Equal(t, "cfg:node1", result.ID)
	})

	t.Run("NilStoreMissNotFound", func(t *testing.T) {
		t.Parallel()
		resolver := NewResolver(nil, nil)

		_, err := resolver.GetByName(context.Background(), "missing")
		require.ErrorIs(t, err, ErrRemoteNodeNotFound)
	})
}

func TestResolver_ListAll(t *testing.T) {
	t.Parallel()

	t.Run("ConfigOnly", func(t *testing.T) {
		t.Parallel()
		resolver := NewResolver([]config.RemoteNode{
			{Name: "node1", APIBaseURL: "http://a.example.com"},
			{Name: "node2", APIBaseURL: "http://b.example.com"},
		}, nil)

		result, err := resolver.ListAll(context.Background())
		require.NoError(t, err)
		assert.Len(t, result, 2)
		for _, n := range result {
			assert.Equal(t, SourceConfig, n.Source)
		}
	})

	t.Run("StoreAndConfigNoOverlap", func(t *testing.T) {
		t.Parallel()
		store := &mockStore{nodes: []*RemoteNode{
			{ID: "uuid-1", Name: "store-node", APIBaseURL: "http://store.example.com"},
		}}
		resolver := NewResolver([]config.RemoteNode{
			{Name: "config-node", APIBaseURL: "http://config.example.com"},
		}, store)

		result, err := resolver.ListAll(context.Background())
		require.NoError(t, err)
		assert.Len(t, result, 2)

		names := make(map[string]Source)
		for _, n := range result {
			names[n.Name] = n.Source
		}
		assert.Equal(t, SourceStore, names["store-node"])
		assert.Equal(t, SourceConfig, names["config-node"])
	})

	t.Run("StoreOverridesConfigOnCollision", func(t *testing.T) {
		t.Parallel()
		store := &mockStore{nodes: []*RemoteNode{
			{ID: "uuid-1", Name: "shared", APIBaseURL: "http://store.example.com"},
		}}
		resolver := NewResolver([]config.RemoteNode{
			{Name: "shared", APIBaseURL: "http://config.example.com"},
		}, store)

		result, err := resolver.ListAll(context.Background())
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "http://store.example.com", result[0].APIBaseURL)
		assert.Equal(t, SourceStore, result[0].Source)
	})
}

func TestResolver_ListNames(t *testing.T) {
	t.Parallel()

	store := &mockStore{nodes: []*RemoteNode{
		{ID: "uuid-1", Name: "store-node"},
	}}
	resolver := NewResolver([]config.RemoteNode{
		{Name: "config-node"},
	}, store)

	names, err := resolver.ListNames(context.Background())
	require.NoError(t, err)
	assert.Len(t, names, 2)
	assert.Contains(t, names, "store-node")
	assert.Contains(t, names, "config-node")
}

func TestResolver_ListAll_StoreError(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		listErr: errors.New("disk failure"),
	}
	resolver := NewResolver([]config.RemoteNode{
		{Name: "config-node", APIBaseURL: "http://config.example.com"},
	}, store)

	result, err := resolver.ListAll(context.Background())
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "disk failure")
}

func TestResolver_GetConfigByName(t *testing.T) {
	t.Parallel()

	store := &mockStore{nodes: []*RemoteNode{
		{ID: "uuid-1", Name: "shared", APIBaseURL: "http://store.example.com"},
	}}
	resolver := NewResolver([]config.RemoteNode{
		{Name: "shared", APIBaseURL: "http://config.example.com"},
		{Name: "config-only", APIBaseURL: "http://config-only.example.com"},
	}, store)

	t.Run("ReturnsConfigNodeEvenWhenStoreHasSameName", func(t *testing.T) {
		t.Parallel()
		node, err := resolver.GetConfigByName("shared")
		require.NoError(t, err)
		assert.Equal(t, "http://config.example.com", node.APIBaseURL)
	})

	t.Run("ReturnsConfigOnlyNode", func(t *testing.T) {
		t.Parallel()
		node, err := resolver.GetConfigByName("config-only")
		require.NoError(t, err)
		assert.Equal(t, "http://config-only.example.com", node.APIBaseURL)
	})

	t.Run("NotFoundReturnsError", func(t *testing.T) {
		t.Parallel()
		_, err := resolver.GetConfigByName("nonexistent")
		assert.ErrorIs(t, err, ErrRemoteNodeNotFound)
	})
}
