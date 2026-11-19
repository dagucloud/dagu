// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

const (
	raceKeyWinner = "sk-ant-api03-winner-0123456789abcdef"
	raceKeyLoser  = "sk-ant-api03-loser-ZYXWVUTSRQPONMLKJ"
)

// sharedStores are the stores of one data directory. Two services on them act as
// two servers that share the directory.
type sharedStores struct {
	secrets   *store.SecretStore
	endpoints *store.AIEndpointStore
	servers   *store.AIMCPServerStore
}

func newSharedStores(t *testing.T) sharedStores {
	t.Helper()
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	backend := testutil.NewMemoryBackend()
	secrets, err := store.NewSecretStore(backend.Collection("secrets"), enc)
	require.NoError(t, err)
	endpoints, err := store.NewAIEndpointStore(backend.Collection("ai_endpoints"))
	require.NoError(t, err)
	servers, err := store.NewAIMCPServerStore(backend.Collection("ai_mcp_servers"))
	require.NoError(t, err)
	return sharedStores{secrets: secrets, endpoints: endpoints, servers: servers}
}

func raceEndpointInput(key string) aiendpoint.CreateInput {
	return aiendpoint.CreateInput{
		Name:     "cle1",
		Preset:   "anthropic",
		Protocol: aiendpoint.ProtocolAnthropic,
		Scope:    aiendpoint.GlobalScope(),
		Allow:    aiendpoint.Allow{Workspaces: []string{"*"}, DAGs: []string{"*"}},
		Key:      key,
		Actor:    "admin",
	}
}

func raceMCPInput(token string) aiendpoint.MCPCreateInput {
	return aiendpoint.MCPCreateInput{
		Name:        "docs",
		URL:         "https://docs.example.com/mcp",
		Scope:       aiendpoint.GlobalScope(),
		Allow:       aiendpoint.Allow{Workspaces: []string{"*"}, DAGs: []string{"*"}},
		Credentials: aiendpoint.MCPCredentialsInput{Token: token},
		Actor:       "admin",
	}
}

// interleavedEndpointStore runs before once, just ahead of the first Create, so
// that another server wins the name in the middle of a create.
type interleavedEndpointStore struct {
	aiendpoint.Store
	before func()
}

func (s *interleavedEndpointStore) Create(ctx context.Context, ep *aiendpoint.Endpoint) error {
	if before := s.before; before != nil {
		s.before = nil
		before()
	}
	return s.Store.Create(ctx, ep)
}

// interleavedMCPStore is interleavedEndpointStore for the MCP server entries.
type interleavedMCPStore struct {
	aiendpoint.MCPStore
	before func()
}

func (s *interleavedMCPStore) Create(ctx context.Context, server *aiendpoint.MCPServer) error {
	if before := s.before; before != nil {
		s.before = nil
		before()
	}
	return s.MCPStore.Create(ctx, server)
}

// refusingSecrets is a secret store whose Create fails.
type refusingSecrets struct{ secret.Store }

func (refusingSecrets) Create(context.Context, *secret.Secret, *secret.WriteValueInput) error {
	return errors.New("disk full")
}

// Two servers on one data directory create the same endpoint at once. The
// create that loses the name leaves the key of the winner as it was.
func TestEndpointCreateRaceKeepsTheKeyOfTheWinner(t *testing.T) {
	ctx := context.Background()
	stores := newSharedStores(t)
	winner, err := aiendpoint.NewService(stores.endpoints, stores.secrets)
	require.NoError(t, err)
	interleaved := &interleavedEndpointStore{Store: stores.endpoints}
	loser, err := aiendpoint.NewService(interleaved, stores.secrets)
	require.NoError(t, err)

	interleaved.before = func() {
		_, err := winner.Create(ctx, raceEndpointInput(raceKeyWinner))
		require.NoError(t, err)
	}
	_, err = loser.Create(ctx, raceEndpointInput(raceKeyLoser))
	require.ErrorIs(t, err, aiendpoint.ErrAlreadyExists)

	saved, err := winner.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	key, err := winner.ReadKey(ctx, *saved)
	require.NoError(t, err)
	assert.Equal(t, raceKeyWinner, key)
	_, fingerprint := aiendpoint.KeyDisplay(raceKeyWinner)
	assert.Equal(t, fingerprint, saved.KeyFingerprint)
}

// A create whose key cannot be saved leaves no endpoint, so the name stays free.
func TestEndpointCreateThatFailsFreesTheName(t *testing.T) {
	ctx := context.Background()
	stores := newSharedStores(t)
	broken, err := aiendpoint.NewService(stores.endpoints, refusingSecrets{stores.secrets})
	require.NoError(t, err)

	_, err = broken.Create(ctx, raceEndpointInput(raceKeyLoser))
	require.Error(t, err)
	_, err = broken.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	require.ErrorIs(t, err, aiendpoint.ErrNotFound)

	svc, err := aiendpoint.NewService(stores.endpoints, stores.secrets)
	require.NoError(t, err)
	_, err = svc.Create(ctx, raceEndpointInput(raceKeyWinner))
	require.NoError(t, err)
}

// The MCP server entries follow the same rule as the endpoints.
func TestMCPCreateRaceKeepsTheCredentialsOfTheWinner(t *testing.T) {
	ctx := context.Background()
	stores := newSharedStores(t)
	winner, err := aiendpoint.NewMCPService(stores.servers, stores.secrets)
	require.NoError(t, err)
	interleaved := &interleavedMCPStore{MCPStore: stores.servers}
	loser, err := aiendpoint.NewMCPService(interleaved, stores.secrets)
	require.NoError(t, err)

	interleaved.before = func() {
		_, err := winner.Create(ctx, raceMCPInput(raceKeyWinner))
		require.NoError(t, err)
	}
	_, err = loser.Create(ctx, raceMCPInput(raceKeyLoser))
	require.ErrorIs(t, err, aiendpoint.ErrAlreadyExists)

	saved, err := winner.Get(ctx, aiendpoint.GlobalScope(), "docs")
	require.NoError(t, err)
	credentials, err := winner.ReadCredentials(ctx, *saved)
	require.NoError(t, err)
	assert.Equal(t, raceKeyWinner, credentials.Token)
}

// A MCP create whose credentials cannot be saved leaves no entry.
func TestMCPCreateThatFailsFreesTheName(t *testing.T) {
	ctx := context.Background()
	stores := newSharedStores(t)
	broken, err := aiendpoint.NewMCPService(stores.servers, refusingSecrets{stores.secrets})
	require.NoError(t, err)

	_, err = broken.Create(ctx, raceMCPInput(raceKeyLoser))
	require.Error(t, err)
	_, err = broken.Get(ctx, aiendpoint.GlobalScope(), "docs")
	require.ErrorIs(t, err, aiendpoint.ErrNotFound)
}
