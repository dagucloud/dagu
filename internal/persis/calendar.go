// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
)

var (
	ErrCalendarNotFound      = errors.New("calendar not found")
	ErrCalendarAlreadyExists = errors.New("calendar already exists")
)

// CalendarStore reads and writes calendar files by calendar name. It keeps
// content as given; checking that it is a valid calendar is the caller's
// job.
type CalendarStore interface {
	// Get returns the content of the named calendar file, or
	// ErrCalendarNotFound.
	Get(ctx context.Context, name string) ([]byte, error)
	// Create writes a new calendar file. It returns
	// ErrCalendarAlreadyExists when one has the name.
	Create(ctx context.Context, name string, data []byte) error
	// Update replaces the content of a calendar file, or returns
	// ErrCalendarNotFound.
	Update(ctx context.Context, name string, data []byte) error
	// Delete removes a calendar file, or returns ErrCalendarNotFound.
	Delete(ctx context.Context, name string) error
}
