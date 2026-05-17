// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package notification

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	notificationmodel "github.com/dagucloud/dagu/internal/notification"
	"github.com/dagucloud/dagu/internal/service/chatbridge"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryStore struct {
	mu       sync.Mutex
	settings map[string]*notificationmodel.Settings
	channels map[string]*notificationmodel.Channel
}

func newMemoryStore(settings ...*notificationmodel.Settings) *memoryStore {
	store := &memoryStore{
		settings: make(map[string]*notificationmodel.Settings),
		channels: make(map[string]*notificationmodel.Channel),
	}
	for _, setting := range settings {
		store.settings[setting.DAGName] = setting
	}
	return store
}

func (s *memoryStore) Save(_ context.Context, settings *notificationmodel.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[settings.DAGName] = settings
	return nil
}

func (s *memoryStore) GetByDAGName(_ context.Context, dagName string) (*notificationmodel.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings := s.settings[dagName]
	if settings == nil {
		return nil, notificationmodel.ErrSettingsNotFound
	}
	return settings, nil
}

func (s *memoryStore) List(context.Context) ([]*notificationmodel.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*notificationmodel.Settings, 0, len(s.settings))
	for _, setting := range s.settings {
		result = append(result, setting)
	}
	return result, nil
}

func (s *memoryStore) DeleteByDAGName(_ context.Context, dagName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings[dagName] == nil {
		return notificationmodel.ErrSettingsNotFound
	}
	delete(s.settings, dagName)
	return nil
}

func (s *memoryStore) SaveChannel(_ context.Context, channel *notificationmodel.Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channels[channel.ID] = channel
	return nil
}

func (s *memoryStore) GetChannel(_ context.Context, channelID string) (*notificationmodel.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channel := s.channels[channelID]
	if channel == nil {
		return nil, notificationmodel.ErrChannelNotFound
	}
	return channel, nil
}

func (s *memoryStore) ListChannels(context.Context) ([]*notificationmodel.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*notificationmodel.Channel, 0, len(s.channels))
	for _, channel := range s.channels {
		result = append(result, channel)
	}
	return result, nil
}

func (s *memoryStore) DeleteChannel(_ context.Context, channelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.channels[channelID] == nil {
		return notificationmodel.ErrChannelNotFound
	}
	delete(s.channels, channelID)
	return nil
}

func TestService_SendTestWebhookIncludesPayloadHeadersAndSignature(t *testing.T) {
	t.Parallel()

	var receivedBody []byte
	var receivedSignature string
	var receivedHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSignature = r.Header.Get("X-Dagu-Signature")
		receivedHeader = r.Header.Get("X-Test")
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		receivedBody = body
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed, eventstore.TypeDAGRunWaiting},
		Targets: []notificationmodel.Target{{
			ID:      "webhook-1",
			Name:    "Ops Webhook",
			Type:    notificationmodel.ProviderWebhook,
			Enabled: true,
			Webhook: &notificationmodel.WebhookTarget{
				URL:                 server.URL,
				Headers:             map[string]string{"X-Test": "yes"},
				HMACSecret:          "secret",
				AllowInsecureHTTP:   true,
				AllowPrivateNetwork: true,
			},
		}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}, "tester")
	require.NoError(t, err)

	svc := New(newMemoryStore(settings), nil)
	results, err := svc.SendTest(context.Background(), "daily-report", "webhook-1", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Delivered)
	assert.Equal(t, "yes", receivedHeader)

	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(receivedBody)
	assert.Equal(t, "sha256="+hex.EncodeToString(mac.Sum(nil)), receivedSignature)
	assert.Contains(t, string(receivedBody), `"dagName":"daily-report"`)
	assert.Contains(t, string(receivedBody), `"dagRunId":"notification-test"`)
}

func TestService_SendTestReturnsProviderError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad target", http.StatusBadRequest)
	}))
	defer server.Close()

	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed, eventstore.TypeDAGRunWaiting},
		Targets: []notificationmodel.Target{{
			ID:      "webhook-1",
			Type:    notificationmodel.ProviderWebhook,
			Enabled: true,
			Webhook: &notificationmodel.WebhookTarget{
				URL:                 server.URL,
				AllowInsecureHTTP:   true,
				AllowPrivateNetwork: true,
			},
		}},
	}, "tester")
	require.NoError(t, err)

	svc := New(newMemoryStore(settings), nil, WithDeliveryRetry(DeliveryRetryConfig{MaxAttempts: 1}))
	results, err := svc.SendTest(context.Background(), "daily-report", "webhook-1", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Delivered)
	assert.Contains(t, results[0].Error, "HTTP 400")
	assert.Contains(t, results[0].Error, "bad target")
}

func TestService_NotificationDestinationsForEventFiltersByDAGAndEvent(t *testing.T) {
	t.Parallel()

	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed, eventstore.TypeDAGRunWaiting},
		Targets: []notificationmodel.Target{
			{
				ID:      "webhook-1",
				Type:    notificationmodel.ProviderWebhook,
				Enabled: true,
				Events:  []eventstore.EventType{eventstore.TypeDAGRunWaiting},
				Webhook: &notificationmodel.WebhookTarget{
					URL: "https://example.com/webhook",
				},
			},
			{
				ID:      "webhook-2",
				Type:    notificationmodel.ProviderWebhook,
				Enabled: false,
				Webhook: &notificationmodel.WebhookTarget{
					URL: "https://example.com/disabled",
				},
			},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}, "tester")
	require.NoError(t, err)
	svc := New(newMemoryStore(settings), nil)

	destinations := svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type: eventstore.TypeDAGRunWaiting,
		Status: &exec.DAGRunStatus{
			Name:      "daily-report",
			Status:    core.Waiting,
			DAGRunID:  "run-1",
			AttemptID: "attempt-1",
		},
	})
	require.Len(t, destinations, 1)
	assert.Contains(t, destinations[0], "webhook-1")

	assert.Empty(t, svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type:   eventstore.TypeDAGRunFailed,
		Status: &exec.DAGRunStatus{Name: "daily-report", Status: core.Failed},
	}))
	assert.Empty(t, svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type:   eventstore.TypeDAGRunFailed,
		Status: &exec.DAGRunStatus{Name: "other-dag", Status: core.Failed},
	}))
}

func TestService_ReusableChannelSubscriptionsDeliverForMatchingDAGEvent(t *testing.T) {
	t.Parallel()

	var receivedBody atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		receivedBody.Store(string(body))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	channel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
		ID:      "channel-1",
		Name:    "Ops Webhook",
		Type:    notificationmodel.ProviderWebhook,
		Enabled: true,
		Webhook: &notificationmodel.WebhookTarget{
			URL:                 server.URL,
			AllowInsecureHTTP:   true,
			AllowPrivateNetwork: true,
		},
	}, "tester")
	require.NoError(t, err)
	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed, eventstore.TypeDAGRunSucceeded},
		Subscriptions: []notificationmodel.Subscription{{
			ID:        "subscription-1",
			ChannelID: "channel-1",
			Enabled:   true,
			Events:    []eventstore.EventType{eventstore.TypeDAGRunFailed},
		}},
	}, "tester")
	require.NoError(t, err)
	store := newMemoryStore(settings)
	require.NoError(t, store.SaveChannel(context.Background(), channel))
	svc := New(store, nil)

	destinations := svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type: eventstore.TypeDAGRunFailed,
		Status: &exec.DAGRunStatus{
			Name:      "daily-report",
			Status:    core.Failed,
			DAGRunID:  "run-1",
			AttemptID: "attempt-1",
		},
	})
	require.Len(t, destinations, 1)

	delivered := svc.FlushNotificationBatch(context.Background(), destinations[0], chatbridge.NotificationBatch{
		Events: []chatbridge.NotificationEvent{{
			Type:       eventstore.TypeDAGRunFailed,
			Status:     &exec.DAGRunStatus{Name: "daily-report", Status: core.Failed, DAGRunID: "run-1"},
			ObservedAt: time.Now().UTC(),
		}},
	}, false)
	assert.True(t, delivered)
	body, _ := receivedBody.Load().(string)
	assert.Contains(t, body, `"dagName":"daily-report"`)

	assert.Empty(t, svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type:   eventstore.TypeDAGRunSucceeded,
		Status: &exec.DAGRunStatus{Name: "daily-report", Status: core.Succeeded},
	}))
}

func TestService_DisabledReusableChannelGateSkipsSubscriptions(t *testing.T) {
	t.Parallel()

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	channel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
		ID:      "channel-1",
		Name:    "Ops Webhook",
		Type:    notificationmodel.ProviderWebhook,
		Enabled: true,
		Webhook: &notificationmodel.WebhookTarget{
			URL:                 server.URL,
			AllowInsecureHTTP:   true,
			AllowPrivateNetwork: true,
		},
	}, "tester")
	require.NoError(t, err)
	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notificationmodel.Target{{
			ID:      "local-webhook",
			Type:    notificationmodel.ProviderWebhook,
			Enabled: true,
			Webhook: &notificationmodel.WebhookTarget{URL: "https://example.com/webhook"},
		}},
		Subscriptions: []notificationmodel.Subscription{{
			ID:        "subscription-1",
			ChannelID: "channel-1",
			Enabled:   true,
			Events:    []eventstore.EventType{eventstore.TypeDAGRunFailed},
		}},
	}, "tester")
	require.NoError(t, err)
	store := newMemoryStore(settings)
	require.NoError(t, store.SaveChannel(context.Background(), channel))
	svc := New(
		store,
		nil,
		WithReusableChannelsEnabled(func() bool { return false }),
	)

	event := chatbridge.NotificationEvent{
		Type: eventstore.TypeDAGRunFailed,
		Status: &exec.DAGRunStatus{
			Name:      "daily-report",
			Status:    core.Failed,
			DAGRunID:  "run-1",
			AttemptID: "attempt-1",
		},
	}
	destinations := svc.NotificationDestinationsForEvent(event)
	require.Len(t, destinations, 1)
	assert.Contains(t, destinations[0], "local-webhook")
	assert.NotContains(t, destinations[0], "subscription-1")

	assert.True(t, svc.FlushNotificationBatch(
		context.Background(),
		channelDestinationID("daily-report", "subscription-1"),
		chatbridge.NotificationBatch{Events: []chatbridge.NotificationEvent{event}},
		false,
	))
	assert.Equal(t, int32(0), requestCount.Load())

	_, err = svc.SendTest(context.Background(), "daily-report", "subscription-1", eventstore.TypeDAGRunFailed)
	assert.ErrorIs(t, err, notificationmodel.ErrTargetNotFound)
}

func TestService_SaveRejectsMissingReusableChannel(t *testing.T) {
	t.Parallel()

	svc := New(newMemoryStore(), nil)
	_, err := svc.Save(context.Background(), &notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Subscriptions: []notificationmodel.Subscription{{
			ChannelID: "missing-channel",
			Enabled:   true,
		}},
	}, "tester")
	assert.ErrorIs(t, err, notificationmodel.ErrChannelNotFound)
}

func TestService_DeleteChannelRejectsInUseChannel(t *testing.T) {
	t.Parallel()

	channel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
		ID:      "channel-1",
		Name:    "Ops Webhook",
		Type:    notificationmodel.ProviderWebhook,
		Enabled: true,
		Webhook: &notificationmodel.WebhookTarget{URL: "https://example.com/webhook"},
	}, "tester")
	require.NoError(t, err)
	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Subscriptions: []notificationmodel.Subscription{{
			ID:        "subscription-1",
			ChannelID: "channel-1",
			Enabled:   true,
		}},
	}, "tester")
	require.NoError(t, err)
	store := newMemoryStore(settings)
	require.NoError(t, store.SaveChannel(context.Background(), channel))
	svc := New(store, nil)

	err = svc.DeleteChannel(context.Background(), "channel-1")
	assert.ErrorIs(t, err, notificationmodel.ErrChannelInUse)
}
