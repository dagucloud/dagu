// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package dagsettings contains server-side DAG settings.
package dagsettings

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/profile"
)

var (
	ErrInvalidDAGName = errors.New("invalid DAG name")
	ErrNotFound       = errors.New("DAG settings not found")
)

type Settings struct {
	DAGName   string
	Profile   string
	CreatedBy string
	CreatedAt time.Time
	UpdatedBy string
	UpdatedAt time.Time
}

type UpdateInput struct {
	DAGName   string
	Profile   string
	UpdatedBy string
}

func New(input UpdateInput, now time.Time) (*Settings, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	dagName := strings.TrimSpace(input.DAGName)
	profileName := strings.TrimSpace(input.Profile)
	if err := ValidateDAGName(dagName); err != nil {
		return nil, err
	}
	if profileName != "" {
		if err := profile.ValidateName(profileName); err != nil {
			return nil, err
		}
	}
	return &Settings{
		DAGName:   dagName,
		Profile:   profileName,
		CreatedBy: input.UpdatedBy,
		CreatedAt: now,
		UpdatedBy: input.UpdatedBy,
		UpdatedAt: now,
	}, nil
}

func (s *Settings) ApplyUpdate(input UpdateInput, now time.Time) error {
	if s == nil {
		return fmt.Errorf("DAG settings cannot be nil")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	dagName := strings.TrimSpace(input.DAGName)
	profileName := strings.TrimSpace(input.Profile)
	if err := ValidateDAGName(dagName); err != nil {
		return err
	}
	if profileName != "" {
		if err := profile.ValidateName(profileName); err != nil {
			return err
		}
	}
	s.DAGName = dagName
	s.Profile = profileName
	s.UpdatedBy = input.UpdatedBy
	s.UpdatedAt = now
	return nil
}

func ValidateDAGName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: DAG name must not be empty", ErrInvalidDAGName)
	}
	if err := core.ValidateDAGName(name); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidDAGName, err)
	}
	return nil
}

func (s *Settings) Clone() *Settings {
	if s == nil {
		return nil
	}
	clone := *s
	return &clone
}
