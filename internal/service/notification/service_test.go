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
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	notificationmodel "github.com/dagucloud/dagu/internal/notification"
	"github.com/dagucloud/dagu/internal/service/chatbridge"
	"github.com/dagucloud/dagu/internal/service/eventstore"
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

type testDAGStore struct {
	dag *core.DAG
}

func (s testDAGStore) Create(context.Context, string, []byte) error {
	return nil
}

func (s testDAGStore) Delete(context.Context, string) error {
	return nil
}

func (s testDAGStore) List(context.Context, exec.ListDAGsOptions) (exec.PaginatedResult[*core.DAG], []string, error) {
	return exec.PaginatedResult[*core.DAG]{}, nil, nil
}

func (s testDAGStore) GetMetadata(context.Context, string) (*core.DAG, error) {
	return s.dag, nil
}

func (s testDAGStore) GetDetails(context.Context, string, ...spec.LoadOption) (*core.DAG, error) {
	return s.dag, nil
}

func (s testDAGStore) Grep(context.Context, string) ([]*exec.GrepDAGsResult, []string, error) {
	return nil, nil, nil
}

func (s testDAGStore) SearchCursor(context.Context, exec.SearchDAGsOptions) (*exec.CursorResult[exec.SearchDAGResult], []string, error) {
	return &exec.CursorResult[exec.SearchDAGResult]{}, nil, nil
}

func (s testDAGStore) SearchMatches(context.Context, string, exec.SearchDAGMatchesOptions) (*exec.CursorResult[*exec.Match], error) {
	return &exec.CursorResult[*exec.Match]{}, nil
}

func (s testDAGStore) Rename(context.Context, string, string) error {
	return nil
}

func (s testDAGStore) GetSpec(context.Context, string) (string, error) {
	return "", nil
}

func (s testDAGStore) UpdateSpec(context.Context, string, []byte) error {
	return nil
}

func (s testDAGStore) LoadSpec(context.Context, []byte, ...spec.LoadOption) (*core.DAG, error) {
	return s.dag, nil
}

func (s testDAGStore) LabelList(context.Context) ([]string, []string, error) {
	return nil, nil, nil
}

func (s testDAGStore) ToggleSuspend(context.Context, string, bool) error {
	return nil
}

func (s testDAGStore) IsSuspended(context.Context, string) bool {
	return false
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
		require.NoError(t, err)
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
		require.NoError(t, err)
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
	globalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		globalRequests.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer globalServer.Close()
	workspaceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		workspaceRequests.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer workspaceServer.Close()

	store := newMemoryStore()
	for _, channel := range []*notificationmodel.Channel{
		{
			ID:      "global-channel",
			Name:    "Global Ops",
			Type:    notificationmodel.ProviderWebhook,
			Enabled: true,
			Webhook: &notificationmodel.WebhookTarget{
				URL:                 globalServer.URL,
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
				URL:                 workspaceServer.URL,
				AllowInsecureHTTP:   true,
				AllowPrivateNetwork: true,
			},
		},
	} {
		normalized, err := notificationmodel.NormalizeChannel(channel, "tester")
		require.NoError(t, err)
		require.NoError(t, store.SaveChannel(context.Background(), normalized))
	}
	svc := New(store, testDAGStore{dag: &core.DAG{
		Name:   "daily-report",
		Labels: core.NewLabels([]string{"workspace=ops"}),
	}})
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

	results, err := svc.SendTest(context.Background(), "daily-report", "", eventstore.TypeDAGRunFailed)
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
		Status: &exec.DAGRunStatus{
			Name:   "daily-report",
			Status: core.Failed,
			Labels: []string{"workspace=ops"},
		},
	})
	assert.ElementsMatch(t, []string{
		routeDestinationID(notificationmodel.RouteScopeGlobal, "", "global-route"),
	}, destinations)

	defaultDestinations := svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type:   eventstore.TypeDAGRunFailed,
		Status: &exec.DAGRunStatus{Name: "daily-report", Status: core.Failed},
	})
	assert.ElementsMatch(t, []string{
		routeDestinationID(notificationmodel.RouteScopeGlobal, "", "global-route"),
	}, defaultDestinations)

	assert.Empty(t, svc.NotificationDestinationsForEvent(chatbridge.NotificationEvent{
		Type:   eventstore.TypeDAGRunSucceeded,
		Status: &exec.DAGRunStatus{Name: "daily-report", Status: core.Succeeded, Labels: []string{"workspace=ops"}},
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
		Status: &exec.DAGRunStatus{
			Name:   "daily-report",
			Status: core.Failed,
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
		Status: &exec.DAGRunStatus{
			Name:   "daily-report",
			Status: core.Failed,
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
		DAGName: "daily-report",
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
		Type: eventstore.TypeDAGRunFailed,
		Status: &exec.DAGRunStatus{
			Name:   "daily-report",
			Status: core.Failed,
			Labels: []string{"workspace=ops"},
		},
	})
	assert.ElementsMatch(t, []string{
		channelDestinationID("daily-report", "dag-route"),
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
		Status: &exec.DAGRunStatus{Name: "daily-report", Status: core.Failed},
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
			Status: &exec.DAGRunStatus{
				Name:   "daily-report",
				Status: core.Failed,
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
		DAGName: "daily-report",
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
			Type: eventstore.TypeDAGRunFailed,
			Status: &exec.DAGRunStatus{
				Name:   "daily-report",
				Status: core.Failed,
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
