// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ notification.Store = (*NotificationStore)(nil)

const notificationGlobalRouteSetID = "routes/global"

// NotificationStore persists notification configuration in a collection.
type NotificationStore struct {
	recordHelper
	encryptor *crypto.Encryptor
}

// NewNotificationStore creates a notification store backed by col.
func NewNotificationStore(col persis.Collection, enc *crypto.Encryptor) (*NotificationStore, error) {
	if col == nil {
		return nil, errors.New("notification store: collection cannot be nil")
	}
	return &NotificationStore{
		col: col, name: "notification store",
		encryptor: enc,
	}, nil
}

func (s *NotificationStore) Save(ctx context.Context, settings *notification.Settings) error {
	if settings == nil {
		return errors.New("notification store: settings cannot be nil")
	}
	if settings.DAGName == "" {
		return errors.New("notification store: dagName is required")
	}
	stored, err := s.settingsToRecord(settings)
	if err != nil {
		return err
	}
	return s.put(ctx, notificationSettingsID(settings.DAGName), stored)
}

func (s *NotificationStore) GetByDAGName(ctx context.Context, dagName string) (*notification.Settings, error) {
	if dagName == "" {
		return nil, notification.ErrSettingsNotFound
	}
	rec, err := s.get(ctx, notificationSettingsID(dagName), notification.ErrSettingsNotFound)
	if err != nil {
		return nil, err
	}
	return s.settingsFromRecord(rec)
}

func (s *NotificationStore) List(ctx context.Context) ([]*notification.Settings, error) {
	recs, err := s.listTolerant(ctx, "dags/", "settings")
	if err != nil {
		return nil, fmt.Errorf("notification store: list settings: %w", err)
	}
	result := make([]*notification.Settings, 0, len(recs))
	for _, rec := range recs {
		settings, err := s.settingsFromRecord(rec)
		if err != nil {
			logger.Warn(ctx, "notification store: failed to load settings", slog.String("record", rec.ID), tag.Error(err))
			continue
		}
		result = append(result, settings)
	}
	return result, nil
}

func (s *NotificationStore) DeleteByDAGName(ctx context.Context, dagName string) error {
	if dagName == "" {
		return notification.ErrSettingsNotFound
	}
	return s.delete(ctx, notificationSettingsID(dagName), notification.ErrSettingsNotFound, "settings")
}

func (s *NotificationStore) SaveChannel(ctx context.Context, channel *notification.Channel) error {
	if channel == nil {
		return errors.New("notification store: channel cannot be nil")
	}
	if channel.ID == "" {
		return errors.New("notification store: channel id is required")
	}
	stored, err := s.channelToRecord(channel)
	if err != nil {
		return err
	}
	return s.put(ctx, notificationChannelID(channel.ID), stored)
}

func (s *NotificationStore) GetChannel(ctx context.Context, channelID string) (*notification.Channel, error) {
	if channelID == "" {
		return nil, notification.ErrChannelNotFound
	}
	rec, err := s.get(ctx, notificationChannelID(channelID), notification.ErrChannelNotFound)
	if err != nil {
		return nil, err
	}
	return s.channelFromRecord(rec)
}

func (s *NotificationStore) ListChannels(ctx context.Context) ([]*notification.Channel, error) {
	recs, err := s.listTolerant(ctx, "channels/", "channel")
	if err != nil {
		return nil, fmt.Errorf("notification store: list channels: %w", err)
	}
	result := make([]*notification.Channel, 0, len(recs))
	for _, rec := range recs {
		channel, err := s.channelFromRecord(rec)
		if err != nil {
			logger.Warn(ctx, "notification store: failed to load channel", slog.String("record", rec.ID), tag.Error(err))
			continue
		}
		result = append(result, channel)
	}
	return result, nil
}

func (s *NotificationStore) DeleteChannel(ctx context.Context, channelID string) error {
	if channelID == "" {
		return notification.ErrChannelNotFound
	}
	return s.delete(ctx, notificationChannelID(channelID), notification.ErrChannelNotFound, "channel")
}

func (s *NotificationStore) SaveWorkspaceSettings(ctx context.Context, settings *notification.WorkspaceSettings) error {
	if settings == nil {
		return errors.New("notification store: workspace settings cannot be nil")
	}
	stored, err := s.workspaceSettingsToRecord(settings)
	if err != nil {
		return err
	}
	return s.put(ctx, "workspace", stored)
}

func (s *NotificationStore) GetWorkspaceSettings(ctx context.Context) (*notification.WorkspaceSettings, error) {
	rec, err := s.col.Get(ctx, "workspace")
	if errors.Is(err, persis.ErrNotFound) {
		return &notification.WorkspaceSettings{}, nil
	}
	if err != nil {
		return nil, err
	}
	return s.workspaceSettingsFromRecord(rec)
}

func (s *NotificationStore) SaveRouteSet(ctx context.Context, routeSet *notification.RouteSet) error {
	if routeSet == nil {
		return errors.New("notification store: route set cannot be nil")
	}
	id, err := notificationRouteSetID(routeSet.Scope, routeSet.Workspace)
	if err != nil {
		return err
	}
	return s.put(ctx, id, routeSetToRecord(routeSet))
}

func (s *NotificationStore) GetRouteSet(
	ctx context.Context,
	scope notification.RouteScope,
	workspace string,
) (*notification.RouteSet, error) {
	id, err := notificationRouteSetID(scope, workspace)
	if err != nil {
		return nil, err
	}
	rec, err := s.get(ctx, id, notification.ErrRouteSetNotFound)
	if err != nil {
		return nil, err
	}
	return routeSetFromRecord(rec)
}

func (s *NotificationStore) ListRouteSets(ctx context.Context) ([]*notification.RouteSet, error) {
	result := make([]*notification.RouteSet, 0)
	if rec, err := s.col.Get(ctx, notificationGlobalRouteSetID); err == nil {
		routeSet, decodeErr := routeSetFromRecord(rec)
		if decodeErr != nil {
			logger.Warn(ctx, "notification store: failed to load global route set", tag.Error(decodeErr))
		} else {
			result = append(result, routeSet)
		}
	} else if !errors.Is(err, persis.ErrNotFound) {
		logger.Warn(ctx, "notification store: failed to load global route set", tag.Error(err))
	}

	recs, err := s.listTolerant(ctx, "routes/workspaces/", "route set")
	if err != nil {
		return nil, fmt.Errorf("notification store: list route sets: %w", err)
	}
	for _, rec := range recs {
		routeSet, err := routeSetFromRecord(rec)
		if err != nil {
			logger.Warn(ctx, "notification store: failed to load route set", slog.String("record", rec.ID), tag.Error(err))
			continue
		}
		result = append(result, routeSet)
	}
	return result, nil
}

func (s *NotificationStore) DeleteRouteSet(ctx context.Context, scope notification.RouteScope, workspace string) error {
	id, err := notificationRouteSetID(scope, workspace)
	if err != nil {
		return err
	}
	return s.delete(ctx, id, notification.ErrRouteSetNotFound, "route set")
}

func notificationSettingsID(dagName string) string {
	return "dags/" + hashRecordID(dagName)
}

func notificationChannelID(channelID string) string {
	return "channels/" + hashRecordID(channelID)
}

func notificationRouteSetID(scope notification.RouteScope, workspace string) (string, error) {
	switch scope {
	case notification.RouteScopeGlobal:
		return notificationGlobalRouteSetID, nil
	case notification.RouteScopeWorkspace:
		return "routes/workspaces/" + hashRecordID(workspace), nil
	default:
		return "", fmt.Errorf("%w: invalid notification route scope %q", notification.ErrInvalidSettings, scope)
	}
}
