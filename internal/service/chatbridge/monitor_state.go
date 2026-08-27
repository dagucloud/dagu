// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chatbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/eventstore"
)

const notificationMonitorStateVersion = 1

type notificationStateLoadResult struct {
	State           notificationMonitorState
	Missing         bool
	Recovered       bool
	QuarantinedPath string
	Warning         error
}

type notificationMonitorState struct {
	Version      int                                      `json:"version"`
	Bootstrapped bool                                     `json:"bootstrapped,omitempty"`
	SourceCursor eventstore.DAGRunCursor                  `json:"source_cursor"`
	Destinations map[string]*notificationDestinationState `json:"destinations,omitempty"`
}

type notificationDestinationState struct {
	Pending   map[string]NotificationEvent `json:"pending,omitempty"`
	Delivered map[string]time.Time         `json:"delivered,omitempty"`
}

func newNotificationMonitorState() notificationMonitorState {
	return notificationMonitorState{
		Version:      notificationMonitorStateVersion,
		SourceCursor: eventstore.DAGRunCursor{CommittedOffsets: make(map[string]int64)},
		Destinations: make(map[string]*notificationDestinationState),
	}
}

func (s *notificationMonitorState) normalize() {
	if s.Version == 0 {
		s.Version = notificationMonitorStateVersion
	}
	s.SourceCursor = s.SourceCursor.Normalize()
	if s.Destinations == nil {
		s.Destinations = make(map[string]*notificationDestinationState)
	}
	for key, destination := range s.Destinations {
		if destination == nil {
			delete(s.Destinations, key)
			continue
		}
		if destination.Pending == nil {
			destination.Pending = make(map[string]NotificationEvent)
		}
		if destination.Delivered == nil {
			destination.Delivered = make(map[string]time.Time)
		}
	}
}

type notificationStateStore struct {
	store StateStore
}

func newNotificationStateStore(store StateStore) *notificationStateStore {
	if store == nil {
		return nil
	}
	return &notificationStateStore{store: store}
}

func (s *notificationStateStore) Load(ctx context.Context) notificationStateLoadResult {
	result := notificationStateLoadResult{State: newNotificationMonitorState()}
	if s == nil {
		return result
	}

	data, found, err := s.store.Load(ctx)
	if err != nil {
		result.Recovered = true
		result.QuarantinedPath, result.Warning = s.recoverUnreadableState(ctx, fmt.Errorf("read notification state: %w", err))
		return result
	}
	if !found {
		result.Missing = true
		return result
	}

	state, err := decodeNotificationMonitorState(data)
	if err != nil {
		result.Recovered = true
		result.QuarantinedPath, result.Warning = s.recoverUnreadableState(ctx, err)
		return result
	}
	result.State = state
	return result
}

func (s *notificationStateStore) IsBootstrapped(ctx context.Context) bool {
	if s == nil {
		return false
	}

	data, found, err := s.store.Load(ctx)
	if err != nil || !found {
		return false
	}

	state, err := decodeNotificationMonitorState(data)
	return err == nil && state.Bootstrapped
}

func (s *notificationStateStore) Save(ctx context.Context, state notificationMonitorState) error {
	if s == nil {
		return nil
	}
	state.normalize()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal notification state: %w", err)
	}

	return s.store.Save(ctx, data)
}

func (s *notificationStateStore) recoverUnreadableState(ctx context.Context, err error) (string, error) {
	quarantinedPath, quarantineErr := s.store.Quarantine(ctx)
	if quarantineErr != nil {
		return "", fmt.Errorf("%w (quarantine failed: %v)", err, quarantineErr)
	}
	return quarantinedPath, err
}

func decodeNotificationMonitorState(data []byte) (notificationMonitorState, error) {
	state := newNotificationMonitorState()
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("decode notification state: %w", err)
	}
	switch state.Version {
	case 0, notificationMonitorStateVersion:
	default:
		return state, fmt.Errorf("unsupported notification state version %d", state.Version)
	}

	state.normalize()
	return state, nil
}
