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
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
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
	retry    DeliveryRetryConfig
}

type TestResult struct {
	TargetID   string
	TargetName string
	Provider   notificationmodel.ProviderType
	Delivered  bool
	Error      string
}

type DeliveryRetryConfig struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
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

func WithDeliveryRetry(cfg DeliveryRetryConfig) Option {
	return func(s *Service) {
		if cfg.MaxAttempts > 0 {
			s.retry.MaxAttempts = cfg.MaxAttempts
		}
		if cfg.InitialBackoff >= 0 {
			s.retry.InitialBackoff = cfg.InitialBackoff
		}
		if cfg.MaxBackoff >= 0 {
			s.retry.MaxBackoff = cfg.MaxBackoff
		}
	}
}

func New(store notificationmodel.Store, dagStore exec.DAGStore, opts ...Option) *Service {
	svc := &Service{
		store:    store,
		dagStore: dagStore,
		http:     &http.Client{Timeout: 30 * time.Second},
		logger:   slog.Default(),
		retry: DeliveryRetryConfig{
			MaxAttempts:    3,
			InitialBackoff: 250 * time.Millisecond,
			MaxBackoff:     2 * time.Second,
		},
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
		if !notificationmodel.IsTargetEventEnabled(setting, target, event.Type) {
			continue
		}
		if destination := destinationID(setting.DAGName, target.ID); destination != "" {
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
	events := matchingEvents(setting, target, batch.Events)
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
	if err := s.deliverTarget(ctx, target, events); err != nil {
		s.logger.Warn("Failed to deliver DAG notification",
			slog.String("destination", destination),
			slog.String("provider", string(target.Type)),
			slog.String("error", err.Error()),
		)
		return false
	}
	return true
}

func (s *Service) ShouldDeliverNotificationBatch(chatbridge.NotificationBatch) bool {
	return true
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
		if notificationmodel.IsTargetEventEnabled(setting, target, eventType) {
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
		err := s.deliverTarget(ctx, target, []chatbridge.NotificationEvent{event})
		result := TestResult{
			TargetID:   target.ID,
			TargetName: target.Name,
			Provider:   target.Type,
			Delivered:  err == nil,
		}
		if err != nil {
			result.Error = err.Error()
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

func (s *Service) deliverTarget(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) error {
	switch target.Type {
	case notificationmodel.ProviderEmail:
		return s.sendEmail(ctx, target, events)
	case notificationmodel.ProviderWebhook:
		return s.withRetry(ctx, func() error { return s.sendWebhook(ctx, target, events) })
	case notificationmodel.ProviderSlack:
		return s.withRetry(ctx, func() error { return s.sendSlack(ctx, target, events) })
	case notificationmodel.ProviderTelegram:
		return s.withRetry(ctx, func() error { return s.sendTelegram(ctx, target, events) })
	default:
		return notificationmodel.ErrUnsupportedTarget
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

func matchingEvents(setting *notificationmodel.Settings, target notificationmodel.Target, events []chatbridge.NotificationEvent) []chatbridge.NotificationEvent {
	result := make([]chatbridge.NotificationEvent, 0, len(events))
	for _, event := range events {
		if event.Status == nil || event.Status.Name != setting.DAGName {
			continue
		}
		if !notificationmodel.IsTargetEventEnabled(setting, target, event.Type) {
			continue
		}
		result = append(result, event)
	}
	return result
}

func (s *Service) sendEmail(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) error {
	if target.Email == nil || len(events) == 0 {
		return nil
	}
	dag, err := s.loadDAG(ctx, events[0].Status.Name)
	if err != nil {
		s.logger.Warn("Failed to load DAG for email notification",
			slog.String("dag", events[0].Status.Name),
			slog.String("error", err.Error()),
		)
		return err
	}
	if dag.SMTP == nil {
		return errors.New("SMTP is not configured for DAG notification email")
	}
	from := target.Email.From
	if from == "" {
		from = fallbackMailFrom(dag, events[0].Type)
	}
	if from == "" {
		return errors.New("email sender is not configured")
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
	return err
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

func (s *Service) sendWebhook(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) error {
	if target.Webhook == nil || target.Webhook.URL == "" {
		return errors.New("webhook url is not configured")
	}
	if err := validateOutboundURL(ctx, target.Webhook.URL, target.Webhook.AllowInsecureHTTP, target.Webhook.AllowPrivateNetwork); err != nil {
		return err
	}
	payload := webhookPayloadForEvents(events)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.Webhook.URL, bytes.NewReader(body))
	if err != nil {
		return err
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

func (s *Service) sendSlack(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) error {
	if target.Slack == nil || target.Slack.WebhookURL == "" {
		return errors.New("slack webhook url is not configured")
	}
	if err := validateOutboundURL(ctx, target.Slack.WebhookURL, false, false); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"text": bodyForEvents(events)})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.Slack.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return s.doWebhookRequest(req)
}

func (s *Service) sendTelegram(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) error {
	if target.Telegram == nil || target.Telegram.BotToken == "" || target.Telegram.ChatID == "" {
		return errors.New("telegram bot token or chat id is not configured")
	}
	body, err := json.Marshal(map[string]string{
		"chat_id": target.Telegram.ChatID,
		"text":    bodyForEvents(events),
	})
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

func (s *Service) doWebhookRequest(req *http.Request) error {
	resp, err := s.http.Do(req)
	if err != nil {
		return temporaryDeliveryError{err: err}
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := limitedResponseBody(resp.Body)
		err := fmt.Errorf("notification endpoint returned HTTP %d%s", resp.StatusCode, body)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return temporaryDeliveryError{err: err}
		}
		return err
	}
	return nil
}

type temporaryDeliveryError struct {
	err error
}

func (e temporaryDeliveryError) Error() string {
	if e.err == nil {
		return "temporary notification delivery error"
	}
	return e.err.Error()
}

func (e temporaryDeliveryError) Unwrap() error {
	return e.err
}

func isTemporaryDeliveryError(err error) bool {
	var temporary temporaryDeliveryError
	return errors.As(err, &temporary)
}

func (s *Service) withRetry(ctx context.Context, send func() error) error {
	attempts := s.retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	backoff := s.retry.InitialBackoff
	maxBackoff := s.retry.MaxBackoff
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := send(); err != nil {
			lastErr = err
			if attempt == attempts || !isTemporaryDeliveryError(err) {
				return err
			}
			if backoff > 0 {
				timer := time.NewTimer(backoff)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
				backoff *= 2
				if maxBackoff > 0 && backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		return nil
	}
	return lastErr
}

func limitedResponseBody(body io.Reader) string {
	if body == nil {
		return ""
	}
	data, _ := io.ReadAll(io.LimitReader(body, 512))
	text := strings.TrimSpace(string(data))
	if text == "" {
		return ""
	}
	return ": " + text
}

func validateOutboundURL(ctx context.Context, rawURL string, allowInsecureHTTP, allowPrivateNetwork bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if req.URL.Scheme == "http" && !allowInsecureHTTP {
		return errors.New("webhook url must use https unless allowInsecureHttp is enabled")
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return errors.New("webhook url must use http or https")
	}
	host := req.URL.Hostname()
	if host == "" {
		return errors.New("webhook url host is required")
	}
	if allowPrivateNetwork {
		return nil
	}
	if isPrivateHostLiteral(host) {
		return errors.New("webhook url targets loopback or private network")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return rejectPrivateAddress(addr)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve webhook host: %w", err)
	}
	for _, addr := range addrs {
		if parsed, ok := netip.AddrFromSlice(addr.IP); ok {
			if err := rejectPrivateAddress(parsed); err != nil {
				return err
			}
		}
	}
	return nil
}

func isPrivateHostLiteral(host string) bool {
	host = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(host), "."))
	return host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost")
}

func rejectPrivateAddress(addr netip.Addr) error {
	addr = addr.Unmap()
	if addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified() {
		return errors.New("webhook url resolves to loopback or private network")
	}
	return nil
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
var _ chatbridge.NotificationBatchDeliveryPolicyTransport = (*Service)(nil)
var _ chatbridge.NotificationRoutingTransport = (*Service)(nil)
