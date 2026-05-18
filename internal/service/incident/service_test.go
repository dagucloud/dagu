// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package incident

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	incidentmodel "github.com/dagucloud/dagu/internal/incident"
	"github.com/dagucloud/dagu/internal/service/chatbridge"
	"github.com/dagucloud/dagu/internal/service/eventstore"
)

func TestServiceNotificationDestinationsUseDAGWorkspaceGlobalInheritance(t *testing.T) {
	store := newMemoryStore(t)
	globalProvider := store.saveProvider(t, "Global", incidentmodel.ProviderPagerDuty)
	workspaceProvider := store.saveProvider(t, "Workspace", incidentmodel.ProviderPagerDuty)
	dagProvider := store.saveProvider(t, "DAG", incidentmodel.ProviderPagerDuty)
	globalSet := store.savePolicySet(t, &incidentmodel.PolicySet{
		Scope:   incidentmodel.PolicyScopeGlobal,
		Enabled: true,
		Policies: []incidentmodel.Policy{{
			ProviderID: globalProvider.ID,
			Enabled:    true,
		}},
	})
	workspaceSet := store.savePolicySet(t, &incidentmodel.PolicySet{
		Scope:         incidentmodel.PolicyScopeWorkspace,
		Workspace:     "ops",
		Enabled:       true,
		InheritParent: false,
		Policies: []incidentmodel.Policy{{
			ProviderID: workspaceProvider.ID,
			Enabled:    true,
		}},
	})
	dagSet := store.savePolicySet(t, &incidentmodel.PolicySet{
		Scope:         incidentmodel.PolicyScopeDAG,
		DAGName:       "daily",
		Enabled:       true,
		InheritParent: false,
		Policies: []incidentmodel.Policy{{
			ProviderID: dagProvider.ID,
			Enabled:    true,
		}},
	})
	svc := New(store)
	event := failedEvent("daily", "run-1")
	event.Status.Labels = []string{"workspace=ops"}

	destinations := svc.NotificationDestinationsForEvent(event)
	require.Len(t, destinations, 1)
	assert.Equal(t, dagSet.Policies[0].ID, parsePolicyDestinationID(destinations[0]).PolicyID)

	require.NoError(t, store.DeletePolicySet(context.Background(), incidentmodel.PolicyScopeDAG, "", "daily"))
	destinations = svc.NotificationDestinationsForEvent(event)
	require.Len(t, destinations, 1)
	assert.Equal(t, workspaceSet.Policies[0].ID, parsePolicyDestinationID(destinations[0]).PolicyID)

	workspaceSet.InheritParent = true
	store.savePolicySet(t, workspaceSet)
	destinations = svc.NotificationDestinationsForEvent(event)
	require.Len(t, destinations, 1)
	assert.Equal(t, globalSet.Policies[0].ID, parsePolicyDestinationID(destinations[0]).PolicyID)
}

func TestServiceSuppressesFailureIncidentUntilAutoRetriesAreExhausted(t *testing.T) {
	store := newMemoryStore(t)
	provider := store.saveProvider(t, "PagerDuty", incidentmodel.ProviderPagerDuty)
	store.savePolicySet(t, &incidentmodel.PolicySet{
		Scope:   incidentmodel.PolicyScopeGlobal,
		Enabled: true,
		Policies: []incidentmodel.Policy{{
			ProviderID: provider.ID,
			Enabled:    true,
		}},
	})
	svc := New(store)
	event := failedEvent("daily", "run-1")
	event.Status.AutoRetryCount = 1
	event.Status.AutoRetryLimit = 3

	assert.Empty(t, svc.NotificationDestinationsForEvent(event))

	event.Status.AutoRetryCount = 3
	assert.NotEmpty(t, svc.NotificationDestinationsForEvent(event))
}

func TestServicePagerDutyTriggerAndResolvePayloads(t *testing.T) {
	var requests []map[string]any
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, incidentmodel.PagerDutyEventsAPIEndpoint, req.URL.String())
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))
		defer req.Body.Close()
		var payload map[string]any
		require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
		requests = append(requests, payload)
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"dedup_key":"dagu:daily"}`)),
		}, nil
	})}

	store := newMemoryStore(t)
	provider := store.saveProvider(t, "PagerDuty", incidentmodel.ProviderPagerDuty)
	policySet := store.savePolicySet(t, &incidentmodel.PolicySet{
		Scope:   incidentmodel.PolicyScopeGlobal,
		Enabled: true,
		Policies: []incidentmodel.Policy{{
			ProviderID:          provider.ID,
			Enabled:             true,
			Severity:            incidentmodel.SeverityCritical,
			ResolveOnRecovery:   true,
			DedupKeyTemplate:    "dagu:{{dag.name}}",
			MessageTemplate:     "Dagu {{dag.name}} {{run.status}}",
			DescriptionTemplate: "Run {{run.id}} failed",
		}},
	})
	svc := New(store, WithHTTPClient(client), WithPublicURL("https://dagu.example.com/workflows"))
	failure := failedEvent("daily", "run-1")
	destinations := svc.NotificationDestinationsForEvent(failure)
	require.Len(t, destinations, 1)

	ok := svc.FlushNotificationBatch(context.Background(), destinations[0], chatbridge.NotificationBatch{
		Events: []chatbridge.NotificationEvent{failure},
	}, false)
	require.True(t, ok)

	success := failure
	success.Type = eventstore.TypeDAGRunSucceeded
	success.Status = cloneStatus(failure.Status)
	success.Status.Status = core.Succeeded
	success.Status.Error = ""
	ok = svc.FlushNotificationBatch(context.Background(), policyDestinationID(policySet, policySet.Policies[0].ID), chatbridge.NotificationBatch{
		Events: []chatbridge.NotificationEvent{success},
	}, false)
	require.True(t, ok)

	require.Len(t, requests, 2)
	assert.Equal(t, provider.PagerDuty.RoutingKey, requests[0]["routing_key"])
	assert.Equal(t, "trigger", requests[0]["event_action"])
	assert.Equal(t, "dagu:daily", requests[0]["dedup_key"])
	triggerPayload, ok := requests[0]["payload"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Dagu daily failed", triggerPayload["summary"])
	assert.Equal(t, "Dagu", triggerPayload["source"])
	assert.Equal(t, "critical", triggerPayload["severity"])
	customDetails, ok := triggerPayload["custom_details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://dagu.example.com/workflows/dag-runs/daily/run-1", customDetails["runUrl"])

	assert.Equal(t, provider.PagerDuty.RoutingKey, requests[1]["routing_key"])
	assert.Equal(t, "resolve", requests[1]["event_action"])
	assert.Equal(t, "dagu:daily", requests[1]["dedup_key"])
	assert.NotContains(t, requests[1], "payload")
}

func TestServiceResolvesIncidentAfterPolicyIDChanges(t *testing.T) {
	var requests []map[string]any
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		var payload map[string]any
		require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
		requests = append(requests, payload)
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"dedup_key":"dagu:daily"}`)),
		}, nil
	})}

	store := newMemoryStore(t)
	provider := store.saveProvider(t, "PagerDuty", incidentmodel.ProviderPagerDuty)
	policySet := store.savePolicySet(t, &incidentmodel.PolicySet{
		Scope:   incidentmodel.PolicyScopeGlobal,
		Enabled: true,
		Policies: []incidentmodel.Policy{{
			ID:                  "old-policy",
			ProviderID:          provider.ID,
			Enabled:             true,
			ResolveOnRecovery:   true,
			DedupKeyTemplate:    "dagu:{{dag.name}}",
			MessageTemplate:     "Dagu {{dag.name}} failed",
			DescriptionTemplate: "Run {{run.id}} failed",
		}},
	})
	svc := New(store, WithHTTPClient(client))
	failure := failedEvent("daily", "run-1")

	destinations := svc.NotificationDestinationsForEvent(failure)
	require.Len(t, destinations, 1)
	require.True(t, svc.FlushNotificationBatch(context.Background(), destinations[0], chatbridge.NotificationBatch{
		Events: []chatbridge.NotificationEvent{failure},
	}, false))

	policySet.Policies = []incidentmodel.Policy{{
		ID:                  "new-policy",
		ProviderID:          provider.ID,
		Enabled:             true,
		ResolveOnRecovery:   true,
		DedupKeyTemplate:    "dagu:{{dag.name}}",
		MessageTemplate:     "Dagu {{dag.name}} recovered",
		DescriptionTemplate: "Run {{run.id}} recovered",
	}}
	policySet = store.savePolicySet(t, policySet)
	success := failure
	success.Type = eventstore.TypeDAGRunSucceeded
	success.Status = cloneStatus(failure.Status)
	success.Status.Status = core.Succeeded
	success.Status.Error = ""

	destinations = svc.NotificationDestinationsForEvent(success)
	require.Len(t, destinations, 1)
	assert.Equal(t, policySet.Policies[0].ID, parsePolicyDestinationID(destinations[0]).PolicyID)
	require.True(t, svc.FlushNotificationBatch(context.Background(), destinations[0], chatbridge.NotificationBatch{
		Events: []chatbridge.NotificationEvent{success},
	}, false))

	require.Len(t, requests, 2)
	assert.Equal(t, "trigger", requests[0]["event_action"])
	assert.Equal(t, "resolve", requests[1]["event_action"])
	assert.Equal(t, "dagu:daily", requests[1]["dedup_key"])
}

func TestServiceReopenedIncidentUsesFreshOpenedAt(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"dedup_key":"dagu:daily"}`)),
		}, nil
	})}

	store := newMemoryStore(t)
	provider := store.saveProvider(t, "PagerDuty", incidentmodel.ProviderPagerDuty)
	store.savePolicySet(t, &incidentmodel.PolicySet{
		Scope:   incidentmodel.PolicyScopeGlobal,
		Enabled: true,
		Policies: []incidentmodel.Policy{{
			ID:                "pagerduty-policy",
			ProviderID:        provider.ID,
			Enabled:           true,
			ResolveOnRecovery: true,
			DedupKeyTemplate:  "dagu:{{dag.name}}",
		}},
	})
	svc := New(store, WithHTTPClient(client))
	failure := failedEvent("daily", "run-1")
	destinations := svc.NotificationDestinationsForEvent(failure)
	require.Len(t, destinations, 1)
	require.True(t, svc.FlushNotificationBatch(context.Background(), destinations[0], chatbridge.NotificationBatch{
		Events: []chatbridge.NotificationEvent{failure},
	}, false))
	state, err := store.GetState(context.Background(), provider.ID, "dagu:daily")
	require.NoError(t, err)
	firstOpenedAt := state.OpenedAt

	success := failure
	success.Type = eventstore.TypeDAGRunSucceeded
	success.Status = cloneStatus(failure.Status)
	success.Status.Status = core.Succeeded
	success.Status.Error = ""
	require.True(t, svc.FlushNotificationBatch(context.Background(), destinations[0], chatbridge.NotificationBatch{
		Events: []chatbridge.NotificationEvent{success},
	}, false))

	time.Sleep(time.Millisecond)
	reopenedAfter := time.Now().UTC()
	secondFailure := failedEvent("daily", "run-2")
	require.True(t, svc.FlushNotificationBatch(context.Background(), destinations[0], chatbridge.NotificationBatch{
		Events: []chatbridge.NotificationEvent{secondFailure},
	}, false))

	state, err = store.GetState(context.Background(), provider.ID, "dagu:daily")
	require.NoError(t, err)
	assert.Equal(t, incidentmodel.IncidentStatusOpen, state.Status)
	assert.True(t, state.OpenedAt.After(firstOpenedAt), "reopened incident should not keep the original open timestamp")
	assert.False(t, state.OpenedAt.Before(reopenedAfter), "reopened incident should use the new failure timestamp")
}

func TestServiceSolarWindsTriggerAndResolvePayloads(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		requests = append(requests, payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"request_id":"req-1","incident_id":"inc-1","event_id":"returned-event"}`))
	}))
	defer server.Close()

	store := newMemoryStore(t)
	provider, err := incidentmodel.NormalizeProvider(&incidentmodel.Provider{
		Name:    "SolarWinds",
		Type:    incidentmodel.ProviderSolarWindsIncidentResponse,
		Enabled: true,
		SolarWinds: &incidentmodel.SolarWindsProvider{
			WebhookURL:          server.URL,
			AllowInsecureHTTP:   true,
			AllowPrivateNetwork: true,
		},
	}, "user-1")
	require.NoError(t, err)
	require.NoError(t, store.SaveProvider(context.Background(), provider))
	policySet := store.savePolicySet(t, &incidentmodel.PolicySet{
		Scope:   incidentmodel.PolicyScopeGlobal,
		Enabled: true,
		Policies: []incidentmodel.Policy{{
			ProviderID:          provider.ID,
			Enabled:             true,
			Severity:            incidentmodel.SeverityCritical,
			ResolveOnRecovery:   true,
			DedupKeyTemplate:    "dagu:{{dag.name}}",
			MessageTemplate:     "Dagu {{dag.name}} {{run.status}}",
			DescriptionTemplate: "Run {{run.id}}: {{run.error}}\n{{run.url}}",
		}},
	})
	svc := New(store, WithPublicURL("https://dagu.example.com/workflows"))
	failure := failedEvent("daily", "run-1")
	destinations := svc.NotificationDestinationsForEvent(failure)
	require.Len(t, destinations, 1)

	ok := svc.FlushNotificationBatch(context.Background(), destinations[0], chatbridge.NotificationBatch{
		Events: []chatbridge.NotificationEvent{failure},
	}, false)
	require.True(t, ok)

	success := failure
	success.Type = eventstore.TypeDAGRunSucceeded
	success.Status = cloneStatus(failure.Status)
	success.Status.Status = core.Succeeded
	success.Status.Error = ""
	ok = svc.FlushNotificationBatch(context.Background(), policyDestinationID(policySet, policySet.Policies[0].ID), chatbridge.NotificationBatch{
		Events: []chatbridge.NotificationEvent{success},
	}, false)
	require.True(t, ok)

	require.Len(t, requests, 2)
	assert.Equal(t, "trigger", requests[0]["status"])
	assert.Equal(t, "dagu:daily", requests[0]["event_id"])
	assert.Equal(t, "Dagu daily failed", requests[0]["message"])
	assert.Contains(t, requests[0]["description"], "Run run-1: boom")
	assert.Contains(t, requests[0]["description"], "https://dagu.example.com/workflows/dag-runs/daily/run-1")
	assert.Equal(t, "resolve", requests[1]["status"])
	assert.Equal(t, "dagu:daily", requests[1]["event_id"])
	assert.NotContains(t, requests[1], "message")
	assert.NotContains(t, requests[1], "description")
}

func failedEvent(dagName, runID string) chatbridge.NotificationEvent {
	now := time.Now().UTC()
	return chatbridge.NotificationEvent{
		Key:        "key:" + dagName + ":" + runID,
		Type:       eventstore.TypeDAGRunFailed,
		ObservedAt: now,
		Status: &exec.DAGRunStatus{
			Name:       dagName,
			DAGRunID:   runID,
			AttemptID:  runID,
			Status:     core.Failed,
			Error:      "boom",
			StartedAt:  stringutilFormat(now.Add(-time.Minute)),
			FinishedAt: stringutilFormat(now),
		},
	}
}

func cloneStatus(status *exec.DAGRunStatus) *exec.DAGRunStatus {
	if status == nil {
		return nil
	}
	copy := *status
	copy.Labels = append([]string(nil), status.Labels...)
	return &copy
}

func stringutilFormat(t time.Time) string {
	return t.Format(time.RFC3339)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
