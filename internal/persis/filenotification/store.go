// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package filenotification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/notification"
	"github.com/dagucloud/dagu/internal/service/eventstore"
)

const (
	settingsFileExtension     = ".json"
	workspaceSettingsFileName = "workspace.json"
	globalRouteSetFileName    = "global.json"
	routeDirName              = "routes"
	workspaceRouteDirName     = "workspaces"
	dirPermissions            = 0750
	filePermissions           = 0600
)

type Option func(*Store)

func WithEncryptor(enc *crypto.Encryptor) Option {
	return func(s *Store) {
		s.encryptor = enc
	}
}

func WithChannelDir(channelDir string) Option {
	return func(s *Store) {
		s.channelDir = channelDir
	}
}

func WithRouteDir(routeDir string) Option {
	return func(s *Store) {
		s.routeDir = routeDir
	}
}

func WithWorkspaceSettingsFile(path string) Option {
	return func(s *Store) {
		s.workspaceSettingsFile = path
	}
}

type Store struct {
	baseDir               string
	channelDir            string
	routeDir              string
	workspaceSettingsFile string
	encryptor             *crypto.Encryptor
	mu                    sync.RWMutex
}

var _ notification.Store = (*Store)(nil)

func New(baseDir string, opts ...Option) (*Store, error) {
	if baseDir == "" {
		return nil, errors.New("filenotification: baseDir cannot be empty")
	}
	store := &Store{
		baseDir:               baseDir,
		channelDir:            defaultChannelDir(baseDir),
		routeDir:              defaultRouteDir(baseDir),
		workspaceSettingsFile: defaultWorkspaceSettingsFile(baseDir),
	}
	for _, opt := range opts {
		opt(store)
	}
	if err := os.MkdirAll(baseDir, dirPermissions); err != nil {
		return nil, fmt.Errorf("filenotification: failed to create directory %s: %w", baseDir, err)
	}
	if err := os.MkdirAll(store.channelDir, dirPermissions); err != nil {
		return nil, fmt.Errorf("filenotification: failed to create directory %s: %w", store.channelDir, err)
	}
	if err := os.MkdirAll(store.routeWorkspaceDir(), dirPermissions); err != nil {
		return nil, fmt.Errorf("filenotification: failed to create directory %s: %w", store.routeWorkspaceDir(), err)
	}
	if err := os.MkdirAll(filepath.Dir(store.workspaceSettingsFile), dirPermissions); err != nil {
		return nil, fmt.Errorf("filenotification: failed to create directory %s: %w", filepath.Dir(store.workspaceSettingsFile), err)
	}
	return store, nil
}

func (s *Store) Save(_ context.Context, settings *notification.Settings) error {
	if settings == nil {
		return errors.New("filenotification: settings cannot be nil")
	}
	if settings.DAGName == "" {
		return errors.New("filenotification: dagName is required")
	}
	stored, err := s.toStorage(settings)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fileutil.WriteJSONAtomic(s.filePath(settings.DAGName), stored, filePermissions); err != nil {
		return fmt.Errorf("filenotification: %w", err)
	}
	return nil
}

func (s *Store) GetByDAGName(_ context.Context, dagName string) (*notification.Settings, error) {
	if dagName == "" {
		return nil, notification.ErrSettingsNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	settings, err := s.loadFromFile(s.filePath(dagName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, notification.ErrSettingsNotFound
		}
		return nil, err
	}
	return settings, nil
}

func (s *Store) List(_ context.Context) ([]*notification.Settings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("filenotification: list directory: %w", err)
	}
	result := make([]*notification.Settings, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != settingsFileExtension {
			continue
		}
		if filepath.Join(s.baseDir, entry.Name()) == s.workspaceSettingsFile {
			continue
		}
		settings, err := s.loadFromFile(filepath.Join(s.baseDir, entry.Name()))
		if err != nil {
			slog.Warn("filenotification: failed to load settings file",
				slog.String("file", entry.Name()),
				slog.String("error", err.Error()),
			)
			continue
		}
		result = append(result, settings)
	}
	return result, nil
}

func (s *Store) DeleteByDAGName(_ context.Context, dagName string) error {
	if dagName == "" {
		return notification.ErrSettingsNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.filePath(dagName)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return notification.ErrSettingsNotFound
		}
		return fmt.Errorf("filenotification: delete settings: %w", err)
	}
	return nil
}

func (s *Store) SaveChannel(_ context.Context, channel *notification.Channel) error {
	if channel == nil {
		return errors.New("filenotification: channel cannot be nil")
	}
	if channel.ID == "" {
		return errors.New("filenotification: channel id is required")
	}
	stored, err := s.channelToStorage(channel)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fileutil.WriteJSONAtomic(s.channelFilePath(channel.ID), stored, filePermissions); err != nil {
		return fmt.Errorf("filenotification: %w", err)
	}
	return nil
}

func (s *Store) GetChannel(_ context.Context, channelID string) (*notification.Channel, error) {
	if channelID == "" {
		return nil, notification.ErrChannelNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	channel, err := s.loadChannelFromFile(s.channelFilePath(channelID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, notification.ErrChannelNotFound
		}
		return nil, err
	}
	return channel, nil
}

func (s *Store) ListChannels(_ context.Context) ([]*notification.Channel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.channelDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("filenotification: list notification channels: %w", err)
	}
	result := make([]*notification.Channel, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != settingsFileExtension {
			continue
		}
		channel, err := s.loadChannelFromFile(filepath.Join(s.channelDir, entry.Name()))
		if err != nil {
			slog.Warn("filenotification: failed to load channel file",
				slog.String("file", entry.Name()),
				slog.String("error", err.Error()),
			)
			continue
		}
		result = append(result, channel)
	}
	return result, nil
}

func (s *Store) DeleteChannel(_ context.Context, channelID string) error {
	if channelID == "" {
		return notification.ErrChannelNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.channelFilePath(channelID)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return notification.ErrChannelNotFound
		}
		return fmt.Errorf("filenotification: delete channel: %w", err)
	}
	return nil
}

func (s *Store) SaveWorkspaceSettings(_ context.Context, settings *notification.WorkspaceSettings) error {
	if settings == nil {
		return errors.New("filenotification: workspace settings cannot be nil")
	}
	stored, err := s.workspaceSettingsToStorage(settings)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fileutil.WriteJSONAtomic(s.workspaceSettingsFile, stored, filePermissions); err != nil {
		return fmt.Errorf("filenotification: %w", err)
	}
	return nil
}

func (s *Store) GetWorkspaceSettings(_ context.Context) (*notification.WorkspaceSettings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	settings, err := s.loadWorkspaceSettingsFromFile(s.workspaceSettingsFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &notification.WorkspaceSettings{}, nil
		}
		return nil, err
	}
	return settings, nil
}

func (s *Store) SaveRouteSet(_ context.Context, routeSet *notification.RouteSet) error {
	if routeSet == nil {
		return errors.New("filenotification: route set cannot be nil")
	}
	stored := routeSetToStorage(routeSet)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fileutil.WriteJSONAtomic(s.routeSetFilePath(routeSet.Scope, routeSet.Workspace), stored, filePermissions); err != nil {
		return fmt.Errorf("filenotification: %w", err)
	}
	return nil
}

func (s *Store) GetRouteSet(_ context.Context, scope notification.RouteScope, workspace string) (*notification.RouteSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	routeSet, err := s.loadRouteSetFromFile(s.routeSetFilePath(scope, workspace))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, notification.ErrRouteSetNotFound
		}
		return nil, err
	}
	return routeSet, nil
}

func (s *Store) ListRouteSets(_ context.Context) ([]*notification.RouteSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*notification.RouteSet, 0)
	if routeSet, err := s.loadRouteSetFromFile(s.routeSetFilePath(notification.RouteScopeGlobal, "")); err == nil {
		result = append(result, routeSet)
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("filenotification: failed to load global route set",
			slog.String("error", err.Error()),
		)
	}

	entries, err := os.ReadDir(s.routeWorkspaceDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return result, nil
		}
		return nil, fmt.Errorf("filenotification: list notification route sets: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != settingsFileExtension {
			continue
		}
		routeSet, err := s.loadRouteSetFromFile(filepath.Join(s.routeWorkspaceDir(), entry.Name()))
		if err != nil {
			slog.Warn("filenotification: failed to load route set file",
				slog.String("file", entry.Name()),
				slog.String("error", err.Error()),
			)
			continue
		}
		result = append(result, routeSet)
	}
	return result, nil
}

func (s *Store) DeleteRouteSet(_ context.Context, scope notification.RouteScope, workspace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.routeSetFilePath(scope, workspace)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return notification.ErrRouteSetNotFound
		}
		return fmt.Errorf("filenotification: delete route set: %w", err)
	}
	return nil
}

func (s *Store) filePath(dagName string) string {
	sum := sha256.Sum256([]byte(dagName))
	return filepath.Join(s.baseDir, hex.EncodeToString(sum[:])+settingsFileExtension)
}

func (s *Store) channelFilePath(channelID string) string {
	sum := sha256.Sum256([]byte(channelID))
	return filepath.Join(s.channelDir, hex.EncodeToString(sum[:])+settingsFileExtension)
}

func (s *Store) routeSetFilePath(scope notification.RouteScope, workspace string) string {
	if scope == notification.RouteScopeWorkspace {
		sum := sha256.Sum256([]byte(workspace))
		return filepath.Join(s.routeWorkspaceDir(), hex.EncodeToString(sum[:])+settingsFileExtension)
	}
	return filepath.Join(s.routeDir, globalRouteSetFileName)
}

func (s *Store) routeWorkspaceDir() string {
	return filepath.Join(s.routeDir, workspaceRouteDirName)
}

func defaultChannelDir(baseDir string) string {
	if filepath.Base(baseDir) == "dags" {
		return filepath.Join(filepath.Dir(baseDir), "channels")
	}
	return filepath.Join(baseDir, "channels")
}

func defaultRouteDir(baseDir string) string {
	if filepath.Base(baseDir) == "dags" {
		return filepath.Join(filepath.Dir(baseDir), routeDirName)
	}
	return filepath.Join(baseDir, routeDirName)
}

func defaultWorkspaceSettingsFile(baseDir string) string {
	if filepath.Base(baseDir) == "dags" {
		return filepath.Join(filepath.Dir(baseDir), workspaceSettingsFileName)
	}
	return filepath.Join(baseDir, workspaceSettingsFileName)
}

func (s *Store) loadFromFile(path string) (*notification.Settings, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from configured store directory and hashed DAG name.
	if err != nil {
		return nil, err
	}
	var stored settingsForStorage
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("filenotification: parse settings: %w", err)
	}
	return s.fromStorage(&stored)
}

func (s *Store) loadChannelFromFile(path string) (*notification.Channel, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from configured store directory and hashed channel ID.
	if err != nil {
		return nil, err
	}
	var stored channelForStorage
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("filenotification: parse channel: %w", err)
	}
	return s.channelFromStorage(&stored)
}

func (s *Store) loadWorkspaceSettingsFromFile(path string) (*notification.WorkspaceSettings, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from configured store directory and constant filename.
	if err != nil {
		return nil, err
	}
	var stored workspaceSettingsForStorage
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("filenotification: parse workspace settings: %w", err)
	}
	return s.workspaceSettingsFromStorage(&stored)
}

func (s *Store) loadRouteSetFromFile(path string) (*notification.RouteSet, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from configured route directory and hashed workspace name.
	if err != nil {
		return nil, err
	}
	var stored routeSetForStorage
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("filenotification: parse route set: %w", err)
	}
	return routeSetFromStorage(&stored), nil
}

type settingsForStorage struct {
	ID            string                   `json:"id"`
	DAGName       string                   `json:"dagName"`
	Enabled       bool                     `json:"enabled"`
	Events        []string                 `json:"events"`
	Targets       []targetForStorage       `json:"targets"`
	Subscriptions []subscriptionForStorage `json:"subscriptions,omitempty"`
	CreatedAt     string                   `json:"createdAt"`
	UpdatedAt     string                   `json:"updatedAt"`
	UpdatedBy     string                   `json:"updatedBy,omitempty"`
}

type channelForStorage struct {
	ID        string                    `json:"id"`
	Name      string                    `json:"name"`
	Type      notification.ProviderType `json:"type"`
	Enabled   bool                      `json:"enabled"`
	Email     *notification.EmailTarget `json:"email,omitempty"`
	Webhook   *webhookTargetForStorage  `json:"webhook,omitempty"`
	Slack     *slackTargetForStorage    `json:"slack,omitempty"`
	Telegram  *telegramTargetForStorage `json:"telegram,omitempty"`
	CreatedAt string                    `json:"createdAt"`
	UpdatedAt string                    `json:"updatedAt"`
	UpdatedBy string                    `json:"updatedBy,omitempty"`
}

type workspaceSettingsForStorage struct {
	SMTP      *smtpConfigForStorage `json:"smtp,omitempty"`
	CreatedAt string                `json:"createdAt"`
	UpdatedAt string                `json:"updatedAt"`
	UpdatedBy string                `json:"updatedBy,omitempty"`
}

type routeSetForStorage struct {
	ID            string                  `json:"id"`
	Scope         notification.RouteScope `json:"scope"`
	Workspace     string                  `json:"workspace,omitempty"`
	Enabled       bool                    `json:"enabled"`
	InheritGlobal bool                    `json:"inheritGlobal"`
	Routes        []routeForStorage       `json:"routes"`
	CreatedAt     string                  `json:"createdAt"`
	UpdatedAt     string                  `json:"updatedAt"`
	UpdatedBy     string                  `json:"updatedBy,omitempty"`
}

type routeForStorage struct {
	ID        string   `json:"id"`
	ChannelID string   `json:"channelId"`
	Enabled   bool     `json:"enabled"`
	Events    []string `json:"events,omitempty"`
}

type smtpConfigForStorage struct {
	Host        string `json:"host,omitempty"`
	Port        string `json:"port,omitempty"`
	Username    string `json:"username,omitempty"`
	PasswordEnc string `json:"passwordEnc,omitempty"`
	From        string `json:"from,omitempty"`
}

type subscriptionForStorage struct {
	ID        string   `json:"id"`
	ChannelID string   `json:"channelId"`
	Enabled   bool     `json:"enabled"`
	Events    []string `json:"events,omitempty"`
}

type targetForStorage struct {
	ID       string                    `json:"id"`
	Name     string                    `json:"name,omitempty"`
	Type     notification.ProviderType `json:"type"`
	Enabled  bool                      `json:"enabled"`
	Events   []string                  `json:"events,omitempty"`
	Email    *notification.EmailTarget `json:"email,omitempty"`
	Webhook  *webhookTargetForStorage  `json:"webhook,omitempty"`
	Slack    *slackTargetForStorage    `json:"slack,omitempty"`
	Telegram *telegramTargetForStorage `json:"telegram,omitempty"`
}

type webhookTargetForStorage struct {
	URLEnc              string            `json:"urlEnc,omitempty"`
	HeadersEnc          map[string]string `json:"headersEnc,omitempty"`
	HMACSecretEnc       string            `json:"hmacSecretEnc,omitempty"`
	MessageTemplate     string            `json:"messageTemplate,omitempty"`
	AllowInsecureHTTP   bool              `json:"allowInsecureHttp,omitempty"`
	AllowPrivateNetwork bool              `json:"allowPrivateNetwork,omitempty"`
}

type slackTargetForStorage struct {
	WebhookURLEnc   string `json:"webhookUrlEnc,omitempty"`
	MessageTemplate string `json:"messageTemplate,omitempty"`
}

type telegramTargetForStorage struct {
	BotTokenEnc     string `json:"botTokenEnc,omitempty"`
	ChatID          string `json:"chatId,omitempty"`
	MessageTemplate string `json:"messageTemplate,omitempty"`
}

func (s *Store) toStorage(settings *notification.Settings) (*settingsForStorage, error) {
	events := make([]string, 0, len(settings.Events))
	for _, event := range settings.Events {
		events = append(events, string(event))
	}
	targets := make([]targetForStorage, 0, len(settings.Targets))
	for _, target := range settings.Targets {
		stored, err := s.targetToStorage(target)
		if err != nil {
			return nil, err
		}
		targets = append(targets, stored)
	}
	subscriptions := make([]subscriptionForStorage, 0, len(settings.Subscriptions))
	for _, subscription := range settings.Subscriptions {
		subscriptions = append(subscriptions, subscriptionForStorage{
			ID:        subscription.ID,
			ChannelID: subscription.ChannelID,
			Enabled:   subscription.Enabled,
			Events:    eventStrings(subscription.Events),
		})
	}
	return &settingsForStorage{
		ID:            settings.ID,
		DAGName:       settings.DAGName,
		Enabled:       settings.Enabled,
		Events:        events,
		Targets:       targets,
		Subscriptions: subscriptions,
		CreatedAt:     settings.CreatedAt.Format(timeFormat),
		UpdatedAt:     settings.UpdatedAt.Format(timeFormat),
		UpdatedBy:     settings.UpdatedBy,
	}, nil
}

func (s *Store) channelToStorage(channel *notification.Channel) (*channelForStorage, error) {
	target, err := s.targetToStorage(channel.ToTarget())
	if err != nil {
		return nil, err
	}
	return &channelForStorage{
		ID:        channel.ID,
		Name:      channel.Name,
		Type:      channel.Type,
		Enabled:   channel.Enabled,
		Email:     target.Email,
		Webhook:   target.Webhook,
		Slack:     target.Slack,
		Telegram:  target.Telegram,
		CreatedAt: channel.CreatedAt.Format(timeFormat),
		UpdatedAt: channel.UpdatedAt.Format(timeFormat),
		UpdatedBy: channel.UpdatedBy,
	}, nil
}

func (s *Store) workspaceSettingsToStorage(settings *notification.WorkspaceSettings) (*workspaceSettingsForStorage, error) {
	stored := &workspaceSettingsForStorage{
		CreatedAt: settings.CreatedAt.Format(timeFormat),
		UpdatedAt: settings.UpdatedAt.Format(timeFormat),
		UpdatedBy: settings.UpdatedBy,
	}
	if settings.SMTP != nil {
		stored.SMTP = &smtpConfigForStorage{
			Host:     settings.SMTP.Host,
			Port:     settings.SMTP.Port,
			Username: settings.SMTP.Username,
			From:     settings.SMTP.From,
		}
		var err error
		if stored.SMTP.PasswordEnc, err = s.encryptOptional(settings.SMTP.Password); err != nil {
			return nil, err
		}
	}
	return stored, nil
}

func routeSetToStorage(routeSet *notification.RouteSet) *routeSetForStorage {
	routes := make([]routeForStorage, 0, len(routeSet.Routes))
	for _, route := range routeSet.Routes {
		routes = append(routes, routeForStorage{
			ID:        route.ID,
			ChannelID: route.ChannelID,
			Enabled:   route.Enabled,
			Events:    eventStrings(route.Events),
		})
	}
	return &routeSetForStorage{
		ID:            routeSet.ID,
		Scope:         routeSet.Scope,
		Workspace:     routeSet.Workspace,
		Enabled:       routeSet.Enabled,
		InheritGlobal: routeSet.InheritGlobal,
		Routes:        routes,
		CreatedAt:     routeSet.CreatedAt.Format(timeFormat),
		UpdatedAt:     routeSet.UpdatedAt.Format(timeFormat),
		UpdatedBy:     routeSet.UpdatedBy,
	}
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

func (s *Store) targetToStorage(target notification.Target) (targetForStorage, error) {
	stored := targetForStorage{
		ID:      target.ID,
		Name:    target.Name,
		Type:    target.Type,
		Enabled: target.Enabled,
		Events:  eventStrings(target.Events),
		Email:   target.Email,
	}
	var err error
	if target.Webhook != nil {
		stored.Webhook = &webhookTargetForStorage{
			AllowInsecureHTTP:   target.Webhook.AllowInsecureHTTP,
			AllowPrivateNetwork: target.Webhook.AllowPrivateNetwork,
			MessageTemplate:     target.Webhook.MessageTemplate,
		}
		if stored.Webhook.URLEnc, err = s.encryptRequired(target.Webhook.URL); err != nil {
			return stored, err
		}
		if len(target.Webhook.Headers) > 0 {
			stored.Webhook.HeadersEnc = make(map[string]string, len(target.Webhook.Headers))
			for key, value := range target.Webhook.Headers {
				enc, err := s.encryptRequired(value)
				if err != nil {
					return stored, err
				}
				stored.Webhook.HeadersEnc[key] = enc
			}
		}
		if stored.Webhook.HMACSecretEnc, err = s.encryptOptional(target.Webhook.HMACSecret); err != nil {
			return stored, err
		}
	}
	if target.Slack != nil {
		stored.Slack = &slackTargetForStorage{MessageTemplate: target.Slack.MessageTemplate}
		if stored.Slack.WebhookURLEnc, err = s.encryptRequired(target.Slack.WebhookURL); err != nil {
			return stored, err
		}
	}
	if target.Telegram != nil {
		stored.Telegram = &telegramTargetForStorage{
			ChatID:          target.Telegram.ChatID,
			MessageTemplate: target.Telegram.MessageTemplate,
		}
		if stored.Telegram.BotTokenEnc, err = s.encryptRequired(target.Telegram.BotToken); err != nil {
			return stored, err
		}
	}
	return stored, nil
}

func (s *Store) fromStorage(stored *settingsForStorage) (*notification.Settings, error) {
	events := make([]eventstore.EventType, 0, len(stored.Events))
	for _, event := range stored.Events {
		events = append(events, eventstore.EventType(event))
	}
	targets := make([]notification.Target, 0, len(stored.Targets))
	for _, target := range stored.Targets {
		decoded, err := s.targetFromStorage(target)
		if err != nil {
			return nil, err
		}
		targets = append(targets, decoded)
	}
	subscriptions := make([]notification.Subscription, 0, len(stored.Subscriptions))
	for _, subscription := range stored.Subscriptions {
		subscriptions = append(subscriptions, notification.Subscription{
			ID:        subscription.ID,
			ChannelID: subscription.ChannelID,
			Enabled:   subscription.Enabled,
			Events:    eventTypes(subscription.Events),
		})
	}
	createdAt, err := time.Parse(timeFormat, stored.CreatedAt)
	if err != nil {
		slog.Default().Debug(
			"Failed to parse notification settings timestamp",
			"field", "CreatedAt",
			"value", stored.CreatedAt,
			"error", err,
		)
	}
	updatedAt, err := time.Parse(timeFormat, stored.UpdatedAt)
	if err != nil {
		slog.Default().Debug(
			"Failed to parse notification settings timestamp",
			"field", "UpdatedAt",
			"value", stored.UpdatedAt,
			"error", err,
		)
	}
	return &notification.Settings{
		ID:            stored.ID,
		DAGName:       stored.DAGName,
		Enabled:       stored.Enabled,
		Events:        events,
		Targets:       targets,
		Subscriptions: subscriptions,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
		UpdatedBy:     stored.UpdatedBy,
	}, nil
}

func (s *Store) channelFromStorage(stored *channelForStorage) (*notification.Channel, error) {
	target, err := s.targetFromStorage(targetForStorage{
		ID:       stored.ID,
		Name:     stored.Name,
		Type:     stored.Type,
		Enabled:  stored.Enabled,
		Email:    stored.Email,
		Webhook:  stored.Webhook,
		Slack:    stored.Slack,
		Telegram: stored.Telegram,
	})
	if err != nil {
		return nil, err
	}
	createdAt, _ := time.Parse(timeFormat, stored.CreatedAt)
	updatedAt, _ := time.Parse(timeFormat, stored.UpdatedAt)
	channel := &notification.Channel{
		ID:        stored.ID,
		Name:      stored.Name,
		Type:      stored.Type,
		Enabled:   stored.Enabled,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		UpdatedBy: stored.UpdatedBy,
	}
	channel.Email = target.Email
	channel.Webhook = target.Webhook
	channel.Slack = target.Slack
	channel.Telegram = target.Telegram
	return channel, nil
}

func (s *Store) workspaceSettingsFromStorage(stored *workspaceSettingsForStorage) (*notification.WorkspaceSettings, error) {
	createdAt, _ := time.Parse(timeFormat, stored.CreatedAt)
	updatedAt, _ := time.Parse(timeFormat, stored.UpdatedAt)
	settings := &notification.WorkspaceSettings{
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		UpdatedBy: stored.UpdatedBy,
	}
	if stored.SMTP != nil {
		password, err := s.decryptOptional(stored.SMTP.PasswordEnc)
		if err != nil {
			return nil, err
		}
		settings.SMTP = &notification.SMTPConfig{
			Host:     stored.SMTP.Host,
			Port:     stored.SMTP.Port,
			Username: stored.SMTP.Username,
			Password: password,
			From:     stored.SMTP.From,
		}
	}
	return settings, nil
}

func routeSetFromStorage(stored *routeSetForStorage) *notification.RouteSet {
	routes := make([]notification.Route, 0, len(stored.Routes))
	for _, route := range stored.Routes {
		routes = append(routes, notification.Route{
			ID:        route.ID,
			ChannelID: route.ChannelID,
			Enabled:   route.Enabled,
			Events:    eventTypes(route.Events),
		})
	}
	createdAt, _ := time.Parse(timeFormat, stored.CreatedAt)
	updatedAt, _ := time.Parse(timeFormat, stored.UpdatedAt)
	return &notification.RouteSet{
		ID:            stored.ID,
		Scope:         stored.Scope,
		Workspace:     stored.Workspace,
		Enabled:       stored.Enabled,
		InheritGlobal: stored.InheritGlobal,
		Routes:        routes,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
		UpdatedBy:     stored.UpdatedBy,
	}
}

func (s *Store) targetFromStorage(stored targetForStorage) (notification.Target, error) {
	target := notification.Target{
		ID:      stored.ID,
		Name:    stored.Name,
		Type:    stored.Type,
		Enabled: stored.Enabled,
		Events:  eventTypes(stored.Events),
		Email:   stored.Email,
	}
	var err error
	if stored.Webhook != nil {
		target.Webhook = &notification.WebhookTarget{
			AllowInsecureHTTP:   stored.Webhook.AllowInsecureHTTP,
			AllowPrivateNetwork: stored.Webhook.AllowPrivateNetwork,
			MessageTemplate:     stored.Webhook.MessageTemplate,
		}
		if target.Webhook.URL, err = s.decryptOptional(stored.Webhook.URLEnc); err != nil {
			return target, err
		}
		if len(stored.Webhook.HeadersEnc) > 0 {
			target.Webhook.Headers = make(map[string]string, len(stored.Webhook.HeadersEnc))
			for key, value := range stored.Webhook.HeadersEnc {
				dec, err := s.decryptOptional(value)
				if err != nil {
					return target, err
				}
				target.Webhook.Headers[key] = dec
			}
		}
		if target.Webhook.HMACSecret, err = s.decryptOptional(stored.Webhook.HMACSecretEnc); err != nil {
			return target, err
		}
	}
	if stored.Slack != nil {
		target.Slack = &notification.SlackTarget{MessageTemplate: stored.Slack.MessageTemplate}
		if target.Slack.WebhookURL, err = s.decryptOptional(stored.Slack.WebhookURLEnc); err != nil {
			return target, err
		}
	}
	if stored.Telegram != nil {
		target.Telegram = &notification.TelegramTarget{
			ChatID:          stored.Telegram.ChatID,
			MessageTemplate: stored.Telegram.MessageTemplate,
		}
		if target.Telegram.BotToken, err = s.decryptOptional(stored.Telegram.BotTokenEnc); err != nil {
			return target, err
		}
	}
	return target, nil
}

func eventStrings(events []eventstore.EventType) []string {
	if len(events) == 0 {
		return nil
	}
	result := make([]string, 0, len(events))
	for _, event := range events {
		result = append(result, string(event))
	}
	return result
}

func eventTypes(events []string) []eventstore.EventType {
	if len(events) == 0 {
		return nil
	}
	result := make([]eventstore.EventType, 0, len(events))
	for _, event := range events {
		result = append(result, eventstore.EventType(event))
	}
	return result
}

func (s *Store) encryptRequired(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if s.encryptor == nil {
		return "", notification.ErrSecretStoreMissing
	}
	return s.encryptor.Encrypt(value)
}

func (s *Store) encryptOptional(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return s.encryptRequired(value)
}

func (s *Store) decryptOptional(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if s.encryptor == nil {
		return "", notification.ErrSecretStoreMissing
	}
	return s.encryptor.Decrypt(value)
}
