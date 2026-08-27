// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package notification

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/persis"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryStore struct {
	mu               sync.Mutex
	settings         map[string]*notificationmodel.Settings
	workspace        *notificationmodel.WorkspaceSettings
	channels         map[string]*notificationmodel.Channel
	routeSets        map[string]*notificationmodel.RouteSet
	getChannelCounts map[string]int
}

func newMemoryStore(settings ...*notificationmodel.Settings) *memoryStore {
	store := &memoryStore{
		settings:         make(map[string]*notificationmodel.Settings),
		channels:         make(map[string]*notificationmodel.Channel),
		routeSets:        make(map[string]*notificationmodel.RouteSet),
		getChannelCounts: make(map[string]int),
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

func (s *memoryStore) SaveWorkspaceSettings(_ context.Context, settings *notificationmodel.WorkspaceSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspace = settings
	return nil
}

func (s *memoryStore) GetWorkspaceSettings(context.Context) (*notificationmodel.WorkspaceSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workspace == nil {
		return &notificationmodel.WorkspaceSettings{}, nil
	}
	return s.workspace, nil
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
	s.getChannelCounts[channelID]++
	channel := s.channels[channelID]
	if channel == nil {
		return nil, notificationmodel.ErrChannelNotFound
	}
	return channel, nil
}

func (s *memoryStore) SaveRouteSet(_ context.Context, routeSet *notificationmodel.RouteSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routeSets[memoryRouteSetKey(routeSet.Scope, routeSet.Workspace)] = routeSet
	return nil
}

func (s *memoryStore) GetRouteSet(_ context.Context, scope notificationmodel.RouteScope, workspace string) (*notificationmodel.RouteSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	routeSet := s.routeSets[memoryRouteSetKey(scope, workspace)]
	if routeSet == nil {
		return nil, notificationmodel.ErrRouteSetNotFound
	}
	return routeSet, nil
}

func (s *memoryStore) ListRouteSets(context.Context) ([]*notificationmodel.RouteSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*notificationmodel.RouteSet, 0, len(s.routeSets))
	for _, routeSet := range s.routeSets {
		result = append(result, routeSet)
	}
	return result, nil
}

func (s *memoryStore) DeleteRouteSet(_ context.Context, scope notificationmodel.RouteScope, workspace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryRouteSetKey(scope, workspace)
	if s.routeSets[key] == nil {
		return notificationmodel.ErrRouteSetNotFound
	}
	delete(s.routeSets, key)
	return nil
}

func memoryRouteSetKey(scope notificationmodel.RouteScope, workspace string) string {
	return string(scope) + ":" + workspace
}

func (s *memoryStore) GetChannelCount(channelID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getChannelCounts[channelID]
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

type testDAGDefinitionStore struct {
	persis.DAGDefinitionStore
	definition persis.DAGDefinition
}

func (s testDAGDefinitionStore) Get(context.Context, string) (persis.DAGDefinition, error) {
	return s.definition, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func acceptedResponse(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusAccepted,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}
}

func mustNormalizeSettings(t *testing.T, settings *notificationmodel.Settings) *notificationmodel.Settings {
	t.Helper()
	normalized, err := notificationmodel.Normalize(settings, "tester")
	require.NoError(t, err)
	return normalized
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
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "failed to read request body", http.StatusInternalServerError)
			return
		}
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

func TestService_DeliversLifecycleEvent(t *testing.T) {
	t.Parallel()

	smtpServer := newRecordingSMTPServer(t)

	settings := mustNormalizeSettings(t, &notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notificationmodel.Target{{
			ID:      "email-1",
			Type:    notificationmodel.ProviderEmail,
			Enabled: true,
			Email:   &notificationmodel.EmailTarget{To: []string{"ops@example.com"}},
		}},
	})
	notificationStore := newMemoryStore(settings)
	workspaceSettings, err := notificationmodel.NormalizeWorkspaceSettings(&notificationmodel.WorkspaceSettings{
		SMTP: &notificationmodel.SMTPConfig{
			Host: smtpServer.host,
			Port: smtpServer.port,
			From: "dagu@example.com",
		},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, notificationStore.SaveWorkspaceSettings(context.Background(), workspaceSettings))
	svc := New(
		notificationStore,
		nil,
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)

	eventStore, err := fileeventstore.New(t.TempDir())
	require.NoError(t, err)
	eventService := eventstore.New(eventStore)

	monitorConfig := chatbridge.DefaultNotificationMonitorConfig()
	monitorConfig.PollInterval = 10 * time.Millisecond
	monitorConfig.UrgentWindow = 10 * time.Millisecond
	monitorConfig.SeenEvictInterval = time.Hour
	stateFile := filepath.Join(t.TempDir(), "monitor-state.json")
	monitor := chatbridge.NewNotificationMonitor(
		eventService,
		filemonitor.NewStateStore(stateFile),
		filemonitor.NewLease(stateFile, &dirlock.LockOptions{
			StaleThreshold: chatbridge.DefaultNotificationLockStaleThreshold,
			RetryInterval:  chatbridge.DefaultNotificationLockRetryInterval,
		}),
		svc,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		monitorConfig,
	)
	require.NoError(t, monitor.Bootstrap(context.Background()))

	require.NoError(t, eventService.Emit(context.Background(), eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceScheduler},
		eventstore.TypeDAGRunFailed,
		&ir.DAGRunStatus{
			Name:      "daily-report",
			DAGRunID:  "run-1",
			AttemptID: "attempt-1",
			Status:    ir.Failed,
			Error:     "boom",
		},
		nil,
	)))

	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	require.Eventually(t, func() bool {
		return smtpServer.data.Load() != nil
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, "dagu@example.com", smtpServer.mailFrom.Load())
	assert.Equal(t, "ops@example.com", smtpServer.rcptTo.Load())
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

func TestService_SendTestRejectsSlackURLConfiguredAsGenericWebhook(t *testing.T) {
	t.Parallel()

	settings := &notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notificationmodel.Target{{
			ID:      "webhook-1",
			Type:    notificationmodel.ProviderWebhook,
			Enabled: true,
			Webhook: &notificationmodel.WebhookTarget{
				URL: "https://hooks.slack.com/services/T000/B000/secret",
			},
		}},
	}

	svc := New(newMemoryStore(settings), nil, WithDeliveryRetry(DeliveryRetryConfig{MaxAttempts: 1}))
	results, err := svc.SendTest(context.Background(), "daily-report", "webhook-1", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Delivered)
	assert.Contains(t, results[0].Error, "generic webhook")
	assert.Contains(t, results[0].Error, "slack provider")
}

func TestService_SendTestEmailUsesWorkspaceSMTP(t *testing.T) {
	t.Parallel()

	smtpServer := newRecordingSMTPServer(t)
	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notificationmodel.Target{{
			ID:      "email-1",
			Name:    "Ops Email",
			Type:    notificationmodel.ProviderEmail,
			Enabled: true,
			Email: &notificationmodel.EmailTarget{
				To:            []string{"ops@example.com"},
				SubjectPrefix: "[Ops]",
			},
		}},
	}, "tester")
	require.NoError(t, err)
	store := newMemoryStore(settings)
	workspace, err := notificationmodel.NormalizeWorkspaceSettings(&notificationmodel.WorkspaceSettings{
		SMTP: &notificationmodel.SMTPConfig{
			Host: smtpServer.host,
			Port: smtpServer.port,
			From: "dagu@example.com",
		},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.SaveWorkspaceSettings(context.Background(), workspace))
	svc := New(store, nil)

	results, err := svc.SendTest(context.Background(), "daily-report", "email-1", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Delivered)
	assert.Equal(t, "dagu@example.com", smtpServer.mailFrom.Load())
	assert.Equal(t, "ops@example.com", smtpServer.rcptTo.Load())
	data, _ := smtpServer.data.Load().(string)
	assert.Contains(t, data, "Subject: [Ops]")
}

func TestService_SendTestEmailUsesCustomSubjectAndBodyTemplates(t *testing.T) {
	t.Parallel()

	smtpServer := newRecordingSMTPServer(t)
	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notificationmodel.Target{{
			ID:      "email-1",
			Name:    "Ops Email",
			Type:    notificationmodel.ProviderEmail,
			Enabled: true,
			Email: &notificationmodel.EmailTarget{
				To:              []string{"ops@example.com"},
				SubjectTemplate: "{{dag.name}} {{run.status}}",
				BodyTemplate:    "Run {{run.id}} failed: {{run.error}}",
			},
		}},
	}, "tester")
	require.NoError(t, err)
	store := newMemoryStore(settings)
	workspace, err := notificationmodel.NormalizeWorkspaceSettings(&notificationmodel.WorkspaceSettings{
		SMTP: &notificationmodel.SMTPConfig{
			Host: smtpServer.host,
			Port: smtpServer.port,
			From: "dagu@example.com",
		},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.SaveWorkspaceSettings(context.Background(), workspace))
	svc := New(store, nil)

	results, err := svc.SendTest(context.Background(), "daily-report", "email-1", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Delivered)
	data, _ := smtpServer.data.Load().(string)
	assert.Contains(t, data, "Subject: daily-report failed")
	assert.Contains(t, data, base64.StdEncoding.EncodeToString(
		[]byte("Run notification-test failed: This is a test notification from Dagu."),
	))
}

func TestService_SendTestWebhookIncludesCustomMessage(t *testing.T) {
	t.Parallel()

	var receivedBody atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "failed to read request body", http.StatusInternalServerError)
			return
		}
		receivedBody.Store(string(body))
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
				URL:                 server.URL,
				AllowInsecureHTTP:   true,
				AllowPrivateNetwork: true,
				MessageTemplate:     "DAG {{dag.name}} {{run.status}} in {{run.id}}",
			},
		}},
	}, "tester")
	require.NoError(t, err)
	svc := New(newMemoryStore(settings), nil)

	results, err := svc.SendTest(context.Background(), "daily-report", "webhook-1", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Delivered)
	body, _ := receivedBody.Load().(string)
	assert.Contains(t, body, `"message":"DAG daily-report failed in notification-test"`)
	assert.Contains(t, body, `"events":[`)
}

func TestService_SendTestWebhookUsesBodyTemplate(t *testing.T) {
	t.Parallel()

	var receivedBody atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "failed to read request body", http.StatusInternalServerError)
			return
		}
		receivedBody.Store(string(body))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notificationmodel.Target{{
			ID:      "webhook-1",
			Name:    "Teams Relay",
			Type:    notificationmodel.ProviderWebhook,
			Enabled: true,
			Webhook: &notificationmodel.WebhookTarget{
				URL:                 server.URL,
				AllowInsecureHTTP:   true,
				AllowPrivateNetwork: true,
				MessageTemplate:     "DAG {{dag.name}} {{run.status}}",
				BodyTemplate:        `{"text": "{{message}}", "dag": "{{dag.name}}"}`,
			},
		}},
	}, "tester")
	require.NoError(t, err)
	svc := New(newMemoryStore(settings), nil)

	results, err := svc.SendTest(context.Background(), "daily-report", "webhook-1", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Delivered)
	body, _ := receivedBody.Load().(string)
	assert.JSONEq(t, `{"text": "DAG daily-report failed", "dag": "daily-report"}`, body)
}

func TestService_WebhookBodyTemplateRetryDoesNotResendDeliveredEvents(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var bodies []string
	failedOnce := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "failed to read request body", http.StatusInternalServerError)
			return
		}

		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(string(body), "run-2") && !failedOnce {
			failedOnce = true
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		bodies = append(bodies, string(body))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	svc := New(
		newMemoryStore(),
		nil,
		WithDeliveryRetry(DeliveryRetryConfig{MaxAttempts: 3}),
	)
	target := notificationmodel.Target{
		ID:      "webhook-1",
		Type:    notificationmodel.ProviderWebhook,
		Enabled: true,
		Webhook: &notificationmodel.WebhookTarget{
			URL:                 server.URL,
			AllowInsecureHTTP:   true,
			AllowPrivateNetwork: true,
			BodyTemplate:        `{"run": "{{run.id}}"}`,
		},
	}

	err := svc.deliverTarget(context.Background(), target, []chatbridge.NotificationEvent{
		notificationEventForRun(t, "run-1"),
		notificationEventForRun(t, "run-2"),
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{`{"run": "run-1"}`, `{"run": "run-2"}`}, bodies)
}

func TestService_WebhookBodyTemplateValidatesBatchBeforeDelivery(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	svc := New(newMemoryStore(), nil, WithDeliveryRetry(DeliveryRetryConfig{MaxAttempts: 1}))
	target := notificationmodel.Target{
		ID:      "webhook-1",
		Type:    notificationmodel.ProviderWebhook,
		Enabled: true,
		Webhook: &notificationmodel.WebhookTarget{
			URL:                 server.URL,
			AllowInsecureHTTP:   true,
			AllowPrivateNetwork: true,
			BodyTemplate:        `{"errorCode": {{run.error}}}`,
		},
	}
	validEvent := notificationEventForRun(t, "run-1")
	validEvent.Status.Error = "1"
	invalidEvent := notificationEventForRun(t, "run-2")
	invalidEvent.Status.Error = "not-a-number"

	err := svc.deliverTarget(context.Background(), target, []chatbridge.NotificationEvent{validEvent, invalidEvent})

	require.ErrorContains(t, err, "webhook body template did not render valid JSON")
	assert.Equal(t, int32(0), requests.Load())
}

func notificationEventForRun(t *testing.T, dagRunID string) chatbridge.NotificationEvent {
	t.Helper()
	return chatbridge.NotificationEvent{
		Type: eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{
			Name:     "daily-report",
			DAGRunID: dagRunID,
			Status:   ir.Failed,
		},
	}
}

func TestRenderWebhookBodyTemplateEscapesValues(t *testing.T) {
	t.Parallel()

	event := chatbridge.NotificationEvent{
		Type: eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{
			Name:     "daily-report",
			DAGRunID: "run-1",
			Status:   ir.Failed,
			Error:    "exit status 1: \"boom\"\nsecond line",
		},
	}

	body := renderWebhookBodyTemplate(`{"text": "{{run.error}}"}`, event, "", "")

	require.True(t, json.Valid([]byte(body)), "rendered body must be valid JSON: %s", body)
	var decoded struct {
		Text string `json:"text"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &decoded))
	assert.Equal(t, "exit status 1: \"boom\"\nsecond line", decoded.Text)
}

func TestService_TeamsThrottledResponseIsRetried(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	svc := New(
		newMemoryStore(),
		nil,
		WithDeliveryRetry(DeliveryRetryConfig{MaxAttempts: 2}),
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if attempts.Add(1) == 1 {
				// Teams reports throttling in the body of a 200 response.
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body: io.NopCloser(strings.NewReader(
						"Microsoft Teams endpoint returned HTTP error 429",
					)),
					Request: req,
				}, nil
			}
			return acceptedResponse(req), nil
		})}),
	)
	target := notificationmodel.Target{
		ID:      "teams-1",
		Type:    notificationmodel.ProviderTeams,
		Enabled: true,
		Teams: &notificationmodel.TeamsTarget{
			WebhookURL: "https://93.184.216.34/workflows/trigger",
		},
	}

	err := svc.deliverTarget(context.Background(), target, []chatbridge.NotificationEvent{
		notificationEventForRun(t, "run-1"),
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), attempts.Load())
}

func TestService_SendTestTeamsPostsMessageCard(t *testing.T) {
	t.Parallel()

	var receivedBody atomic.Value
	svc := New(
		newMemoryStore(mustNormalizeSettings(t, &notificationmodel.Settings{
			DAGName: "daily-report",
			Enabled: true,
			Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
			Targets: []notificationmodel.Target{{
				ID:      "teams-1",
				Name:    "Ops Teams",
				Type:    notificationmodel.ProviderTeams,
				Enabled: true,
				Teams: &notificationmodel.TeamsTarget{
					WebhookURL:      "https://93.184.216.34/workflows/trigger",
					MessageTemplate: "DAG {{dag.name}} {{run.status}}\n{{run.url}}",
				},
			}},
		})),
		nil,
		WithPublicURL("https://dagu.example.com"),
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			receivedBody.Store(string(body))
			return acceptedResponse(req), nil
		})}),
	)

	results, err := svc.SendTest(context.Background(), "daily-report", "teams-1", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Delivered)

	body, _ := receivedBody.Load().(string)
	var payload struct {
		Type    string `json:"@type"`
		Context string `json:"@context"`
		Summary string `json:"summary"`
		Title   string `json:"title"`
		Text    string `json:"text"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &payload))
	assert.Equal(t, "MessageCard", payload.Type)
	assert.Equal(t, "http://schema.org/extensions", payload.Context)
	assert.Equal(t, "daily-report failed", payload.Summary)
	assert.Equal(t, "daily-report failed", payload.Title)
	assert.Equal(t,
		"DAG daily-report failed\nhttps://dagu.example.com/dag-runs/daily-report/notification-test",
		payload.Text,
	)
}

func TestTeamsPayloadForEventsSummarizesBatch(t *testing.T) {
	t.Parallel()

	payload := teamsPayloadForEvents("", []chatbridge.NotificationEvent{
		notificationEventForRun(t, "run-1"),
		notificationEventForRun(t, "run-2"),
	}, "")

	assert.Equal(t, "daily-report: 2 notifications", payload["summary"])
	assert.Equal(t, "daily-report: 2 notifications", payload["title"])
}

func TestService_SendTestWebhookIncludesRunLinks(t *testing.T) {
	t.Parallel()

	var receivedBody atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "failed to read request body", http.StatusInternalServerError)
			return
		}
		receivedBody.Store(string(body))
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
				URL:                 server.URL,
				AllowInsecureHTTP:   true,
				AllowPrivateNetwork: true,
			},
		}},
	}, "tester")
	require.NoError(t, err)
	svc := New(
		newMemoryStore(settings),
		nil,
		WithPublicURL("https://dagu.example.com/workflows/"),
	)

	results, err := svc.SendTest(context.Background(), "daily-report", "webhook-1", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Delivered)
	body, _ := receivedBody.Load().(string)
	assert.Contains(t, body, `"runPath":"/dag-runs/daily-report/notification-test"`)
	assert.Contains(t, body, `"runUrl":"https://dagu.example.com/workflows/dag-runs/daily-report/notification-test"`)
	assert.Contains(t, body, `Run: https://dagu.example.com/workflows/dag-runs/daily-report/notification-test`)
}

func TestService_SendTestSlackUsesCustomMessageTemplate(t *testing.T) {
	t.Parallel()

	var receivedBody atomic.Value
	svc := New(
		newMemoryStore(mustNormalizeSettings(t, &notificationmodel.Settings{
			DAGName: "daily-report",
			Enabled: true,
			Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
			Targets: []notificationmodel.Target{{
				ID:      "slack-1",
				Name:    "Ops Slack",
				Type:    notificationmodel.ProviderSlack,
				Enabled: true,
				Slack: &notificationmodel.SlackTarget{
					WebhookURL:      "https://93.184.216.34/slack",
					MessageTemplate: "DAG {{dag.name}} {{run.status}}: {{run.error}}",
				},
			}},
		})),
		nil,
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			receivedBody.Store(string(body))
			return acceptedResponse(req), nil
		})}),
	)

	results, err := svc.SendTest(context.Background(), "daily-report", "slack-1", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Delivered)
	body, _ := receivedBody.Load().(string)
	assert.Contains(t, body, `"text":"DAG daily-report failed: This is a test notification from Dagu."`)
}

func TestService_SendTestSlackDefaultMessageIncludesRunLink(t *testing.T) {
	t.Parallel()

	var receivedBody atomic.Value
	svc := New(
		newMemoryStore(mustNormalizeSettings(t, &notificationmodel.Settings{
			DAGName: "daily-report",
			Enabled: true,
			Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
			Targets: []notificationmodel.Target{{
				ID:      "slack-1",
				Name:    "Ops Slack",
				Type:    notificationmodel.ProviderSlack,
				Enabled: true,
				Slack: &notificationmodel.SlackTarget{
					WebhookURL: "https://93.184.216.34/slack",
				},
			}},
		})),
		nil,
		WithPublicURL("https://dagu.example.com/workflows"),
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			receivedBody.Store(string(body))
			return acceptedResponse(req), nil
		})}),
	)

	results, err := svc.SendTest(context.Background(), "daily-report", "slack-1", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Delivered)
	body, _ := receivedBody.Load().(string)
	assert.Contains(t, body, `Run: https://dagu.example.com/workflows/dag-runs/daily-report/notification-test`)
}

func TestService_SendTestSlackTemplateRunLinkIsEmptyWithoutPublicURL(t *testing.T) {
	t.Parallel()

	var receivedBody atomic.Value
	svc := New(
		newMemoryStore(mustNormalizeSettings(t, &notificationmodel.Settings{
			DAGName: "daily-report",
			Enabled: true,
			Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
			Targets: []notificationmodel.Target{{
				ID:      "slack-1",
				Name:    "Ops Slack",
				Type:    notificationmodel.ProviderSlack,
				Enabled: true,
				Slack: &notificationmodel.SlackTarget{
					WebhookURL:      "https://93.184.216.34/slack",
					MessageTemplate: "DAG {{dag.name}} {{run.status}}\n{{run.link}}",
				},
			}},
		})),
		nil,
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			receivedBody.Store(string(body))
			return acceptedResponse(req), nil
		})}),
	)

	results, err := svc.SendTest(context.Background(), "daily-report", "slack-1", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Delivered)
	body, _ := receivedBody.Load().(string)
	assert.Contains(t, body, `"text":"DAG daily-report failed"`)
	assert.NotContains(t, body, "Run:")
	assert.NotContains(t, body, "localhost")
}

func TestNotificationTemplateRunPathSupportsSubDAGRun(t *testing.T) {
	t.Parallel()

	event := chatbridge.NotificationEvent{
		Type: eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{
			Root:     ir.NewDAGRunRef("root dag", "root run"),
			Parent:   ir.NewDAGRunRef("root dag", "root run"),
			Name:     "child dag",
			DAGRunID: "child run",
			Status:   ir.Failed,
		},
		ObservedAt: time.Now().UTC(),
	}

	rendered := renderNotificationTemplate(
		"{{run.path}}\n{{run.url}}\n{{run.link}}",
		event,
		"https://dagu.example.com/workflows/",
	)

	assert.Contains(t, rendered, "/dag-runs/root%20dag/root%20run?")
	assert.Contains(t, rendered, "dagRunId=root+run")
	assert.Contains(t, rendered, "dagRunName=root+dag")
	assert.Contains(t, rendered, "subDAGRunId=child+run")
	assert.Contains(t, rendered, "https://dagu.example.com/workflows/dag-runs/root%20dag/root%20run?")
	assert.Contains(t, rendered, "Run: https://dagu.example.com/workflows/dag-runs/root%20dag/root%20run?")
}

func TestNotificationTemplateIncludesStepStatusLists(t *testing.T) {
	t.Parallel()

	event := chatbridge.NotificationEvent{
		Type: eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{
			Name:     "daily-report",
			DAGRunID: "run-1",
			Status:   ir.Failed,
			Nodes: []*ir.Node{
				{Step: ir.Step{Name: "fetch"}, Status: ir.NodeFailed},
				{Step: ir.Step{Name: "publish"}, Status: ir.NodePartiallySucceeded},
				{Step: ir.Step{Name: "cleanup"}, Status: ir.NodeAborted},
				{Step: ir.Step{Name: "prepare"}, Status: ir.NodeSucceeded},
				{
					Step:   ir.Step{Name: "process"},
					Status: ir.NodeFailed,
					StatusDetails: []ir.NodeStatusDetail{
						{Label: "customer-a", Status: ir.NodeFailed},
						{Label: "customer-b", Status: ir.NodeSucceeded},
					},
				},
				{
					Step:   ir.Step{Name: "children"},
					Status: ir.NodePartiallySucceeded,
					StatusDetails: []ir.NodeStatusDetail{
						{Label: "child-a", Status: ir.NodePartiallySucceeded},
						{Label: "child-b", Status: ir.NodeAborted},
					},
				},
			},
		},
	}

	rendered := renderNotificationTemplate(
		"Failed: {{run.failed_steps}}\nPartial: {{run.partially_succeeded_steps}}\nAborted: {{run.aborted_steps}}\nSucceeded: {{run.succeeded_steps}}",
		event,
		"",
	)

	assert.Equal(t, strings.Join([]string{
		"Failed: fetch, process[customer-a]",
		"Partial: publish, children[child-a]",
		"Aborted: cleanup, children[child-b]",
		"Succeeded: prepare, process[customer-b]",
	}, "\n"), rendered)

	emptyEvent := chatbridge.NotificationEvent{Status: &ir.DAGRunStatus{
		Nodes: []*ir.Node{{Step: ir.Step{Name: "fetch"}, Status: ir.NodeFailed}},
	}}
	assert.Empty(t, renderNotificationTemplate("{{run.succeeded_steps}}", emptyEvent, ""))
}

func TestService_SendTestTelegramUsesCustomMessageTemplate(t *testing.T) {
	t.Parallel()

	var receivedBody atomic.Value
	svc := New(
		newMemoryStore(mustNormalizeSettings(t, &notificationmodel.Settings{
			DAGName: "daily-report",
			Enabled: true,
			Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
			Targets: []notificationmodel.Target{{
				ID:      "telegram-1",
				Name:    "Ops Telegram",
				Type:    notificationmodel.ProviderTelegram,
				Enabled: true,
				Telegram: &notificationmodel.TelegramTarget{
					BotToken:        "telegram-token",
					ChatID:          "12345",
					TopicID:         "67890",
					MessageTemplate: "DAG {{dag.name}} {{run.status}}",
				},
			}},
		})),
		nil,
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			receivedBody.Store(string(body))
			return acceptedResponse(req), nil
		})}),
	)

	results, err := svc.SendTest(context.Background(), "daily-report", "telegram-1", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Delivered)
	body, _ := receivedBody.Load().(string)
	assert.Contains(t, body, `"chat_id":"12345"`)
	assert.Contains(t, body, `"message_thread_id":67890`)
	assert.Contains(t, body, `"text":"DAG daily-report failed"`)
}

func TestService_SendTestEmailRequiresWorkspaceSMTP(t *testing.T) {
	t.Parallel()

	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notificationmodel.Target{{
			ID:      "email-1",
			Type:    notificationmodel.ProviderEmail,
			Enabled: true,
			Email:   &notificationmodel.EmailTarget{To: []string{"ops@example.com"}},
		}},
	}, "tester")
	require.NoError(t, err)
	svc := New(newMemoryStore(settings), nil)

	results, err := svc.SendTest(context.Background(), "daily-report", "email-1", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Delivered)
	assert.Contains(t, results[0].Error, "SMTP is not configured for notification email")
}

func TestService_SendTestUsesEffectiveGlobalRouteWithoutDAGSettings(t *testing.T) {
	t.Parallel()

	var receivedBody atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "failed to read request body", http.StatusInternalServerError)
			return
		}
		receivedBody.Store(string(body))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	channel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
		ID:      "channel-1",
		Name:    "Global Ops",
		Type:    notificationmodel.ProviderWebhook,
		Enabled: true,
		Webhook: &notificationmodel.WebhookTarget{
			URL:                 server.URL,
			AllowInsecureHTTP:   true,
			AllowPrivateNetwork: true,
		},
	}, "tester")
	require.NoError(t, err)
	store := newMemoryStore()
	require.NoError(t, store.SaveChannel(context.Background(), channel))
	svc := New(store, nil)
	_, err = svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeGlobal,
		Enabled:       true,
		InheritGlobal: true,
		Routes: []notificationmodel.Route{{
			ID:        "global-route",
			ChannelID: "channel-1",
			Enabled:   true,
			Events:    []eventstore.EventType{eventstore.TypeDAGRunFailed},
		}},
	}, "tester")
	require.NoError(t, err)

	results, err := svc.SendTest(context.Background(), "daily-report", "", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "global-route", results[0].TargetID)
	assert.Equal(t, "Global Ops", results[0].TargetName)
	assert.True(t, results[0].Delivered)
	body, _ := receivedBody.Load().(string)
	assert.Contains(t, body, `"dagName":"daily-report"`)
}

func TestService_SendTestUsesEffectiveWorkspaceRouteFromDAGLabels(t *testing.T) {
	t.Parallel()

	var globalRequests atomic.Int32
	var workspaceRequests atomic.Int32
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Hostname() {
		case "global.example.com":
			globalRequests.Add(1)
		case "workspace.example.com":
			workspaceRequests.Add(1)
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}
		return acceptedResponse(req), nil
	})}

	store := newMemoryStore()
	for _, channel := range []*notificationmodel.Channel{
		{
			ID:      "global-channel",
			Name:    "Global Ops",
			Type:    notificationmodel.ProviderWebhook,
			Enabled: true,
			Webhook: &notificationmodel.WebhookTarget{
				URL:                 "https://global.example.com/webhook",
				AllowInsecureHTTP:   true,
				AllowPrivateNetwork: true,
			},
		},
		{
			ID:      "workspace-channel",
			Name:    "Workspace Ops",
			Type:    notificationmodel.ProviderWebhook,
			Enabled: true,
			Webhook: &notificationmodel.WebhookTarget{
				URL:                 "https://workspace.example.com/webhook",
				AllowInsecureHTTP:   true,
				AllowPrivateNetwork: true,
			},
		},
	} {
		normalized, err := notificationmodel.NormalizeChannel(channel, "tester")
		require.NoError(t, err)
		require.NoError(t, store.SaveChannel(context.Background(), normalized))
	}
	dagRepository := persis.NewDAGRepository(testDAGDefinitionStore{
		definition: persis.DAGDefinition{
			ID: "daily-report",
			Source: []byte(`
name: daily-report
labels:
  - workspace=ops
steps: []
`),
		},
	}, persis.DAGRepositoryOptions{})
	svc := New(store, dagRepository, WithHTTPClient(httpClient))
	_, err := svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeGlobal,
		Enabled:       true,
		InheritGlobal: true,
		Routes: []notificationmodel.Route{{
			ID:        "global-route",
			ChannelID: "global-channel",
			Enabled:   true,
		}},
	}, "tester")
	require.NoError(t, err)
	_, err = svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeWorkspace,
		Workspace:     "ops",
		Enabled:       true,
		InheritGlobal: false,
		Routes: []notificationmodel.Route{{
			ID:        "workspace-route",
			ChannelID: "workspace-channel",
			Enabled:   true,
		}},
	}, "tester")
	require.NoError(t, err)

	results, err := svc.SendTest(context.Background(), "daily-report-file", "", eventstore.TypeDAGRunFailed)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "workspace-route", results[0].TargetID)
	assert.Equal(t, "Workspace Ops", results[0].TargetName)
	assert.True(t, results[0].Delivered)
	assert.Equal(t, int32(0), globalRequests.Load())
	assert.Equal(t, int32(1), workspaceRequests.Load())
}

func TestService_SaveWorkspaceSettingsPreservesCreatedAtAndPassword(t *testing.T) {
	t.Parallel()

	svc := New(newMemoryStore(), nil)
	first, err := svc.SaveWorkspaceSettings(context.Background(), &notificationmodel.WorkspaceSettings{
		SMTP: &notificationmodel.SMTPConfig{
			Host:     "smtp.example.com",
			Port:     "587",
			Username: "smtp-user",
			Password: "smtp-secret",
			From:     "dagu@example.com",
		},
	}, "creator")
	require.NoError(t, err)

	time.Sleep(2 * time.Millisecond)

	updated, err := svc.SaveWorkspaceSettings(context.Background(), &notificationmodel.WorkspaceSettings{
		SMTP: &notificationmodel.SMTPConfig{
			Host:     "smtp2.example.com",
			Port:     "2525",
			Username: "smtp-user",
			From:     "dagu@example.com",
		},
	}, "updater")
	require.NoError(t, err)

	assert.Equal(t, first.CreatedAt, updated.CreatedAt)
	assert.True(t, updated.UpdatedAt.After(first.UpdatedAt))
	require.NotNil(t, updated.SMTP)
	assert.Equal(t, "smtp-secret", updated.SMTP.Password)
	assert.Equal(t, "updater", updated.UpdatedBy)
}

func TestService_WorkspaceInheritUsesGlobalRoutesOnly(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	globalChannel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
		ID:      "global-channel",
		Name:    "Global Ops",
		Type:    notificationmodel.ProviderWebhook,
		Enabled: true,
		Webhook: &notificationmodel.WebhookTarget{URL: "https://example.com/global"},
	}, "tester")
	require.NoError(t, err)
	workspaceChannel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
		ID:      "ops-channel",
		Name:    "Ops",
		Type:    notificationmodel.ProviderWebhook,
		Enabled: true,
		Webhook: &notificationmodel.WebhookTarget{URL: "https://example.com/ops"},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.SaveChannel(context.Background(), globalChannel))
	require.NoError(t, store.SaveChannel(context.Background(), workspaceChannel))

	svc := New(store, nil)
	_, err = svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeGlobal,
		Enabled:       true,
		InheritGlobal: true,
		Routes: []notificationmodel.Route{{
			ID:        "global-route",
			ChannelID: "global-channel",
			Enabled:   true,
			Events:    []eventstore.EventType{eventstore.TypeDAGRunFailed},
		}},
	}, "tester")
	require.NoError(t, err)
	_, err = svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeWorkspace,
		Workspace:     "ops",
		Enabled:       true,
		InheritGlobal: true,
		Routes: []notificationmodel.Route{{
			ID:        "workspace-route",
			ChannelID: "ops-channel",
			Enabled:   true,
			Events:    []eventstore.EventType{eventstore.TypeDAGRunFailed},
		}},
	}, "tester")
	require.NoError(t, err)

	destinations := svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type: eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{
			Name:   "daily-report",
			Status: ir.Failed,
			Labels: []string{"workspace=ops"},
		},
	})
	assert.ElementsMatch(t, []string{
		routeDestinationID(notificationmodel.RouteScopeGlobal, "", "global-route"),
	}, destinations)

	defaultDestinations := svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type:   eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{Name: "daily-report", Status: ir.Failed},
	})
	assert.ElementsMatch(t, []string{
		routeDestinationID(notificationmodel.RouteScopeGlobal, "", "global-route"),
	}, defaultDestinations)

	invalidWorkspace := svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type: eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{
			Name:   "daily-report",
			Status: ir.Failed,
			Labels: []string{"workspace=ops", "workspace=engineering"},
		},
	})
	assert.Empty(t, invalidWorkspace)

	assert.Empty(t, svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type:   eventstore.TypeDAGRunSucceeded,
		Status: &ir.DAGRunStatus{Name: "daily-report", Status: ir.Succeeded, Labels: []string{"workspace=ops"}},
	}))
}

func TestService_WorkspaceConfiguredRoutesOverrideGlobalRoutes(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	for _, channelID := range []string{"global-channel", "ops-channel"} {
		channel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
			ID:      channelID,
			Name:    channelID,
			Type:    notificationmodel.ProviderWebhook,
			Enabled: true,
			Webhook: &notificationmodel.WebhookTarget{URL: "https://example.com/" + channelID},
		}, "tester")
		require.NoError(t, err)
		require.NoError(t, store.SaveChannel(context.Background(), channel))
	}

	svc := New(store, nil)
	_, err := svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeGlobal,
		Enabled:       true,
		InheritGlobal: true,
		Routes: []notificationmodel.Route{{
			ID:        "global-route",
			ChannelID: "global-channel",
			Enabled:   true,
		}},
	}, "tester")
	require.NoError(t, err)
	_, err = svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeWorkspace,
		Workspace:     "ops",
		Enabled:       true,
		InheritGlobal: false,
		Routes: []notificationmodel.Route{{
			ID:        "workspace-route",
			ChannelID: "ops-channel",
			Enabled:   true,
		}},
	}, "tester")
	require.NoError(t, err)

	destinations := svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type: eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{
			Name:   "daily-report",
			Status: ir.Failed,
			Labels: []string{"workspace=ops"},
		},
	})
	assert.ElementsMatch(t, []string{
		routeDestinationID(notificationmodel.RouteScopeWorkspace, "ops", "workspace-route"),
	}, destinations)
}

func TestService_ConfiguredWorkspaceWithoutRoutesSuppressesGlobalRoutes(t *testing.T) {
	t.Parallel()

	channel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
		ID:      "global-channel",
		Name:    "Global Ops",
		Type:    notificationmodel.ProviderWebhook,
		Enabled: true,
		Webhook: &notificationmodel.WebhookTarget{URL: "https://example.com/global"},
	}, "tester")
	require.NoError(t, err)
	store := newMemoryStore()
	require.NoError(t, store.SaveChannel(context.Background(), channel))
	svc := New(store, nil)
	_, err = svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeGlobal,
		Enabled:       true,
		InheritGlobal: true,
		Routes: []notificationmodel.Route{{
			ID:        "global-route",
			ChannelID: "global-channel",
			Enabled:   true,
		}},
	}, "tester")
	require.NoError(t, err)
	_, err = svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeWorkspace,
		Workspace:     "ops",
		Enabled:       true,
		InheritGlobal: false,
		Routes:        []notificationmodel.Route{},
	}, "tester")
	require.NoError(t, err)

	destinations := svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type: eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{
			Name:   "daily-report",
			Status: ir.Failed,
			Labels: []string{"workspace=ops"},
		},
	})
	assert.Empty(t, destinations)
}

func TestService_DAGSettingsOverrideGlobalAndWorkspaceRoutes(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	for _, channelID := range []string{"global-channel", "workspace-channel", "dag-channel"} {
		channel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
			ID:      channelID,
			Name:    channelID,
			Type:    notificationmodel.ProviderWebhook,
			Enabled: true,
			Webhook: &notificationmodel.WebhookTarget{URL: "https://example.com/" + channelID},
		}, "tester")
		require.NoError(t, err)
		require.NoError(t, store.SaveChannel(context.Background(), channel))
	}
	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report-file",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Subscriptions: []notificationmodel.Subscription{{
			ID:        "dag-route",
			ChannelID: "dag-channel",
			Enabled:   true,
		}},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), settings))
	svc := New(store, nil)
	_, err = svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeGlobal,
		Enabled:       true,
		InheritGlobal: true,
		Routes: []notificationmodel.Route{{
			ID:        "global-route",
			ChannelID: "global-channel",
			Enabled:   true,
		}},
	}, "tester")
	require.NoError(t, err)
	_, err = svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeWorkspace,
		Workspace:     "ops",
		Enabled:       true,
		InheritGlobal: false,
		Routes: []notificationmodel.Route{{
			ID:        "workspace-route",
			ChannelID: "workspace-channel",
			Enabled:   true,
		}},
	}, "tester")
	require.NoError(t, err)

	destinations := svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type:    eventstore.TypeDAGRunFailed,
		DAGFile: "daily-report-file",
		Status: &ir.DAGRunStatus{
			Name:   "daily-report",
			Status: ir.Failed,
			Labels: []string{"workspace=ops"},
		},
	})
	assert.ElementsMatch(t, []string{
		channelDestinationID("daily-report-file", "dag-route"),
	}, destinations)
}

func TestService_DisabledDAGSettingsSuppressInheritedRoutes(t *testing.T) {
	t.Parallel()

	channel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
		ID:      "global-channel",
		Name:    "Global Ops",
		Type:    notificationmodel.ProviderWebhook,
		Enabled: true,
		Webhook: &notificationmodel.WebhookTarget{URL: "https://example.com/global"},
	}, "tester")
	require.NoError(t, err)
	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: false,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
	}, "tester")
	require.NoError(t, err)
	store := newMemoryStore(settings)
	require.NoError(t, store.SaveChannel(context.Background(), channel))
	svc := New(store, nil)
	_, err = svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeGlobal,
		Enabled:       true,
		InheritGlobal: true,
		Routes: []notificationmodel.Route{{
			ID:        "global-route",
			ChannelID: "global-channel",
			Enabled:   true,
		}},
	}, "tester")
	require.NoError(t, err)

	destinations := svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type:   eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{Name: "daily-report", Status: ir.Failed},
	})
	assert.Empty(t, destinations)
}

func TestService_GlobalRouteFlushSkipsWorkspaceWithDisabledInheritance(t *testing.T) {
	t.Parallel()

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	channel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
		ID:      "channel-1",
		Name:    "Ops",
		Type:    notificationmodel.ProviderWebhook,
		Enabled: true,
		Webhook: &notificationmodel.WebhookTarget{
			URL:                 server.URL,
			AllowInsecureHTTP:   true,
			AllowPrivateNetwork: true,
		},
	}, "tester")
	require.NoError(t, err)
	store := newMemoryStore()
	require.NoError(t, store.SaveChannel(context.Background(), channel))
	svc := New(store, nil)
	_, err = svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeGlobal,
		Enabled:       true,
		InheritGlobal: true,
		Routes: []notificationmodel.Route{{
			ID:        "global-route",
			ChannelID: "channel-1",
			Enabled:   true,
		}},
	}, "tester")
	require.NoError(t, err)
	_, err = svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeWorkspace,
		Workspace:     "ops",
		Enabled:       true,
		InheritGlobal: false,
	}, "tester")
	require.NoError(t, err)

	delivered := svc.FlushNotificationBatch(
		context.Background(),
		routeDestinationID(notificationmodel.RouteScopeGlobal, "", "global-route"),
		chatbridge.NotificationBatch{Events: []chatbridge.NotificationEvent{{
			Type: eventstore.TypeDAGRunFailed,
			Status: &ir.DAGRunStatus{
				Name:   "daily-report",
				Status: ir.Failed,
				Labels: []string{"workspace=ops"},
			},
			ObservedAt: time.Now().UTC(),
		}}},
		false,
	)
	assert.True(t, delivered)
	assert.Equal(t, int32(0), requestCount.Load())
}

func TestService_RouteFlushSkipsDAGWithConfiguredNotifications(t *testing.T) {
	t.Parallel()

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	channel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
		ID:      "channel-1",
		Name:    "Ops",
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
		DAGName: "daily-report-file",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
	}, "tester")
	require.NoError(t, err)
	store := newMemoryStore(settings)
	require.NoError(t, store.SaveChannel(context.Background(), channel))
	svc := New(store, nil)
	_, err = svc.SaveRouteSet(context.Background(), &notificationmodel.RouteSet{
		Scope:         notificationmodel.RouteScopeGlobal,
		Enabled:       true,
		InheritGlobal: true,
		Routes: []notificationmodel.Route{{
			ID:        "global-route",
			ChannelID: "channel-1",
			Enabled:   true,
		}},
	}, "tester")
	require.NoError(t, err)

	delivered := svc.FlushNotificationBatch(
		context.Background(),
		routeDestinationID(notificationmodel.RouteScopeGlobal, "", "global-route"),
		chatbridge.NotificationBatch{Events: []chatbridge.NotificationEvent{{
			Type:    eventstore.TypeDAGRunFailed,
			DAGFile: "daily-report-file",
			Status: &ir.DAGRunStatus{
				Name:   "daily-report",
				Status: ir.Failed,
			},
			ObservedAt: time.Now().UTC(),
		}}},
		false,
	)
	assert.True(t, delivered)
	assert.Equal(t, int32(0), requestCount.Load())
}

func TestService_NotificationDestinationsForEventFiltersByDAGAndEvent(t *testing.T) {
	t.Parallel()

	var requestCount atomic.Int32
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestCount.Add(1)
		return acceptedResponse(req), nil
	})}
	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report-file",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed, eventstore.TypeDAGRunWaiting},
		Targets: []notificationmodel.Target{
			{
				ID:      "webhook-1",
				Type:    notificationmodel.ProviderWebhook,
				Enabled: true,
				Events:  []eventstore.EventType{eventstore.TypeDAGRunWaiting},
				Webhook: &notificationmodel.WebhookTarget{
					URL:                 "https://example.com/webhook",
					AllowPrivateNetwork: true,
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
	svc := New(newMemoryStore(settings), nil, WithHTTPClient(httpClient))

	waitingEvent := chatbridge.NotificationEvent{
		Type:    eventstore.TypeDAGRunWaiting,
		DAGFile: "daily-report-file",
		Status: &ir.DAGRunStatus{
			Name:      "daily-report",
			Status:    ir.Waiting,
			DAGRunID:  "run-1",
			AttemptID: "attempt-1",
		},
	}
	destinations := svc.NotificationDestinationsForEvent(waitingEvent)
	require.Len(t, destinations, 1)
	assert.Contains(t, destinations[0], "webhook-1")
	assert.True(t, svc.FlushNotificationBatch(context.Background(), destinations[0], chatbridge.NotificationBatch{
		Events: []chatbridge.NotificationEvent{waitingEvent},
	}, false))
	assert.Equal(t, int32(1), requestCount.Load())

	assert.Empty(t, svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type:    eventstore.TypeDAGRunFailed,
		DAGFile: "daily-report-file",
		Status:  &ir.DAGRunStatus{Name: "daily-report", Status: ir.Failed},
	}))
	assert.Empty(t, svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type:    eventstore.TypeDAGRunFailed,
		DAGFile: "other-file",
		Status:  &ir.DAGRunStatus{Name: "other-dag", Status: ir.Failed},
	}))
}

func TestServicePartialSuccessRouting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		events []eventstore.EventType
		event  eventstore.EventType
		status ir.Status
		want   int
	}{
		{
			name:   "succeeded includes partial success",
			events: []eventstore.EventType{eventstore.TypeDAGRunSucceeded},
			event:  eventstore.TypeDAGRunPartiallySucceeded,
			status: ir.PartiallySucceeded,
			want:   1,
		},
		{
			name:   "partial success matches partial success",
			events: []eventstore.EventType{eventstore.TypeDAGRunPartiallySucceeded},
			event:  eventstore.TypeDAGRunPartiallySucceeded,
			status: ir.PartiallySucceeded,
			want:   1,
		},
		{
			name:   "partial success excludes clean success",
			events: []eventstore.EventType{eventstore.TypeDAGRunPartiallySucceeded},
			event:  eventstore.TypeDAGRunSucceeded,
			status: ir.Succeeded,
		},
		{
			name: "selecting both produces one destination",
			events: []eventstore.EventType{
				eventstore.TypeDAGRunSucceeded,
				eventstore.TypeDAGRunPartiallySucceeded,
			},
			event:  eventstore.TypeDAGRunPartiallySucceeded,
			status: ir.PartiallySucceeded,
			want:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
				DAGName: "daily-report",
				Enabled: true,
				Events:  tt.events,
				Targets: []notificationmodel.Target{{
					ID:      "webhook-1",
					Type:    notificationmodel.ProviderWebhook,
					Enabled: true,
					Webhook: &notificationmodel.WebhookTarget{URL: "https://example.com/webhook"},
				}},
			}, "tester")
			require.NoError(t, err)

			svc := New(newMemoryStore(settings), nil)
			destinations := svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
				Type: tt.event,
				Status: &ir.DAGRunStatus{
					Name:      "daily-report",
					Status:    tt.status,
					DAGRunID:  "run-1",
					AttemptID: "attempt-1",
				},
			})

			assert.Len(t, destinations, tt.want)
		})
	}
}

func TestPartialSuccessTestStatus(t *testing.T) {
	t.Parallel()

	status := testStatus("daily-report", eventstore.TypeDAGRunPartiallySucceeded)

	assert.Equal(t, ir.PartiallySucceeded, status.Status)
	assert.Contains(t, status.Error, "partially succeeded")
}

type recordingSMTPServer struct {
	host     string
	port     string
	listener net.Listener
	mailFrom atomic.Value
	rcptTo   atomic.Value
	data     atomic.Value
}

func newRecordingSMTPServer(t *testing.T) *recordingSMTPServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	server := &recordingSMTPServer{
		host:     host,
		port:     port,
		listener: listener,
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})
	go server.serve()
	return server
}

func (s *recordingSMTPServer) serve() {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer func() {
		_ = conn.Close()
	}()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeSMTPLine(writer, "220 mock.local ESMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			writeSMTPLine(writer, "250 mock.local")
		case strings.HasPrefix(line, "MAIL FROM:"):
			s.mailFrom.Store(extractSMTPAddress(line))
			writeSMTPLine(writer, "250 OK")
		case strings.HasPrefix(line, "RCPT TO:"):
			s.rcptTo.Store(extractSMTPAddress(line))
			writeSMTPLine(writer, "250 OK")
		case line == "DATA":
			writeSMTPLine(writer, "354 End data with <CR><LF>.<CR><LF>")
			var data strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				data.WriteString(dataLine)
			}
			s.data.Store(data.String())
			writeSMTPLine(writer, "250 OK")
		case line == "QUIT":
			writeSMTPLine(writer, "221 Bye")
			return
		default:
			writeSMTPLine(writer, "250 OK")
		}
	}
}

func writeSMTPLine(writer *bufio.Writer, line string) {
	_, _ = writer.WriteString(line + "\r\n")
	_ = writer.Flush()
}

func extractSMTPAddress(line string) string {
	start := strings.Index(line, "<")
	end := strings.LastIndex(line, ">")
	if start >= 0 && end > start {
		return line[start+1 : end]
	}
	_, value, ok := strings.Cut(line, ":")
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func TestService_NotificationDestinationsCachesReusableChannelLookups(t *testing.T) {
	t.Parallel()

	channel, err := notificationmodel.NormalizeChannel(&notificationmodel.Channel{
		ID:      "channel-1",
		Name:    "Ops Webhook",
		Type:    notificationmodel.ProviderWebhook,
		Enabled: true,
		Webhook: &notificationmodel.WebhookTarget{
			URL: "https://example.com/webhook",
		},
	}, "tester")
	require.NoError(t, err)
	dailyReport, err := notificationmodel.Normalize(&notificationmodel.Settings{
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
	nightlyReport, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "nightly-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Subscriptions: []notificationmodel.Subscription{{
			ID:        "subscription-2",
			ChannelID: "channel-1",
			Enabled:   true,
		}},
	}, "tester")
	require.NoError(t, err)
	store := newMemoryStore(dailyReport, nightlyReport)
	require.NoError(t, store.SaveChannel(context.Background(), channel))
	svc := New(store, nil)

	destinations := svc.NotificationDestinations()
	assert.ElementsMatch(t, []string{
		channelDestinationID("daily-report", "subscription-1"),
		channelDestinationID("nightly-report", "subscription-2"),
	}, destinations)
	assert.Equal(t, 1, store.GetChannelCount("channel-1"))
}

func TestService_ReusableChannelSubscriptionsDeliverForMatchingDAGEvent(t *testing.T) {
	t.Parallel()

	var receivedBody atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "failed to read request body", http.StatusInternalServerError)
			return
		}
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
		DAGName: "daily-report-file",
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
		Type:    eventstore.TypeDAGRunFailed,
		DAGFile: "daily-report-file",
		Status: &ir.DAGRunStatus{
			Name:      "daily-report",
			Status:    ir.Failed,
			DAGRunID:  "run-1",
			AttemptID: "attempt-1",
		},
	})
	require.Len(t, destinations, 1)

	delivered := svc.FlushNotificationBatch(context.Background(), destinations[0], chatbridge.NotificationBatch{
		Events: []chatbridge.NotificationEvent{{
			Type:       eventstore.TypeDAGRunFailed,
			DAGFile:    "daily-report-file",
			Status:     &ir.DAGRunStatus{Name: "daily-report", Status: ir.Failed, DAGRunID: "run-1"},
			ObservedAt: time.Now().UTC(),
		}},
	}, false)
	assert.True(t, delivered)
	body, _ := receivedBody.Load().(string)
	assert.Contains(t, body, `"dagName":"daily-report"`)

	assert.Empty(t, svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type:    eventstore.TypeDAGRunSucceeded,
		DAGFile: "daily-report-file",
		Status:  &ir.DAGRunStatus{Name: "daily-report", Status: ir.Succeeded},
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
		Status: &ir.DAGRunStatus{
			Name:      "daily-report",
			Status:    ir.Failed,
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

func TestService_SaveRejectsNilSettings(t *testing.T) {
	t.Parallel()

	svc := New(newMemoryStore(), nil)
	_, err := svc.Save(context.Background(), nil, "tester")
	assert.ErrorIs(t, err, notificationmodel.ErrInvalidSettings)
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
