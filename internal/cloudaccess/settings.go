// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cloudaccess serves the calls Dagu Cloud carries to this server over
// a relay connection the server opens, within the role the server's
// administrator allows.
package cloudaccess

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

// settingsTTL bounds how long a process keeps the choices it read, so a
// choice made by another process sharing the data directory takes effect
// within it.
const settingsTTL = 5 * time.Second

var (
	// ErrConfigured means configuration fixes the setting.
	ErrConfigured = errors.New("configuration sets this, so it cannot be changed here")
	// ErrUnknownLevel means a level other than off, viewer, operator, or
	// developer.
	ErrUnknownLevel = errors.New("unknown remote access level")
)

// StateStore keeps the choices an administrator made.
type StateStore interface {
	Load(ctx context.Context) (data []byte, found bool, err error)
	Save(ctx context.Context, data []byte) error
}

// Status is how far calls from Dagu Cloud may go, and how it was decided.
type Status struct {
	// Level is the highest role a call gets.
	Level config.RemoteAccessLevel
	// LevelConfigured means configuration fixes Level.
	LevelConfigured bool
	// Approvals lets calls decide steps waiting for a person.
	Approvals bool
	// ApprovalsConfigured means configuration fixes Approvals.
	ApprovalsConfigured bool
}

// Settings decides how far calls from Dagu Cloud may go on this server:
// as configuration fixes, or else as an administrator chooses. Remote
// access stays off and approvals stay refused until either says otherwise.
type Settings struct {
	configured config.CloudConfig
	store      StateStore
	now        func() time.Time

	mu       sync.Mutex
	cached   choice
	cachedAt time.Time
	// changed signals choices made in this process.
	changed chan struct{}
}

// choice is what an administrator chose, as the store keeps it.
type choice struct {
	Level     config.RemoteAccessLevel `json:"level,omitempty"`
	Approvals bool                     `json:"approvals,omitempty"`
}

// NewSettings returns the remote access settings of a server: those cloud
// configures, and the choices store keeps for the rest.
func NewSettings(cloud config.CloudConfig, store StateStore) *Settings {
	return &Settings{configured: cloud, store: store, now: time.Now, changed: make(chan struct{}, 1)}
}

// Status returns the settings in effect.
func (s *Settings) Status(ctx context.Context) (Status, error) {
	status := Status{Level: s.configured.RemoteAccess, LevelConfigured: s.configured.RemoteAccess != ""}
	if approvals := s.configured.RemoteApprovals; approvals != nil {
		status.Approvals, status.ApprovalsConfigured = *approvals, true
	}
	if status.LevelConfigured && status.ApprovalsConfigured {
		return status, nil
	}
	c, err := s.read(ctx)
	if err != nil {
		return Status{}, err
	}
	if !status.LevelConfigured {
		status.Level = cmp.Or(c.Level, config.RemoteAccessOff)
	}
	if !status.ApprovalsConfigured {
		status.Approvals = c.Approvals
	}
	return status, nil
}

// Set chooses the highest role calls get, whether they may decide steps
// waiting for a person, or both, in one change; nil leaves either as it is.
// It fails, changing nothing, with ErrConfigured when configuration fixes a
// setting it was given, and with ErrUnknownLevel for an unknown level.
func (s *Settings) Set(ctx context.Context, level *config.RemoteAccessLevel, approvals *bool) error {
	if level != nil && s.configured.RemoteAccess != "" || approvals != nil && s.configured.RemoteApprovals != nil {
		return ErrConfigured
	}
	if level != nil {
		switch *level {
		case config.RemoteAccessOff, config.RemoteAccessViewer, config.RemoteAccessOperator, config.RemoteAccessDeveloper:
		default:
			return fmt.Errorf("%w: %q", ErrUnknownLevel, *level)
		}
	}
	return s.update(ctx, func(c *choice) {
		if level != nil {
			c.Level = *level
		}
		if approvals != nil {
			c.Approvals = *approvals
		}
	})
}

// changes signals each choice made in this process.
func (s *Settings) changes() <-chan struct{} { return s.changed }

// read returns the choices, read again from the store once settingsTTL has
// passed.
func (s *Settings) read(ctx context.Context) (choice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cachedAt.IsZero() && s.now().Sub(s.cachedAt) < settingsTTL {
		return s.cached, nil
	}
	return s.loadLocked(ctx)
}

func (s *Settings) loadLocked(ctx context.Context) (choice, error) {
	var c choice
	data, found, err := s.store.Load(ctx)
	if err != nil {
		return choice{}, fmt.Errorf("read the remote access settings: %w", err)
	}
	if found {
		if err := json.Unmarshal(data, &c); err != nil {
			return choice{}, fmt.Errorf("read the remote access settings: %w", err)
		}
	}
	s.cached, s.cachedAt = c, s.now()
	return c, nil
}

// update changes the choices as the store holds them now, since another
// process may have changed them.
func (s *Settings) update(ctx context.Context, change func(*choice)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.loadLocked(ctx)
	if err != nil {
		return err
	}
	change(&c)
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := s.store.Save(ctx, data); err != nil {
		return fmt.Errorf("save the remote access settings: %w", err)
	}
	s.cached, s.cachedAt = c, s.now()
	select {
	case s.changed <- struct{}{}:
	default:
	}
	return nil
}
