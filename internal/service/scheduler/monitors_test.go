// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	notificationservice "github.com/dagucloud/dagu/v2/internal/service/notification"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type notificationRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn notificationRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestNotificationServiceUsesConfiguredPublicURL(t *testing.T) {
	t.Parallel()

	type receivedRequest struct {
		body string
		err  error
	}
	received := make(chan receivedRequest, 1)
	httpClient := &http.Client{Transport: notificationRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		received <- receivedRequest{body: string(body), err: err}
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})}

	cfg := &config.Config{
		Paths:  config.PathsConfig{DataDir: t.TempDir()},
		Server: config.Server{PublicURL: "https://dagu.example.com/workflows"},
	}
	key, err := crypto.ResolveKey(cfg.Paths.DataDir)
	require.NoError(t, err)
	encryptor, err := crypto.NewEncryptor(key)
	require.NoError(t, err)
	store, err := persisfile.NewNotificationStore(
		filepath.Join(cfg.Paths.DataDir, "notifications"),
		persisfile.NewBackend(cfg.Paths).Collection(persis.CollectionNotifications),
		encryptor,
	)
	require.NoError(t, err)

	settings, err := notificationmodel.Normalize(&notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notificationmodel.Target{{
			ID:      "webhook-1",
			Type:    notificationmodel.ProviderWebhook,
			Enabled: true,
			Webhook: &notificationmodel.WebhookTarget{
				URL:             "https://93.184.216.34/webhook",
				MessageTemplate: "{{run.url}}",
			},
		}},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), settings))

	service := newNotificationService(
		cfg,
		store,
		nil,
		notificationservice.WithHTTPClient(httpClient),
	)
	results, err := service.SendTest(
		context.Background(),
		"daily-report",
		"webhook-1",
		eventstore.TypeDAGRunFailed,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Delivered)
	request := <-received
	require.NoError(t, request.err)
	assert.Contains(t, request.body, `"message":"https://dagu.example.com/workflows/dag-runs/daily-report/notification-test"`)
}
