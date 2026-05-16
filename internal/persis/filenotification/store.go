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
	settingsFileExtension = ".json"
	dirPermissions        = 0750
	filePermissions       = 0600
)

type Option func(*Store)

func WithEncryptor(enc *crypto.Encryptor) Option {
	return func(s *Store) {
		s.encryptor = enc
	}
}

type Store struct {
	baseDir   string
	encryptor *crypto.Encryptor
	mu        sync.RWMutex
}

var _ notification.Store = (*Store)(nil)

func New(baseDir string, opts ...Option) (*Store, error) {
	if baseDir == "" {
		return nil, errors.New("filenotification: baseDir cannot be empty")
	}
	store := &Store{baseDir: baseDir}
	for _, opt := range opts {
		opt(store)
	}
	if err := os.MkdirAll(baseDir, dirPermissions); err != nil {
		return nil, fmt.Errorf("filenotification: failed to create directory %s: %w", baseDir, err)
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

func (s *Store) filePath(dagName string) string {
	sum := sha256.Sum256([]byte(dagName))
	return filepath.Join(s.baseDir, hex.EncodeToString(sum[:])+settingsFileExtension)
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

type settingsForStorage struct {
	ID        string             `json:"id"`
	DAGName   string             `json:"dagName"`
	Enabled   bool               `json:"enabled"`
	Events    []string           `json:"events"`
	Targets   []targetForStorage `json:"targets"`
	CreatedAt string             `json:"createdAt"`
	UpdatedAt string             `json:"updatedAt"`
	UpdatedBy string             `json:"updatedBy,omitempty"`
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
	AllowInsecureHTTP   bool              `json:"allowInsecureHttp,omitempty"`
	AllowPrivateNetwork bool              `json:"allowPrivateNetwork,omitempty"`
}

type slackTargetForStorage struct {
	WebhookURLEnc string `json:"webhookUrlEnc,omitempty"`
}

type telegramTargetForStorage struct {
	BotTokenEnc string `json:"botTokenEnc,omitempty"`
	ChatID      string `json:"chatId,omitempty"`
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
	return &settingsForStorage{
		ID:        settings.ID,
		DAGName:   settings.DAGName,
		Enabled:   settings.Enabled,
		Events:    events,
		Targets:   targets,
		CreatedAt: settings.CreatedAt.Format(timeFormat),
		UpdatedAt: settings.UpdatedAt.Format(timeFormat),
		UpdatedBy: settings.UpdatedBy,
	}, nil
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
		stored.Slack = &slackTargetForStorage{}
		if stored.Slack.WebhookURLEnc, err = s.encryptRequired(target.Slack.WebhookURL); err != nil {
			return stored, err
		}
	}
	if target.Telegram != nil {
		stored.Telegram = &telegramTargetForStorage{ChatID: target.Telegram.ChatID}
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
	createdAt, _ := time.Parse(timeFormat, stored.CreatedAt)
	updatedAt, _ := time.Parse(timeFormat, stored.UpdatedAt)
	return &notification.Settings{
		ID:        stored.ID,
		DAGName:   stored.DAGName,
		Enabled:   stored.Enabled,
		Events:    events,
		Targets:   targets,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		UpdatedBy: stored.UpdatedBy,
	}, nil
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
		target.Slack = &notification.SlackTarget{}
		if target.Slack.WebhookURL, err = s.decryptOptional(stored.Slack.WebhookURLEnc); err != nil {
			return target, err
		}
	}
	if stored.Telegram != nil {
		target.Telegram = &notification.TelegramTarget{ChatID: stored.Telegram.ChatID}
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
