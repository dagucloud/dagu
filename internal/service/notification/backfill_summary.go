// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
)

// BackfillSummary is what the one notification of an ended backfill tells.
type BackfillSummary struct {
	ID      string `json:"id"`
	DAGName string `json:"dag"`
	// Labels are the DAG's labels, which place it in a workspace.
	Labels    []string `json:"labels,omitempty"`
	Succeeded bool     `json:"succeeded"`
	// Slots counts the backfill's slots, and the others those that ended
	// each way.
	Slots          int           `json:"slots"`
	SucceededSlots int           `json:"succeededSlots"`
	FailedSlots    int           `json:"failedSlots"`
	SkippedSlots   int           `json:"skippedSlots"`
	NotRunSlots    int           `json:"notRunSlots"`
	Duration       time.Duration `json:"-"`
	// Failures groups the failed slots by how their runs failed, the
	// largest group first.
	Failures []BackfillFailure `json:"failures,omitempty"`
}

// BackfillFailure is a group of failed slots whose runs failed alike.
type BackfillFailure struct {
	Step  string `json:"step,omitempty"`
	Error string `json:"error,omitempty"`
	Slots int    `json:"slots"`
	// Slot and RunID are the group's earliest slot and its run.
	Slot  time.Time `json:"slot"`
	RunID string    `json:"runId"`
}

// maxSummaryFailures bounds the groups of failed slots a summary lists.
const maxSummaryFailures = 10

// SendBackfillSummary sends one summary of an ended backfill to the targets
// that a run of its DAG with the same outcome notifies. A command target is
// left out: its command runs with the values of one run.
func (s *Service) SendBackfillSummary(ctx context.Context, summary BackfillSummary) error {
	eventType := eventstore.TypeDAGRunFailed
	if summary.Succeeded {
		eventType = eventstore.TypeDAGRunSucceeded
	}
	targets, err := s.testTargetsForDAGSettings(ctx, summary.DAGName, "", eventType)
	if errors.Is(err, notificationmodel.ErrSettingsNotFound) {
		// Routes match the run's workspace, which its DAG's labels give.
		targets, err = s.testTargetsForRoutes(ctx, chatbridge.NotificationEvent{
			Type:   eventType,
			Status: &ir.DAGRunStatus{Name: summary.DAGName, Labels: summary.Labels},
		}, "")
	}
	if err != nil {
		return err
	}
	title, text := backfillSummaryText(summary, s.publicURL())
	var errs []error
	for _, target := range targets {
		if err := s.deliverBackfillSummary(ctx, target.Target, summary, title, text); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", target.ResultName, err))
		}
	}
	return errors.Join(errs...)
}

// backfillSummaryText returns the title and text of a backfill's summary.
func backfillSummaryText(summary BackfillSummary, publicURL string) (string, string) {
	outcome := "failed"
	if summary.Succeeded {
		outcome = "succeeded"
	}
	title := fmt.Sprintf("Backfill %s of %s %s", summary.ID, summary.DAGName, outcome)
	counts := []string{countOf(summary.Slots, "slot")}
	for _, part := range []struct {
		n    int
		word string
	}{
		{summary.SucceededSlots, "succeeded"},
		{summary.FailedSlots, "failed"},
		{summary.SkippedSlots, "skipped"},
		{summary.NotRunSlots, "not run"},
	} {
		if part.n > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", part.n, part.word))
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", title, strings.Join(counts, ", "))
	if summary.Duration > 0 {
		fmt.Fprintf(&b, ", in %s", roughDuration(summary.Duration))
	}
	b.WriteString("\n")
	if len(summary.Failures) > 0 {
		b.WriteString("\nFailed slots:\n")
	}
	for i, failure := range summary.Failures {
		if i == maxSummaryFailures {
			fmt.Fprintf(&b, "- %s more\n", countOf(len(summary.Failures)-i, "group"))
			break
		}
		b.WriteString("- " + countOf(failure.Slots, "slot"))
		if failure.Step != "" {
			b.WriteString(" at step " + failure.Step)
		}
		if failure.Error != "" {
			b.WriteString(": " + failure.Error)
		}
		fmt.Fprintf(&b, ", first %s", failure.Slot.UTC().Format("2006-01-02 15:04 UTC"))
		runPath := "/dag-runs/" + url.PathEscape(summary.DAGName) + "/" + url.PathEscape(failure.RunID)
		if link := notificationRunURL(publicURL, runPath); link != "" {
			b.WriteString(": " + link)
		} else {
			b.WriteString(" (run " + failure.RunID + ")")
		}
		b.WriteString("\n")
	}
	if link := notificationRunURL(publicURL, "/backfills/"+url.PathEscape(summary.ID)); link != "" {
		b.WriteString("\n" + link + "\n")
	}
	return title, b.String()
}

// countOf writes n with its noun, plural when n is not one.
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// roughDuration writes d to the minute, at least one, as "40m" or "1h40m".
func roughDuration(d time.Duration) string {
	d = max(d.Round(time.Minute), time.Minute)
	return strings.TrimSuffix(d.String(), "0s")
}

// deliverBackfillSummary sends a backfill's summary to one target.
func (s *Service) deliverBackfillSummary(ctx context.Context, target notificationmodel.Target, summary BackfillSummary, title, text string) error {
	switch target.Type {
	case notificationmodel.ProviderSlack:
		if target.Slack == nil || target.Slack.WebhookURL == "" {
			return errors.New("slack webhook url is not configured")
		}
		return s.withRetry(ctx, func() error {
			return s.postSummaryJSON(ctx, target.Slack.WebhookURL, map[string]string{"text": text}, nil)
		})
	case notificationmodel.ProviderTeams:
		if target.Teams == nil || target.Teams.WebhookURL == "" {
			return errors.New("teams webhook url is not configured")
		}
		body, err := marshalTeamsPayload(map[string]any{
			"@type":    "MessageCard",
			"@context": "http://schema.org/extensions",
			"summary":  title,
			"title":    title,
			"text":     text,
		})
		if err != nil {
			return err
		}
		return s.withRetry(ctx, func() error {
			return s.postSummaryBody(ctx, target.Teams.WebhookURL, body, teamsThrottleError)
		})
	case notificationmodel.ProviderTelegram:
		return s.withRetry(ctx, func() error { return s.sendTelegramText(ctx, target, text) })
	case notificationmodel.ProviderWebhook:
		return s.sendSummaryWebhook(ctx, target, summary, text)
	case notificationmodel.ProviderEmail:
		return s.sendSummaryEmail(ctx, target, title, text)
	case notificationmodel.ProviderCommand:
		// A command runs with the values of one run, which a summary has not.
		return nil
	default:
		return notificationmodel.ErrUnsupportedTarget
	}
}

func (s *Service) postSummaryJSON(ctx context.Context, endpoint string, payload any, checkBody func(string) error) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.postSummaryBody(ctx, endpoint, body, checkBody)
}

func (s *Service) postSummaryBody(ctx context.Context, endpoint string, body []byte, checkBody func(string) error) error {
	if err := validateOutboundURL(ctx, endpoint, false, false); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return s.deliverRequest(req, checkBody)
}

// sendTelegramText sends text to a Telegram target's chat.
func (s *Service) sendTelegramText(ctx context.Context, target notificationmodel.Target, text string) error {
	if target.Telegram == nil || target.Telegram.BotToken == "" || target.Telegram.ChatID == "" {
		return errors.New("telegram bot token or chat id is not configured")
	}
	payload := map[string]any{
		"chat_id": target.Telegram.ChatID,
		"text":    truncateTelegramText(text),
	}
	if topicID, err := strconv.Atoi(target.Telegram.TopicID); err == nil {
		payload["message_thread_id"] = topicID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := "https://api.telegram.org/bot" + target.Telegram.BotToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return s.doWebhookRequest(req)
}

// sendSummaryWebhook posts a backfill's summary as JSON. A body template
// renders run events, so a summary takes the default body instead.
func (s *Service) sendSummaryWebhook(ctx context.Context, target notificationmodel.Target, summary BackfillSummary, text string) error {
	if target.Webhook == nil || target.Webhook.URL == "" {
		return errors.New("webhook url is not configured")
	}
	if err := validateOutboundURL(ctx, target.Webhook.URL, target.Webhook.AllowInsecureHTTP, target.Webhook.AllowPrivateNetwork); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"type": "backfill.ended",
		"backfill": struct {
			BackfillSummary
			DurationSeconds int `json:"durationSeconds"`
		}{summary, int(summary.Duration.Round(time.Second) / time.Second)},
		"message": text,
	})
	if err != nil {
		return err
	}
	return s.postWebhookBody(ctx, target, body)
}

// sendSummaryEmail mails a backfill's summary through the notification
// SMTP, as run notifications are.
func (s *Service) sendSummaryEmail(ctx context.Context, target notificationmodel.Target, title, text string) error {
	if target.Email == nil {
		return nil
	}
	workspaceSettings, err := s.GetWorkspaceSettings(ctx)
	if err != nil {
		return err
	}
	if workspaceSettings == nil || workspaceSettings.SMTP == nil {
		return errors.New("SMTP is not configured for notification email")
	}
	from := target.Email.From
	if from == "" {
		from = workspaceSettings.SMTP.From
	}
	if from == "" {
		return errors.New("email sender is not configured")
	}
	mailerConfig, err := mailer.BuildConfig(
		workspaceSettings.SMTP.Host,
		workspaceSettings.SMTP.Port,
		workspaceSettings.SMTP.Username,
		workspaceSettings.SMTP.Password,
		workspaceSettings.SMTP.OAuth,
	)
	if err != nil {
		return fmt.Errorf("invalid SMTP configuration: %w", err)
	}
	return mailer.New(mailerConfig).SendWithRecipients(
		ctx, from, target.Email.To, target.Email.Cc, target.Email.Bcc, title, text, nil,
	)
}
