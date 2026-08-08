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
	"maps"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagstore"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

type Service struct {
	store                   notificationmodel.Store
	dagStore                dagstore.DAGStore
	http                    *http.Client
	logger                  *slog.Logger
	retry                   DeliveryRetryConfig
	reusableChannelsEnabled func() bool
	publicURL               func() string
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

func WithReusableChannelsEnabled(enabled func() bool) Option {
	return func(s *Service) {
		if enabled != nil {
			s.reusableChannelsEnabled = enabled
		}
	}
}

func WithPublicURL(publicURL string) Option {
	return WithPublicURLResolver(func() string { return publicURL })
}

func WithPublicURLResolver(resolver func() string) Option {
	return func(s *Service) {
		s.SetPublicURLResolver(resolver)
	}
}

func (s *Service) SetPublicURLResolver(resolver func() string) {
	if resolver != nil {
		s.publicURL = resolver
	}
}

func New(store notificationmodel.Store, dagStore dagstore.DAGStore, opts ...Option) *Service {
	svc := &Service{
		store:                   store,
		dagStore:                dagStore,
		http:                    &http.Client{Timeout: 30 * time.Second},
		logger:                  slog.Default(),
		reusableChannelsEnabled: func() bool { return true },
		publicURL:               func() string { return "" },
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

func (s *Service) reusableChannelsAllowed() bool {
	return s.reusableChannelsEnabled == nil || s.reusableChannelsEnabled()
}

func (s *Service) GetByDAGName(ctx context.Context, dagName string) (*notificationmodel.Settings, error) {
	if s.store == nil {
		return nil, notificationmodel.ErrSettingsNotFound
	}
	return s.store.GetByDAGName(ctx, dagName)
}

func (s *Service) ListChannels(ctx context.Context) ([]*notificationmodel.Channel, error) {
	if s.store == nil {
		return nil, notificationmodel.ErrChannelNotFound
	}
	channels, err := s.store.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(channels, func(a, b *notificationmodel.Channel) int {
		if a == nil || b == nil {
			switch {
			case a == nil && b == nil:
				return 0
			case a == nil:
				return -1
			default:
				return 1
			}
		}
		if cmp := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID, b.ID)
	})
	return channels, nil
}

func (s *Service) GetChannel(ctx context.Context, channelID string) (*notificationmodel.Channel, error) {
	if s.store == nil {
		return nil, notificationmodel.ErrChannelNotFound
	}
	return s.store.GetChannel(ctx, channelID)
}

func (s *Service) GetRouteSet(ctx context.Context, scope notificationmodel.RouteScope, workspace string) (*notificationmodel.RouteSet, error) {
	routeSet, err := s.loadRouteSet(ctx, scope, workspace)
	if err != nil {
		if errors.Is(err, notificationmodel.ErrRouteSetNotFound) {
			return defaultRouteSet(scope, workspace), nil
		}
		return nil, err
	}
	return routeSet, nil
}

func (s *Service) ListRouteSets(ctx context.Context) ([]*notificationmodel.RouteSet, error) {
	if s.store == nil {
		return nil, nil
	}
	routeSets, err := s.store.ListRouteSets(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(routeSets, func(a, b *notificationmodel.RouteSet) int {
		if a == nil || b == nil {
			switch {
			case a == nil && b == nil:
				return 0
			case a == nil:
				return -1
			default:
				return 1
			}
		}
		if cmp := strings.Compare(string(a.Scope), string(b.Scope)); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Workspace, b.Workspace)
	})
	return routeSets, nil
}

func (s *Service) SaveRouteSet(ctx context.Context, routeSet *notificationmodel.RouteSet, updatedBy string) (*notificationmodel.RouteSet, error) {
	if s.store == nil {
		return nil, notificationmodel.ErrRouteSetNotFound
	}
	if routeSet == nil {
		return nil, notificationmodel.ErrInvalidSettings
	}
	existing, err := s.loadRouteSet(ctx, routeSet.Scope, routeSet.Workspace)
	if err != nil && !errors.Is(err, notificationmodel.ErrRouteSetNotFound) {
		return nil, err
	}
	if existing != nil {
		routeSet.ID = existing.ID
		routeSet.CreatedAt = existing.CreatedAt
	}
	normalized, err := notificationmodel.NormalizeRouteSet(routeSet, updatedBy)
	if err != nil {
		return nil, err
	}
	if err := s.validateRoutes(ctx, normalized); err != nil {
		return nil, err
	}
	if err := s.store.SaveRouteSet(ctx, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func (s *Service) GetWorkspaceSettings(ctx context.Context) (*notificationmodel.WorkspaceSettings, error) {
	if s.store == nil {
		return &notificationmodel.WorkspaceSettings{}, nil
	}
	return s.store.GetWorkspaceSettings(ctx)
}

func (s *Service) SaveWorkspaceSettings(ctx context.Context, settings *notificationmodel.WorkspaceSettings, updatedBy string) (*notificationmodel.WorkspaceSettings, error) {
	if s.store == nil {
		return nil, notificationmodel.ErrSettingsNotFound
	}
	if settings == nil {
		settings = &notificationmodel.WorkspaceSettings{}
	}
	existing, err := s.store.GetWorkspaceSettings(ctx)
	if err != nil {
		return nil, err
	}
	if existing != nil && !existing.CreatedAt.IsZero() {
		settings.CreatedAt = existing.CreatedAt
	}
	notificationmodel.PreserveWorkspaceSecrets(settings, existing)
	normalized, err := notificationmodel.NormalizeWorkspaceSettings(settings, updatedBy)
	if err != nil {
		return nil, err
	}
	if err := s.store.SaveWorkspaceSettings(ctx, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func (s *Service) SaveChannel(ctx context.Context, channel *notificationmodel.Channel, updatedBy string) (*notificationmodel.Channel, error) {
	if s.store == nil {
		return nil, notificationmodel.ErrChannelNotFound
	}
	if channel == nil {
		return nil, notificationmodel.ErrInvalidSettings
	}
	existing, err := s.store.GetChannel(ctx, channel.ID)
	if err != nil && !errors.Is(err, notificationmodel.ErrChannelNotFound) {
		return nil, err
	}
	if existing != nil {
		channel.ID = existing.ID
		channel.CreatedAt = existing.CreatedAt
		notificationmodel.PreserveChannelSecrets(channel, existing)
	}
	normalized, err := notificationmodel.NormalizeChannel(channel, updatedBy)
	if err != nil {
		return nil, err
	}
	if err := s.store.SaveChannel(ctx, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func (s *Service) DeleteChannel(ctx context.Context, channelID string) error {
	if s.store == nil {
		return notificationmodel.ErrChannelNotFound
	}
	settings, err := s.listSettings(ctx)
	if err != nil {
		return err
	}
	for _, setting := range settings {
		for _, subscription := range setting.Subscriptions {
			if subscription.ChannelID == channelID {
				return fmt.Errorf("%w: %s is used by DAG %s", notificationmodel.ErrChannelInUse, channelID, setting.DAGName)
			}
		}
	}
	routeSets, err := s.store.ListRouteSets(ctx)
	if err != nil {
		return err
	}
	for _, routeSet := range routeSets {
		for _, route := range routeSet.Routes {
			if route.ChannelID == channelID {
				return fmt.Errorf("%w: %s is used by notification route set %s", notificationmodel.ErrChannelInUse, channelID, routeSetID(routeSet))
			}
		}
	}
	return s.store.DeleteChannel(ctx, channelID)
}

func (s *Service) Save(ctx context.Context, settings *notificationmodel.Settings, updatedBy string) (*notificationmodel.Settings, error) {
	if s.store == nil {
		return nil, notificationmodel.ErrSettingsNotFound
	}
	if settings == nil {
		return nil, notificationmodel.ErrInvalidSettings
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
	if err := s.validateSubscriptions(ctx, normalized); err != nil {
		return nil, err
	}
	if err := s.store.Save(ctx, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func (s *Service) validateSubscriptions(ctx context.Context, settings *notificationmodel.Settings) error {
	for _, subscription := range settings.Subscriptions {
		if _, err := s.store.GetChannel(ctx, subscription.ChannelID); err != nil {
			if errors.Is(err, notificationmodel.ErrChannelNotFound) {
				return fmt.Errorf("%w: %s", notificationmodel.ErrChannelNotFound, subscription.ChannelID)
			}
			return err
		}
	}
	return nil
}

func (s *Service) validateRoutes(ctx context.Context, routeSet *notificationmodel.RouteSet) error {
	for _, route := range routeSet.Routes {
		if _, err := s.store.GetChannel(ctx, route.ChannelID); err != nil {
			if errors.Is(err, notificationmodel.ErrChannelNotFound) {
				return fmt.Errorf("%w: %s", notificationmodel.ErrChannelNotFound, route.ChannelID)
			}
			return err
		}
	}
	return nil
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
	channels := make(map[string]*notificationmodel.Channel)
	loadChannel := func(channelID string) (*notificationmodel.Channel, bool) {
		if channelID == "" {
			return nil, false
		}
		if channel, ok := channels[channelID]; ok {
			return channel, channel != nil
		}
		channel, err := s.GetChannel(context.Background(), channelID)
		if err != nil {
			channels[channelID] = nil
			return nil, false
		}
		channels[channelID] = channel
		return channel, true
	}

	var destinations []string
	if s.reusableChannelsAllowed() {
		routeSets, err := s.ListRouteSets(context.Background())
		if err != nil {
			s.logger.Warn("Failed to list notification route sets", slog.String("error", err.Error()))
		}
		for _, routeSet := range routeSets {
			if routeSet == nil || !routeSet.Enabled {
				continue
			}
			for _, route := range routeSet.Routes {
				if !route.Enabled {
					continue
				}
				channel, ok := loadChannel(route.ChannelID)
				if !ok || !channel.Enabled {
					continue
				}
				if destination := routeDestinationID(routeSet.Scope, routeSet.Workspace, route.ID); destination != "" {
					destinations = append(destinations, destination)
				}
			}
		}
	}
	for _, setting := range settings {
		for _, target := range setting.Targets {
			if destination := inlineDestinationID(setting.DAGName, target.ID); setting.Enabled && target.Enabled && destination != "" {
				destinations = append(destinations, destination)
			}
		}
		if s.reusableChannelsAllowed() {
			for _, subscription := range setting.Subscriptions {
				if !setting.Enabled || !subscription.Enabled {
					continue
				}
				channel, ok := loadChannel(subscription.ChannelID)
				if !ok {
					continue
				}
				if destination := channelDestinationID(setting.DAGName, subscription.ID); channel.Enabled && destination != "" {
					destinations = append(destinations, destination)
				}
			}
		}
	}
	slices.Sort(destinations)
	return destinations
}

func (s *Service) NotificationDestinationsForEvent(event chatbridge.NotificationEvent) []string {
	routeKey := event.DAGRouteKey()
	if event.Status == nil || routeKey == "" {
		return nil
	}
	if _, state := eventWorkspace(event); state == workspace.WorkspaceLabelInvalid {
		return nil
	}
	ctx := context.Background()
	setting, err := s.GetByDAGName(ctx, routeKey)
	if err != nil {
		if !errors.Is(err, notificationmodel.ErrSettingsNotFound) {
			s.logger.Warn("Failed to load notification settings",
				slog.String("dag", routeKey),
				slog.String("error", err.Error()),
			)
			return nil
		}
		destinations := s.routeDestinationsForEvent(event, map[string]struct{}{})
		slices.Sort(destinations)
		return destinations
	}
	destinations := s.dagDestinationsForEvent(ctx, setting, event)
	slices.Sort(destinations)
	return destinations
}

func (s *Service) dagDestinationsForEvent(ctx context.Context, setting *notificationmodel.Settings, event chatbridge.NotificationEvent) []string {
	if !notificationmodel.IsEventEnabled(setting, event.Type) {
		return nil
	}
	seenChannelIDs := map[string]struct{}{}
	destinations := make([]string, 0, len(setting.Targets)+len(setting.Subscriptions))
	for _, target := range setting.Targets {
		if !notificationmodel.IsTargetEventEnabled(setting, target, event.Type) {
			continue
		}
		if destination := inlineDestinationID(setting.DAGName, target.ID); destination != "" {
			destinations = append(destinations, destination)
		}
	}
	if s.reusableChannelsAllowed() {
		for _, subscription := range setting.Subscriptions {
			if !notificationmodel.IsSubscriptionEventEnabled(setting, subscription, event.Type) {
				continue
			}
			if _, ok := seenChannelIDs[subscription.ChannelID]; ok {
				continue
			}
			channel, err := s.GetChannel(ctx, subscription.ChannelID)
			if err != nil {
				if !errors.Is(err, notificationmodel.ErrChannelNotFound) {
					s.logger.Warn("Failed to load notification channel",
						slog.String("dag", event.Status.Name),
						slog.String("channel", subscription.ChannelID),
						slog.String("error", err.Error()),
					)
				}
				continue
			}
			if !channel.Enabled {
				continue
			}
			if destination := channelDestinationID(setting.DAGName, subscription.ID); destination != "" {
				destinations = append(destinations, destination)
				seenChannelIDs[subscription.ChannelID] = struct{}{}
			}
		}
	}
	return destinations
}

func (s *Service) FlushNotificationBatch(ctx context.Context, destination string, batch chatbridge.NotificationBatch, _ bool) bool {
	if route := parseRouteDestinationID(destination); route.OK {
		return s.flushRouteNotificationBatch(ctx, destination, route.Scope, route.Workspace, route.RouteID, batch)
	}
	parsed := parseDestinationID(destination)
	if !parsed.OK {
		return false
	}
	setting, err := s.GetByDAGName(ctx, parsed.DAGName)
	if err != nil {
		s.logger.Warn("Failed to load notification settings for delivery",
			slog.String("destination", destination),
			slog.String("error", err.Error()),
		)
		return false
	}
	if parsed.Kind == destinationKindChannel {
		if !s.reusableChannelsAllowed() {
			return true
		}
		subscription, ok := findSubscription(setting, parsed.TargetID)
		if !ok || !subscription.Enabled {
			return true
		}
		channel, err := s.GetChannel(ctx, subscription.ChannelID)
		if err != nil {
			if errors.Is(err, notificationmodel.ErrChannelNotFound) {
				return true
			}
			s.logger.Warn("Failed to load notification channel for delivery",
				slog.String("destination", destination),
				slog.String("error", err.Error()),
			)
			return false
		}
		if !channel.Enabled {
			return true
		}
		events := matchingSubscriptionEvents(setting, subscription, batch.Events)
		if len(events) == 0 {
			return true
		}
		target := channel.ToTarget()
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
	target, ok := findTarget(setting, parsed.TargetID)
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
		target.Type != notificationmodel.ProviderTelegram &&
		target.Type != notificationmodel.ProviderTeams {
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

func (s *Service) flushRouteNotificationBatch(
	ctx context.Context,
	destination string,
	scope notificationmodel.RouteScope,
	workspace string,
	routeID string,
	batch chatbridge.NotificationBatch,
) bool {
	if !s.reusableChannelsAllowed() {
		return true
	}
	routeSet, err := s.loadRouteSet(ctx, scope, workspace)
	if err != nil {
		if errors.Is(err, notificationmodel.ErrRouteSetNotFound) {
			return true
		}
		s.logger.Warn("Failed to load notification route set for delivery",
			slog.String("destination", destination),
			slog.String("error", err.Error()),
		)
		return false
	}
	route, ok := findRoute(routeSet, routeID)
	if !ok || !route.Enabled || !routeSet.Enabled {
		return true
	}
	channel, err := s.GetChannel(ctx, route.ChannelID)
	if err != nil {
		if errors.Is(err, notificationmodel.ErrChannelNotFound) {
			return true
		}
		s.logger.Warn("Failed to load notification channel for route delivery",
			slog.String("destination", destination),
			slog.String("error", err.Error()),
		)
		return false
	}
	if !channel.Enabled {
		return true
	}
	events := s.matchingRouteEvents(ctx, routeSet, route, batch.Events)
	if len(events) == 0 {
		return true
	}
	target := channel.ToTarget()
	if err := s.deliverTarget(ctx, target, events); err != nil {
		s.logger.Warn("Failed to deliver notification route",
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
	event := s.testEvent(ctx, dagName, eventType)

	targets, err := s.testTargetsForDAGSettings(ctx, dagName, targetID, eventType)
	if err != nil {
		if !errors.Is(err, notificationmodel.ErrSettingsNotFound) || targetID != "" {
			return nil, err
		}
		targets, err = s.testTargetsForRoutes(ctx, event, targetID)
		if err != nil {
			return nil, err
		}
	}
	if len(targets) == 0 {
		if targetID != "" {
			return nil, notificationmodel.ErrTargetNotFound
		}
		return []TestResult{}, nil
	}

	return s.deliverTestTargets(ctx, targets, event), nil
}

func (s *Service) testTargetsForDAGSettings(ctx context.Context, dagName, targetID string, eventType eventstore.EventType) ([]resolvedTarget, error) {
	setting, err := s.GetByDAGName(ctx, dagName)
	if err != nil {
		return nil, err
	}
	targets := make([]resolvedTarget, 0, len(setting.Targets)+len(setting.Subscriptions))
	for _, target := range setting.Targets {
		if targetID != "" {
			if target.ID == targetID {
				targets = append(targets, resolvedTarget{
					ResultID:   target.ID,
					ResultName: target.Name,
					Target:     target,
				})
				break
			}
			continue
		}
		if notificationmodel.IsTargetEventEnabled(setting, target, eventType) {
			targets = append(targets, resolvedTarget{
				ResultID:   target.ID,
				ResultName: target.Name,
				Target:     target,
			})
		}
	}
	if s.reusableChannelsAllowed() {
		for _, subscription := range setting.Subscriptions {
			if targetID != "" && subscription.ID != targetID && subscription.ChannelID != targetID {
				continue
			}
			if targetID == "" && !notificationmodel.IsSubscriptionEventEnabled(setting, subscription, eventType) {
				continue
			}
			channel, err := s.GetChannel(ctx, subscription.ChannelID)
			if err != nil {
				if targetID != "" && errors.Is(err, notificationmodel.ErrChannelNotFound) {
					return nil, err
				}
				continue
			}
			if !channel.Enabled {
				continue
			}
			targets = append(targets, resolvedTarget{
				ResultID:   subscription.ID,
				ResultName: channel.Name,
				Provider:   channel.Type,
				Target:     channel.ToTarget(),
			})
		}
	} else if targetID != "" {
		for _, subscription := range setting.Subscriptions {
			if subscription.ID == targetID || subscription.ChannelID == targetID {
				return nil, notificationmodel.ErrTargetNotFound
			}
		}
	}
	return targets, nil
}

func (s *Service) testTargetsForRoutes(ctx context.Context, event chatbridge.NotificationEvent, targetID string) ([]resolvedTarget, error) {
	if !s.reusableChannelsAllowed() {
		return nil, nil
	}
	routeSet := s.effectiveRouteSetForEvent(ctx, event)
	if routeSet == nil || !routeSet.Enabled {
		return nil, nil
	}
	targets := make([]resolvedTarget, 0, len(routeSet.Routes))
	for _, route := range routeSet.Routes {
		if targetID != "" && route.ID != targetID && route.ChannelID != targetID {
			continue
		}
		if targetID == "" && !notificationmodel.IsRouteEventEnabled(routeSet, route, event.Type) {
			continue
		}
		channel, err := s.GetChannel(ctx, route.ChannelID)
		if err != nil {
			if targetID != "" && errors.Is(err, notificationmodel.ErrChannelNotFound) {
				return nil, err
			}
			continue
		}
		if !channel.Enabled {
			continue
		}
		targets = append(targets, resolvedTarget{
			ResultID:   route.ID,
			ResultName: channel.Name,
			Provider:   channel.Type,
			Target:     channel.ToTarget(),
		})
	}
	return targets, nil
}

func (s *Service) deliverTestTargets(ctx context.Context, targets []resolvedTarget, event chatbridge.NotificationEvent) []TestResult {
	results := make([]TestResult, 0, len(targets))
	for _, target := range targets {
		err := s.deliverTarget(ctx, target.Target, []chatbridge.NotificationEvent{event})
		provider := target.Provider
		if provider == "" {
			provider = target.Target.Type
		}
		result := TestResult{
			TargetID:   target.ResultID,
			TargetName: target.ResultName,
			Provider:   provider,
			Delivered:  err == nil,
		}
		if err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	return results
}

func (s *Service) testEvent(ctx context.Context, dagName string, eventType eventstore.EventType) chatbridge.NotificationEvent {
	status := testStatus(dagName, eventType)
	if s.dagStore != nil {
		if dag, err := s.dagStore.GetDetails(ctx, dagName, dagstore.DAGLoadOptions{}); err == nil && dag != nil {
			if dag.Name != "" {
				status.Name = dag.Name
			}
			status.Labels = dag.Labels.Strings()
		}
	}
	return chatbridge.NotificationEvent{
		Key:        "notification-test:" + dagName,
		Type:       eventType,
		Status:     status,
		DAGFile:    dagName,
		ObservedAt: time.Now().UTC(),
	}
}

type resolvedTarget struct {
	ResultID   string
	ResultName string
	Provider   notificationmodel.ProviderType
	Target     notificationmodel.Target
}

func (s *Service) isSupportedEvent(eventType eventstore.EventType) bool {
	switch eventType {
	case eventstore.TypeDAGRunWaiting,
		eventstore.TypeDAGRunSucceeded,
		eventstore.TypeDAGRunPartiallySucceeded,
		eventstore.TypeDAGRunFailed,
		eventstore.TypeDAGRunAborted,
		eventstore.TypeDAGRunRejected:
		return true
	case eventstore.TypeDAGRunQueued,
		eventstore.TypeDAGRunRunning,
		eventstore.TypeDAGRunUpdated,
		eventstore.TypeLLMUsageRecorded:
		return false
	}
	return false
}

func testStatus(dagName string, eventType eventstore.EventType) *ir.DAGRunStatus {
	now := time.Now().UTC()
	status := ir.Failed
	message := "This is a test notification from Dagu."
	switch eventType {
	case eventstore.TypeDAGRunWaiting:
		status = ir.Waiting
		message = "This is a test waiting notification from Dagu."
	case eventstore.TypeDAGRunSucceeded:
		status = ir.Succeeded
		message = ""
	case eventstore.TypeDAGRunPartiallySucceeded:
		status = ir.PartiallySucceeded
		message = "This is a test partially succeeded notification from Dagu."
	case eventstore.TypeDAGRunFailed:
	case eventstore.TypeDAGRunAborted:
		status = ir.Aborted
		message = "This is a test aborted notification from Dagu."
	case eventstore.TypeDAGRunRejected:
		status = ir.Rejected
		message = "This is a test rejected notification from Dagu."
	case eventstore.TypeDAGRunQueued,
		eventstore.TypeDAGRunRunning,
		eventstore.TypeDAGRunUpdated,
		eventstore.TypeLLMUsageRecorded:
	}
	return &ir.DAGRunStatus{
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
		return s.sendWebhook(ctx, target, events)
	case notificationmodel.ProviderSlack:
		return s.withRetry(ctx, func() error { return s.sendSlack(ctx, target, events) })
	case notificationmodel.ProviderTelegram:
		return s.withRetry(ctx, func() error { return s.sendTelegram(ctx, target, events) })
	case notificationmodel.ProviderTeams:
		return s.withRetry(ctx, func() error { return s.sendTeams(ctx, target, events) })
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

func (s *Service) loadRouteSet(ctx context.Context, scope notificationmodel.RouteScope, workspace string) (*notificationmodel.RouteSet, error) {
	if s.store == nil {
		return nil, notificationmodel.ErrRouteSetNotFound
	}
	return s.store.GetRouteSet(ctx, scope, workspace)
}

func defaultRouteSet(scope notificationmodel.RouteScope, workspace string) *notificationmodel.RouteSet {
	routeSet := &notificationmodel.RouteSet{
		Scope:         scope,
		Workspace:     workspace,
		Enabled:       true,
		InheritGlobal: true,
		Routes:        []notificationmodel.Route{},
	}
	if scope == notificationmodel.RouteScopeGlobal {
		routeSet.Workspace = ""
	}
	return routeSet
}

func routeSetID(routeSet *notificationmodel.RouteSet) string {
	if routeSet == nil {
		return ""
	}
	if routeSet.Scope == notificationmodel.RouteScopeWorkspace {
		return string(routeSet.Scope) + ":" + routeSet.Workspace
	}
	return string(routeSet.Scope)
}

const (
	destinationKindInline  = "target"
	destinationKindChannel = "channel"
	destinationKindRoute   = "route"
)

func inlineDestinationID(dagName, targetID string) string {
	if dagName == "" || targetID == "" {
		return ""
	}
	return dagName + "\x00" + targetID
}

func channelDestinationID(dagName, subscriptionID string) string {
	if dagName == "" || subscriptionID == "" {
		return ""
	}
	return destinationKindChannel + "\x00" + dagName + "\x00" + subscriptionID
}

func routeDestinationID(scope notificationmodel.RouteScope, workspace, routeID string) string {
	if scope == "" || routeID == "" {
		return ""
	}
	return destinationKindRoute + "\x00" + string(scope) + "\x00" + workspace + "\x00" + routeID
}

type parsedDestination struct {
	Kind     string
	DAGName  string
	TargetID string
	OK       bool
}

func parseDestinationID(destination string) parsedDestination {
	if rest, ok := strings.CutPrefix(destination, destinationKindChannel+"\x00"); ok {
		dagName, subscriptionID, ok := strings.Cut(rest, "\x00")
		return parsedDestination{
			Kind:     destinationKindChannel,
			DAGName:  dagName,
			TargetID: subscriptionID,
			OK:       ok && dagName != "" && subscriptionID != "",
		}
	}
	dagName, targetID, ok := strings.Cut(destination, "\x00")
	return parsedDestination{
		Kind:     destinationKindInline,
		DAGName:  dagName,
		TargetID: targetID,
		OK:       ok && dagName != "" && targetID != "",
	}
}

type parsedRouteDestination struct {
	Scope     notificationmodel.RouteScope
	Workspace string
	RouteID   string
	OK        bool
}

func parseRouteDestinationID(destination string) parsedRouteDestination {
	rest, ok := strings.CutPrefix(destination, destinationKindRoute+"\x00")
	if !ok {
		return parsedRouteDestination{}
	}
	parts := strings.SplitN(rest, "\x00", 3)
	if len(parts) != 3 || parts[0] == "" || parts[2] == "" {
		return parsedRouteDestination{}
	}
	return parsedRouteDestination{
		Scope:     notificationmodel.RouteScope(parts[0]),
		Workspace: parts[1],
		RouteID:   parts[2],
		OK:        true,
	}
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

func findRoute(routeSet *notificationmodel.RouteSet, routeID string) (notificationmodel.Route, bool) {
	if routeSet == nil || routeID == "" {
		return notificationmodel.Route{}, false
	}
	for _, route := range routeSet.Routes {
		if route.ID == routeID {
			return route, true
		}
	}
	return notificationmodel.Route{}, false
}

func findSubscription(setting *notificationmodel.Settings, subscriptionID string) (notificationmodel.Subscription, bool) {
	if setting == nil || subscriptionID == "" {
		return notificationmodel.Subscription{}, false
	}
	for _, subscription := range setting.Subscriptions {
		if subscription.ID == subscriptionID {
			return subscription, true
		}
	}
	return notificationmodel.Subscription{}, false
}

func (s *Service) routeDestinationsForEvent(event chatbridge.NotificationEvent, seenChannelIDs map[string]struct{}) []string {
	if !s.reusableChannelsAllowed() {
		return nil
	}
	ctx := context.Background()
	routeSet := s.effectiveRouteSetForEvent(ctx, event)
	return s.routeSetDestinationsForEvent(routeSet, event, seenChannelIDs)
}

func (s *Service) routeSetDestinationsForEvent(
	routeSet *notificationmodel.RouteSet,
	event chatbridge.NotificationEvent,
	seenChannelIDs map[string]struct{},
) []string {
	if routeSet == nil || !routeSet.Enabled {
		return nil
	}
	destinations := make([]string, 0, len(routeSet.Routes))
	for _, route := range routeSet.Routes {
		if !notificationmodel.IsRouteEventEnabled(routeSet, route, event.Type) {
			continue
		}
		if _, ok := seenChannelIDs[route.ChannelID]; ok {
			continue
		}
		channel, err := s.GetChannel(context.Background(), route.ChannelID)
		if err != nil {
			if !errors.Is(err, notificationmodel.ErrChannelNotFound) {
				s.logger.Warn("Failed to load notification route channel",
					slog.String("routeSet", routeSetID(routeSet)),
					slog.String("channel", route.ChannelID),
					slog.String("error", err.Error()),
				)
			}
			continue
		}
		if !channel.Enabled {
			continue
		}
		if destination := routeDestinationID(routeSet.Scope, routeSet.Workspace, route.ID); destination != "" {
			destinations = append(destinations, destination)
			seenChannelIDs[route.ChannelID] = struct{}{}
		}
	}
	return destinations
}

func (s *Service) effectiveRouteSetForEvent(ctx context.Context, event chatbridge.NotificationEvent) *notificationmodel.RouteSet {
	workspaceName, state := eventWorkspace(event)
	if state == workspace.WorkspaceLabelInvalid {
		return nil
	}
	if workspaceName != "" {
		workspaceRouteSet, err := s.loadRouteSet(ctx, notificationmodel.RouteScopeWorkspace, workspaceName)
		if err == nil {
			if !workspaceRouteSet.InheritGlobal {
				return workspaceRouteSet
			}
		} else if !errors.Is(err, notificationmodel.ErrRouteSetNotFound) {
			s.logger.Warn("Failed to load workspace notification route set",
				slog.String("workspace", workspaceName),
				slog.String("error", err.Error()),
			)
			return nil
		}
	}
	globalRouteSet, err := s.loadRouteSet(ctx, notificationmodel.RouteScopeGlobal, "")
	if err == nil {
		return globalRouteSet
	}
	if !errors.Is(err, notificationmodel.ErrRouteSetNotFound) {
		s.logger.Warn("Failed to load global notification route set",
			slog.String("error", err.Error()),
		)
	}
	return nil
}

func matchingEvents(setting *notificationmodel.Settings, target notificationmodel.Target, events []chatbridge.NotificationEvent) []chatbridge.NotificationEvent {
	result := make([]chatbridge.NotificationEvent, 0, len(events))
	for _, event := range events {
		if event.Status == nil || event.DAGRouteKey() != setting.DAGName {
			continue
		}
		if !notificationmodel.IsTargetEventEnabled(setting, target, event.Type) {
			continue
		}
		result = append(result, event)
	}
	return result
}

func matchingSubscriptionEvents(setting *notificationmodel.Settings, subscription notificationmodel.Subscription, events []chatbridge.NotificationEvent) []chatbridge.NotificationEvent {
	result := make([]chatbridge.NotificationEvent, 0, len(events))
	for _, event := range events {
		if event.Status == nil || event.DAGRouteKey() != setting.DAGName {
			continue
		}
		if !notificationmodel.IsSubscriptionEventEnabled(setting, subscription, event.Type) {
			continue
		}
		result = append(result, event)
	}
	return result
}

func (s *Service) matchingRouteEvents(ctx context.Context, routeSet *notificationmodel.RouteSet, route notificationmodel.Route, events []chatbridge.NotificationEvent) []chatbridge.NotificationEvent {
	result := make([]chatbridge.NotificationEvent, 0, len(events))
	for _, event := range events {
		routeKey := event.DAGRouteKey()
		if event.Status == nil || routeKey == "" {
			continue
		}
		if !notificationmodel.IsRouteEventEnabled(routeSet, route, event.Type) {
			continue
		}
		if _, err := s.GetByDAGName(ctx, routeKey); err == nil {
			continue
		} else if !errors.Is(err, notificationmodel.ErrSettingsNotFound) {
			s.logger.Warn("Failed to load notification settings",
				slog.String("dag", routeKey),
				slog.String("error", err.Error()),
			)
			continue
		}
		effectiveRouteSet := s.effectiveRouteSetForEvent(ctx, event)
		if routeSetID(effectiveRouteSet) != routeSetID(routeSet) {
			continue
		}
		result = append(result, event)
	}
	return result
}

func eventWorkspace(event chatbridge.NotificationEvent) (string, workspace.WorkspaceLabelState) {
	if event.Status == nil {
		return "", workspace.WorkspaceLabelMissing
	}
	return workspace.WorkspaceLabelFromLabels(ir.NewLabels(event.Status.Labels))
}

func eventWorkspaceName(event chatbridge.NotificationEvent) string {
	workspaceName, state := eventWorkspace(event)
	if state == workspace.WorkspaceLabelValid {
		return workspaceName
	}
	return ""
}

func (s *Service) sendEmail(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) error {
	if target.Email == nil || len(events) == 0 {
		return nil
	}
	workspaceSettings, err := s.GetWorkspaceSettings(ctx)
	if err != nil {
		s.logger.Warn("Failed to load notification workspace settings",
			slog.String("error", err.Error()),
		)
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
	subject := emailSubjectForEvents(target.Email, events, s.publicURL())
	attachments := []string{}
	if target.Email.AttachLogs {
		attachments = logAttachments(events)
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
	err = mailer.New(mailerConfig).SendWithRecipients(
		ctx,
		from,
		target.Email.To,
		target.Email.Cc,
		target.Email.Bcc,
		subject,
		messageForEvents(target.Email.BodyTemplate, events, s.publicURL()),
		attachments,
	)
	return err
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
	if notificationmodel.IsSlackIncomingWebhookURL(target.Webhook.URL) {
		return errors.New("slack incoming webhook URL is configured as generic webhook; use the slack provider")
	}
	if err := validateOutboundURL(ctx, target.Webhook.URL, target.Webhook.AllowInsecureHTTP, target.Webhook.AllowPrivateNetwork); err != nil {
		return err
	}
	publicURL := s.publicURL()
	if target.Webhook.BodyTemplate != "" {
		return s.sendWebhookBodyTemplate(ctx, target, events, publicURL)
	}
	payload := webhookPayloadForEvents(events, publicURL)
	payload["message"] = messageForEvents(target.Webhook.MessageTemplate, events, publicURL)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.postWebhookBody(ctx, target, body)
}

// sendWebhookBodyTemplate posts one custom-rendered request per event, so that
// every request carries a payload the receiving service can parse on its own.
// Each request is retried on its own, so a transient failure part-way through
// does not re-deliver the events that already succeeded.
func (s *Service) sendWebhookBodyTemplate(
	ctx context.Context,
	target notificationmodel.Target,
	events []chatbridge.NotificationEvent,
	publicURL string,
) error {
	bodies := make([][]byte, 0, len(events))
	for _, event := range events {
		if event.Status == nil {
			continue
		}
		single := []chatbridge.NotificationEvent{event}
		message := messageForEvents(target.Webhook.MessageTemplate, single, publicURL)
		body := []byte(renderWebhookBodyTemplate(target.Webhook.BodyTemplate, event, message, publicURL))
		if !json.Valid(body) {
			return errors.New("webhook body template did not render valid JSON")
		}
		bodies = append(bodies, body)
	}
	for _, body := range bodies {
		if err := s.postWebhookBody(ctx, target, body); err != nil {
			return err
		}
	}
	return nil
}

// postWebhookBody delivers a single request body, retrying transient failures.
func (s *Service) postWebhookBody(ctx context.Context, target notificationmodel.Target, body []byte) error {
	return s.withRetry(ctx, func() error {
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
	})
}

func (s *Service) sendSlack(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) error {
	if target.Slack == nil || target.Slack.WebhookURL == "" {
		return errors.New("slack webhook url is not configured")
	}
	if err := validateOutboundURL(ctx, target.Slack.WebhookURL, false, false); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{
		"text": messageForEvents(target.Slack.MessageTemplate, events, s.publicURL()),
	})
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

func (s *Service) sendTeams(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) error {
	if target.Teams == nil || target.Teams.WebhookURL == "" {
		return errors.New("teams webhook url is not configured")
	}
	if err := validateOutboundURL(ctx, target.Teams.WebhookURL, false, false); err != nil {
		return err
	}
	body, err := json.Marshal(teamsPayloadForEvents(target.Teams.MessageTemplate, events, s.publicURL()))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.Teams.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return s.deliverRequest(req, teamsThrottleError)
}

// teamsThrottleError reports rate limiting for Microsoft Teams incoming
// webhooks, which report it in the body of a 200 response instead of with
// HTTP 429.
func teamsThrottleError(body string) error {
	if strings.Contains(body, "Microsoft Teams endpoint returned HTTP error 429") {
		return temporaryDeliveryError{err: errors.New("teams webhook is rate limited")}
	}
	return nil
}

func (s *Service) sendTelegram(ctx context.Context, target notificationmodel.Target, events []chatbridge.NotificationEvent) error {
	if target.Telegram == nil || target.Telegram.BotToken == "" || target.Telegram.ChatID == "" {
		return errors.New("telegram bot token or chat id is not configured")
	}
	payload := map[string]any{
		"chat_id": target.Telegram.ChatID,
		"text":    messageForEvents(target.Telegram.MessageTemplate, events, s.publicURL()),
	}
	if target.Telegram.TopicID != "" {
		if topicID, err := strconv.Atoi(target.Telegram.TopicID); err == nil {
			payload["message_thread_id"] = topicID
		}
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

func (s *Service) doWebhookRequest(req *http.Request) error {
	return s.deliverRequest(req, nil)
}

// deliverRequest sends req and maps the response to a delivery error. checkBody,
// when set, inspects the body of an otherwise successful response.
func (s *Service) deliverRequest(req *http.Request, checkBody func(body string) error) error {
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
	if checkBody != nil {
		return checkBody(readLimitedBody(resp.Body))
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
	text := readLimitedBody(body)
	if text == "" {
		return ""
	}
	return ": " + text
}

func readLimitedBody(body io.Reader) string {
	if body == nil {
		return ""
	}
	data, _ := io.ReadAll(io.LimitReader(body, 512))
	return strings.TrimSpace(string(data))
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

func emailSubjectForEvents(email *notificationmodel.EmailTarget, events []chatbridge.NotificationEvent, publicURL string) string {
	if email != nil && strings.TrimSpace(email.SubjectTemplate) != "" {
		for _, event := range events {
			if event.Status == nil {
				continue
			}
			subject := strings.TrimSpace(renderNotificationTemplate(email.SubjectTemplate, event, publicURL))
			if subject != "" {
				return subject
			}
		}
		return titleForEvents(events)
	}
	subject := ""
	if email != nil {
		subject = email.SubjectPrefix
	}
	if subject == "" {
		subject = "[DAGU]"
	}
	return strings.TrimSpace(fmt.Sprintf("%s %s", subject, titleForEvents(events)))
}

func bodyForEvents(events []chatbridge.NotificationEvent, publicURL string) string {
	var b strings.Builder
	for i, event := range events {
		if event.Status == nil {
			continue
		}
		if i > 0 {
			b.WriteString("\n\n")
		}
		status := event.Status
		fmt.Fprintf(&b, "DAG: %s\n", status.Name)
		fmt.Fprintf(&b, "Run ID: %s\n", status.DAGRunID)
		fmt.Fprintf(&b, "Status: %s\n", status.Status.String())
		if startedAt, err := stringutil.ParseTime(status.StartedAt); err == nil && !startedAt.IsZero() {
			fmt.Fprintf(&b, "Started: %s\n", startedAt.Format(time.RFC3339))
		}
		if finishedAt, err := stringutil.ParseTime(status.FinishedAt); err == nil && !finishedAt.IsZero() {
			fmt.Fprintf(&b, "Finished: %s\n", finishedAt.Format(time.RFC3339))
		}
		if status.Error != "" {
			fmt.Fprintf(&b, "Error: %s\n", status.Error)
		}
		if runLink := notificationRunLink(status, publicURL); runLink != "" {
			fmt.Fprintf(&b, "%s\n", runLink)
		}
	}
	return b.String()
}

func messageForEvents(template string, events []chatbridge.NotificationEvent, publicURL string) string {
	if strings.TrimSpace(template) == "" {
		return bodyForEvents(events, publicURL)
	}
	parts := make([]string, 0, len(events))
	for _, event := range events {
		if event.Status == nil {
			continue
		}
		rendered := strings.TrimSpace(renderNotificationTemplate(template, event, publicURL))
		if rendered != "" {
			parts = append(parts, rendered)
		}
	}
	return strings.Join(parts, "\n\n")
}

var notificationTemplateTokenRE = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}`)

func renderNotificationTemplate(template string, event chatbridge.NotificationEvent, publicURL string) string {
	return renderTemplateTokens(template, notificationTemplateValues(event, publicURL), nil)
}

// renderWebhookBodyTemplate renders a user-supplied JSON request body. Token
// values are escaped as JSON string content, so a value carrying quotes or
// newlines cannot break out of the surrounding string literal.
func renderWebhookBodyTemplate(
	template string,
	event chatbridge.NotificationEvent,
	message string,
	publicURL string,
) string {
	values := notificationTemplateValues(event, publicURL)
	values["message"] = message
	return renderTemplateTokens(template, values, escapeJSONStringContent)
}

func renderTemplateTokens(template string, values map[string]string, escape func(string) string) string {
	return notificationTemplateTokenRE.ReplaceAllStringFunc(template, func(token string) string {
		matches := notificationTemplateTokenRE.FindStringSubmatch(token)
		if len(matches) != 2 {
			return ""
		}
		value := values[matches[1]]
		if escape != nil {
			return escape(value)
		}
		return value
	})
}

func escapeJSONStringContent(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded[1 : len(encoded)-1])
}

func notificationTemplateValues(event chatbridge.NotificationEvent, publicURL string) map[string]string {
	values := map[string]string{
		"event.type": string(event.Type),
	}
	if !event.ObservedAt.IsZero() {
		values["event.observedAt"] = event.ObservedAt.Format(time.RFC3339)
	}
	if event.Status == nil {
		return values
	}
	status := event.Status
	workspaceName := eventWorkspaceName(event)
	values["dag.name"] = status.Name
	values["dagName"] = status.Name
	values["run.id"] = status.DAGRunID
	values["dagRunId"] = status.DAGRunID
	values["run.status"] = status.Status.String()
	values["status"] = status.Status.String()
	values["run.error"] = status.Error
	values["error"] = status.Error
	maps.Copy(values, notificationStepStatusValues(status))
	values["run.startedAt"] = notificationTemplateTime(status.StartedAt)
	values["run.finishedAt"] = notificationTemplateTime(status.FinishedAt)
	values["run.attemptId"] = status.AttemptID
	values["attempt.id"] = status.AttemptID
	values["attemptId"] = status.AttemptID
	values["workspace"] = workspaceName
	values["worker.id"] = status.WorkerID
	values["eventType"] = string(event.Type)
	runPath := notificationRunPath(status)
	runURL := notificationRunURL(publicURL, runPath)
	runLink := ""
	if runURL != "" {
		runLink = "Run: " + runURL
	}
	values["run.path"] = runPath
	values["runPath"] = runPath
	values["run.url"] = runURL
	values["runUrl"] = runURL
	values["run.link"] = runLink
	values["runLink"] = runLink
	return values
}

func notificationStepStatusValues(status *ir.DAGRunStatus) map[string]string {
	labels := map[ir.NodeStatus][]string{
		ir.NodeFailed:             nil,
		ir.NodePartiallySucceeded: nil,
		ir.NodeAborted:            nil,
		ir.NodeSucceeded:          nil,
	}
	for _, node := range status.Nodes {
		if node == nil {
			continue
		}
		if len(node.StatusDetails) == 0 {
			if _, ok := labels[node.Status]; ok {
				labels[node.Status] = append(labels[node.Status], node.Step.Name)
			}
			continue
		}
		detailMatchesNodeStatus := false
		for _, detail := range node.StatusDetails {
			if _, ok := labels[detail.Status]; !ok {
				continue
			}
			labels[detail.Status] = append(labels[detail.Status], notificationStatusDetailLabel(node.Step.Name, detail.Label))
			detailMatchesNodeStatus = detailMatchesNodeStatus || detail.Status == node.Status
		}
		if _, ok := labels[node.Status]; ok && !detailMatchesNodeStatus {
			labels[node.Status] = append(labels[node.Status], node.Step.Name)
		}
	}

	return map[string]string{
		"run.failed_steps":              strings.Join(labels[ir.NodeFailed], ", "),
		"run.partially_succeeded_steps": strings.Join(labels[ir.NodePartiallySucceeded], ", "),
		"run.aborted_steps":             strings.Join(labels[ir.NodeAborted], ", "),
		"run.succeeded_steps":           strings.Join(labels[ir.NodeSucceeded], ", "),
	}
}

func notificationStatusDetailLabel(stepName, detail string) string {
	switch {
	case stepName == "":
		return detail
	case detail == "":
		return stepName
	default:
		return fmt.Sprintf("%s[%s]", stepName, detail)
	}
}

func notificationTemplateTime(value string) string {
	if value == "" {
		return ""
	}
	parsed, err := stringutil.ParseTime(value)
	if err != nil || parsed.IsZero() {
		return value
	}
	return parsed.Format(time.RFC3339)
}

func notificationRunPath(status *ir.DAGRunStatus) string {
	if status == nil || status.Name == "" || status.DAGRunID == "" {
		return ""
	}

	root := status.Root
	if root.Zero() {
		root = ir.NewDAGRunRef(status.Name, status.DAGRunID)
	}
	if root.Name == "" || root.ID == "" {
		return ""
	}

	base := "/dag-runs/" + url.PathEscape(root.Name) + "/" + url.PathEscape(root.ID)
	if status.Parent.Zero() || (status.Name == root.Name && status.DAGRunID == root.ID) {
		return base
	}

	query := url.Values{}
	query.Set("subDAGRunId", status.DAGRunID)
	query.Set("dagRunId", root.ID)
	query.Set("dagRunName", root.Name)
	return base + "?" + query.Encode()
}

func notificationRunURL(publicURL, runPath string) string {
	if runPath == "" {
		return ""
	}
	publicURL = normalizeNotificationPublicURL(publicURL)
	if publicURL == "" {
		return ""
	}
	return strings.TrimRight(publicURL, "/") + "/" + strings.TrimLeft(runPath, "/")
}

func notificationRunLink(status *ir.DAGRunStatus, publicURL string) string {
	if runURL := notificationRunURL(publicURL, notificationRunPath(status)); runURL != "" {
		return "Run: " + runURL
	}
	return ""
}

func normalizeNotificationPublicURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String()
}

// teamsPayloadForEvents builds a Message Card accepted by Teams Workflows and
// legacy connectors. The summary provides concise notification preview text.
func teamsPayloadForEvents(template string, events []chatbridge.NotificationEvent, publicURL string) map[string]any {
	title := titleForEvents(events)
	return map[string]any{
		"@type":    "MessageCard",
		"@context": "http://schema.org/extensions",
		"summary":  title,
		"title":    title,
		"text":     messageForEvents(template, events, publicURL),
	}
}

func webhookPayloadForEvents(events []chatbridge.NotificationEvent, publicURL string) map[string]any {
	items := make([]map[string]any, 0, len(events))
	for _, event := range events {
		if event.Status == nil {
			continue
		}
		runPath := notificationRunPath(event.Status)
		item := map[string]any{
			"eventType":  string(event.Type),
			"dagName":    event.Status.Name,
			"dagRunId":   event.Status.DAGRunID,
			"runPath":    runPath,
			"status":     event.Status.Status.String(),
			"error":      event.Status.Error,
			"observedAt": event.ObservedAt.Format(time.RFC3339Nano),
		}
		if runURL := notificationRunURL(publicURL, runPath); runURL != "" {
			item["runUrl"] = runURL
		}
		items = append(items, item)
	}
	return map[string]any{
		"version": "v1",
		"events":  items,
	}
}

var _ chatbridge.NotificationTransport = (*Service)(nil)
var _ chatbridge.NotificationBatchDeliveryPolicyTransport = (*Service)(nil)
var _ chatbridge.NotificationRoutingTransport = (*Service)(nil)
