// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package incident

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeProviderRejectsSolarWindsPrivateURLByDefault(t *testing.T) {
	provider := &Provider{
		Name:    "Local SolarWinds",
		Type:    ProviderSolarWindsIncidentResponse,
		Enabled: true,
		SolarWinds: &SolarWindsProvider{
			WebhookURL: "http://127.0.0.1/incoming",
		},
	}

	_, err := NormalizeProvider(provider, "user-1")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidProvider))
}

func TestNormalizeProviderAllowsSolarWindsPrivateURLWhenExplicitlyAllowed(t *testing.T) {
	provider := &Provider{
		Name:    "Local SolarWinds",
		Type:    ProviderSolarWindsIncidentResponse,
		Enabled: true,
		SolarWinds: &SolarWindsProvider{
			WebhookURL:          "http://127.0.0.1/incoming",
			AllowInsecureHTTP:   true,
			AllowPrivateNetwork: true,
		},
	}

	normalized, err := NormalizeProvider(provider, "user-1")

	require.NoError(t, err)
	assert.NotEmpty(t, normalized.ID)
	assert.Nil(t, normalized.PagerDuty)
	assert.Equal(t, "user-1", normalized.UpdatedBy)
}

func TestNormalizeProviderAllowsDisabledProviderWithoutSecret(t *testing.T) {
	provider := &Provider{
		Name:      "PagerDuty Draft",
		Type:      ProviderPagerDuty,
		Enabled:   false,
		PagerDuty: &PagerDutyProvider{},
	}

	normalized, err := NormalizeProvider(provider, "user-1")

	require.NoError(t, err)
	assert.False(t, normalized.Enabled)
	assert.Empty(t, normalized.PagerDuty.RoutingKey)
}

func TestNormalizePolicySetDefaultsIncidentTemplates(t *testing.T) {
	policySet := &PolicySet{
		Scope:   PolicyScopeGlobal,
		Enabled: true,
		Policies: []Policy{{
			ProviderID:        "provider-1",
			Enabled:           true,
			ResolveOnRecovery: true,
		}},
	}

	normalized, err := NormalizePolicySet(policySet, "user-1")

	require.NoError(t, err)
	require.Len(t, normalized.Policies, 1)
	policy := normalized.Policies[0]
	assert.NotEmpty(t, policy.ID)
	assert.Equal(t, SeverityError, policy.Severity)
	assert.Equal(t, DefaultDedupKeyTemplate, policy.DedupKeyTemplate)
	assert.Equal(t, DefaultMessageTemplate, policy.MessageTemplate)
	assert.Equal(t, DefaultDescriptionTemplate, policy.DescriptionTemplate)
	assert.False(t, normalized.InheritParent)
}

func TestNormalizePolicySetValidatesWorkspaceScope(t *testing.T) {
	policySet := &PolicySet{
		Scope:     PolicyScopeWorkspace,
		Workspace: "bad/name",
		Enabled:   true,
	}

	_, err := NormalizePolicySet(policySet, "user-1")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidPolicySet))
}

func TestPreserveProviderSecretsKeepsExistingSecretWhenOmitted(t *testing.T) {
	existing := &Provider{
		Type: ProviderPagerDuty,
		PagerDuty: &PagerDutyProvider{
			RoutingKey: "existing-routing-key",
		},
	}
	next := &Provider{
		Type:      ProviderPagerDuty,
		PagerDuty: &PagerDutyProvider{},
	}

	PreserveProviderSecrets(next, existing)

	assert.Equal(t, "existing-routing-key", next.PagerDuty.RoutingKey)
}
