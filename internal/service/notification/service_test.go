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
}

func newMemoryStore(settings ...*notificationmodel.Settings) *memoryStore {
	store := &memoryStore{settings: make(map[string]*notificationmodel.Settings)}
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
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notificationmodel.Target{{
			ID:      "webhook-1",
			Name:    "Ops Webhook",
			Type:    notificationmodel.ProviderWebhook,
			Enabled: true,
			Webhook: &notificationmodel.WebhookTarget{
				URL:        server.URL,
				Headers:    map[string]string{"X-Test": "yes"},
				HMACSecret: "secret",
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

func TestService_NotificationDestinationsForEventFiltersByDAGAndEvent(t *testing.T) {
	t.Parallel()

	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notificationmodel.Target{
			{
				ID:      "webhook-1",
				Type:    notificationmodel.ProviderWebhook,
				Enabled: true,
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
		Type: eventstore.TypeDAGRunFailed,
		Status: &exec.DAGRunStatus{
			Name:      "daily-report",
			Status:    core.Failed,
			DAGRunID:  "run-1",
			AttemptID: "attempt-1",
		},
	})
	require.Len(t, destinations, 1)
	assert.Contains(t, destinations[0], "webhook-1")

	assert.Empty(t, svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type:   eventstore.TypeDAGRunSucceeded,
		Status: &exec.DAGRunStatus{Name: "daily-report", Status: core.Succeeded},
	}))
	assert.Empty(t, svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type:   eventstore.TypeDAGRunFailed,
		Status: &exec.DAGRunStatus{Name: "other-dag", Status: core.Failed},
	}))
}
