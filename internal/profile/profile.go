// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package profile contains runtime profile domain models.
package profile

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
)

type EntryKind string

const (
	EntryKindVariable EntryKind = "variable"
	EntryKindSecret   EntryKind = "secret"
)

var (
	ErrAlreadyExists = errors.New("profile already exists")
	ErrDisabled      = errors.New("profile is disabled")
	ErrDuplicateKey  = errors.New("profile key already exists")
	ErrInvalidKey    = errors.New("invalid profile key")
	ErrInvalidName   = errors.New("invalid profile name")
	ErrInvalidStatus = errors.New("invalid profile status")
	ErrNotFound      = errors.New("profile not found")
	ErrReservedKey   = errors.New("profile key is reserved")
)

var (
	profileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	envKeyPattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type Profile struct {
	ID          string
	Name        string
	Description string
	Status      Status
	Protected   bool
	Entries     []Entry
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedBy   string
	UpdatedAt   time.Time
}

type Entry struct {
	Key       string
	Kind      EntryKind
	Value     string
	SecretID  string
	CreatedBy string
	CreatedAt time.Time
	UpdatedBy string
	UpdatedAt time.Time
}

type CreateInput struct {
	Name        string
	Description string
	Protected   bool
	CreatedBy   string
}

type UpdateInput struct {
	Description *string
	Protected   *bool
	UpdatedBy   string
}

func New(input CreateInput, now time.Time) (*Profile, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := ValidateName(input.Name); err != nil {
		return nil, err
	}
	return &Profile{
		ID:          uuid.NewString(),
		Name:        input.Name,
		Description: input.Description,
		Status:      StatusActive,
		Protected:   input.Protected,
		CreatedBy:   input.CreatedBy,
		CreatedAt:   now,
		UpdatedBy:   input.CreatedBy,
		UpdatedAt:   now,
	}, nil
}

func ValidateName(name string) error {
	if !profileNamePattern.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	return nil
}

func ValidateKey(key string) error {
	if !envKeyPattern.MatchString(key) {
		return fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	if strings.HasPrefix(key, "DAGU_") {
		return fmt.Errorf("%w: %q", ErrReservedKey, key)
	}
	return nil
}

func (p *Profile) Clone() *Profile {
	if p == nil {
		return nil
	}
	clone := *p
	clone.Entries = append([]Entry(nil), p.Entries...)
	return &clone
}

func (p *Profile) SetStatus(status Status, actor string, now time.Time) error {
	if status != StatusActive && status != StatusDisabled {
		return ErrInvalidStatus
	}
	p.Status = status
	p.touch(actor, now)
	return nil
}

func (p *Profile) ApplyUpdate(input UpdateInput, now time.Time) {
	if input.Description != nil {
		p.Description = *input.Description
	}
	if input.Protected != nil {
		p.Protected = *input.Protected
	}
	p.touch(input.UpdatedBy, now)
}

func (p *Profile) SetVariable(key, value, actor string, now time.Time) error {
	return p.setEntry(key, EntryKindVariable, actor, now, func(entry *Entry) {
		entry.Value = value
	})
}

func (p *Profile) SetSecret(key, secretID, actor string, now time.Time) error {
	return p.setEntry(key, EntryKindSecret, actor, now, func(entry *Entry) {
		entry.SecretID = secretID
	})
}

func (p *Profile) setEntry(key string, kind EntryKind, actor string, now time.Time, apply func(*Entry)) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	idx, ok := p.entryIndex(key)
	if ok && p.Entries[idx].Kind != kind {
		return fmt.Errorf("%w: %s", ErrDuplicateKey, key)
	}
	if ok {
		apply(&p.Entries[idx])
		p.Entries[idx].UpdatedBy = actor
		p.Entries[idx].UpdatedAt = normalizeTime(now)
		p.touch(actor, now)
		return nil
	}
	t := normalizeTime(now)
	entry := Entry{
		Key:       key,
		Kind:      kind,
		CreatedBy: actor,
		CreatedAt: t,
		UpdatedBy: actor,
		UpdatedAt: t,
	}
	apply(&entry)
	p.Entries = append(p.Entries, entry)
	p.touch(actor, now)
	return nil
}

func (p *Profile) DeleteEntry(key, actor string, now time.Time) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	idx, ok := p.entryIndex(key)
	if !ok {
		return ErrNotFound
	}
	p.Entries = append(p.Entries[:idx], p.Entries[idx+1:]...)
	p.touch(actor, now)
	return nil
}

func (p *Profile) entryIndex(key string) (int, bool) {
	for i, entry := range p.Entries {
		if entry.Key == key {
			return i, true
		}
	}
	return 0, false
}

func (p *Profile) touch(actor string, now time.Time) {
	p.UpdatedBy = actor
	p.UpdatedAt = normalizeTime(now)
}

func normalizeTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}
