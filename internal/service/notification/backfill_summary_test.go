// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package notification

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/eventstore"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSummaryService is a service whose DAG settings send failed runs to one
// Slack target, recording each request body it posts.
func newSummaryService(t *testing.T, bodies *atomic.Value, sent *atomic.Int32) *Service {
	t.Helper()
	return New(
		newMemoryStore(mustNormalizeSettings(t, &notificationmodel.Settings{
			DAGName: "daily-report",
			Enabled: true,
			Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
			Targets: []notificationmodel.Target{{
				ID:      "slack-1",
				Name:    "Ops Slack",
				Type:    notificationmodel.ProviderSlack,
				Enabled: true,
				Slack:   &notificationmodel.SlackTarget{WebhookURL: "https://93.184.216.34/slack"},
			}},
		})),
		nil,
		WithPublicURL("https://dagu.example.com"),
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			bodies.Store(string(body))
			sent.Add(1)
			return acceptedResponse(req), nil
		})}),
	)
}

// A backfill that ends sends one summary to the targets a run of its DAG
// with the same outcome notifies: the counts, the time it took, its failed
// slots grouped by how they failed, and links to their runs and to it.
func TestService_SendBackfillSummary(t *testing.T) {
	t.Parallel()

	var bodies atomic.Value
	var sent atomic.Int32
	svc := newSummaryService(t, &bodies, &sent)

	err := svc.SendBackfillSummary(context.Background(), BackfillSummary{
		ID:             "bf123456",
		DAGName:        "daily-report",
		Succeeded:      false,
		Slots:          28,
		SucceededSlots: 25,
		FailedSlots:    3,
		Duration:       100 * time.Minute,
		Failures: []BackfillFailure{{
			Step:  "load",
			Error: "connection refused",
			Slots: 2,
			Slot:  time.Date(2026, 10, 2, 2, 0, 0, 0, time.UTC),
			RunID: "backfill-bf123456-0-20261002T020000Z",
		}},
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, sent.Load())
	body, _ := bodies.Load().(string)
	assert.Contains(t, body, "Backfill bf123456 of daily-report failed: 28 slots, 25 succeeded, 3 failed, in 1h40m")
	assert.Contains(t, body, "2 slots at step load: connection refused, first 2026-10-02 02:00 UTC: https://dagu.example.com/dag-runs/daily-report/backfill-bf123456-0-20261002T020000Z")
	assert.Contains(t, body, "https://dagu.example.com/backfills/bf123456")

	// The DAG's settings notify failed runs only, so a backfill that
	// succeeded sends nothing.
	require.NoError(t, svc.SendBackfillSummary(context.Background(), BackfillSummary{
		ID: "bf654321", DAGName: "daily-report", Succeeded: true, Slots: 3, SucceededSlots: 3,
	}))
	assert.EqualValues(t, 1, sent.Load())
}

// A webhook target receives the summary as JSON its receiver can read.
func TestService_SendBackfillSummaryWebhook(t *testing.T) {
	t.Parallel()

	var body atomic.Value
	svc := New(
		newMemoryStore(mustNormalizeSettings(t, &notificationmodel.Settings{
			DAGName: "daily-report",
			Enabled: true,
			Events:  []eventstore.EventType{eventstore.TypeDAGRunSucceeded},
			Targets: []notificationmodel.Target{{
				ID:      "hook-1",
				Name:    "Hook",
				Type:    notificationmodel.ProviderWebhook,
				Enabled: true,
				Webhook: &notificationmodel.WebhookTarget{URL: "https://93.184.216.34/hook"},
			}},
		})),
		nil,
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			data, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			body.Store(string(data))
			return acceptedResponse(req), nil
		})}),
	)

	require.NoError(t, svc.SendBackfillSummary(context.Background(), BackfillSummary{
		ID: "bf123456", DAGName: "daily-report", Succeeded: true, Slots: 3, SucceededSlots: 3, Duration: 90 * time.Second,
	}))
	got, _ := body.Load().(string)
	assert.JSONEq(t, `{
		"type": "backfill.ended",
		"backfill": {"id": "bf123456", "dag": "daily-report", "succeeded": true, "slots": 3,
			"succeededSlots": 3, "failedSlots": 0, "skippedSlots": 0, "notRunSlots": 0, "durationSeconds": 90},
		"message": "Backfill bf123456 of daily-report succeeded: 3 slots, 3 succeeded, in 2m\n"
	}`, got)
}

// markupError is a failure's error that Slack would read as a mention and a
// link, and a mail client as markup.
const markupError = `<!channel> <b>bad</b> & <https://example.com|click>`

// A failure's error reaches Slack as text: it cannot mention or link.
func TestService_SendBackfillSummaryEscapesSlackText(t *testing.T) {
	t.Parallel()

	var bodies atomic.Value
	var sent atomic.Int32
	svc := newSummaryService(t, &bodies, &sent)

	require.NoError(t, svc.SendBackfillSummary(context.Background(), BackfillSummary{
		ID: "bf123456", DAGName: "daily-report", Slots: 1, FailedSlots: 1,
		Failures: []BackfillFailure{{Step: "load", Error: markupError, Slots: 1}},
	}))
	body, _ := bodies.Load().(string)
	var payload map[string]string
	require.NoError(t, json.Unmarshal([]byte(body), &payload))
	assert.Contains(t, payload["text"], "&lt;!channel&gt; &lt;b&gt;bad&lt;/b&gt; &amp; &lt;https://example.com|click&gt;")
	assert.NotContains(t, payload["text"], "<!channel>")
}

// A failure's error reaches an email as text, not as markup.
func TestService_SendBackfillSummaryEscapesEmailBody(t *testing.T) {
	t.Parallel()

	smtpServer := newRecordingSMTPServer(t)
	store := newMemoryStore(mustNormalizeSettings(t, &notificationmodel.Settings{
		DAGName: "daily-report",
		Enabled: true,
		Events:  []eventstore.EventType{eventstore.TypeDAGRunFailed},
		Targets: []notificationmodel.Target{{
			ID:      "email-1",
			Type:    notificationmodel.ProviderEmail,
			Enabled: true,
			Email:   &notificationmodel.EmailTarget{To: []string{"ops@example.com"}},
		}},
	}))
	workspace, err := notificationmodel.NormalizeWorkspaceSettings(&notificationmodel.WorkspaceSettings{
		SMTP: &notificationmodel.SMTPConfig{Host: smtpServer.host, Port: smtpServer.port, From: "dagu@example.com"},
	}, "tester")
	require.NoError(t, err)
	require.NoError(t, store.SaveWorkspaceSettings(context.Background(), workspace))
	svc := New(store, nil)

	require.NoError(t, svc.SendBackfillSummary(context.Background(), BackfillSummary{
		ID: "bf123456", DAGName: "daily-report", Slots: 1, FailedSlots: 1,
		Failures: []BackfillFailure{{Step: "load", Error: markupError, Slots: 1}},
	}))
	data, _ := smtpServer.data.Load().(string)
	message, err := mail.ReadMessage(strings.NewReader(data))
	require.NoError(t, err)
	body, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, message.Body))
	require.NoError(t, err)
	assert.Contains(t, string(body), "&lt;!channel&gt; &lt;b&gt;bad&lt;/b&gt; &amp; &lt;https://example.com|click&gt;")
	assert.NotContains(t, string(body), "<b>bad</b>")
}
