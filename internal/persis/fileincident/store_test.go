// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileincident

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/incident"
)

func TestStoreEncryptsIncidentProviderSecretsAtRest(t *testing.T) {
	encryptor, err := crypto.NewEncryptor("test-encryption-key")
	require.NoError(t, err)

	store, err := New(t.TempDir(), WithEncryptor(encryptor))
	require.NoError(t, err)
	provider, err := incident.NormalizeProvider(&incident.Provider{
		Name:    "PagerDuty",
		Type:    incident.ProviderPagerDuty,
		Enabled: true,
		PagerDuty: &incident.PagerDutyProvider{
			RoutingKey: "pagerduty-routing-key",
		},
	}, "user-1")
	require.NoError(t, err)

	require.NoError(t, store.SaveProvider(context.Background(), provider))

	var foundPlaintext bool
	err = filepath.WalkDir(store.providerDir(), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // test reads temp store file.
		if readErr != nil {
			return readErr
		}
		foundPlaintext = foundPlaintext || string(data) == "pagerduty-routing-key"
		foundPlaintext = foundPlaintext || containsString(data, "pagerduty-routing-key")
		return nil
	})
	require.NoError(t, err)
	assert.False(t, foundPlaintext)

	loaded, err := store.GetProvider(context.Background(), provider.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.PagerDuty)
	assert.Equal(t, "pagerduty-routing-key", loaded.PagerDuty.RoutingKey)
}

func TestStorePersistsPolicySetAndState(t *testing.T) {
	store, err := New(t.TempDir())
	require.NoError(t, err)
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

	require.NoError(t, store.SavePolicySet(context.Background(), policySet))

	loadedPolicySet, err := store.GetPolicySet(context.Background(), incident.PolicyScopeWorkspace, "ops", "")
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
	require.NoError(t, store.SaveState(context.Background(), state))

	loadedState, err := store.GetState(context.Background(), state.ProviderID, state.DedupKey)
	require.NoError(t, err)
	assert.Equal(t, incident.IncidentStatusOpen, loadedState.Status)
	assert.Equal(t, "daily", loadedState.DAGName)

	openStates, err := store.ListOpenStatesByDAG(context.Background(), "daily")
	require.NoError(t, err)
	require.Len(t, openStates, 1)
	assert.Equal(t, state.DedupKey, openStates[0].DedupKey)
}

func containsString(data []byte, value string) bool {
	return len(value) > 0 && string(data) != "" && indexString(string(data), value) >= 0
}

func indexString(data, value string) int {
	for i := 0; i+len(value) <= len(data); i++ {
		if data[i:i+len(value)] == value {
			return i
		}
	}
	return -1
}
