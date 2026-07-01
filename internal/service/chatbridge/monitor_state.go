// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chatbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/service/eventstore"
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
	SourceCursor eventstore.NotificationCursor            `json:"source_cursor"`
	Destinations map[string]*notificationDestinationState `json:"destinations,omitempty"`
}

type notificationDestinationState struct {
	Pending   map[string]NotificationEvent `json:"pending,omitempty"`
	Delivered map[string]time.Time         `json:"delivered,omitempty"`
}

func newNotificationMonitorState() notificationMonitorState {
	return notificationMonitorState{
		Version:      notificationMonitorStateVersion,
		SourceCursor: eventstore.NotificationCursor{CommittedOffsets: make(map[string]int64)},
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
	path string
}

func newNotificationStateStore(path string) *notificationStateStore {
	if path == "" {
		return nil
	}
	return &notificationStateStore{path: path}
}

func (s *notificationStateStore) Load(_ context.Context) notificationStateLoadResult {
	result := notificationStateLoadResult{State: newNotificationMonitorState()}
	if s == nil {
		return result
	}

	data, err := fileutil.ReadFile(filepath.Clean(s.path))
	if err != nil {
		if os.IsNotExist(err) {
			result.Missing = true
			return result
		}
		result.Recovered = true
		result.QuarantinedPath, result.Warning = s.recoverUnreadableState(fmt.Errorf("read notification state: %w", err))
		return result
	}

	state := newNotificationMonitorState()
	if err := json.Unmarshal(data, &state); err != nil {
		result.Recovered = true
		result.QuarantinedPath, result.Warning = s.recoverUnreadableState(fmt.Errorf("decode notification state: %w", err))
		return result
	}
	switch state.Version {
	case 0, notificationMonitorStateVersion:
	default:
		result.Recovered = true
		result.QuarantinedPath, result.Warning = s.recoverUnreadableState(fmt.Errorf("unsupported notification state version %d", state.Version))
		return result
	}

	state.normalize()
	result.State = state
	return result
}

func (s *notificationStateStore) Save(_ context.Context, state notificationMonitorState) error {
	if s == nil {
		return nil
	}
	state.normalize()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return fmt.Errorf("create notification state dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal notification state: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := writeNotificationStateFile(tmp, data); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := fileutil.ReplaceFile(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename notification state: %w", err)
	}
	return nil
}

func writeNotificationStateFile(path string, data []byte) error {
	file, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // internal state path
	if err != nil {
		return fmt.Errorf("open notification state file: %w", err)
	}

	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write notification state file: %w", writeErr)
	}
	if syncErr != nil {
		return fmt.Errorf("sync notification state file: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close notification state file: %w", closeErr)
	}
	return nil
}

func (s *notificationStateStore) quarantineCorruptStateFile() (string, error) {
	if s == nil || s.path == "" {
		return "", nil
	}
	quarantinedPath := fmt.Sprintf("%s.corrupt.%s", s.path, time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := fileutil.Rename(s.path, quarantinedPath); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("quarantine notification state: %w", err)
	}
	return quarantinedPath, nil
}

func (s *notificationStateStore) recoverUnreadableState(err error) (string, error) {
	quarantinedPath, quarantineErr := s.quarantineCorruptStateFile()
	if quarantineErr != nil {
		return "", fmt.Errorf("%w (quarantine failed: %v)", err, quarantineErr)
	}
	return quarantinedPath, err
}
