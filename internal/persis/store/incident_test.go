// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/incident"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

func newMemoryIncidentStore(t *testing.T) (*store.IncidentStore, persis.Collection) {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("incidents")
	s, err := store.NewIncidentStore(col, newTestEncryptor(t))
	require.NoError(t, err)
	return s, col
}

func TestIncidentStoreEncryptsProviderSecrets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, col := newMemoryIncidentStore(t)
	provider, err := incident.NormalizeProvider(&incident.Provider{
		Name:    "PagerDuty",
		Type:    incident.ProviderPagerDuty,
		Enabled: true,
		PagerDuty: &incident.PagerDutyProvider{
			RoutingKey: "pagerduty-routing-key",
		},
	}, "user-1")
	require.NoError(t, err)

	require.NoError(t, s.SaveProvider(ctx, provider))
	page, err := col.List(ctx, persis.ListQuery{Prefix: "providers/"})
	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	assert.False(t, bytes.Contains(page.Records[0].Data, []byte("pagerduty-routing-key")))

	loaded, err := s.GetProvider(ctx, provider.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.PagerDuty)
	assert.Equal(t, "pagerduty-routing-key", loaded.PagerDuty.RoutingKey)

	unencrypted, err := store.NewIncidentStore(testutil.NewMemoryBackend().Collection("incidents"), nil)
	require.NoError(t, err)
	assert.ErrorIs(t, unencrypted.SaveProvider(ctx, provider), incident.ErrSecretStoreMissing)
}

func TestIncidentStorePersistsPolicySetAndState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := newMemoryIncidentStore(t)
	policySet, err := incident.NormalizePolicySet(&incident.PolicySet{
		Scope:     incident.PolicyScopeWorkspace,
		Workspace: "ops",
		Enabled:   true,
		Policies: []incident.Policy{{
			ProviderID:        "provider-1",
			Enabled:           true,
			ResolveOnRecovery: true,
		}},
	}, "user-1")
	require.NoError(t, err)
	require.NoError(t, s.SavePolicySet(ctx, policySet))

	loadedPolicySet, err := s.GetPolicySet(ctx, incident.PolicyScopeWorkspace, "ops", "")
	require.NoError(t, err)
	assert.Equal(t, policySet.ID, loadedPolicySet.ID)
	assert.Equal(t, "ops", loadedPolicySet.Workspace)
	require.Len(t, loadedPolicySet.Policies, 1)
	assert.Equal(t, "provider-1", loadedPolicySet.Policies[0].ProviderID)

	state, err := incident.NormalizeState(&incident.IncidentState{
		ProviderID: "provider-1",
		PolicyID:   loadedPolicySet.Policies[0].ID,
		DAGName:    "daily",
		DedupKey:   "dagu:daily",
		Status:     incident.IncidentStatusOpen,
		OpenedAt:   time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NoError(t, s.SaveState(ctx, state))

	loadedState, err := s.GetState(ctx, state.ProviderID, state.DedupKey)
	require.NoError(t, err)
	assert.Equal(t, incident.IncidentStatusOpen, loadedState.Status)
	assert.Equal(t, "daily", loadedState.DAGName)

	openStates, err := s.ListOpenStates(ctx)
	require.NoError(t, err)
	require.Len(t, openStates, 1)
	assert.Equal(t, state.DedupKey, openStates[0].DedupKey)
}

func TestIncidentStoreNotFoundErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := newMemoryIncidentStore(t)

	_, err := s.GetProvider(ctx, "missing")
	assert.ErrorIs(t, err, incident.ErrProviderNotFound)
	_, err = s.GetPolicySet(ctx, incident.PolicyScopeGlobal, "", "")
	assert.ErrorIs(t, err, incident.ErrPolicySetNotFound)
	_, err = s.GetState(ctx, "missing", "missing")
	assert.True(t, errors.Is(err, os.ErrNotExist))
	assert.ErrorIs(t, s.DeleteProvider(ctx, "missing"), incident.ErrProviderNotFound)
	assert.ErrorIs(t, s.DeletePolicySet(ctx, incident.PolicyScopeGlobal, "", ""), incident.ErrPolicySetNotFound)
	assert.ErrorIs(t, s.DeleteState(ctx, "missing", "missing"), os.ErrNotExist)
}
