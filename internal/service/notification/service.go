// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/mailer"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	notificationmodel "github.com/dagucloud/dagu/internal/notification"
	"github.com/dagucloud/dagu/internal/service/chatbridge"
	"github.com/dagucloud/dagu/internal/service/eventstore"
)

type Service struct {
	store    notificationmodel.Store
	dagStore exec.DAGStore
	http     *http.Client
	logger   *slog.Logger
}

type TestResult struct {
	TargetID   string
	TargetName string
	Provider   notificationmodel.ProviderType
	Delivered  bool
	Error      string
}

type Option func(*Service)

func WithHTTPClient(client *http.Client) Option {
	return func(s *Service) {
		if client != nil {
			s.http = client
		}
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(s *Service) {
		if logger != nil {
			s.logger = logger
		}
	}
}

func New(store notificationmodel.Store, dagStore exec.DAGStore, opts ...Option) *Service {
	svc := &Service{
		store:    store,
		dagStore: dagStore,
		http:     &http.Client{Timeout: 30 * time.Second},
		logger:   slog.Default(),
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

func (s *Service) GetByDAGName(ctx context.Context, dagName string) (*notificationmodel.Settings, error) {
	if s.store == nil {
		return nil, notificationmodel.ErrSettingsNotFound
	}
	return s.store.GetByDAGName(ctx, dagName)
}

func (s *Service) Save(ctx context.Context, settings *notificationmodel.Settings, updatedBy string) (*notificationmodel.Settings, error) {
	if s.store == nil {
		return nil, notificationmodel.ErrSettingsNotFound
	}
	existing, err := s.store.GetByDAGName(ctx, settings.DAGName)
	if err != nil && !errors.Is(err, notificationmodel.ErrSettingsNotFound) {
		return nil, err
	}
	if existing != nil {
		settings.ID = existing.ID
		settings.CreatedAt = existing.CreatedAt
		notificationmodel.PreserveSecrets(settings, existing)
	}
	normalized, err := notificationmodel.Normalize(settings, updatedBy)
	if err != nil {
		return nil, err
	}
	if err := s.store.Save(ctx, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func (s *Service) DeleteByDAGName(ctx context.Context, dagName string) error {
	if s.store == nil {
		return notificationmodel.ErrSettingsNotFound
	}
	return s.store.DeleteByDAGName(ctx, dagName)
}

func (s *Service) NotificationDestinations() []string {
	settings, err := s.listSettings(context.Background())
	if err != nil {
		s.logger.Warn("Failed to list notification destinations", slog.String("error", err.Error()))
		return nil
	}
	var destinations []string
	for _, setting := range settings {
		for _, target := range setting.Targets {
			if destination := destinationID(setting.DAGName, target.ID); setting.Enabled && target.Enabled && destination != "" {
				destinations = append(destinations, destination)
			}
		}
	}
	slices.Sort(destinations)
	return destinations
}

func (s *Service) NotificationDestinationsForEvent(event chatbridge.NotificationEvent) []string {
	if event.Status == nil || event.Status.Name == "" {
		return nil
	}
	setting, err := s.GetByDAGName(context.Background(), event.Status.Name)
	if err != nil {
		if !errors.Is(err, notificationmodel.ErrSettingsNotFound) {
			s.logger.Warn("Failed to load notification settings",
				slog.String("dag", event.Status.Name),
				slog.String("error", err.Error()),
			)
		}
		return nil
	}
	if !notificationmodel.IsEventEnabled(setting, event.Type) {
		return nil
	}
	destinations := make([]string, 0, len(setting.Targets))
	for _, target := range setting.Targets {
		if destination := destinationID(setting.DAGName, target.ID); target.Enabled && destination != "" {
			destinations = append(destinations, destination)
		}
	}
	return destinations
}

func (s *Service) FlushNotificationBatch(ctx context.Context, destination string, batch chatbridge.NotificationBatch, _ bool) bool {
	dagName, targetID, ok := parseDestinationID(destination)
	if !ok {
		return false
	}
	setting, err := s.GetByDAGName(ctx, dagName)
	if err != nil {
		s.logger.Warn("Failed to load notification settings for delivery",
			slog.String("destination", destination),
			slog.String("error", err.Error()),
		)
		return false
	}
	target, ok := findTarget(setting, targetID)
	if !ok || !target.Enabled {
		return true
	}
	events := matchingEvents(setting, batch.Events)
	if len(events) == 0 {
		return true
	}

	if target.Type != notificationmodel.ProviderEmail &&
		target.Type != notificationmodel.ProviderWebhook &&
		target.Type != notificationmodel.ProviderSlack &&
		target.Type != notificationmodel.ProviderTelegram {
		s.logger.Warn("Unsupported notification target provider",
			slog.String("destination", destination),
			slog.String("provider", string(target.Type)),
		)
		return false
	}
	return s.sendTarget(ctx, target, events)
}

func (s *Service) SendTest(ctx context.Context, dagName, targetID string, eventType eventstore.EventType) ([]TestResult, error) {
	if eventType == "" {
		eventType = eventstore.TypeDAGRunFailed
	}
	if !s.isSupportedEvent(eventType) {
		return nil, notificationmodel.ErrUnsupportedEvent
	}
	setting, err := s.GetByDAGName(ctx, dagName)
	if err != nil {
		return nil, err
	}
	targets := make([]notificationmodel.Target, 0, len(setting.Targets))
	for _, target := range setting.Targets {
		if targetID != "" {
			if target.ID == targetID {
				targets = append(targets, target)
				break
			}
			continue
		}
		if target.Enabled {
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		if targetID != "" {
			return nil, notificationmodel.ErrTargetNotFound
		}
		return nil, notificationmodel.ErrInvalidSettings
	}

	event := chatbridge.NotificationEvent{
		Key:        "notification-test:" + dagName,
		Type:       eventType,
		Status:     testStatus(dagName, eventType),
		ObservedAt: time.Now().UTC(),
	}
	results := make([]TestResult, 0, len(targets))
	for _, target := range targets {
		delivered := s.sendTarget(ctx, target, []chatbridge.NotificationEvent{event})
		result := TestResult{
			TargetID:   target.ID,
			TargetName: target.Name,
			Provider:   target.Type,
			Delivered:  delivered,
		}
		if !delivered {
			result.Error = "delivery failed"
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Service) isSupportedEvent(eventType eventstore.EventType) bool {
	switch eventType {
	case eventstore.TypeDAGRunWaiting,
		eventstore.TypeDAGRunSucceeded,
		eventstore.TypeDAGRunFailed,
		eventstore.TypeDAGRunAborted,
		eventstore.TypeDAGRunRejected:
		return true
	default:
		return false
	}
}

func testStatus(dagName string, eventType eventstore.EventType) *exec.DAGRunStatus {
	now := time.Now().UTC()
	status := core.Failed
	message := "This is a test notification from Dagu."
	switch eventType {
	case eventstore.TypeDAGRunWaiting:
		status = core.Waiting
		message = "This is a test waiting notification from Dagu."
	case eventstore.TypeDAGRunSucceeded:
		status = core.Succeeded
		message = ""
	case eventstore.TypeDAGRunAborted:
		status = core.Aborted
		message = "This is a test aborted notification from Dagu."
	case eventstore.TypeDAGRunRejected:
		status = core.Rejected
		message = "This is a test rejected notification from Dagu."
	}
	return &exec.DAGRunStatus{
		Name:       dagName,
		DAGRunID:   "notification-test",
		AttemptID:  "notification-test",
		Status:     status,
		Error:      message,
		StartedAt:  stringutil.FormatTime(now.Add(-time.Minute)),
		FinishedAt: stringutil.FormatTime(now),
	}
}

func (s *Service) sendTarget(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) bool {
	switch target.Type {
	case notificationmodel.ProviderEmail:
		return s.sendEmail(ctx, target, events)
	case notificationmodel.ProviderWebhook:
		return s.sendWebhook(ctx, target, events)
	case notificationmodel.ProviderSlack:
		return s.sendSlack(ctx, target, events)
	case notificationmodel.ProviderTelegram:
		return s.sendTelegram(ctx, target, events)
	default:
		return false
	}
}

func (s *Service) listSettings(ctx context.Context) ([]*notificationmodel.Settings, error) {
	if s.store == nil {
		return nil, nil
	}
	return s.store.List(ctx)
}

func destinationID(dagName, targetID string) string {
	if dagName == "" || targetID == "" {
		return ""
	}
	return dagName + "\x00" + targetID
}

func parseDestinationID(destination string) (string, string, bool) {
	dagName, targetID, ok := strings.Cut(destination, "\x00")
	return dagName, targetID, ok && dagName != "" && targetID != ""
}

func findTarget(setting *notificationmodel.Settings, targetID string) (notificationmodel.Target, bool) {
	if setting == nil || targetID == "" {
		return notificationmodel.Target{}, false
	}
	for _, target := range setting.Targets {
		if target.ID == targetID {
			return target, true
		}
	}
	return notificationmodel.Target{}, false
}

func matchingEvents(setting *notificationmodel.Settings, events []chatbridge.NotificationEvent) []chatbridge.NotificationEvent {
	result := make([]chatbridge.NotificationEvent, 0, len(events))
	for _, event := range events {
		if event.Status == nil || event.Status.Name != setting.DAGName {
			continue
		}
		if !notificationmodel.IsEventEnabled(setting, event.Type) {
			continue
		}
		result = append(result, event)
	}
	return result
}

func (s *Service) sendEmail(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) bool {
	if target.Email == nil || len(events) == 0 {
		return true
	}
	dag, err := s.loadDAG(ctx, events[0].Status.Name)
	if err != nil {
		s.logger.Warn("Failed to load DAG for email notification",
			slog.String("dag", events[0].Status.Name),
			slog.String("error", err.Error()),
		)
		return false
	}
	if dag.SMTP == nil {
		s.logger.Warn("SMTP is not configured for DAG notification email",
			slog.String("dag", events[0].Status.Name),
		)
		return false
	}
	from := target.Email.From
	if from == "" {
		from = fallbackMailFrom(dag, events[0].Type)
	}
	if from == "" {
		s.logger.Warn("Email sender is not configured",
			slog.String("dag", events[0].Status.Name),
		)
		return false
	}
	subject := target.Email.SubjectPrefix
	if subject == "" {
		subject = fallbackMailPrefix(dag, events[0].Type)
	}
	subject = strings.TrimSpace(fmt.Sprintf("%s %s", subject, titleForEvents(events)))
	attachments := []string{}
	if target.Email.AttachLogs {
		attachments = logAttachments(events)
	}
	err = mailer.New(mailer.Config{
		Host:     dag.SMTP.Host,
		Port:     dag.SMTP.Port,
		Username: dag.SMTP.Username,
		Password: dag.SMTP.Password,
	}).SendWithRecipients(
		ctx,
		from,
		target.Email.To,
		target.Email.Cc,
		target.Email.Bcc,
		subject,
		bodyForEvents(events),
		attachments,
	)
	if err != nil {
		s.logger.Warn("Failed to send notification email",
			slog.String("dag", events[0].Status.Name),
			slog.String("error", err.Error()),
		)
	}
	return err == nil
}

func (s *Service) loadDAG(ctx context.Context, dagName string) (*core.DAG, error) {
	if s.dagStore == nil {
		return nil, errors.New("DAG store is not configured")
	}
	return s.dagStore.GetDetails(ctx, dagName)
}

func fallbackMailFrom(dag *core.DAG, eventType eventstore.EventType) string {
	cfg := fallbackMailConfig(dag, eventType)
	if cfg == nil {
		return ""
	}
	return cfg.From
}

func fallbackMailPrefix(dag *core.DAG, eventType eventstore.EventType) string {
	cfg := fallbackMailConfig(dag, eventType)
	if cfg == nil {
		return "[DAGU]"
	}
	return cfg.Prefix
}

func fallbackMailConfig(dag *core.DAG, eventType eventstore.EventType) *core.MailConfig {
	if dag == nil {
		return nil
	}
	switch eventType {
	case eventstore.TypeDAGRunSucceeded:
		return dag.InfoMail
	case eventstore.TypeDAGRunWaiting:
		return dag.WaitMail
	default:
		return dag.ErrorMail
	}
}

func logAttachments(events []chatbridge.NotificationEvent) []string {
	seen := map[string]struct{}{}
	var files []string
	for _, event := range events {
		if event.Status == nil {
			continue
		}
		if event.Status.Log != "" {
			if _, ok := seen[event.Status.Log]; !ok {
				seen[event.Status.Log] = struct{}{}
				files = append(files, event.Status.Log)
			}
		}
		for _, node := range event.Status.Nodes {
			if node == nil {
				continue
			}
			for _, file := range []string{node.Stdout, node.Stderr} {
				if file == "" {
					continue
				}
				if _, ok := seen[file]; ok {
					continue
				}
				seen[file] = struct{}{}
				files = append(files, file)
			}
		}
	}
	return files
}

func (s *Service) sendWebhook(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) bool {
	if target.Webhook == nil || target.Webhook.URL == "" {
		return false
	}
	payload := webhookPayloadForEvents(events)
	body, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.Webhook.URL, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range target.Webhook.Headers {
		req.Header.Set(key, value)
	}
	if target.Webhook.HMACSecret != "" {
		req.Header.Set("X-Dagu-Signature", "sha256="+signWebhookBody(body, target.Webhook.HMACSecret))
	}
	return s.doWebhookRequest(req)
}

func (s *Service) sendSlack(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) bool {
	if target.Slack == nil || target.Slack.WebhookURL == "" {
		return false
	}
	body, err := json.Marshal(map[string]string{"text": bodyForEvents(events)})
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.Slack.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	return s.doWebhookRequest(req)
}

func (s *Service) sendTelegram(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) bool {
	if target.Telegram == nil || target.Telegram.BotToken == "" || target.Telegram.ChatID == "" {
		return false
	}
	body, err := json.Marshal(map[string]string{
		"chat_id": target.Telegram.ChatID,
		"text":    bodyForEvents(events),
	})
	if err != nil {
		return false
	}
	endpoint := "https://api.telegram.org/bot" + target.Telegram.BotToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	return s.doWebhookRequest(req)
}

func (s *Service) doWebhookRequest(req *http.Request) bool {
	resp, err := s.http.Do(req)
	if err != nil {
		s.logger.Warn("Failed to send notification request",
			slog.String("error", err.Error()),
		)
		return false
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.logger.Warn("Notification request returned non-success status",
			slog.Int("status", resp.StatusCode),
		)
		return false
	}
	return true
}

func signWebhookBody(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func titleForEvents(events []chatbridge.NotificationEvent) string {
	if len(events) == 0 || events[0].Status == nil {
		return "DAG notification"
	}
	if len(events) == 1 {
		return fmt.Sprintf("%s %s", events[0].Status.Name, events[0].Status.Status.String())
	}
	return fmt.Sprintf("%s: %d notifications", events[0].Status.Name, len(events))
}

func bodyForEvents(events []chatbridge.NotificationEvent) string {
	var b strings.Builder
	for i, event := range events {
		if event.Status == nil {
			continue
		}
		if i > 0 {
			b.WriteString("\n\n")
		}
		status := event.Status
		b.WriteString(fmt.Sprintf("DAG: %s\n", status.Name))
		b.WriteString(fmt.Sprintf("Run ID: %s\n", status.DAGRunID))
		b.WriteString(fmt.Sprintf("Status: %s\n", status.Status.String()))
		if startedAt, err := stringutil.ParseTime(status.StartedAt); err == nil && !startedAt.IsZero() {
			b.WriteString(fmt.Sprintf("Started: %s\n", startedAt.Format(time.RFC3339)))
		}
		if finishedAt, err := stringutil.ParseTime(status.FinishedAt); err == nil && !finishedAt.IsZero() {
			b.WriteString(fmt.Sprintf("Finished: %s\n", finishedAt.Format(time.RFC3339)))
		}
		if status.Error != "" {
			b.WriteString(fmt.Sprintf("Error: %s\n", status.Error))
		}
	}
	return b.String()
}

func webhookPayloadForEvents(events []chatbridge.NotificationEvent) map[string]any {
	items := make([]map[string]any, 0, len(events))
	for _, event := range events {
		if event.Status == nil {
			continue
		}
		items = append(items, map[string]any{
			"eventType":  string(event.Type),
			"dagName":    event.Status.Name,
			"dagRunId":   event.Status.DAGRunID,
			"status":     event.Status.Status.String(),
			"error":      event.Status.Error,
			"observedAt": event.ObservedAt.Format(time.RFC3339Nano),
		})
	}
	return map[string]any{
		"version": "v1",
		"events":  items,
	}
}

var _ chatbridge.NotificationTransport = (*Service)(nil)
var _ chatbridge.NotificationRoutingTransport = (*Service)(nil)
